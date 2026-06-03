// Package rag is the ONLY entry point app code uses for retrieval. It wraps
// the verbatim-copied acctutor packages (embedding, chunker, ragindex).
package rag

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/cajundata/starshp_app/internal/rag/chunker"
	"github.com/cajundata/starshp_app/internal/rag/embedding"
	"github.com/cajundata/starshp_app/internal/rag/ragindex"
	"github.com/cajundata/starshp_app/internal/textbooks"
)

const maxChunkTokens = 800

// maxQueryTokens caps the text embedded for a retrieval query. OpenAI's
// embedding models reject any single input over 8192 tokens; we truncate with
// margin so pasting a large document as a question degrades to best-effort
// grounding instead of failing the whole turn.
const maxQueryTokens = 8000

type Options struct {
	RAGDBPath      string
	EmbeddingModel string
	OpenAIKey      string
	OpenAIBaseURL  string // empty = default OpenAI endpoint
}

type Adapter struct {
	store    ragindex.Store
	embedder embedding.Embedder
}

type IndexResult struct {
	ChunksIndexed   int  `json:"chunksIndexed"`
	SkippedUpToDate bool `json:"skippedUpToDate"`
}

func NewAdapter(o Options) (*Adapter, error) {
	st, err := ragindex.NewStore(o.RAGDBPath)
	if err != nil {
		return nil, fmt.Errorf("open rag store: %w", err)
	}
	var emb embedding.Embedder
	if o.OpenAIBaseURL != "" {
		emb = embedding.NewEmbedderWithBaseURL(o.OpenAIKey, o.EmbeddingModel, o.OpenAIBaseURL)
	} else {
		emb = embedding.NewEmbedder(o.OpenAIKey, o.EmbeddingModel)
	}
	return &Adapter{store: st, embedder: emb}, nil
}

func (a *Adapter) Close() error { return a.store.Close() }

func bookHash(b textbooks.Book) (string, error) {
	h := sha256.New()
	for _, ch := range b.Chapters {
		data, err := os.ReadFile(ch.Path)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%d:%x\n", ch.Num, sha256.Sum256(data))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// IndexBook chunks+embeds+stores all chapters of book unless the content hash
// is unchanged since last index. progress (may be nil) is called per chapter.
func (a *Adapter) IndexBook(ctx context.Context, book textbooks.Book, progress func(done, total int)) (IndexResult, error) {
	hash, err := bookHash(book)
	if err != nil {
		return IndexResult{}, err
	}
	metaKey := "book_hash:" + book.Name
	if prev, _ := a.store.GetMeta(metaKey); prev == hash {
		return IndexResult{SkippedUpToDate: true}, nil
	}
	total := len(book.Chapters)
	indexed := 0
	for i, ch := range book.Chapters {
		content, err := os.ReadFile(ch.Path)
		if err != nil {
			return IndexResult{}, err
		}
		chunks, err := chunker.ChunkFile(string(content), chunker.ChapterMeta{
			TextbookTitle: book.Name,
			ChapterNum:    ch.Num,
			ChapterTitle:  fmt.Sprintf("Chapter %d", ch.Num),
			SourceFile:    ch.Path,
		}, maxChunkTokens)
		if err != nil {
			return IndexResult{}, fmt.Errorf("chunk ch%d: %w", ch.Num, err)
		}
		if len(chunks) == 0 {
			continue
		}
		texts := make([]string, len(chunks))
		ids := make([]string, len(chunks))
		for j, c := range chunks {
			texts[j] = c.Content
			ids[j] = c.ID
		}
		vecs, err := a.embedder.EmbedBatch(ctx, texts)
		if err != nil {
			return IndexResult{}, fmt.Errorf("embed ch%d: %w", ch.Num, err)
		}
		if err := a.store.InsertChunks(chunks); err != nil {
			return IndexResult{}, err
		}
		if err := a.store.InsertEmbeddings(ids, vecs); err != nil {
			return IndexResult{}, err
		}
		indexed += len(chunks)
		if progress != nil {
			progress(i+1, total)
		}
	}
	if err := a.store.SetMeta(metaKey, hash); err != nil {
		return IndexResult{}, err
	}
	return IndexResult{ChunksIndexed: indexed}, nil
}

const overfetchFactor = 6

type ScopeFilter struct {
	Book     string `json:"book"`
	Chapters []int  `json:"chapters"` // nil/empty = whole book
}

type Source struct {
	Book    string `json:"book"`
	Chapter int    `json:"chapter"`
	ChunkID string `json:"chunkId"`
}

type RetrieveResult struct {
	Context string   `json:"context"`
	Sources []Source `json:"sources"`
}

func inScope(book string, chapter int, filters []ScopeFilter) bool {
	for _, f := range filters {
		if f.Book != book {
			continue
		}
		if len(f.Chapters) == 0 {
			return true
		}
		for _, c := range f.Chapters {
			if c == chapter {
				return true
			}
		}
	}
	return false
}

// Retrieve embeds query, fetches topK*overfetch candidates, filters to the
// given scope, trims to budgetTokens, and formats a context block. With no
// scope filters it returns an empty result (RAG skipped) and no error.
func (a *Adapter) Retrieve(ctx context.Context, query string, filters []ScopeFilter, topK, budgetTokens int) (RetrieveResult, error) {
	if len(filters) == 0 {
		return RetrieveResult{}, nil
	}
	capped, _, err := chunker.TruncateToTokens(query, maxQueryTokens)
	if err != nil {
		return RetrieveResult{}, fmt.Errorf("cap query: %w", err)
	}
	qv, err := a.embedder.EmbedSingle(ctx, capped)
	if err != nil {
		return RetrieveResult{}, fmt.Errorf("embed query: %w", err)
	}
	cands, err := a.store.QueryTopK(ctx, qv, topK*overfetchFactor)
	if err != nil {
		return RetrieveResult{}, fmt.Errorf("query topk: %w", err)
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].Score > cands[j].Score })

	var b strings.Builder
	var sources []Source
	used, tokens := 0, 0
	for _, sc := range cands {
		if used >= topK {
			break
		}
		if !inScope(sc.TextbookTitle, sc.ChapterNum, filters) {
			continue
		}
		if tokens+sc.TokenCount > budgetTokens && used > 0 {
			break
		}
		fmt.Fprintf(&b, "## %s — Chapter %d\n%s\n\n", sc.TextbookTitle, sc.ChapterNum, sc.Content)
		sources = append(sources, Source{Book: sc.TextbookTitle, Chapter: sc.ChapterNum, ChunkID: sc.ID})
		tokens += sc.TokenCount
		used++
	}
	out := strings.TrimSpace(b.String())
	if out != "" && tokens > budgetTokens && len(out) > budgetTokens*4 {
		out = out[:budgetTokens*4] // hard char cap as a budget backstop
	}
	return RetrieveResult{Context: out, Sources: sources}, nil
}
