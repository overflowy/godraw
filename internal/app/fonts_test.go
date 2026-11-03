package app

import (
	"testing"

	"github.com/egoist/mygo/ui"

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
