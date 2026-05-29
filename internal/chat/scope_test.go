package chat

import (
	"context"
	"reflect"
	"testing"
)

type fakeResolver struct {
	entries []TextbookEntry
	err     error
}

func (f fakeResolver) Resolve(_ context.Context, _ string) ([]TextbookEntry, error) {
	return f.entries, f.err
}

func TestScopeResolverInterfaceShape(t *testing.T) {
	var _ ScopeResolver = fakeResolver{}
	want := []TextbookEntry{
		{Book: "intermediate-accounting", Chapters: []int{4, 5}},
		{Book: "tax-accounting", Chapters: nil},
	}
	got, err := fakeResolver{entries: want}.Resolve(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("want %v, got %v", want, got)
	}
}

func TestTextbookEntry_BookNames(t *testing.T) {
	entries := []TextbookEntry{
		{Book: "intermediate-accounting"},
		{Book: "tax-accounting", Chapters: []int{4}},
	}
	got := BookNames(entries)
	want := []string{"intermediate-accounting", "tax-accounting"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("want %v, got %v", want, got)
	}
}
