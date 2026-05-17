// Package embedding wraps the OpenAI embeddings API for batch and single-text
// embedding operations used by the RAG index pipeline.
package embedding

import (
	"context"
	"fmt"
	"strings"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// Embedder calls the OpenAI embeddings API to convert text into float64 vectors.
type Embedder struct {
	client *openai.Client
	model  string
}

// maxBatchSize limits the number of texts sent in a single API call
// to avoid rate limit errors (OpenAI limit: 2048 inputs per request).
const maxBatchSize = 100

// NewEmbedder creates an Embedder with the given API key and model name.
func NewEmbedder(apiKey, model string) Embedder {
	client := openai.NewClient(option.WithAPIKey(apiKey))
	return Embedder{client: &client, model: model}
}

// NewEmbedderWithBaseURL creates an Embedder pointing at a custom base URL.
// Used for testing with httptest servers.
func NewEmbedderWithBaseURL(apiKey, model, baseURL string) Embedder {
	client := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(baseURL),
	)
	return Embedder{client: &client, model: model}
}

// EmbedBatch embeds multiple texts in batches of up to maxBatchSize.
// Returns one float64 vector per input text, in the same order.
// Returns an error if any input text is empty (the OpenAI API rejects empty strings).
func (e Embedder) EmbedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	for i, t := range texts {
		if strings.TrimSpace(t) == "" {
			return nil, fmt.Errorf("embed batch: input[%d] is empty (OpenAI rejects empty strings)", i)
		}
	}

	allVectors := make([][]float64, 0, len(texts))

	for start := 0; start < len(texts); start += maxBatchSize {
		end := start + maxBatchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[start:end]

		resp, err := e.client.Embeddings.New(ctx, openai.EmbeddingNewParams{
			Input: openai.EmbeddingNewParamsInputUnion{
				OfArrayOfStrings: batch,
			},
			Model: openai.EmbeddingModel(e.model),
		})
		if err != nil {
			return nil, fmt.Errorf("embed batch: %w", err)
		}

		for _, emb := range resp.Data {
			allVectors = append(allVectors, emb.Embedding)
		}
	}

	return allVectors, nil
}

// EmbedSingle embeds a single text string. Convenience wrapper around EmbedBatch.
func (e Embedder) EmbedSingle(ctx context.Context, text string) ([]float64, error) {
	vecs, err := e.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("embed single: no vectors returned")
	}
	return vecs[0], nil
}
