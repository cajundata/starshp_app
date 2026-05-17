package chunker

import (
	"regexp"
	"strings"
)

// Compiled regex patterns for chunk type detection (compiled once at init).
var (
	examplePattern         = regexp.MustCompile(`(?i)\b(example|illustration|exhibit)\b`)
	ruleKeywordPattern     = regexp.MustCompile(`(?i)\b(rule|principle|standard)\b`)
	numberedListPattern    = regexp.MustCompile(`(?m)^\d+\.`)
	exerciseSupportPattern = regexp.MustCompile(`(?i)\b(exercise|problem|practice|homework|assignment)\b`)
)

// DetectChunkType classifies chunk content by type using content heuristics.
// Priority order: table > example > rule > exercise-support > concept.
func DetectChunkType(content string) string {
	// 1. Table: 3+ lines starting with |
	if isTable(content) {
		return "table"
	}

	// For keyword checks, use first 200 chars where specified
	prefix := content
	if len(prefix) > 200 {
		prefix = prefix[:200]
	}

	// 2. Example: first 200 chars contain example/illustration/exhibit
	if examplePattern.MatchString(prefix) {
		return "example"
	}

	// 3. Rule: keyword in first 200 chars OR 3+ numbered list items
	if ruleKeywordPattern.MatchString(prefix) {
		return "rule"
	}
	if matches := numberedListPattern.FindAllString(content, -1); len(matches) >= 3 {
		return "rule"
	}

	// 4. Exercise-support: keyword anywhere in content
	if exerciseSupportPattern.MatchString(content) {
		return "exercise-support"
	}

	// 5. Concept: default fallback
	return "concept"
}

// isTable returns true if the content contains 3 or more lines starting with |.
func isTable(content string) bool {
	count := 0
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "|") {
			count++
			if count >= 3 {
				return true
			}
		}
	}
	return false
}
