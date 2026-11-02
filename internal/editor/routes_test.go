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

// With something in the way of the route without it, a via isn't cleared, but gives
// way to a route over what's in the way with fewer turns.
func TestDragFindsAWayAroundWhatsInTheWay(t *testing.T) {
	h := newHarness(t)
	in := h.rect(60, 280, 120, 60)
	_, b, arrow := h.pair(geo.V(320, 320))
	h.Select(b.ID)
	h.drag(340, 414, 241, 414)
	if via := h.pageVia(arrow.ID); len(via) == 0 || via[0].Dist(geo.V(320, 320)) < 0.01 {
		t.Errorf("via %v", via)
	}
	route := h.Arrow(h.shape(arrow.ID)).Elbow
	if len(route)-1 > 3 {
		t.Errorf("%d legs: %v", len(route)-1, route)
	}
	pm, box := h.PageMat(arrow.ID), h.PageBounds(in.ID)
	for i := 1; i < len(route); i++ {
		if crossesBox(pm.Apply(route[i-1]), pm.Apply(route[i]), box) {
			t.Errorf("runs through what's in the way: %v", route)
		}
	}
}

// A via that isn't in anything's way, and adds no turns, was put there for a reason.
func TestDragKeepsAViaWithNoBetterWay(t *testing.T) {
	h := newHarness(t)
	a := h.rect(0, 0, 100, 100)
	b := h.rect(500, 0, 100, 100)
	h.SetTool(ToolArrow)
	h.drag(50, 50, 550, 50)
	id := h.Selection()[0]
	if err := h.Batch(func() error {
		c := h.shape(id).Clone()
		c.Kind = "elbow"
		c.SetVia([]geo.Vec{h.PageMat(id).Invert().Apply(geo.V(300, -40))})
		h.Put(c)
		h.Bind(id, "start", a.ID, geo.V(0.5, 0))
		h.Bind(id, "end", b.ID, geo.V(0.5, 0))
		return nil
	}); err != nil {
		h.t.Fatal(err)
	}
	h.SetTool(ToolSelect)
	h.Select(b.ID)
	h.drag(550, 50, 600, 50)
	if via := h.pageVia(id); len(via) != 1 || via[0].Dist(geo.V(300, -40)) > 0.01 {
		t.Errorf("via %v", via)
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

// Points that went around something tie a route in a knot once its end has moved,
// and a better way is there: over what's in the way, with no more turns than it
// takes. The route takes that way, and coming back takes it back.
func TestDragReroutesARouteTiedInAKnot(t *testing.T) {
	h := newHarness(t)
	a := h.rect(420, 1060, 120, 60)
	m := h.rect(400, 1150, 160, 56)
	b := h.rect(440, 1240, 80, 45)
	h.SetTool(ToolArrow)
	h.drag(480, 1090, 480, 1262)
	id := h.Selection()[0]
	if err := h.Batch(func() error {
		c := h.shape(id).Clone()
		c.Kind = "elbow"
		inv := h.PageMat(id).Invert()
		c.SetVia([]geo.Vec{inv.Apply(geo.V(620, 1135)), inv.Apply(geo.V(620, 1222))})
		h.Put(c)
		h.Bind(id, "start", a.ID, geo.V(0.5, 1))
		h.Bind(id, "end", b.ID, geo.V(0.5, 0))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h.SetTool(ToolSelect)
	was := h.pageVia(id)

	h.Select(b.ID)
	h.PointerMove(480, 1262, Mods{}, 0)
	h.PointerDown(480, 1262, 0, 1, Mods{}, 0)
	h.PointerMove(571.6, 1241.7, Mods{}, 0)

	route := h.Arrow(h.shape(id)).Elbow
	if len(route)-1 > 3 {
		t.Errorf("%d legs, want 3 at most: %v", len(route)-1, route)
	}
	pm := h.PageMat(id)
	box := h.PageBounds(m.ID)
	for i := 1; i < len(route); i++ {
		if crossesBox(pm.Apply(route[i-1]), pm.Apply(route[i]), box) {
			t.Errorf("runs through what's in the way: %v", route)
		}
	}

	h.PointerMove(480, 1262, Mods{}, 0)
	h.PointerUp(480, 1262, Mods{}, 0)
	if got := h.pageVia(id); !sameVia(got, was) {
		t.Errorf("coming back: via %v, was %v", got, was)
	}
}

// joined makes an elbow arrow with no via from the bottom of one shape to the top
// of another.
func (h *harness) joined(a, b *doc.Shape) *doc.Shape {
	pa, pb := h.PageBounds(a.ID).Center(), h.PageBounds(b.ID).Center()
	h.SetTool(ToolArrow)
	h.drag(pa.X, pa.Y, pb.X, pb.Y)
	id := h.Selection()[0]
	if err := h.Batch(func() error {
		c := h.shape(id).Clone()
		c.Kind = "elbow"
		h.Put(c)
		h.Bind(id, "start", a.ID, geo.V(0.5, 1))
		h.Bind(id, "end", b.ID, geo.V(0.5, 0))
		return nil
	}); err != nil {
		h.t.Fatal(err)
	}
	h.SetTool(ToolSelect)
	return h.shape(id)
}

func (h *harness) meets(arrow doc.ID, box geo.Box) bool {
	route := h.Arrow(h.shape(arrow)).Elbow
	pm := h.PageMat(arrow)
	for i := 1; i < len(route); i++ {
		if crossesBox(pm.Apply(route[i-1]), pm.Apply(route[i]), box) {
			return true
		}
	}
	return false
}

// An arrow with no via finds a way around what a drag takes it through, as the drag
// goes, and needs no via once it's clear again.
func TestDragTakesAnArrowAround(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 120, 60)
	in := h.rect(90, 260, 140, 50)
	b := h.rect(400, 340, 80, 45)
	arrow := h.joined(a, b)
	if len(h.pageVia(arrow.ID)) != 0 {
		t.Fatal("started with a via")
	}
	undos := len(h.undo)

	h.Select(b.ID)
	h.PointerMove(440, 362, Mods{}, 0)
	h.PointerDown(440, 362, 0, 1, Mods{}, 0)
	h.PointerMove(160, 362, Mods{}, 0) // under what's in the way
	if len(h.pageVia(arrow.ID)) == 0 || h.meets(arrow.ID, h.PageBounds(in.ID)) {
		t.Errorf("during the drag: via %v, route %v", h.pageVia(arrow.ID), h.Arrow(h.shape(arrow.ID)).Elbow)
	}
	h.PointerUp(160, 362, Mods{}, 0)
	if h.meets(arrow.ID, h.PageBounds(in.ID)) {
		t.Errorf("after the drag: %v", h.Arrow(h.shape(arrow.ID)).Elbow)
	}
	if len(h.undo) != undos+1 {
		t.Errorf("%d steps of undo, want one", len(h.undo)-undos)
	}
	h.Undo()
	if via := h.pageVia(arrow.ID); len(via) != 0 {
		t.Errorf("after undo: via %v", via)
	}

	// Moved on from there in another drag, what it stored goes when it's not needed.
	h.Redo()
	h.Select(b.ID)
	h.drag(160, 362, 440, 362)
	if via := h.pageVia(arrow.ID); len(via) != 0 {
		t.Errorf("clear again: via %v", via)
	}
}

// A drag that doesn't take an arrow through anything leaves it with no via.
func TestDragStoresNoViaItDoesntNeed(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 120, 60)
	b := h.rect(400, 340, 80, 45)
	arrow := h.joined(a, b)
	h.Select(b.ID)
	h.drag(440, 362, 300, 500)
	if via := h.pageVia(arrow.ID); len(via) != 0 {
		t.Errorf("via %v", via)
	}
}

// Arrows of a tree share a trunk on purpose: moving a branch leaves it on it.
func TestDragLeavesATreeOnItsTrunk(t *testing.T) {
	h := newHarness(t)
	root := h.rect(300, 100, 120, 60)
	l := h.rect(100, 300, 120, 60)
	r := h.rect(500, 300, 120, 60)
	left, right := h.joined(root, l), h.joined(root, r)
	h.Select(r.ID)
	h.drag(560, 330, 600, 330)
	if via := h.pageVia(right.ID); len(via) != 0 {
		t.Errorf("the branch left the trunk: via %v", via)
	}
	if via := h.pageVia(left.ID); len(via) != 0 {
		t.Errorf("the other branch moved: via %v", via)
	}
}

// What isn't moved stays as it is, even if something's put in its way.
func TestDragLeavesOtherArrowsAlone(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 120, 60)
	b := h.rect(100, 400, 120, 60)
	arrow := h.joined(a, b)
	in := h.rect(500, 250, 120, 60)
	h.Select(in.ID)
	h.drag(560, 280, 160, 280)
	if via := h.pageVia(arrow.ID); len(via) != 0 {
		t.Errorf("via %v", via)
	}
}

// With no clear way, it goes as it did, rather than anywhere worse.
func TestDragWithNoWayAroundLeavesTheRoute(t *testing.T) {
	h := newHarness(t)
	a := h.rect(300, 100, 120, 60)
	b := h.rect(300, 800, 120, 60)
	arrow := h.joined(a, b)
	wall := h.rect(-3000, 400, 7000, 100)
	h.Select(b.ID)
	h.drag(360, 830, 380, 830)
	if via := h.pageVia(arrow.ID); len(via) != 0 {
		t.Errorf("via %v around %v", via, wall.ID)
	}
}

// A frame of a drag looks for only so long, but all are settled when it ends.
func TestDragSettlesEveryArrowInTheEnd(t *testing.T) {
	h := newHarness(t)
	hub := h.rect(100, 100, 120, 60)
	in := h.rect(90, 260, 140, 50)
	var arrows []doc.ID
	var ends []*doc.Shape
	for i := range 4 {
		b := h.rect(float64(400+i*40), 400+float64(i)*100, 30, 30)
		ends = append(ends, b)
		arrows = append(arrows, h.joined(hub, b).ID)
	}
	h.Select(ends[0].ID, ends[1].ID, ends[2].ID, ends[3].ID)
	h.PointerMove(415, 415, Mods{}, 0)
	h.PointerDown(415, 415, 0, 1, Mods{}, 0)
	// Far enough left to take each through what's in the way, and they move alike
	// only with the hub, which stays.
	h.PointerMove(155, 415, Mods{}, 0)
	h.PointerUp(155, 415, Mods{}, 0)
	for _, id := range arrows {
		if h.meets(id, h.PageBounds(in.ID)) {
			t.Errorf("%s runs through what's in the way: %v", id, h.Arrow(h.shape(id)).Elbow)
		}
	}
}

// A way around keeps clear of what it goes around, rather than run along its edge
// where it can't be told from it.
func TestWayAroundKeepsItsDistance(t *testing.T) {
	for _, x := range []float64{1372.7, 1340, 1400, 1420} {
		h := newHarness(t)
		a := h.rect(1340, 1060, 120, 60)
		in := h.rect(1320, 1230, 160, 60)
		b := h.rect(1580, 1320, 80, 45)
		arrow := h.joined(a, b)
		h.Select(b.ID)
		h.drag(1620, 1342, x+40, 1342)
		route := h.Arrow(h.shape(arrow.ID)).Elbow
		pm := h.PageMat(arrow.ID)
		box := h.PageBounds(in.ID).Expand(routeClearance - 0.5)
		for i := 1; i < len(route); i++ {
			if crossesBox(pm.Apply(route[i-1]), pm.Apply(route[i]), box) {
				t.Errorf("box at %v: within %v of what it goes around: via %v, route %v", x, routeClearance, h.pageVia(arrow.ID), route)
				break
			}
		}
	}
}

// In a gap too narrow to keep its distance, it takes what room there is, rather than
// go through.
func TestWayAroundTakesANarrowGap(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 120, 60)
	in := h.rect(90, 240, 140, 60)
	b := h.rect(400, 316, 80, 45)
	arrow := h.joined(a, b)
	h.Select(b.ID)
	h.drag(440, 338, 160, 338) // 16 under what's in the way
	if h.meets(arrow.ID, h.PageBounds(in.ID)) {
		t.Errorf("via %v, route %v", h.pageVia(arrow.ID), h.Arrow(h.shape(arrow.ID)).Elbow)
	}
	if len(h.pageVia(arrow.ID)) == 0 {
		t.Error("no way around")
	}
}
