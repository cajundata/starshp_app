package assignment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
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

// Run loads the companion directory, creates the assignment + item rows, then
// solves each question sequentially. (Concurrency + cancellation arrive in a
// follow-up task.)
func (o *Orchestrator) Run(ctx context.Context, dir string) (string, error) {
	loaded, err := Load(dir)
	if err != nil {
		return "", err
	}
	if err := o.opts.Grounding.Ensure(ctx); err != nil {
		return "", fmt.Errorf("grounding: %w", err)
	}
	asgID := uuid.NewString()
	asg := store.Assignment{
		ID: asgID, SourceDir: dir, Title: titleFor(dir, loaded),
		ManifestHash: hashManifest(loaded), Model: o.opts.Model,
		Status: "in_progress", TotalItems: len(loaded.Questions),
	}
	if err := o.st.CreateAssignment(asg); err != nil {
		return "", err
	}
	o.opts.Emit("assignment:started", map[string]any{
		"assignmentId": asgID, "total": len(loaded.Questions), "title": asg.Title})

	for i, q := range loaded.Questions {
		itemID := uuid.NewString()
		if err := o.st.CreateAssignmentItem(store.AssignmentItem{
			ID: itemID, AssignmentID: asgID, Seq: i, SourcePath: q.Path,
			Type: string(q.Type), Title: q.Title, Status: "pending",
		}); err != nil {
			slog.Error("assignment: create item failed; skipping",
				"assignmentId", asgID, "seq", i, "path", q.Path, "err", err)
			continue
		}
		o.solveItem(ctx, dir, asgID, itemID, i, q)
	}

	_ = o.st.UpdateAssignmentStatus(asgID, "completed")
	o.opts.Emit("assignment:completed", map[string]any{"assignmentId": asgID})
	return asgID, nil
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
