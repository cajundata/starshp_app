// Package mention parses the leading @persona mention that routes a single
// turn to a named assistant. The rule fits in one sentence: a mention counts
// only when, after trimming leading whitespace, the message starts with @,
// followed by one or more of [a-zA-Z0-9-], followed by whitespace or
// end-of-string. Everything else — a mid-sentence @, an email address, a
// @decorator in pasted code, a second @name in the body — is literal text.
package mention

import (
	"regexp"
	"strings"
)

var re = regexp.MustCompile(`^@([a-zA-Z0-9-]+)($|\s)`)

// Parse returns the persona ID a message is addressed to. ok is false when
// the message carries no leading mention. The captured name is lowercased to
// match persona IDs, which are already [a-z0-9-].
func Parse(text string) (personaID string, ok bool) {
	m := re.FindStringSubmatch(strings.TrimLeft(text, " \t\r\n"))
	if m == nil {
		return "", false
	}
	return strings.ToLower(m[1]), true
}
