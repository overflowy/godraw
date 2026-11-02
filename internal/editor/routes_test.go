package editor

import (
	"testing"

	"godraw/internal/doc"
	"godraw/internal/geo"
)

// pair makes two boxes and an elbow arrow from the bottom of one to the top of
// the other, passing through a point.
func (h *harness) pair(via geo.Vec) (a, b, arrow *doc.Shape) {
	a = h.rect(20, 20, 200, 200)
	b = h.rect(300, 392, 80, 45)
	h.SetTool(ToolArrow)
	h.drag(120, 120, 340, 414)
	id := h.Selection()[0]
	if err := h.Batch(func() error {
		c := h.shape(id).Clone()
		c.Kind = "elbow"
		c.SetVia([]geo.Vec{h.PageMat(id).Invert().Apply(via)})
		h.Put(c)
		h.Bind(id, "start", a.ID, geo.V(0.5, 1))
		h.Bind(id, "end", b.ID, geo.V(0.5, 0))
		return nil
	}); err != nil {
		h.t.Fatal(err)
	}
	h.SetTool(ToolSelect)
	return a, b, h.shape(id)
}

func (h *harness) pageVia(id doc.ID) []geo.Vec {
	m := h.PageMat(id)
	via := h.shape(id).Via()
	for i, p := range via {
		via[i] = m.Apply(p)
	}
	return via
}

// Dragging what an arrow goes to takes away a via that only adds turns, and the
// route shows that as the drag goes, rather than when it ends.
func TestDragClearsANeedlessVia(t *testing.T) {
	h := newHarness(t)
	_, b, arrow := h.pair(geo.V(320, 320))
	undos := len(h.undo)

	h.Select(b.ID)
	h.PointerMove(340, 414, Mods{}, 0)
	h.PointerDown(340, 414, 0, 1, Mods{}, 0)
	h.PointerMove(241, 414, Mods{}, 0)
	if via := h.pageVia(arrow.ID); len(via) != 0 {
		t.Errorf("during the drag: via %v", via)
	}
	if n := len(h.Arrow(h.shape(arrow.ID)).Elbow) - 1; n != 3 {
		t.Errorf("during the drag: %d legs", n)
	}
	h.PointerUp(241, 414, Mods{}, 0)
	if via := h.pageVia(arrow.ID); len(via) != 0 {
		t.Errorf("after the drag: via %v", via)
	}
	if len(h.undo) != undos+1 {
		t.Errorf("%d steps of undo, want one", len(h.undo)-undos)
	}
	h.Undo()
	if via := h.pageVia(arrow.ID); len(via) != 1 || via[0].Dist(geo.V(320, 320)) > 0.01 {
		t.Errorf("after undo: via %v", via)
	}
}

// A drag that comes back puts things back as they were.
func TestDragBackKeepsVia(t *testing.T) {
	h := newHarness(t)
	_, b, arrow := h.pair(geo.V(320, 320))
	h.Select(b.ID)
	h.PointerMove(340, 414, Mods{}, 0)
	h.PointerDown(340, 414, 0, 1, Mods{}, 0)
	h.PointerMove(241, 414, Mods{}, 0)
	h.PointerMove(340, 414, Mods{}, 0)
	h.PointerUp(340, 414, Mods{}, 0)
	if via := h.pageVia(arrow.ID); len(via) != 1 || via[0].Dist(geo.V(320, 320)) > 0.01 {
		t.Errorf("via %v", via)
	}
}

// Dragging both ends together takes the route along.
func TestDragBothEndsMovesVia(t *testing.T) {
	h := newHarness(t)
	a, b, arrow := h.pair(geo.V(320, 320))
	h.Select(a.ID, b.ID)
	h.drag(120, 120, 170, 180)
	if via := h.pageVia(arrow.ID); len(via) != 1 || via[0].Dist(geo.V(370, 380)) > 0.01 {
		t.Errorf("via %v", via)
	}
}

// With something in the way of the route without it, a via stays.
func TestDragKeepsAViaThatGoesAround(t *testing.T) {
	h := newHarness(t)
	in := h.rect(60, 280, 120, 60)
	_, b, arrow := h.pair(geo.V(320, 320))
	h.Select(b.ID)
	h.drag(340, 414, 241, 414)
	if via := h.pageVia(arrow.ID); len(via) != 1 {
		t.Errorf("via went with %v in the way", in.ID)
	}
}

// Export goes the way the canvas does: highlights first, under what's in the same
// page or frame, wherever they come in the order.
func TestWalkPaintPutsHighlightsUnder(t *testing.T) {
	h := newHarness(t)
	rect := h.rect(100, 100, 100, 100)
	h.SetTool(ToolHighlight)
	h.drag(90, 150, 210, 150)
	var mark doc.ID
	for _, id := range h.Children(h.Page) {
		if h.shape(id).Type == doc.TypeHighlight {
			mark = id
		}
	}
	var got []doc.ID
	h.WalkPaint(h.Children(h.Page), func(s *doc.Shape) { got = append(got, s.ID) }, func(*doc.Shape) {}, func(*doc.Shape) {})
	if len(got) != 2 || got[0] != mark || got[1] != rect.ID {
		t.Errorf("painted %v, want %v then %v", got, mark, rect.ID)
	}
}
