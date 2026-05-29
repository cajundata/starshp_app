package chat

type RetrievalMode string

const (
	RetrievalAutoGroundedDefault      RetrievalMode = "auto_grounded_default"
	RetrievalAgenticOnly              RetrievalMode = "agentic_only"
	RetrievalTextbookOnly             RetrievalMode = "textbook_only"
	RetrievalNoRetrieval              RetrievalMode = "no_retrieval"
	RetrievalExternalAuthorityAllowed RetrievalMode = "external_authority_allowed"
)

func (m RetrievalMode) Valid() bool {
	switch m {
	case RetrievalAutoGroundedDefault, RetrievalAgenticOnly,
		RetrievalTextbookOnly, RetrievalNoRetrieval, RetrievalExternalAuthorityAllowed:
		return true
	}
	return false
}

// RequiresPreTurnRAG reports whether this mode runs a pre-turn retrieval when
// the conversation has textbooks attached.
func (m RetrievalMode) RequiresPreTurnRAG() bool {
	switch m {
	case RetrievalAutoGroundedDefault, RetrievalTextbookOnly,
		RetrievalExternalAuthorityAllowed:
		return true
	}
	return false
}

// ResolveRetrievalMode applies the developer env override on top of the
// per-conversation mode. STARSHP_SKIP_AUTO_GROUNDING=1 forces no_retrieval.
// getenv is injected so tests can avoid touching os.Getenv.
func ResolveRetrievalMode(mode RetrievalMode, getenv func(string) string) RetrievalMode {
	if getenv("STARSHP_SKIP_AUTO_GROUNDING") == "1" {
		return RetrievalNoRetrieval
	}
	return mode
}
