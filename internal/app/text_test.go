package app

import (
	"math"
	"testing"
)

// Text must take the room in this app that it takes in tldraw, or what fits here
// wraps there. The widths are what tldraw measures, with its own fonts.
func TestTextWidthsMatchTldraw(t *testing.T) {
	registerFonts()
	const fox = "The quick brown fox jumps over the lazy dog"
	for _, c := range []struct {
		font, text string
		size, want float64
	}{
		{"draw", fox, 18, 416.39},
		{"draw", fox, 24, 555.17},
		{"draw", fox, 36, 832.77},
		{"draw", "A body under a medium title", 18, 267.31},
		{"draw", "Medium card", 24, 161.64},
		{"draw", "0123456789", 18, 115.42},
		{"sans", fox, 18, 364.66},
		{"serif", fox, 18, 383.77},
		{"mono", fox, 18, 464.41},
	} {
		got := layoutBlock(c.text, c.font, c.size, 100000).W
		if math.Abs(got-c.want)/c.want > 0.005 {
			t.Errorf("%s %q at %v: %.2f wide, tldraw has %.2f", c.font, c.text, c.size, got, c.want)
		}
	}
}

// What a font lacks still takes room, from another font.
func TestTextFallsBack(t *testing.T) {
	registerFonts()
	for _, font := range []string{"draw", "sans", "serif", "mono"} {
		if got := layoutBlock("日本語", font, 18, 100000).W; got < 18 {
			t.Errorf("%s: %.2f wide", font, got)
		}
	}
}
