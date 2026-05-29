package chat

import (
	"testing"
)

func TestResolveRetrievalMode_RespectsArgument(t *testing.T) {
	got := ResolveRetrievalMode(RetrievalAutoGroundedDefault, func(string) string { return "" })
	if got != RetrievalAutoGroundedDefault {
		t.Fatalf("want %q, got %q", RetrievalAutoGroundedDefault, got)
	}
}

func TestResolveRetrievalMode_EnvOverrideForcesNoRetrieval(t *testing.T) {
	getenv := func(k string) string {
		if k == "STARSHP_SKIP_AUTO_GROUNDING" {
			return "1"
		}
		return ""
	}
	got := ResolveRetrievalMode(RetrievalAutoGroundedDefault, getenv)
	if got != RetrievalNoRetrieval {
		t.Fatalf("env override should force no_retrieval; got %q", got)
	}
}

func TestResolveRetrievalMode_EnvUnsetIgnored(t *testing.T) {
	getenv := func(k string) string {
		if k == "STARSHP_SKIP_AUTO_GROUNDING" {
			return "0"
		}
		return ""
	}
	got := ResolveRetrievalMode(RetrievalAgenticOnly, getenv)
	if got != RetrievalAgenticOnly {
		t.Fatalf("env=0 must not override; got %q", got)
	}
}

func TestRetrievalMode_AllValid(t *testing.T) {
	modes := []RetrievalMode{
		RetrievalAutoGroundedDefault, RetrievalAgenticOnly,
		RetrievalTextbookOnly, RetrievalNoRetrieval, RetrievalExternalAuthorityAllowed,
	}
	for _, m := range modes {
		if !m.Valid() {
			t.Fatalf("mode %q should be valid", m)
		}
	}
	if RetrievalMode("bogus").Valid() {
		t.Fatal("bogus mode should not be valid")
	}
}
