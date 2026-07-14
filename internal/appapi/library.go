package appapi

import (
	"sort"
	"strings"

	"github.com/cajundata/starshp_app/internal/library"
	"github.com/cajundata/starshp_app/internal/persona"
	"github.com/cajundata/starshp_app/internal/provider"
)

// ListLibraryItems returns every snippet in the library folder.
func (a *API) ListLibraryItems() ([]library.Item, error) {
	items, err := a.lib.List()
	if err != nil {
		return nil, provider.NormalizeError(err)
	}
	return items, nil
}

// ReadLibraryItem returns one item's raw markdown content.
func (a *API) ReadLibraryItem(filename string) (string, error) {
	content, err := a.lib.Read(filename)
	if err != nil {
		return "", libraryError(err)
	}
	return content, nil
}

// CreateLibraryItem writes a new snippet and returns the created item.
func (a *API) CreateLibraryItem(content string) (library.Item, error) {
	item, err := a.lib.Create(content)
	if err != nil {
		return library.Item{}, libraryError(err)
	}
	return item, nil
}

// SaveLibraryItem overwrites an existing snippet's content.
func (a *API) SaveLibraryItem(filename, content string) error {
	if err := a.lib.Save(filename, content); err != nil {
		return libraryError(err)
	}
	return nil
}

// DeleteLibraryItem removes a snippet file.
func (a *API) DeleteLibraryItem(filename string) error {
	if err := a.lib.Delete(filename); err != nil {
		return libraryError(err)
	}
	return nil
}

// GetActiveItems returns a conversation's active item filenames, pruning any
// whose files no longer exist on disk (self-healing on panel load).
func (a *API) GetActiveItems(convID string) ([]string, error) {
	names, err := a.st.GetActiveItems(convID)
	if err != nil {
		return nil, provider.NormalizeError(err)
	}
	items, err := a.lib.List()
	if err != nil {
		return nil, provider.NormalizeError(err)
	}
	valid := map[string]bool{}
	for _, it := range items {
		valid[it.Filename] = true
	}
	live := []string{}
	pruned := false
	for _, n := range names {
		if valid[n] {
			live = append(live, n)
		} else {
			pruned = true
		}
	}
	if pruned {
		_ = a.st.SetActiveItems(convID, live) // best-effort self-heal
	}
	return live, nil
}

// SetActiveItems replaces the active set for a conversation.
func (a *API) SetActiveItems(convID string, names []string) error {
	if err := a.st.SetActiveItems(convID, names); err != nil {
		return provider.NormalizeError(err)
	}
	return nil
}

// libraryError maps a library validation error to a friendly AppError and
// falls back to the generic normalizer for everything else.
func libraryError(err error) provider.AppError {
	switch err {
	case library.ErrNoH1:
		return provider.AppError{Code: "validation", UserMessage: `Add an H1 heading (e.g. "# Title") — it becomes the item's name.`, Retryable: false}
	case library.ErrBadName:
		return provider.AppError{Code: "validation", UserMessage: "That library item name is invalid.", Retryable: false}
	default:
		return provider.NormalizeError(err)
	}
}

// assembleSystemPrompt builds the system prompt for one turn: the persona's
// body (identity), then the library items the persona always carries, then the
// items attached to this conversation. An item claimed by both appears once, in
// the persona's position — a conversation attachment reads as an addition to
// the persona's standing context, not an interruption of it.
//
// Missing library files are skipped, not fatal, and returned in `skipped`.
func (a *API) assembleSystemPrompt(convID string, p persona.Persona) (prompt string, skipped []string, err error) {
	convNames, err := a.st.GetActiveItems(convID)
	if err != nil {
		return "", nil, err
	}
	personaNames := make([]string, 0, len(p.Library))
	claimed := map[string]bool{}
	for _, n := range p.Library {
		n = normalizeLibraryName(n)
		personaNames = append(personaNames, n)
		claimed[n] = true
	}
	var rest []string
	for _, n := range convNames {
		if !claimed[n] {
			rest = append(rest, n)
		}
	}

	personaPre, skippedA, err := a.assembleLibraryPreamble(personaNames)
	if err != nil {
		return "", nil, err
	}
	convPre, skippedB, err := a.assembleLibraryPreamble(rest)
	if err != nil {
		return "", nil, err
	}
	return joinNonEmpty(p.Prompt, personaPre, convPre),
		append(skippedA, skippedB...), nil
}

// normalizeLibraryName lets a persona write `library: [style-guide]` instead of
// `[style-guide.md]`. Library IDs are filenames; this supplies the extension.
func normalizeLibraryName(n string) string {
	if strings.HasSuffix(strings.ToLower(n), ".md") {
		return n
	}
	return n + ".md"
}

func joinNonEmpty(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			kept = append(kept, s)
		}
	}
	return strings.Join(kept, "\n\n")
}

// assembleLibraryPreamble reads each named library item, strips its H1, sorts by
// display name (case-insensitive), and joins the non-empty bodies with "\n\n".
// Missing/unreadable items are skipped and returned in `skipped`. The error
// return is always nil today; it is kept for symmetry with assembleSystemPrompt
// and future store-backed callers.
func (a *API) assembleLibraryPreamble(names []string) (prompt string, skipped []string, err error) {
	type entry struct{ display, body string }
	var entries []entry
	for _, name := range names {
		content, rerr := a.lib.Read(name)
		if rerr != nil {
			skipped = append(skipped, name)
			continue
		}
		// A readable item always has an H1 (Create/Save enforce it); if one
		// somehow lacks it, display is "" and it simply sorts first.
		entries = append(entries, entry{
			display: library.ExtractH1(content),
			body:    library.StripH1(content),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].display) < strings.ToLower(entries[j].display)
	})
	var bodies []string
	for _, e := range entries {
		if e.body != "" {
			bodies = append(bodies, e.body)
		}
	}
	return strings.Join(bodies, "\n\n"), skipped, nil
}
