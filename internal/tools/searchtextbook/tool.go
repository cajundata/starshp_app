// Package searchtextbook implements the model-callable RAG escalation tool.
package searchtextbook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cajundata/starshp_app/internal/chat"
	"github.com/cajundata/starshp_app/internal/rag"
	"github.com/cajundata/starshp_app/internal/tools"
)

const description = `Search the user's attached accounting textbooks for relevant passages. Call this when the pre-turn grounding context (already in your prompt) is insufficient — when you need a different chapter, a specific rule the grounding did not cover, a follow-up lookup for a multi-step problem, or a check to verify a claim before answering. Each result has a stable source_id you can cite back to the user.`

const inputSchema = `{
  "type": "object",
  "properties": {
    "query":   {"type": "string", "minLength": 1},
    "book":    {"type": "string"},
    "chapter": {"type": ["string", "integer"]},
    "top_k":   {"type": "integer", "minimum": 1, "maximum": 10, "default": 5}
  },
  "required": ["query"],
  "additionalProperties": false
}`

// Retriever is the subset of rag.Adapter the tool needs. Defined here so tests
// can substitute a fake without touching the rag package.
type Retriever interface {
	Retrieve(ctx context.Context, query string, filters []rag.ScopeFilter, topK, budgetTokens int) (rag.RetrieveResult, error)
}

type Tool struct {
	retriever    Retriever
	resolver     chat.ScopeResolver
	outputCap    int
	budgetTokens int
}

func New(r Retriever, sr chat.ScopeResolver, outputCap int) *Tool {
	if outputCap <= 0 {
		outputCap = 4000
	}
	return &Tool{retriever: r, resolver: sr, outputCap: outputCap, budgetTokens: 2500}
}

func (Tool) Name() string                 { return "search_textbook" }
func (Tool) Description() string          { return description }
func (Tool) InputSchema() json.RawMessage { return json.RawMessage(inputSchema) }
func (Tool) Timeout() time.Duration       { return 0 } // registry default

func (t *Tool) Execute(ctx context.Context, ec tools.ExecContext, input json.RawMessage) (tools.ExecResult, error) {
	var args struct {
		Query   string          `json:"query"`
		Book    string          `json:"book"`
		Chapter json.RawMessage `json:"chapter"`
		TopK    int             `json:"top_k"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return tools.ExecResult{}, fmt.Errorf("search_textbook: invalid input json: %w", err)
	}
	if args.TopK == 0 {
		args.TopK = 5
	}
	if len(ec.TextbookScope) == 0 {
		return tools.ExecResult{}, fmt.Errorf("no_textbooks_attached: no textbooks are attached to this conversation")
	}
	if args.Book != "" && !contains(ec.TextbookScope, args.Book) {
		return tools.ExecResult{}, fmt.Errorf("invalid_book: %q is not attached to this conversation", args.Book)
	}

	chapter, err := parseChapterArg(args.Chapter)
	if err != nil {
		return tools.ExecResult{}, fmt.Errorf("search_textbook: %w", err)
	}

	entries, err := t.resolver.Resolve(ctx, ec.ConversationID)
	if err != nil {
		return tools.ExecResult{}, fmt.Errorf("search_textbook: resolve scope: %w", err)
	}

	filters := buildFilters(entries, args.Book, chapter)

	rres, err := t.retriever.Retrieve(ctx, args.Query, filters, args.TopK, t.budgetTokens)
	if err != nil {
		return tools.ExecResult{}, fmt.Errorf("rag_unavailable: %w", err)
	}

	formatted, truncated := formatResults(rres.Sources, rres.Context, t.outputCap)

	type src struct {
		ID        string  `json:"id"`
		Book      string  `json:"book"`
		Chapter   int     `json:"chapter"`
		Score     float64 `json:"score,omitempty"`
		ChunkHash string  `json:"chunk_hash,omitempty"`
	}
	metaSources := make([]src, 0, len(rres.Sources))
	for _, s := range rres.Sources {
		id := stableSourceID(s)
		metaSources = append(metaSources, src{
			ID: id, Book: s.Book, Chapter: s.Chapter, ChunkHash: chunkHash(s),
		})
	}
	sum := sha256.Sum256([]byte(formatted))
	meta, _ := json.Marshal(struct {
		Sources         []src  `json:"sources"`
		ResultHash      string `json:"result_hash"`
		QueryNormalized string `json:"query_normalized"`
		TopKRequested   int    `json:"top_k_requested"`
		TopKReturned    int    `json:"top_k_returned"`
		Truncated       bool   `json:"truncated"`
	}{metaSources, hex.EncodeToString(sum[:]), strings.TrimSpace(args.Query),
		args.TopK, len(rres.Sources), truncated})

	return tools.ExecResult{Output: formatted, Metadata: meta}, nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func parseChapterArg(raw json.RawMessage) (int, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	var asInt int
	if err := json.Unmarshal(raw, &asInt); err == nil {
		return asInt, nil
	}
	var asStr string
	if err := json.Unmarshal(raw, &asStr); err == nil {
		n := 0
		if _, err := fmt.Sscanf(asStr, "%d", &n); err != nil {
			return 0, fmt.Errorf("chapter must be an integer or numeric string; got %q", asStr)
		}
		return n, nil
	}
	return 0, fmt.Errorf("chapter must be an integer or numeric string")
}

// buildFilters builds the ScopeFilter slice for rag.Retrieve, honoring both
// the conversation's per-book chapter restrictions and the tool's optional
// book/chapter narrowing arguments.
func buildFilters(entries []chat.TextbookEntry, argBook string, argChapter int) []rag.ScopeFilter {
	if argBook != "" {
		chs := []int{}
		if argChapter > 0 {
			chs = []int{argChapter}
		} else {
			for _, e := range entries {
				if e.Book == argBook {
					chs = e.Chapters
					break
				}
			}
		}
		return []rag.ScopeFilter{{Book: argBook, Chapters: chs}}
	}
	out := make([]rag.ScopeFilter, 0, len(entries))
	for _, e := range entries {
		out = append(out, rag.ScopeFilter{Book: e.Book, Chapters: e.Chapters})
	}
	return out
}

func formatResults(sources []rag.Source, rawContext string, cap int) (string, bool) {
	// The rag.Adapter already returns a formatted "## <book> — Chapter N\n<text>\n\n" block.
	// We re-emit it with "## Source N [source_id: <id>] — <book> · Chapter N" headers so the
	// model has stable IDs to cite. Falls back to rawContext for chunks without a Source row.
	if len(sources) == 0 {
		if strings.TrimSpace(rawContext) == "" {
			return "(no results)", false
		}
		return capWithMarker(rawContext, cap)
	}
	blocks := strings.Split(strings.TrimSpace(rawContext), "\n\n")
	var sb strings.Builder
	for i, blk := range blocks {
		if i >= len(sources) {
			break
		}
		s := sources[i]
		lines := strings.SplitN(blk, "\n", 2)
		body := ""
		if len(lines) == 2 {
			body = lines[1]
		}
		fmt.Fprintf(&sb, "## Source %d [source_id: %s] — %s · Chapter %d\n%s\n\n",
			i+1, stableSourceID(s), s.Book, s.Chapter, body)
	}
	return capWithMarker(strings.TrimRight(sb.String(), "\n"), cap)
}

func capWithMarker(s string, cap int) (string, bool) {
	if len(s) <= cap {
		return s, false
	}
	return s[:cap] + "\n\n…(truncated; call again with a narrower query for more)\n", true
}

// stableSourceID derives chunk_<first16hex> from the chunk identity. We
// prefer the existing persistent ChunkID exposed by rag.Source; if absent
// fall back to a hash of (book, chapter, chunk-locator).
func stableSourceID(s rag.Source) string {
	base := s.ChunkID
	if base == "" {
		base = fmt.Sprintf("%s|%d", s.Book, s.Chapter)
	}
	sum := sha256.Sum256([]byte(base))
	return "chunk_" + hex.EncodeToString(sum[:8])
}

func chunkHash(s rag.Source) string {
	if s.ChunkID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s.ChunkID))
	return hex.EncodeToString(sum[:])
}
