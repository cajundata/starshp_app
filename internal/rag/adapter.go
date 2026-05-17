// Package rag is the ONLY entry point app code uses for retrieval. It wraps
// the verbatim-copied acctutor packages (embedding, chunker, ragindex).
package rag

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/cajundata/discussion_engine/internal/rag/chunker"
	"github.com/cajundata/discussion_engine/internal/rag/embedding"
	"github.com/cajundata/discussion_engine/internal/rag/ragindex"
	"github.com/cajundata/discussion_engine/internal/textbooks"
)

const maxChunkTokens = 800

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
