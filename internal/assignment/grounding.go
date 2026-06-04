package assignment

import (
	"context"

	"github.com/cajundata/starshp_app/internal/chat"
)

// GroundingSource is the pluggable grounding seam. Ensure prepares any backing
// index (idempotent); Retriever returns the chat.Retriever used for pre-turn
// grounding, or nil for no grounding. v1 ships NoGrounding and StaticGrounding;
// a future lesson/content source implements the same interface.
type GroundingSource interface {
	Ensure(ctx context.Context) error
	Retriever() chat.Retriever
}

// NoGrounding disables pre-turn retrieval (model knowledge + any model-called
// tools only).
type NoGrounding struct{}

func (NoGrounding) Ensure(context.Context) error { return nil }
func (NoGrounding) Retriever() chat.Retriever    { return nil }

// StaticGrounding wraps an already-prepared chat.Retriever (e.g. the appapi
// textbook retriever for attached books, whose index EnsureIndexed already
// built). Ensure is a no-op because indexing happens in appapi.
type StaticGrounding struct{ R chat.Retriever }

func (StaticGrounding) Ensure(context.Context) error { return nil }
func (g StaticGrounding) Retriever() chat.Retriever  { return g.R }
