package assignment

import (
	"context"
	"testing"

	"github.com/cajundata/starshp_app/internal/chat"
)

func TestNoGrounding_ReturnsNilRetriever(t *testing.T) {
	g := NoGrounding{}
	if err := g.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if g.Retriever() != nil {
		t.Fatal("NoGrounding must provide a nil retriever")
	}
}

// fakeRetriever proves a GroundingSource can supply a chat.Retriever.
type fakeRetriever struct{}

func (fakeRetriever) Retrieve(_ context.Context, _ string) (string, string, []chat.RetrievedSource, error) {
	return "ctx", "[]", nil, nil
}

func TestStaticGrounding_SuppliesRetriever(t *testing.T) {
	g := StaticGrounding{R: fakeRetriever{}}
	if err := g.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if g.Retriever() == nil {
		t.Fatal("StaticGrounding must provide its retriever")
	}
}
