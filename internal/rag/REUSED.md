# Reused from acctutor (verbatim — do not modify these files)

- chunker.ChunkFile(content string, meta chunker.ChapterMeta, maxTokens int) ([]chunker.Chunk, error)
- chunker.ChapterMeta{TextbookTitle, Edition string; ChapterNum int; ChapterTitle, SourceFile string}
- chunker.Chunk{ID, TextbookTitle, ChapterTitle string; ChapterNum int; SectionHeading, Subheading, Content string; TokenCount, ChunkOrder int; SourceFile, ChunkType, ParentSectionID string}
- embedding.NewEmbedder(apiKey, model string) embedding.Embedder
- embedding.NewEmbedderWithBaseURL(apiKey, model, baseURL string) embedding.Embedder
- embedding.Embedder.EmbedBatch(ctx, []string) ([][]float64, error)
- embedding.Embedder.EmbedSingle(ctx, string) ([]float64, error)
- ragindex.NewStore(dbPath string) (ragindex.Store, error)
- ragindex.Store.InsertChunks([]chunker.Chunk) error
- ragindex.Store.InsertEmbeddings(chunkIDs []string, vectors [][]float64) error
- ragindex.Store.QueryTopK(ctx, queryVec []float64, k int) ([]ragindex.ScoredChunk, error)
- ragindex.Store.GetMeta(key)/SetMeta(key,value)/Close()
- ragindex.ScoredChunk{ chunker.Chunk (embedded); Score float64 }

Any scope-aware query MUST be a NEW file in our copy, never a modification of an upstream file.
