package app

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"godraw/internal/doc"
)

// The style panel holds what it shows: an arrow's head menus stay inside its
// padding, whichever head is chosen.
func TestArrowHeadMenusFitThePanel(t *testing.T) {
	const window, right, width, padding = 1280, 12, 180, 8
	for _, head := range []string{"none", "arrow", "triangle", "square", "dot", "diamond", "inverted", "bar", "pipe"} {
		d := doc.New()
		s := doc.NewShape("shape:a", doc.TypeArrow)
		s.Parent, s.Index, s.HeadStart, s.HeadEnd = "page:page", doc.FirstIndex, head, head
		s.End.X, s.End.Y = 300, 200
		d.Shapes[s.ID] = s
		a := New(d, &testHost{})
		a.Editor().Select(s.ID)
		tt := ui.NewTester(a.View, window, 820)
		tt.Frame()
		for _, name := range []string{"Start arrowhead", "End arrowhead"} {
			box, ok := tt.Find(name)
			if !ok {
				t.Fatalf("no %s menu", name)
			}
			if lo, hi := float32(window-right-width+padding), float32(window-right-padding); box.X < lo || box.X+box.W > hi {
				t.Errorf("%s with %s: %v to %v, outside %v to %v", name, head, box.X, box.X+box.W, lo, hi)
			}
		}
	}
}
