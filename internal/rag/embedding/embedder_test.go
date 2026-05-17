package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// embeddingResponse mirrors the OpenAI embeddings API response structure.
type embeddingResponse struct {
	Object string              `json:"object"`
	Data   []embeddingDataItem `json:"data"`
	Model  string              `json:"model"`
}

type embeddingDataItem struct {
	Object    string    `json:"object"`
	Embedding []float64 `json:"embedding"`
	Index     int       `json:"index"`
}

func newMockEmbeddingServer(t *testing.T, dim int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody struct {
			Input []string `json:"input"`
			Model string   `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		data := make([]embeddingDataItem, len(reqBody.Input))
		for i := range reqBody.Input {
			vec := make([]float64, dim)
			for j := range vec {
				vec[j] = float64(i+1) * 0.01 * float64(j+1)
			}
			data[i] = embeddingDataItem{
				Object:    "embedding",
				Embedding: vec,
				Index:     i,
			}
		}

		resp := embeddingResponse{
			Object: "list",
			Data:   data,
			Model:  reqBody.Model,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestEmbedBatch_ReturnsCorrectNumberOfVectors(t *testing.T) {
	server := newMockEmbeddingServer(t, 4)
	defer server.Close()

	embedder := NewEmbedderWithBaseURL("test-key", "text-embedding-3-small", server.URL)
	texts := []string{"hello world", "accounting basics", "journal entries"}

	vectors, err := embedder.EmbedBatch(context.Background(), texts)
	require.NoError(t, err)
	assert.Len(t, vectors, 3)
	for _, v := range vectors {
		assert.Len(t, v, 4)
	}
}

func TestEmbedBatch_EmptyInput(t *testing.T) {
	embedder := NewEmbedder("test-key", "text-embedding-3-small")
	vectors, err := embedder.EmbedBatch(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, vectors)
}

func TestEmbedBatch_RejectsEmptyString(t *testing.T) {
	embedder := NewEmbedder("test-key", "text-embedding-3-small")
	_, err := embedder.EmbedBatch(context.Background(), []string{"hello", ""})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "input[1] is empty")
}

func TestEmbedBatch_RejectsWhitespaceOnly(t *testing.T) {
	embedder := NewEmbedder("test-key", "text-embedding-3-small")
	_, err := embedder.EmbedBatch(context.Background(), []string{"  \t\n  "})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "input[0] is empty")
}

func TestEmbedSingle_WrapsEmbedBatch(t *testing.T) {
	server := newMockEmbeddingServer(t, 4)
	defer server.Close()

	embedder := NewEmbedderWithBaseURL("test-key", "text-embedding-3-small", server.URL)

	vec, err := embedder.EmbedSingle(context.Background(), "hello world")
	require.NoError(t, err)
	assert.Len(t, vec, 4)
}
