package persona

import (
	"math"
	"testing"
)

func TestContrastRatioKnownValues(t *testing.T) {
	// White on black is the maximum, 21:1.
	got, err := ContrastRatio("#ffffff", "#000000")
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got-21) > 0.01 {
		t.Errorf("white/black = %.2f, want 21", got)
	}
	// A color against itself is 1:1.
	got, err = ContrastRatio("#4fb3ff", "#4fb3ff")
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got-1) > 0.001 {
		t.Errorf("self contrast = %.3f, want 1", got)
	}
}

func TestContrastRatioRejectsBadHex(t *testing.T) {
	for _, bad := range []string{"", "blurple", "#12", "#12345", "1d1d20", "#ggg000"} {
		if _, err := ContrastRatio(bad, BubbleBG); err == nil {
			t.Errorf("ContrastRatio(%q) accepted an invalid color", bad)
		}
	}
}

// Every palette entry must be legible as text on the assistant bubble.
func TestPaletteMeetsContrastFloor(t *testing.T) {
	if len(palette) == 0 {
		t.Fatal("palette is empty")
	}
	for _, c := range palette {
		r, err := ContrastRatio(c, BubbleBG)
		if err != nil {
			t.Fatalf("palette color %q: %v", c, err)
		}
		if r < 4.5 {
			t.Errorf("palette color %s has contrast %.2f against %s, want >= 4.5", c, r, BubbleBG)
		}
	}
}

func TestAssignColorIsDeterministicAndInPalette(t *testing.T) {
	inPalette := map[string]bool{}
	for _, c := range palette {
		inPalette[c] = true
	}
	for _, id := range []string{"scout", "skeptic", "editor", "assistant"} {
		a, b := assignColor(id), assignColor(id)
		if a != b {
			t.Errorf("assignColor(%q) not deterministic: %q vs %q", id, a, b)
		}
		if !inPalette[a] {
			t.Errorf("assignColor(%q) = %q, not a palette color", id, a)
		}
	}
}
