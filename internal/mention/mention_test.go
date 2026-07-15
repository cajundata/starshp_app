package mention

import "testing"

// Every row of the table in the spec's Mention Parsing section, plus edge
// cases. A mention counts only when, after trimming leading whitespace, the
// message starts with @name followed by whitespace or end-of-string.
func TestParse(t *testing.T) {
	cases := []struct {
		in string
		id string
		ok bool
	}{
		{"@skeptic poke holes", "skeptic", true},
		{"@Skeptic poke holes", "skeptic", true},    // case-insensitive
		{"@scout", "scout", true},                   // mention alone is legal
		{"  @scout\nreview this", "scout", true},    // leading whitespace trimmed
		{"@scout @skeptic both?", "scout", true},    // second mention is literal text
		{"ask @skeptic about it", "", false},        // not leading
		{"email me @ 5pm", "", false},               // @ not followed by a name
		{"@property\ndef foo():", "property", true}, // pasted decorator: parses, resolution errors later
		{"", "", false},
		{"@", "", false},
		{"@!bad", "", false},
		{"\t@scout hi", "scout", true},
	}
	for _, c := range cases {
		id, ok := Parse(c.in)
		if id != c.id || ok != c.ok {
			t.Errorf("Parse(%q) = (%q, %v), want (%q, %v)", c.in, id, ok, c.id, c.ok)
		}
	}
}
