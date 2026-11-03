package app

import (
	"math"
	"testing"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"godraw/internal/agent"
	"godraw/internal/geo"
	"godraw/internal/stress"
)

// The text system lets go of its fonts after some hundreds of sizes, and text
// painted from layouts made before that crashes in the system's font code. A long
// zoom must get past that.
func TestZoomingThroughManySizes(t *testing.T) {
	a := New(stress.Diagram(60), &testHost{})
	tt := ui.NewTester(a.View, 1280, 820)
	tt.SetScale(2)
	tt.Frame()
	e := a.Editor()
	e.ZoomToFit()
	tt.Frame()
	for i := range 1400 {
		f := 1.003
		if i%700 >= 350 {
			f = 1 / f
		}
		e.ZoomAt(geo.V(640, 410), f)
		tt.Frame()
	}
}

// What the guide tells agents of how wide text runs must hold for the real fonts:
// it's what they lay out from before they measure.
func TestGuideSaysHowWideTheFontsRun(t *testing.T) {
	registerFonts()
	const size = 100.0
	perChar := func(text, font string) float64 {
		return layoutBlock(text, font, size, 0).W / size / float64(utf8.RuneCountInString(text))
	}
	sentences := []string{
		"The quick brown fox jumps over the lazy dog and keeps on running through the field",
		"Sign-in and sessions are kept in a small store that every service reads from at start",
		"Particles behave like waves until they are measured, and then they seem to pick one place",
	}
	titles := []string{"Quantum computers", "Wave-particle duality", "Uncertainty Principle", "Quantum Tunneling", "Superposition", "API Gateway"}
	for _, c := range agent.CharWidths {
		avg := 0.0
		for _, s := range sentences {
			avg += perChar(s, c.Font)
		}
		avg /= float64(len(sentences))
		if math.Abs(avg-c.Sentence) > 0.015 {
			t.Errorf("%s: a sentence runs %.3f, the guide says %v", c.Font, avg, c.Sentence)
		}
		most := 0.0
		for _, s := range titles {
			most = math.Max(most, perChar(s, c.Font))
		}
		if math.Abs(most-c.Title) > 0.015 {
			t.Errorf("%s: a title runs up to %.3f, the guide says %v", c.Font, most, c.Title)
		}
	}
}
