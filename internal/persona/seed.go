package persona

import (
	"os"
	"path/filepath"
	"strings"
)

// seedAssistant is the out-of-the-box persona. It reproduces today's plain-chat
// behavior — no tool restriction, no auto-attached library — so a fresh install
// behaves the way the app did before personas existed.
const seedAssistant = `---
name: Assistant
model: %MODEL%
---
You are a capable, direct assistant. Answer the question that was asked.
State your reasoning when it is load-bearing and skip it when it is not.
If you are uncertain, say so plainly rather than hedging.
`

// Seed writes a single starter persona, but only when dir does not exist. An
// existing directory is never written to: if the operator emptied it or every
// file in it is invalid, a surprise default persona appearing would attribute
// output to an assistant they never configured.
//
// A no-op when defaultModelID is empty — there would be no valid model to point
// the seeded persona at.
func Seed(dir, defaultModelID string) error {
	if defaultModelID == "" {
		return nil
	}
	if _, err := os.Stat(dir); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := strings.ReplaceAll(seedAssistant, "%MODEL%", defaultModelID)
	return os.WriteFile(filepath.Join(dir, "assistant.md"), []byte(body), 0o644)
}
