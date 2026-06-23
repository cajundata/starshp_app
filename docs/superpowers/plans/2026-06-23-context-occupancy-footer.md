# Context Occupancy Footer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split the conversation footer into current window occupancy (final provider call's input+output) and this-turn cumulative usage.

**Architecture:** The agentic loop already sums `totalUsage` across iterations. Add a `lastCall` local that tracks the final provider call's usage, emit two extra keys (`lastInput`, `lastOutput`) on the existing `chat:usage` event, and render occupancy vs this-turn in the footer. Ephemeral — no DB schema change, no appapi change.

**Tech Stack:** Go (backend chat loop, `internal/chat`), TypeScript/Vite (frontend, `frontend/src/main.ts`).

## Global Constraints

- Occupancy = `lastCall.InputTokens + lastCall.OutputTokens` where `lastCall` is the **terminal** provider call's usage.
- This-turn cumulative = existing `totalUsage` (summed across iterations) — retained, not replaced.
- No schema migration, no new DB columns; the persisted run still records cumulative totals only.
- No `internal/appapi` changes — `wailsSink.Emit` forwards all payload keys.
- No files under `internal/rag/{embedding,chunker,ragindex}/` are touched.
- Footer format (verbose): `context <occupancy>[ / <max>] · this turn <cumIn>→<cumOut> · cache <cumCached>`; `~` stale marker prefixes the occupancy number; drop ` / <max>` when `max_context` is 0.

---

### Task 1: Capture final-call usage and emit it on `chat:usage`

**Files:**
- Modify: `internal/chat/chat.go` — `runLoop` (line 191), `finalizeWithoutTools` (line 318), `completeRunSuccess` (line 372)
- Test: `internal/chat/chat_test.go`

**Interfaces:**
- Consumes: existing `provider.Usage{InputTokens, OutputTokens, CachedInputTokens}`; existing `scriptedProvider` / `captureSink` test helpers.
- Produces: `SinkUsage` payload gains integer keys `lastInput` and `lastOutput` (the terminal call's input/output). Existing keys `input`, `output`, `cached`, `modelID` unchanged. `completeRunSuccess` signature gains a `lastCall provider.Usage` parameter (inserted after `totalUsage`).

- [ ] **Step 1: Write the failing test**

Add to `internal/chat/chat_test.go`. A helper to pull the usage payload, plus three tests covering divergent tool turns, single-call equality, and final-call-without-usage degradation:

```go
// usagePayload returns the payload of the (last) SinkUsage event, or nil.
func usagePayload(sink *captureSink) map[string]any {
	var p map[string]any
	for _, e := range sink.events {
		if e.Kind == SinkUsage {
			p = e.Payload
		}
	}
	return p
}

func TestSend_ToolLoop_OccupancyVsCumulative(t *testing.T) {
	st := openStore(t)
	conv, _ := st.CreateConversation("c")
	svc := New(st)
	sink := &captureSink{}
	reg := tools.NewRegistry(time.Second)
	p1 := probe.New("p1", `{"type":"object"}`)
	p1.Out = "r1"
	_ = reg.Register(p1)

	prov := &scriptedProvider{iterations: [][]provider.Delta{
		{
			{ToolCall: &provider.ToolCall{ID: "c1", Name: "p1", Input: json.RawMessage(`{}`)}},
			{Done: true, StopReason: "tool_use",
				Usage: &provider.Usage{InputTokens: 50000, OutputTokens: 0}},
		},
		{
			{Text: "answer"},
			{Done: true, StopReason: "end_turn",
				Usage: &provider.Usage{InputTokens: 200000, OutputTokens: 2000}},
		},
	}}

	_, err := svc.Send(context.Background(), SendParams{
		ConversationID: conv.ID, UserText: "q",
		Model: "x", Provider: prov, Registry: reg,
		Resolver: emptyResolver{}, RetrievalMode: RetrievalAutoGroundedDefault,
		Sink: sink,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pl := usagePayload(sink)
	if pl == nil {
		t.Fatal("no usage event emitted")
	}
	// Cumulative (summed across iterations).
	if pl["input"] != 250000 || pl["output"] != 2000 {
		t.Errorf("cumulative: input=%v output=%v want 250000/2000", pl["input"], pl["output"])
	}
	// Final call only (occupancy basis).
	if pl["lastInput"] != 200000 || pl["lastOutput"] != 2000 {
		t.Errorf("final call: lastInput=%v lastOutput=%v want 200000/2000", pl["lastInput"], pl["lastOutput"])
	}
}

func TestSend_SingleCall_LastEqualsCumulative(t *testing.T) {
	st := openStore(t)
	conv, _ := st.CreateConversation("c")
	svc := New(st)
	sink := &captureSink{}
	reg := tools.NewRegistry(time.Second)

	_, err := svc.Send(context.Background(), SendParams{
		ConversationID: conv.ID, UserText: "hi",
		Model: "x", Provider: oneShotProvider{text: "hello"}, Registry: reg,
		Resolver: emptyResolver{}, RetrievalMode: RetrievalAutoGroundedDefault,
		Sink: sink,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pl := usagePayload(sink)
	// oneShotProvider reports Usage{Input:10, Output:5}.
	if pl["lastInput"] != 10 || pl["lastOutput"] != 5 {
		t.Errorf("lastInput=%v lastOutput=%v want 10/5", pl["lastInput"], pl["lastOutput"])
	}
	if pl["lastInput"] != pl["input"] || pl["lastOutput"] != pl["output"] {
		t.Errorf("single call: last should equal cumulative; got last=%v/%v cum=%v/%v",
			pl["lastInput"], pl["lastOutput"], pl["input"], pl["output"])
	}
}

func TestSend_FinalCallNoUsage_RetainsLastReported(t *testing.T) {
	st := openStore(t)
	conv, _ := st.CreateConversation("c")
	svc := New(st)
	sink := &captureSink{}
	reg := tools.NewRegistry(time.Second)
	p1 := probe.New("p1", `{"type":"object"}`)
	p1.Out = "r1"
	_ = reg.Register(p1)

	prov := &scriptedProvider{iterations: [][]provider.Delta{
		{
			{ToolCall: &provider.ToolCall{ID: "c1", Name: "p1", Input: json.RawMessage(`{}`)}},
			{Done: true, StopReason: "tool_use",
				Usage: &provider.Usage{InputTokens: 50000, OutputTokens: 1000}},
		},
		{
			{Text: "answer"},
			{Done: true, StopReason: "end_turn"}, // no Usage on the terminal call
		},
	}}

	_, err := svc.Send(context.Background(), SendParams{
		ConversationID: conv.ID, UserText: "q",
		Model: "x", Provider: prov, Registry: reg,
		Resolver: emptyResolver{}, RetrievalMode: RetrievalAutoGroundedDefault,
		Sink: sink,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pl := usagePayload(sink)
	// Final call reported no usage; lastCall retains the last call that did.
	if pl["lastInput"] != 50000 || pl["lastOutput"] != 1000 {
		t.Errorf("lastInput=%v lastOutput=%v want 50000/1000 (retained)", pl["lastInput"], pl["lastOutput"])
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/chat/ -run 'TestSend_ToolLoop_OccupancyVsCumulative|TestSend_SingleCall_LastEqualsCumulative|TestSend_FinalCallNoUsage_RetainsLastReported' -v`
Expected: FAIL — the payload has no `lastInput`/`lastOutput` keys, so `pl["lastInput"]` is `nil` and the comparisons fail (and `completeRunSuccess` does not yet accept `lastCall`, but the test only inspects the payload).

- [ ] **Step 3: Track `lastCall` in `runLoop`**

In `internal/chat/chat.go`, in `runLoop`, extend the var block (currently around line 199-203) to declare `lastCall`:

```go
	var (
		totalUsage     provider.Usage
		lastCall       provider.Usage
		totalToolCalls int
		catalog        []provider.ToolDef
	)
```

In the main stream loop's usage block (currently around line 245-249), set `lastCall` after the cumulative add:

```go
			if d.Usage != nil {
				totalUsage.InputTokens += d.Usage.InputTokens
				totalUsage.OutputTokens += d.Usage.OutputTokens
				totalUsage.CachedInputTokens += d.Usage.CachedInputTokens
				lastCall = *d.Usage
			}
```

Update the in-loop success return (currently line 264) to pass `lastCall`:

```go
			return s.completeRunSuccess(p, runID, turnID, stopReason, totalUsage,
				lastCall, totalToolCalls, iter)
```

- [ ] **Step 4: Track `lastCall` in `finalizeWithoutTools`**

In `finalizeWithoutTools`, declare a local `lastCall` in its var block (currently around line 340-343):

```go
	var (
		text      strings.Builder
		lastCall  provider.Usage
		streamErr error
	)
```

In its stream loop's usage block (currently around line 354-358), set it:

```go
		if d.Usage != nil {
			totalUsage.InputTokens += d.Usage.InputTokens
			totalUsage.OutputTokens += d.Usage.OutputTokens
			totalUsage.CachedInputTokens += d.Usage.CachedInputTokens
			lastCall = *d.Usage
		}
```

Update its return (currently line 369) to pass `lastCall`:

```go
	return s.completeRunSuccess(p, runID, turnID, "max_iterations", totalUsage, lastCall, totalToolCalls, maxIter+1)
```

- [ ] **Step 5: Add the `lastCall` parameter and emit the new keys in `completeRunSuccess`**

Change the signature (currently line 372-373) to insert `lastCall` after `totalUsage`:

```go
func (s *Service) completeRunSuccess(p SendParams, runID, turnID, stopReason string,
	totalUsage, lastCall provider.Usage, totalToolCalls, iter int) (RunResult, error) {
```

Add the two keys to the `SinkUsage` emit map (currently line 392-396):

```go
	emit(p.Sink, SinkUsage, p.ConversationID, runID, turnID,
		map[string]any{"input": totalUsage.InputTokens,
			"output":     totalUsage.OutputTokens,
			"cached":     totalUsage.CachedInputTokens,
			"lastInput":  lastCall.InputTokens,
			"lastOutput": lastCall.OutputTokens,
			"modelID":    p.Model}) // frontend footer resolves max_context by modelID
```

- [ ] **Step 6: Run the new tests to verify they pass**

Run: `go test ./internal/chat/ -run 'TestSend_ToolLoop_OccupancyVsCumulative|TestSend_SingleCall_LastEqualsCumulative|TestSend_FinalCallNoUsage_RetainsLastReported' -v`
Expected: PASS

- [ ] **Step 7: Run the full chat package to confirm no regression**

Run: `go test ./internal/chat/`
Expected: ok (the existing `TestSend_ToolCallLoop_*`, max-iterations, and error tests still pass; `completeRunSuccess` signature change compiles since both call sites were updated).

- [ ] **Step 8: Commit**

```bash
git add internal/chat/chat.go internal/chat/chat_test.go
git commit -m "feat(chat): emit final-call usage on chat:usage for window occupancy"
```

---

### Task 2: Render occupancy vs this-turn in the footer

**Files:**
- Modify: `frontend/src/main.ts` — `Usage` type (line 10), `updateFooter` (line 23-33), `chat:usage` handler (line 428)
- Modify: `docs/SMOKE.md` — context-tracking footer steps

**Interfaces:**
- Consumes: `chat:usage` event payload with `input`, `output`, `cached`, `modelID`, plus the new `lastInput`, `lastOutput` (from Task 1).
- Produces: footer string `context <occ>[ / <max>] · this turn <in>→<out> · cache <cached>`.

- [ ] **Step 1: Extend the `Usage` type**

In `frontend/src/main.ts`, line 10, add the two fields:

```ts
type Usage = { input: number; output: number; cached: number; lastInput: number; lastOutput: number; modelID: string; stale: boolean }
```

- [ ] **Step 2: Add the fields to the `chat:usage` handler's payload type**

In `frontend/src/main.ts`, the handler near line 428 — extend its inline payload type so the new fields type-check (the existing `{ ...p, stale: false }` already copies them into the map):

```ts
EventsOn('chat:usage', (p: { convID: string; input: number; output: number; cached: number; lastInput: number; lastOutput: number; modelID: string }) => {
  latestUsage.set(p.convID, { ...p, stale: false })
  if (p.convID === activeConv) {
    usagePendingForConv = null
    updateFooter()
  }
})
```

- [ ] **Step 3: Rewrite the footer render**

Replace the body of `updateFooter()` (line 23-33) render line with the occupancy/this-turn format. The full function:

```ts
function updateFooter() {
  const el = $('ctxFooter')
  if (!activeConv) { el.classList.add('hidden'); el.textContent = ''; return }
  const u = latestUsage.get(activeConv)
  if (!u) { el.classList.add('hidden'); el.textContent = ''; return }
  const max = modelMaxContext(u.modelID, cachedModels)
  const prefix = u.stale ? '~' : ''
  const denom = max > 0 ? ` / ${fmt(max)}` : ''
  const occ = (Number.isFinite(u.lastInput) && Number.isFinite(u.lastOutput))
    ? u.lastInput + u.lastOutput
    : u.input
  el.textContent = `context ${prefix}${fmt(occ)}${denom} · this turn ${fmt(u.input)}→${fmt(u.output)} · cache ${fmt(u.cached)}`
  el.classList.remove('hidden')
}
```

- [ ] **Step 4: Typecheck and build the frontend**

Run: `cd frontend && npm run build`
Expected: `tsc` passes (no type errors) and `vite build` completes. This compiles to `frontend/dist/`, replacing the interim `· out` bundle.

- [ ] **Step 5: Update the smoke doc**

In `docs/SMOKE.md`, under "Context tracking footer", replace the two format references and add a divergence step. Change step 20's format string:

```
The strip below the thread shows `context N / M · this turn I→O · cache K` (with denominator if the active model has `max_context` set in `models.yaml`).
```

Change step 23's format string:

```
Footer shows `context N · this turn I→O · cache K` (no `/ M` segment).
```

Add a new step after step 24:

```
24a. [ ] **Occupancy diverges from this-turn on tool turns.** Attach a textbook, ask a question that triggers a search (multi-iteration). The `context` occupancy number is visibly smaller than the `this turn` input (which sums every iteration). On a no-tool turn the occupancy ≈ this-turn input+output.
```

- [ ] **Step 6: Commit**

```bash
git add frontend/src/main.ts frontend/dist docs/SMOKE.md
git commit -m "feat(frontend): footer shows window occupancy vs this-turn cumulative"
```

---

## Self-Review

- **Spec coverage:** Occupancy = final input+output (Task 1 lastCall, Task 2 `occ`). Cumulative retained (`input`/`output`/`cached` kept). Ephemeral — no schema/appapi change (Global Constraints; Task 1 touches only `chat.go`). Verbose footer format (Task 2 Step 3). Edge cases: single-call equality (Task 1 Test 2), final-call-no-usage degradation (Task 1 Test 3), `max_context` missing → no denominator (Task 2 `denom`), `NaN` guard (Task 2 `occ` fallback). All spec sections map to a task.
- **Placeholder scan:** No TBD/TODO; all code blocks complete.
- **Type consistency:** `lastCall provider.Usage` parameter inserted after `totalUsage` at all three sites (decl, two call sites, signature). Frontend `lastInput`/`lastOutput` added to the `Usage` type, the handler payload type, and consumed in `updateFooter`. Payload keys `lastInput`/`lastOutput` match between Task 1 emit and Task 2 consumption.
