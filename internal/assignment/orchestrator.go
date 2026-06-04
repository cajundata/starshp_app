package assignment

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/cajundata/starshp_app/internal/chat"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/cajundata/starshp_app/internal/store"
	"github.com/cajundata/starshp_app/internal/tools"
	"github.com/google/uuid"
)

// ProviderFactory builds a provider for a model id, returning the provider and
// its provider name ("openai"|"anthropic"). Injected so tests use a fake.
type ProviderFactory func(modelID string) (provider.ChatProvider, string, error)

// Options configures a batch run.
type Options struct {
	Model       string
	Concurrency int
	Grounding   GroundingSource
	Emit        func(name string, payload any) // batch progress events; never nil in prod
	// SafeMath and SearchTool, when non-nil, are registered into each item's
	// registry so the solver can verify arithmetic / search textbooks. nil
	// disables that tool. appapi sets these; unit tests leave them nil.
	SafeMath   tools.Tool
	SearchTool tools.Tool
}

type Orchestrator struct {
	st   *store.Store
	chat *chat.Service
	pf   ProviderFactory
	opts Options
}

func New(st *store.Store, chatSvc *chat.Service, pf ProviderFactory, opts Options) *Orchestrator {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	if opts.Emit == nil {
		opts.Emit = func(string, any) {}
	}
	if opts.Grounding == nil {
		opts.Grounding = NoGrounding{}
	}
	return &Orchestrator{st: st, chat: chatSvc, pf: pf, opts: opts}
}

// prepare loads the dir, ensures grounding, and finds-or-creates the assignment
// row, returning its id and the state runItems needs.
func (o *Orchestrator) prepare(ctx context.Context, dir string) (string, *Loaded, map[string]store.AssignmentItem, error) {
	loaded, err := Load(dir)
	if err != nil {
		return "", nil, nil, err
	}
	if err := o.opts.Grounding.Ensure(ctx); err != nil {
		return "", nil, nil, fmt.Errorf("grounding: %w", err)
	}
	manifestHash := hashManifest(loaded)
	priorByPath := map[string]store.AssignmentItem{}
	var asgID string
	if existing, ok, ferr := o.st.FindAssignmentByManifest(dir, manifestHash); ferr != nil {
		return "", nil, nil, ferr
	} else if ok {
		asgID = existing.ID
		_ = o.st.UpdateAssignmentStatus(asgID, "in_progress")
		prior, perr := o.st.ListAssignmentItems(asgID)
		if perr != nil {
			return "", nil, nil, fmt.Errorf("list prior items: %w", perr)
		}
		for _, it := range prior {
			priorByPath[it.SourcePath] = it
		}
	} else {
		asgID = uuid.NewString()
		if err := o.st.CreateAssignment(store.Assignment{
			ID: asgID, SourceDir: dir, Title: titleFor(dir, loaded),
			ManifestHash: manifestHash, Model: o.opts.Model,
			Status: "in_progress", TotalItems: len(loaded.Questions),
		}); err != nil {
			return "", nil, nil, err
		}
	}
	return asgID, loaded, priorByPath, nil
}

// runItems executes the bounded-concurrent fan-out for a prepared assignment
// and finalizes its status. Cancellation via ctx stops scheduling new items and
// marks the batch + unfinished items cancelled; per-item failures are isolated
// and never abort the batch.
func (o *Orchestrator) runItems(ctx context.Context, dir, asgID string, loaded *Loaded, priorByPath map[string]store.AssignmentItem) {
	o.opts.Emit("assignment:started", map[string]any{
		"assignmentId": asgID, "total": len(loaded.Questions), "title": titleFor(dir, loaded)})

	sem := make(chan struct{}, o.opts.Concurrency)
	var wg sync.WaitGroup
	cancelled := false
	for i, q := range loaded.Questions {
		if ctx.Err() != nil {
			cancelled = true
			break
		}
		prior, hasPrior := priorByPath[q.Path]
		if hasPrior && prior.Status == "answered" {
			continue // already solved on a previous run
		}
		var itemID string
		if hasPrior {
			itemID = prior.ID // reuse the existing row; solveItem updates it
		} else {
			itemID = uuid.NewString()
			if err := o.st.CreateAssignmentItem(store.AssignmentItem{
				ID: itemID, AssignmentID: asgID, Seq: i, SourcePath: q.Path,
				Type: string(q.Type), Title: q.Title, Status: "pending",
			}); err != nil {
				slog.Error("assignment: create item failed; skipping",
					"assignmentId", asgID, "seq", i, "path", q.Path, "err", err)
				continue
			}
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(itemID string, seq int, q Question) {
			defer wg.Done()
			defer func() { <-sem }()
			o.solveItem(ctx, dir, asgID, itemID, seq, q)
		}(itemID, i, q)
	}
	wg.Wait()

	status := "completed"
	if cancelled {
		// Stopped scheduling early — definitely cancelled.
		status = "cancelled"
		o.markUnfinishedCancelled(asgID)
	} else if ctx.Err() != nil {
		// Context cancelled after all items were scheduled; only "cancelled" if
		// something actually didn't finish.
		if o.markUnfinishedCancelled(asgID) > 0 {
			status = "cancelled"
		}
	}
	_ = o.st.UpdateAssignmentStatus(asgID, status)
	o.opts.Emit("assignment:"+status, map[string]any{"assignmentId": asgID})
}

// Run solves a directory synchronously (used by tests).
func (o *Orchestrator) Run(ctx context.Context, dir string) (string, error) {
	asgID, loaded, prior, err := o.prepare(ctx, dir)
	if err != nil {
		return "", err
	}
	o.runItems(ctx, dir, asgID, loaded, prior)
	return asgID, nil
}

// RerunItem re-solves a single item in place, overwriting its prior answer, and
// returns the updated item. Idle-only: rejects unsupported items, items still
// running, and items whose batch is in progress. Errors are typed
// provider.AppError so the API boundary can surface them verbatim.
func (o *Orchestrator) RerunItem(ctx context.Context, asgID string, seq int) (store.AssignmentItem, error) {
	item, ok, err := o.st.GetAssignmentItem(asgID, seq)
	if err != nil {
		return store.AssignmentItem{}, err
	}
	if !ok {
		return store.AssignmentItem{}, provider.AppError{Code: "not_found", UserMessage: "That item no longer exists.", Retryable: false}
	}
	if item.Type == string(TypeUnsupported) {
		return store.AssignmentItem{}, provider.AppError{Code: "unsupported", UserMessage: "This item type can't be solved.", Retryable: false}
	}
	// cancelled, errored, and no_answer items are all re-runnable; only an
	// item that is actively being solved (or queued) is blocked.
	if item.Status == "solving" || item.Status == "pending" {
		return store.AssignmentItem{}, provider.AppError{Code: "busy", UserMessage: "This item is still being solved.", Retryable: false}
	}

	asg, err := o.st.GetAssignment(asgID)
	if errors.Is(err, sql.ErrNoRows) {
		return store.AssignmentItem{}, provider.AppError{Code: "not_found", UserMessage: "That assignment no longer exists.", Retryable: false}
	}
	if err != nil {
		return store.AssignmentItem{}, err
	}
	if asg.Status == "in_progress" {
		return store.AssignmentItem{}, provider.AppError{Code: "busy", UserMessage: "A solve is already running — wait for it to finish.", Retryable: false}
	}

	loaded, err := Load(asg.SourceDir)
	if err != nil {
		return store.AssignmentItem{}, err
	}
	var q Question
	found := false
	for _, cand := range loaded.Questions {
		if cand.Path == item.SourcePath {
			q, found = cand, true
			break
		}
	}
	if !found {
		return store.AssignmentItem{}, provider.AppError{Code: "not_found", UserMessage: "That question is no longer in the folder.", Retryable: false}
	}

	if err := o.opts.Grounding.Ensure(ctx); err != nil {
		return store.AssignmentItem{}, fmt.Errorf("grounding: %w", err)
	}
	o.solveItem(ctx, asg.SourceDir, asgID, item.ID, seq, q)

	updated, _, err := o.st.GetAssignmentItem(asgID, seq) // ok always true: solveItem updates, never deletes
	if err != nil {
		return store.AssignmentItem{}, err
	}
	return updated, nil
}

// Start prepares synchronously (so the assignment id is available immediately)
// then runs the batch in a background goroutine. onDone (may be nil) runs when
// the batch finishes — callers use it to release the run's context.
func (o *Orchestrator) Start(ctx context.Context, dir string, onDone func()) (string, error) {
	asgID, loaded, prior, err := o.prepare(ctx, dir)
	if err != nil {
		return "", err
	}
	go func() {
		if onDone != nil {
			defer onDone()
		}
		o.runItems(ctx, dir, asgID, loaded, prior)
	}()
	return asgID, nil
}

// markUnfinishedCancelled flips any pending/solving items to cancelled after a
// batch is stopped, returning how many it changed.
func (o *Orchestrator) markUnfinishedCancelled(asgID string) int {
	items, _ := o.st.ListAssignmentItems(asgID)
	n := 0
	for _, it := range items {
		if it.Status == "pending" || it.Status == "solving" {
			it.Status = "cancelled"
			_ = o.st.UpdateAssignmentItem(it)
			n++
		}
	}
	return n
}

// solveItem runs one question through the agentic loop and persists the result.
func (o *Orchestrator) solveItem(ctx context.Context, dir, asgID, itemID string, seq int, q Question) {
	item := store.AssignmentItem{ID: itemID, AssignmentID: asgID, Seq: seq,
		SourcePath: q.Path, Type: string(q.Type), Title: q.Title}

	if q.Type == TypeUnsupported {
		item.Status = "unsupported"
		_ = o.st.UpdateAssignmentItem(item)
		o.emitItemDone(asgID, item)
		return
	}

	prov, provName, err := o.pf(o.opts.Model)
	if err != nil {
		item.Status = "errored"
		item.Error = err.Error()
		_ = o.st.UpdateAssignmentItem(item)
		o.emitItemDone(asgID, item)
		return
	}

	conv, err := o.st.CreateConversation(q.Title)
	if err != nil {
		item.Status = "errored"
		item.Error = err.Error()
		_ = o.st.UpdateAssignmentItem(item)
		o.emitItemDone(asgID, item)
		return
	}
	_ = o.st.SetConversationAssignment(conv.ID, asgID)
	item.ConversationID = conv.ID
	item.Status = "solving"
	_ = o.st.UpdateAssignmentItem(item)
	o.opts.Emit("assignment:item_started",
		map[string]any{"assignmentId": asgID, "seq": seq, "title": q.Title, "type": q.Type})

	reg := o.buildRegistry(q)
	system, user := RenderPrompt(q)
	mode := chat.RetrievalNoRetrieval
	if o.opts.Grounding.Retriever() != nil {
		mode = chat.RetrievalAutoGroundedDefault
	}
	res, sendErr := o.chat.Send(ctx, chat.SendParams{
		ConversationID: conv.ID, UserText: user, SystemPrompt: system,
		Model: o.opts.Model, Provider: prov, ProviderName: provName,
		Registry: reg, Resolver: nil, Retriever: o.opts.Grounding.Retriever(),
		RetrievalMode: mode,
	}, nil)
	item.RunID = res.RunID
	if sendErr != nil {
		item.Status = "errored"
		item.Error = sendErr.Error()
		_ = o.st.UpdateAssignmentItem(item)
		o.emitItemDone(asgID, item)
		return
	}

	raw, err := o.st.GetSubmittedAnswer(res.RunID)
	if err != nil {
		item.Status = "errored"
		item.Error = "read submitted answer: " + err.Error()
		_ = o.st.UpdateAssignmentItem(item)
		o.emitItemDone(asgID, item)
		return
	}
	if len(raw) == 0 {
		item.Status = "no_answer"
		_ = o.st.UpdateAssignmentItem(item)
		o.emitItemDone(asgID, item)
		return
	}
	var ans Answer
	if err := json.Unmarshal(raw, &ans); err != nil {
		item.Status = "errored"
		item.Error = "unparseable submit_answer: " + err.Error()
		_ = o.st.UpdateAssignmentItem(item)
		o.emitItemDone(asgID, item)
		return
	}
	flagsJSON, _ := json.Marshal(ans.Flags)
	path, _ := writeAnswerFile(dir, q.Path, string(q.Type), q.Title, res.RunID, raw)
	item.Status = "answered"
	item.Confidence = ans.Confidence
	item.AnswerJSON = string(raw)
	item.FlagsJSON = string(flagsJSON)
	item.AnswerPath = path
	_ = o.st.UpdateAssignmentItem(item)
	o.emitItemDone(asgID, item)
}

func (o *Orchestrator) buildRegistry(q Question) *tools.Registry {
	reg := tools.NewRegistry(30 * time.Second)
	_ = reg.Register(NewSubmitAnswer(q))
	if o.opts.SafeMath != nil {
		_ = reg.Register(o.opts.SafeMath)
	}
	if o.opts.SearchTool != nil {
		_ = reg.Register(o.opts.SearchTool)
	}
	return reg
}

func (o *Orchestrator) emitItemDone(asgID string, item store.AssignmentItem) {
	flagCount := 0
	if item.FlagsJSON != "" {
		var fl []Flag
		_ = json.Unmarshal([]byte(item.FlagsJSON), &fl)
		flagCount = len(fl)
	}
	o.opts.Emit("assignment:item_done", map[string]any{
		"assignmentId": asgID, "seq": item.Seq, "status": item.Status,
		"confidence": item.Confidence, "flagCount": flagCount})
}

func hashManifest(l *Loaded) string {
	b, _ := json.Marshal(l.Manifest)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func titleFor(dir string, l *Loaded) string {
	if l.Manifest.GeneratedFrom != "" {
		return l.Manifest.GeneratedFrom
	}
	return dir
}
