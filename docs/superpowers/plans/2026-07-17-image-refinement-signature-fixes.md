# Image Refinement Signature Fixes (Smoke 73) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Nano Banana 2 refinement actually edit the prior image by persisting and echoing Gemini's image-part thought signatures (and real mime types) through the event log.

**Architecture:** Diagnosis (2026-07-17, live-API-verified): NB2 attaches a ~1.4MB `thoughtSignature` to its `inlineData` response part; multi-turn image editing requires echoing it verbatim in replayed history. Our adapter captured signatures only on `functionCall` parts, so refinement regenerated instead of editing (smoke 73). Also verified: NB2 returns `image/jpeg` (not PNG — the spec's PNG assumption was wrong), and its stream ends with a data-less part that 400s if echoed. Fix: `ImageBlob` carries `ThoughtSignature`; the chat engine persists signature+mime as JSON in the existing `tool_metadata` column on `assistant_image` rows (mirroring Spec A's tool-call signature pattern); replay echoes both. `maxInlineImages` drops 6 → 4 (each replayed image now adds ~1.9MB of base64 signature on top of ~0.8MB image; 6 would brush the ~20MB request ceiling). The asset handler serves a sniffed content type instead of hardcoded `image/png`.

**Tech Stack:** Go, `google.golang.org/genai` v1.63.0, modernc.org/sqlite, Wails v2.13.

**Related:** Spec `docs/superpowers/specs/2026-07-16-gemini-image-generation-design.md` (its "PNG is assumed, mime is not stored" decision is superseded by this plan — mime is now stored in event metadata; file names keep the `.png` suffix as a content-hash naming convention only).

## Global Constraints

- NEVER modify anything under `internal/rag/{chunker,embedding,ragindex}/`.
- Signature persistence format on `assistant_image` rows' `tool_metadata` (JSON): `{"thought_signature":"<base64 std-encoding>","mime":"image/jpeg"}` — `thought_signature` omitted when the part carried none; `mime` always present when known. Echo signatures VERBATIM (base64-decode exactly what was stored); never invent a value when absent — same rule as Spec A's tool-call signatures.
- Replay mime fallback when metadata absent (pre-fix rows): `image/png`, exactly today's behavior.
- `maxInlineImages` becomes 4. Placeholder text `[earlier image omitted]` unchanged.
- No schema migration — `tool_metadata` already exists on `conversation_events` and already flows through both replay queries into `ConversationEvent.ToolMetadata`.
- Every task ends with `go test ./...` green. Commits follow repo style with the Claude co-author trailer.

---

### Task F1: Signature + mime capture, persistence, replay echo

**Files:**
- Modify: `internal/provider/provider.go` (ImageBlob.ThoughtSignature)
- Modify: `internal/provider/gemini.go` (capture on InlineData; echo in contents)
- Modify: `internal/store/events.go` (`AppendAssistantImage` gains metadata param)
- Modify: `internal/chat/chat.go` (build metadata JSON on image delta; copy ToolMetadata for image rows in canonicalEvents; cap 6→4)
- Test: `internal/provider/gemini_test.go`, `internal/store/events_test.go`, `internal/chat/chat_test.go`, `internal/chat/canonical_events_test.go`

**Interfaces:**
- Consumes: existing `tool_metadata` column + `ConversationEvent.ToolMetadata` / `provider.Event.ToolMetadata` flow.
- Produces: `ImageBlob{MIME string; Data []byte; ThoughtSignature []byte}`; `AppendAssistantImage(convID, turnID, runID, imageHash string, metadata json.RawMessage) (ConversationEvent, error)`.

- [ ] **Step 1: Write the failing provider tests**

Append to `internal/provider/gemini_test.go`:

```go
func TestGeminiStreamImageDeltaCapturesSignature(t *testing.T) {
	// "c2lnLWltZy0x" is base64 for "sig-img-1".
	srv := newGeminiFake(t, []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"inlineData":{"mimeType":"image/jpeg","data":"aGVsbG8="},"thoughtSignature":"c2lnLWltZy0x"}]},"finishReason":"STOP"}]}`,
	}, nil)
	defer srv.Close()

	p := NewGemini("test-key", srv.URL, true)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model:  "gemini-3-pro-image",
		Events: []Event{{Kind: "user_message", Text: "draw"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var img *ImageBlob
	for d := range ch {
		if d.Err != nil {
			t.Fatalf("delta err: %v", d.Err)
		}
		if d.Image != nil {
			img = d.Image
		}
	}
	if img == nil || img.MIME != "image/jpeg" {
		t.Fatalf("image = %+v, want mime image/jpeg", img)
	}
	if string(img.ThoughtSignature) != "sig-img-1" {
		t.Fatalf("signature = %q, want sig-img-1", img.ThoughtSignature)
	}
}

func TestGeminiContentsAssistantImageEchoesSignatureAndMime(t *testing.T) {
	var body []byte
	srv := newGeminiFake(t, []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`,
	}, &body)
	defer srv.Close()

	p := NewGemini("test-key", srv.URL, true)
	ch, err := p.Stream(context.Background(), ChatRequest{
		Model: "gemini-3-pro-image",
		Events: []Event{
			{Kind: "user_message", Text: "draw"},
			{Kind: "assistant_image", ImageHash: strings.Repeat("a", 64),
				ImageData:    []byte{1, 2, 3},
				ToolMetadata: json.RawMessage(`{"thought_signature":"c2lnLWltZy0x","mime":"image/jpeg"}`)},
			{Kind: "user_message", Text: "make the sky darker"},
		},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range ch {
	}
	s := string(body)
	if !strings.Contains(s, `"thoughtSignature":"c2lnLWltZy0x"`) {
		t.Fatalf("request lacks echoed image thoughtSignature: %.400s", s)
	}
	if !strings.Contains(s, `"mimeType":"image/jpeg"`) {
		t.Fatalf("request lacks stored mime: %.400s", s)
	}
}

func TestGeminiContentsAssistantImageNoMetadataFallsBackToPNG(t *testing.T) {
	events := []Event{
		{Kind: "assistant_image", ImageHash: strings.Repeat("a", 64), ImageData: []byte{1}},
	}
	got := geminiContentsFromEvents(events)
	if len(got) != 1 || got[0].Parts[0].InlineData == nil {
		t.Fatalf("contents = %+v", got)
	}
	if got[0].Parts[0].InlineData.MIMEType != "image/png" {
		t.Fatalf("mime = %q, want image/png fallback", got[0].Parts[0].InlineData.MIMEType)
	}
	if len(got[0].Parts[0].ThoughtSignature) != 0 {
		t.Fatal("must not invent a signature when none stored")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/provider/ -run 'CapturesSignature|EchoesSignature|FallsBackToPNG' -v`
Expected: FAIL — `ThoughtSignature` undefined on ImageBlob; body lacks signature.

- [ ] **Step 3: Implement the provider side**

`provider.go` — extend ImageBlob:

```go
// ImageBlob is one generated image emitted mid-stream by an image-capable
// provider. Data is the raw (already base64-decoded) file bytes.
// ThoughtSignature is Gemini's encrypted reasoning-state token attached to
// the image part; multi-turn image editing requires echoing it verbatim on
// replay, or the model regenerates instead of editing (smoke 73).
type ImageBlob struct {
	MIME             string
	Data             []byte
	ThoughtSignature []byte
}
```

`gemini.go` — the InlineData case captures the signature:

```go
					case part.InlineData != nil:
						img := &ImageBlob{MIME: part.InlineData.MIMEType, Data: part.InlineData.Data}
						if len(part.ThoughtSignature) > 0 {
							img.ThoughtSignature = part.ThoughtSignature
						}
						select {
						case out <- Delta{Image: img}:
						case <-ctx.Done():
							return
						}
```

`geminiContentsFromEvents` — the assistant_image case echoes stored signature + mime (mirror the existing assistant_tool_call metadata decode):

```go
		case "assistant_image":
			// Inflated bytes replay inline so refinement edits the actual image;
			// an event without bytes (beyond the cap, or file deleted) degrades
			// to a placeholder the model can still anchor ordering on. The stored
			// thought signature is echoed verbatim — never invented — or the
			// model loses its reasoning chain and regenerates (smoke 73).
			if len(e.ImageData) > 0 {
				mime := "image/png"
				part := &genai.Part{}
				if len(e.ToolMetadata) > 0 {
					var meta struct {
						ThoughtSignature string `json:"thought_signature"`
						MIME             string `json:"mime"`
					}
					if err := json.Unmarshal(e.ToolMetadata, &meta); err == nil {
						if meta.MIME != "" {
							mime = meta.MIME
						}
						if meta.ThoughtSignature != "" {
							if sig, derr := base64.StdEncoding.DecodeString(meta.ThoughtSignature); derr == nil {
								part.ThoughtSignature = sig
							}
						}
					}
				}
				part.InlineData = &genai.Blob{MIMEType: mime, Data: e.ImageData}
				appendPart(genai.RoleModel, part)
			} else {
				appendPart(genai.RoleModel, genai.NewPartFromText("[earlier image omitted]"))
			}
```

- [ ] **Step 4: Provider green**

Run: `go test ./internal/provider/ -v`
Expected: PASS (all, including pre-existing image tests — `TestGeminiContentsAssistantImage` keeps passing because metadata-less events still fall back to PNG with no signature).

- [ ] **Step 5: Write the failing store + chat tests**

`internal/store/events_test.go` — extend the existing `TestAppendAssistantImageAndReplay` (or add alongside, matching its helper):

```go
func TestAppendAssistantImageMetadataRoundTrip(t *testing.T) {
	s := openTestStore(t) // match the file's existing helper name
	conv, _ := s.CreateConversation("c")
	u, _ := s.AppendUserMessage(conv.ID, "draw")
	runID := "run-img-meta"
	if err := s.CreateRun(conv.ID, u.TurnID, runID, "gemini", "gemini-3-pro-image", "auto_grounded_default", "artist"); err != nil {
		t.Fatal(err)
	}
	meta := json.RawMessage(`{"thought_signature":"c2ln","mime":"image/jpeg"}`)
	if _, err := s.AppendAssistantImage(conv.ID, u.TurnID, runID, strings.Repeat("ab", 32), meta); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteRun(runID, RunTotals{}, "end_turn"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.GetProviderReplayEvents(conv.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || string(rows[1].ToolMetadata) != string(meta) {
		t.Fatalf("replayed metadata = %s, want %s", rows[1].ToolMetadata, meta)
	}
	// nil metadata stores NULL and replays empty.
	u2, _ := s.AppendUserMessage(conv.ID, "again")
	runID2 := "run-img-meta-2"
	_ = s.CreateRun(conv.ID, u2.TurnID, runID2, "gemini", "gemini-3-pro-image", "auto_grounded_default", "artist")
	if _, err := s.AppendAssistantImage(conv.ID, u2.TurnID, runID2, strings.Repeat("cd", 32), nil); err != nil {
		t.Fatal(err)
	}
}
```

`internal/chat/chat_test.go` — extend the image-delta Send test path with a signature-bearing image (add a new test, reusing `fakeImages`/`scriptedProvider`):

```go
func TestSend_ImageDeltaPersistsSignatureMetadata(t *testing.T) {
	st := openStore(t)
	conv, _ := st.CreateConversation("c")
	svc := New(st)
	images := newFakeImages()
	prov := &scriptedProvider{iterations: [][]provider.Delta{{
		{Image: &provider.ImageBlob{MIME: "image/jpeg", Data: []byte("png-1"),
			ThoughtSignature: []byte("sig-img-1")}},
		{Done: true, StopReason: "end_turn"},
	}}}
	_, err := svc.Send(context.Background(), SendParams{
		ConversationID: conv.ID, UserText: "draw", Model: "gemini-3-pro-image",
		Provider: prov, Registry: tools.NewRegistry(time.Second),
		Resolver: emptyResolver{}, RetrievalMode: RetrievalAutoGroundedDefault,
		Sink: &captureSink{}, Images: images,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	evs, _ := st.GetProviderReplayEvents(conv.ID, "")
	if len(evs) != 2 || evs[1].Kind != store.EventKindAssistantImage {
		t.Fatalf("events = %+v", evs)
	}
	var meta struct {
		ThoughtSignature string `json:"thought_signature"`
		MIME             string `json:"mime"`
	}
	if err := json.Unmarshal(evs[1].ToolMetadata, &meta); err != nil {
		t.Fatalf("metadata unmarshal: %v (raw %s)", err, evs[1].ToolMetadata)
	}
	if meta.MIME != "image/jpeg" {
		t.Fatalf("mime = %q", meta.MIME)
	}
	if got, _ := base64.StdEncoding.DecodeString(meta.ThoughtSignature); string(got) != "sig-img-1" {
		t.Fatalf("signature = %q, want sig-img-1", got)
	}
}
```

`internal/chat/canonical_events_test.go` — metadata flows to the provider event for own image rows:

```go
func TestCanonicalEvents_OwnImageCarriesMetadata(t *testing.T) {
	st := openStore(t)
	conv, _ := st.CreateConversation("c")
	u, _ := st.AppendUserMessage(conv.ID, "draw")
	runID := uuid.NewString()
	_ = st.CreateRun(conv.ID, u.TurnID, runID, "gemini", "gemini-3-pro-image", "auto_grounded_default", "artist")
	meta := json.RawMessage(`{"thought_signature":"c2ln","mime":"image/jpeg"}`)
	if _, err := st.AppendAssistantImage(conv.ID, u.TurnID, runID, strings.Repeat("aa", 32), meta); err != nil {
		t.Fatal(err)
	}
	_ = st.CompleteRun(runID, store.RunTotals{}, "end_turn")
	turnID, curRunID := currentTurn(t, st, conv.ID, "darker", "artist", "gemini-3-pro-image")

	rows, err := st.GetProviderReplayEvents(conv.ID, curRunID)
	if err != nil {
		t.Fatal(err)
	}
	got := canonicalEvents(rows, turnID, "artist", nil)
	var imgEv *provider.Event
	for i := range got {
		if got[i].Kind == store.EventKindAssistantImage {
			imgEv = &got[i]
		}
	}
	if imgEv == nil || string(imgEv.ToolMetadata) != string(meta) {
		t.Fatalf("image event metadata = %+v, want %s", imgEv, meta)
	}
}
```

Also update the existing `imageTurn` helper and any existing `AppendAssistantImage` call sites in tests to pass `nil` metadata.

- [ ] **Step 6: Run to verify failure**

Run: `go test ./internal/store/ ./internal/chat/ -run 'ImageMetadata|SignatureMetadata|OwnImageCarriesMetadata' -v`
Expected: FAIL — `AppendAssistantImage` arity.

- [ ] **Step 7: Implement store + chat**

`internal/store/events.go` — `AppendAssistantImage` gains metadata (NULL when empty, mirroring `AppendAssistantToolCall`):

```go
// AppendAssistantImage persists one generated image the model emitted, by
// content hash. The bytes live in the imagestore (<app-dir>/images/); the
// event log carries only the reference, so a deleted file degrades to a
// placeholder rather than corrupting replay. metadata is a nullable
// provider-opaque payload ({"thought_signature":..., "mime":...}) written
// verbatim to tool_metadata and echoed on provider replay — Gemini image
// models require the signature back for multi-turn editing.
func (s *Store) AppendAssistantImage(convID, turnID, runID, imageHash string, metadata json.RawMessage) (ConversationEvent, error) {
	id := uuid.NewString()
	seq, err := nextSequenceIndex(s, convID)
	if err != nil {
		return ConversationEvent{}, err
	}
	ev := ConversationEvent{
		ID: id, ConversationID: convID, TurnID: turnID, RunID: runID,
		SequenceIndex: seq, Kind: EventKindAssistantImage, ImageHash: imageHash,
		ToolMetadata: metadata,
		CreatedAt:    time.Now().UnixMilli(),
	}
	var metaArg any
	if len(metadata) > 0 {
		metaArg = string(metadata)
	}
	_, err = s.db.Exec(
		`INSERT INTO conversation_events
            (id, conversation_id, turn_id, run_id, sequence_index, kind,
             image_hash, tool_metadata, is_error, created_at)
         VALUES (?,?,?,?,?,?,?,?,0,?)`,
		ev.ID, convID, turnID, runID, ev.SequenceIndex, ev.Kind, imageHash,
		metaArg, ev.CreatedAt)
	return ev, err
}
```

`internal/chat/chat.go` — build the metadata at the image-delta site (inside the `d.Image != nil` block, replacing the current Put/Append/emit sequence's append call):

```go
				if persistErr == nil {
					hash, err := putImage(p.Images, d.Image.Data)
					if err != nil {
						persistErr, persistCode = err, "persist_image"
					} else if _, err := s.st.AppendAssistantImage(p.ConversationID, turnID, runID, hash,
						imageEventMetadata(d.Image)); err != nil {
						persistErr, persistCode = err, "persist_image"
					} else {
						emit(p.Sink, SinkImage, p.ConversationID, runID, turnID,
							map[string]any{"hash": hash})
					}
				}
```

Add near `putImage`:

```go
// imageEventMetadata packages the provider-opaque per-image payload persisted
// to tool_metadata: the real mime type and, when present, Gemini's thought
// signature (base64) — echoed verbatim on replay for multi-turn editing.
func imageEventMetadata(img *provider.ImageBlob) json.RawMessage {
	m := map[string]string{}
	if img.MIME != "" {
		m["mime"] = img.MIME
	}
	if len(img.ThoughtSignature) > 0 {
		m["thought_signature"] = base64.StdEncoding.EncodeToString(img.ThoughtSignature)
	}
	if len(m) == 0 {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return b
}
```

(import `encoding/base64`.)

`canonicalEvents` — the whitelist case copies metadata for image rows too. Change:

```go
				if r.Kind == store.EventKindAssistantToolCall || r.Kind == store.EventKindAssistantImage {
					ev.ToolMetadata = r.ToolMetadata
				}
```

Cap: change the constant and its comment:

```go
// maxInlineImages caps how many of the persona's own prior images ride back
// into provider context as inline bytes (newest first). Each replayed image
// carries its ~1.4MB thought signature (~1.9MB as base64) on top of ~1MB of
// image data, and Gemini's inline request payload tops out around 20 MB —
// 4 keeps comfortable headroom. Older images degrade to a textual
// placeholder instead of hard-failing the call.
const maxInlineImages = 4
```

Update the existing inflate test if it asserts 6 (`TestInflateImages_CapNewestAndSkipMissing` calls `inflateImages(events, images, 6)` explicitly — the function under test takes the cap as a parameter, so it needs no change; only update it if it references the constant).

- [ ] **Step 8: Green + full suite**

Run: `go test ./internal/store/ ./internal/chat/ ./internal/provider/ -v`, then `go test ./...`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/provider/ internal/store/ internal/chat/
git commit -m "fix(images): persist and echo Gemini image thought signatures + real mime — refinement edits instead of regenerating

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task F2: Sniffed content type, backlog, spec addendum

**Files:**
- Modify: `internal/imagestore/imagestore.go` (Handler serves sniffed mime)
- Modify: `BACKLOG.md` (multimodal-baton Someday line)
- Modify: `docs/superpowers/specs/2026-07-16-gemini-image-generation-design.md` (post-smoke corrections addendum)
- Test: `internal/imagestore/imagestore_test.go`

- [ ] **Step 1: Failing test**

Append to `internal/imagestore/imagestore_test.go`:

```go
func TestHandlerServesSniffedContentType(t *testing.T) {
	s := newStore(t)
	// Minimal JPEG magic — enough for http.DetectContentType.
	jpeg := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, []byte("fakejpegbody")...)
	hash, _ := s.Put(jpeg)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/appimages/" + hash + ".png")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("Content-Type = %q, want image/jpeg (sniffed)", ct)
	}
}
```

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/imagestore/ -run Sniffed -v`
Expected: FAIL — got image/png.

- [ ] **Step 3: Implement**

In `Handler`, replace the hardcoded header:

```go
		// Nano Banana 2 emits JPEG despite the .png content-hash naming
		// convention — sniff the real type rather than trusting the suffix.
		w.Header().Set("Content-Type", http.DetectContentType(data))
```

Update the existing `TestHandlerServesStoredImage` expectation: its body `"png-payload"` sniffs as `text/plain; charset=utf-8` — change that assertion accordingly (or use real PNG magic `\x89PNG\r\n\x1a\n` in the fixture and keep expecting `image/png`; prefer the PNG-magic fixture).

- [ ] **Step 4: Green**

Run: `go test ./internal/imagestore/ -v`
Expected: PASS.

- [ ] **Step 5: Docs**

`BACKLOG.md` Someday — add:

```markdown
- Multimodal baton: pass real image bytes to text personas whose models declare image input (smoke 74 — critique personas currently see only the textual image note) — Spec B deferred.
```

Spec doc — append at the end of the Decisions section:

```markdown
- **Post-smoke corrections (2026-07-17, smoke 73):** Nano Banana 2 attaches a
  thought signature to its image parts; multi-turn editing requires echoing it
  verbatim, so `assistant_image` rows persist `{"thought_signature","mime"}` in
  the existing `tool_metadata` column and the adapter echoes both on replay.
  NB2 also emits `image/jpeg`, superseding "PNG is assumed" — the mime is
  stored in metadata and the asset handler sniffs the served content type;
  `.png` file naming remains a content-hash convention only. The refinement
  cap dropped 6 → 4 to keep signature-laden requests under the payload limit.
```

- [ ] **Step 6: Full suite + build + commit**

Run: `go test ./... && wails build`
Expected: PASS / builds (chmod 644 any wailsjs churn; content changes unlikely — Go-only).

```bash
git add internal/imagestore/ BACKLOG.md docs/superpowers/specs/2026-07-16-gemini-image-generation-design.md
git commit -m "fix(imagestore): serve sniffed content type; docs: smoke-73/74 corrections and multimodal-baton deferral

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Verification gate

`go test ./...`; `wails build`; then operator re-smoke of docs/SMOKE.md step 73 (refinement must edit the prior image — same subject, requested change applied) with 74 recorded as pass-by-design. The diagnostic harnesses (`internal/chat/debug_smoke73_test.go`, `internal/provider/debug_smoke73_live_test.go`) are deleted after the fix is verified — never committed.
