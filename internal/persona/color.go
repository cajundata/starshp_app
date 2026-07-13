package persona

import (
	"fmt"
	"hash/fnv"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// BubbleBG is the assistant bubble background (style.css). Palette colors are
// verified legible against it.
const BubbleBG = "#1d1d20"

// palette holds the auto-assignment colors. Every entry is verified at test
// time to clear a 4.5:1 contrast ratio against BubbleBG, so a persona that
// omits `color:` still gets a legible one without the author thinking about it.
var palette = []string{
	"#4fb3ff", // blue
	"#5ddc9a", // mint
	"#ffb454", // amber
	"#ff7b72", // salmon
	"#c792ea", // lavender
	"#7ee787", // green
	"#f2cc60", // yellow
	"#ff9ec7", // pink
	"#56d4dd", // cyan
	"#d3b58d", // sand
}

var hexRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// ValidColor reports whether s is a 6-digit hex color. Shorthand (#abc) is
// rejected so the stored value is always directly usable as a CSS custom
// property and directly parseable here.
func ValidColor(s string) bool { return hexRe.MatchString(s) }

// assignColor picks a stable palette entry for a persona ID. Same ID, same
// color, across restarts and machines — so history does not recolor itself.
func assignColor(id string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return palette[h.Sum32()%uint32(len(palette))]
}

// ContrastRatio returns the WCAG 2.x contrast ratio between two hex colors,
// from 1 (identical) to 21 (black on white).
func ContrastRatio(hexA, hexB string) (float64, error) {
	la, err := relativeLuminance(hexA)
	if err != nil {
		return 0, err
	}
	lb, err := relativeLuminance(hexB)
	if err != nil {
		return 0, err
	}
	hi, lo := math.Max(la, lb), math.Min(la, lb)
	return (hi + 0.05) / (lo + 0.05), nil
}

func relativeLuminance(hex string) (float64, error) {
	if !ValidColor(hex) {
		return 0, fmt.Errorf("invalid hex color %q", hex)
	}
	s := strings.TrimPrefix(hex, "#")
	ch := make([]float64, 3)
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseUint(s[i*2:i*2+2], 16, 8)
		if err != nil {
			return 0, fmt.Errorf("invalid hex color %q: %w", hex, err)
		}
		ch[i] = linearize(float64(v) / 255)
	}
	return 0.2126*ch[0] + 0.7152*ch[1] + 0.0722*ch[2], nil
}

func linearize(c float64) float64 {
	if c <= 0.03928 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}
