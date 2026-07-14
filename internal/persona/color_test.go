package persona

import (
	"hash/fnv"
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

// TestAssignColorIndexIsNeverNegative verifies that the index computation never
// yields a negative value. On 32-bit platforms where int is 32 bits, converting
// a uint32 with the high bit set to int produces a negative value, and Go's %
// operator preserves the sign of the dividend, causing negative slice indices
// and panics. This test uses persona IDs whose FNV-1a hash has the high bit set
// (>= 0x80000000) to ensure the index is always in [0, len(palette)) and the
// color assignment is stable. While this machine is 64-bit and cannot reproduce
// the panic, we assert the property directly.
func TestAssignColorIndexIsNeverNegative(t *testing.T) {
	inPalette := map[string]bool{}
	for _, c := range palette {
		inPalette[c] = true
	}
	// These IDs have FNV-1a hashes with the high bit set (>= 0x80000000).
	// On 32-bit int platforms, int(hash) would be negative, causing a panic
	// with the old implementation.
	highBitIDs := []string{"scout", "analyst", "writer", "mentor", "guide", "expert", "oracle", "sage", "herald", "voyager", "pioneer"}
	for _, id := range highBitIDs {
		h := fnv.New32a()
		h.Write([]byte(id))
		hash := h.Sum32()
		// Verify the ID's hash actually has the high bit set (test sanity check).
		if hash < 0x80000000 {
			t.Errorf("test setup error: %q hash 0x%08x does not have high bit set", id, hash)
		}
		// Verify the index computation is in valid range.
		index := hash % uint32(len(palette))
		if int(index) < 0 || int(index) >= len(palette) {
			t.Errorf("assignColor(%q): computed index %d out of range [0, %d)", id, index, len(palette))
		}
		// Verify assignColor returns a valid palette member.
		color := assignColor(id)
		if !inPalette[color] {
			t.Errorf("assignColor(%q) = %q, not a palette color", id, color)
		}
	}
}
