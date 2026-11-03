package editor

import (
	"fmt"
	"math"
	"testing"

	"godraw/internal/doc"
	"godraw/internal/geo"
)

var elbowSides = map[string]geo.Vec{
	"left": geo.V(-1, 0), "right": geo.V(1, 0), "top": geo.V(0, -1), "bottom": geo.V(0, 1),
}

// sideOf returns the middle of a side of a box.
func sideOf(b geo.Box, out geo.Vec) geo.Vec {
	return b.Center().Add(geo.V(out.X*b.W/2, out.Y*b.H/2))
}

// However two shapes lie, a route through via points between any two of their sides
// leaves and arrives the way it was told, in right angles, and stays out of both.
func TestElbowRoutesEverySide(t *testing.T) {
	a := geo.Box{X: 0, Y: 0, W: 120, H: 80}
	for name, from := range elbowSides {
		for other, to := range elbowSides {
			for dx := -480.0; dx <= 480; dx += 120 {
				for dy := -360.0; dy <= 360; dy += 120 {
					if max(dx, -dx) < 160 && max(dy, -dy) < 120 {
						continue // the boxes overlap
					}
					b := geo.Box{X: dx, Y: dy, W: 120, H: 80}
					label := fmt.Sprintf("%s to %s at %v,%v", name, other, dx, dy)
					pts := routeElbow([]geo.Vec{sideOf(a, from), sideOf(b, to)}, from, to, 0.5, []geo.Box{a, b})
					checkRoute(t, label, pts, from, to, a, b)
				}
			}
		}
	}
}

func checkRoute(t *testing.T, label string, pts []geo.Vec, from, to geo.Vec, boxes ...geo.Box) {
	t.Helper()
	if len(pts) < 2 {
		t.Errorf("%s: %v has no legs", label, pts)
		return
	}
	if d := pts[1].Sub(pts[0]).Uni(); d.Dist(from) > 1e-6 {
		t.Errorf("%s: %v leaves going %v", label, pts, d)
	}
	if d := pts[len(pts)-1].Sub(pts[len(pts)-2]).Uni(); d.Dist(to.Mul(-1)) > 1e-6 {
		t.Errorf("%s: %v arrives going %v", label, pts, d)
	}
	for i := 1; i < len(pts); i++ {
		if d := pts[i].Sub(pts[i-1]); d.X != 0 && d.Y != 0 {
			t.Errorf("%s: %v has a slanted leg", label, pts)
		}
		if i >= 2 && pts[i].Sub(pts[i-1]).Uni().Dot(pts[i-1].Sub(pts[i-2]).Uni()) < -0.5 {
			t.Errorf("%s: %v doubles back", label, pts)
		}
		for _, box := range boxes {
			if crossesBox(pts[i-1], pts[i], box.Expand(-1)) {
				t.Errorf("%s: %v enters %v", label, pts, box)
			}
		}
	}
}

// Between via points, sides that face each other in line take the straight way, and
// those that don't, no more turns than they need.
func TestElbowRoutesAreSimple(t *testing.T) {
	a := geo.Box{X: 0, Y: 0, W: 100, H: 100}
	for _, c := range []struct {
		name     string
		b        geo.Box
		from, to string
		legs     int
	}{
		{"facing", geo.Box{X: 300, Y: 0, W: 100, H: 100}, "right", "left", 1},
		{"step", geo.Box{X: 300, Y: 200, W: 100, H: 100}, "right", "left", 3},
		{"corner", geo.Box{X: 300, Y: 200, W: 100, H: 100}, "right", "top", 2},
		{"over", geo.Box{X: 300, Y: 0, W: 100, H: 100}, "top", "top", 3},
		{"under", geo.Box{X: 300, Y: 0, W: 100, H: 100}, "bottom", "bottom", 3},
	} {
		from, to := elbowSides[c.from], elbowSides[c.to]
		pts := routeElbow([]geo.Vec{sideOf(a, from), sideOf(c.b, to)}, from, to, 0.5, []geo.Box{a, c.b})
		if len(pts)-1 != c.legs {
			t.Errorf("%s: %d legs, want %d: %v", c.name, len(pts)-1, c.legs, pts)
		}
	}
}

// One point is enough to take a route over something, keeping its sides.
func TestElbowPassesThroughVia(t *testing.T) {
	a, b := geo.Box{X: 0, Y: 0, W: 100, H: 100}, geo.Box{X: 600, Y: 0, W: 100, H: 100}
	up := elbowSides["top"]
	via := geo.V(350, -60)
	pts := routeElbow([]geo.Vec{sideOf(a, up), via, sideOf(b, up)}, up, up, 0.5, []geo.Box{a, b})
	want := []geo.Vec{geo.V(50, 0), geo.V(50, -60), geo.V(650, -60), geo.V(650, 0)}
	if fmt.Sprint(pts) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", pts, want)
	}
}

// An anchor just inside the edge it's on, as rounding leaves it, ends the arrow on
// that edge rather than on the far side of the shape.
func TestElbowMeetsTheNearEdge(t *testing.T) {
	outline := []geo.Vec{geo.V(0, 0), geo.V(100, 0), geo.V(100, 80), geo.V(0, 80)}
	for _, c := range []struct {
		from, out, want geo.Vec
	}{
		{geo.V(50, 79.999), geo.V(0, 1), geo.V(50, 80)},
		{geo.V(50, 80), geo.V(0, 1), geo.V(50, 80)},
		{geo.V(50, 80.5), geo.V(0, 1), geo.V(50, 80)},
		{geo.V(50, 0.001), geo.V(0, -1), geo.V(50, 0)},
		{geo.V(99.2, 40), geo.V(1, 0), geo.V(100, 40)},
		{geo.V(0.8, 40), geo.V(-1, 0), geo.V(0, 40)},
	} {
		pts := meetOutline([]geo.Vec{c.from, c.from.Add(c.out.Mul(50))}, c.out, outline)
		if pts[0].Dist(c.want) > 1e-6 {
			t.Errorf("from %v going %v: met at %v, not %v", c.from, c.out, pts[0], c.want)
		}
	}
}

// A hexagon and an octagon have a point at the top.
func TestPolygonsHaveAPointOnTop(t *testing.T) {
	for _, kind := range []string{"pentagon", "hexagon", "octagon"} {
		pts := geoOutline(kind, 100, 100, "")
		if pts[0].Dist(geo.V(50, 0)) > 1e-6 {
			t.Errorf("%s starts at %v, not the top", kind, pts[0])
		}
		top := 0
		for _, p := range pts {
			if p.Y < 1e-6 {
				top++
			}
		}
		if top != 1 {
			t.Errorf("%s has %d points on top", kind, top)
		}
	}
}

// boxEnd is an end of an arrow bound to a box with no head, aiming at a part of it.
func boxEnd(id string, b geo.Box, anchor geo.Vec, side elbowFace) elbowEnd {
	c := b.Corners()
	return elbowEnd{
		id: doc.ID(id), box: b, outline: c[:], closed: true, side: side, minEnd: 5.25,
		target: geo.V(b.X+anchor.X*b.W, b.Y+anchor.Y*b.H),
		onEdge: anchor.X == 0 || anchor.X == 1 || anchor.Y == 0 || anchor.Y == 1,
	}
}

func pointEnd(p geo.Vec) elbowEnd {
	return elbowEnd{box: geo.Box{X: p.X, Y: p.Y}, target: p, side: faceAny, point: true, minEnd: 5.25}
}

// Each shape a route can take, by how its ends lie.
func TestElbowRouteShapes(t *testing.T) {
	a := geo.Box{X: 0, Y: 0, W: 100, H: 100}
	mid := geo.V(0.5, 0.5)
	top, bottom, left, right := geo.V(0.5, 0), geo.V(0.5, 1), geo.V(0, 0.5), geo.V(1, 0.5)
	for _, c := range []struct {
		name       string
		start, end elbowEnd
		mid        float64
		want       []geo.Vec
	}{
		{
			"straight across", boxEnd("a", a, mid, faceAny), boxEnd("b", geo.Box{X: 300, Y: 0, W: 100, H: 100}, mid, faceAny), 0.5,
			[]geo.Vec{geo.V(100, 50), geo.V(300, 50)},
		},
		{
			"a step, on the line between", boxEnd("a", a, right, faceRight), boxEnd("b", geo.Box{X: 300, Y: 200, W: 100, H: 100}, left, faceLeft), 0.5,
			[]geo.Vec{geo.V(100, 50), geo.V(200, 50), geo.V(200, 250), geo.V(300, 250)},
		},
		{
			"that line, moved along", boxEnd("a", a, right, faceRight), boxEnd("b", geo.Box{X: 300, Y: 200, W: 100, H: 100}, left, faceLeft), 0.25,
			[]geo.Vec{geo.V(100, 50), geo.V(168, 50), geo.V(168, 250), geo.V(300, 250)},
		},
		{
			"a corner", boxEnd("a", a, right, faceRight), boxEnd("b", geo.Box{X: 300, Y: 200, W: 100, H: 100}, top, faceTop), 0.5,
			[]geo.Vec{geo.V(100, 50), geo.V(350, 50), geo.V(350, 200)},
		},
		{
			"out by the same side of each", boxEnd("a", a, top, faceTop), boxEnd("b", geo.Box{X: 300, Y: 0, W: 100, H: 100}, top, faceTop), 0.5,
			[]geo.Vec{geo.V(50, 0), geo.V(50, -36), geo.V(350, -36), geo.V(350, 0)},
		},
		{
			"round the back of both", boxEnd("a", a, right, faceRight), boxEnd("b", geo.Box{X: -300, Y: 200, W: 100, H: 100}, left, faceLeft), 0.5,
			[]geo.Vec{geo.V(100, 50), geo.V(136, 50), geo.V(136, 150), geo.V(-336, 150), geo.V(-336, 250), geo.V(-300, 250)},
		},
		{
			"a side only at the end", boxEnd("a", a, mid, faceAny), boxEnd("b", geo.Box{X: 300, Y: 400, W: 100, H: 100}, bottom, faceBottom), 0.5,
			[]geo.Vec{geo.V(50, 100), geo.V(50, 536), geo.V(350, 536), geo.V(350, 500)},
		},
		{
			"between points", pointEnd(geo.V(0, 0)), pointEnd(geo.V(200, 100)), 0.5,
			[]geo.Vec{geo.V(0, 0), geo.V(100, 0), geo.V(100, 100), geo.V(200, 100)},
		},
	} {
		got := routeElbowEnds(c.start, c.end, c.mid, 36, 10.5)
		if !sameVia(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// A shape turned a quarter has its top on the right.
func TestElbowSideTurnsWithItsShape(t *testing.T) {
	for _, c := range []struct {
		anchor geo.Vec
		turn   float64
		want   elbowFace
	}{
		{geo.V(0.5, 0), 0, faceTop},
		{geo.V(0.5, 1), 0, faceBottom},
		{geo.V(0, 0.5), 0, faceLeft},
		{geo.V(1, 0.5), 0, faceRight},
		{geo.V(0.2, 0.45), 0, faceLeft},
		{geo.V(0.45, 0.2), 0, faceTop},
		{geo.V(1, 1), 0, faceRight}, // a corner leans across
		{geo.V(0.5, 0.5), 0, faceAny},
		{geo.V(0.5, 0), math.Pi / 2, faceRight},
		{geo.V(1, 0.5), math.Pi / 2, faceBottom},
		{geo.V(0.5, 0.5), math.Pi / 2, faceAny},
	} {
		if got := anchorFace(c.anchor, c.turn); got != c.want {
			t.Errorf("%v turned %v: side %v, want %v", c.anchor, c.turn, got, c.want)
		}
	}
}

// An arrow comes to a line that doesn't close from the side with less of it.
func TestElbowComesToAnOpenLineFromOutside(t *testing.T) {
	far := pointEnd(geo.V(1000, 1000))
	hook := []geo.Vec{geo.V(0, 0), geo.V(100, 0), geo.V(100, 100), geo.V(0, 100)}
	end := elbowEnd{box: geo.BoxOf(hook), outline: hook, target: geo.V(50, 100), side: faceAny}
	if got := end.settleOnOpenLine(far, 36).side; got != faceBottom {
		t.Errorf("under a hook: side %v", got)
	}
	end.target = geo.V(50, 0)
	if got := end.settleOnOpenLine(far, 36).side; got != faceTop {
		t.Errorf("over a hook: side %v", got)
	}
	// With nothing of it on either side, either will do.
	end.target = geo.V(100, 50)
	if got := end.settleOnOpenLine(far, 36).side; got != faceAcross {
		t.Errorf("beside a hook: side %v", got)
	}
}

// Wherever two shapes lie and whatever their sides, a route comes out in right angles
// and stays out of both, as long as they're apart.
func TestElbowRoutesEveryLayout(t *testing.T) {
	a := geo.Box{X: 0, Y: 0, W: 120, H: 80}
	anchors := []geo.Vec{geo.V(0.5, 0.5), geo.V(0.5, 0), geo.V(0.5, 1), geo.V(0, 0.5), geo.V(1, 0.5)}
	for _, from := range anchors {
		for _, to := range anchors {
			for dx := -480.0; dx <= 480; dx += 60 {
				for dy := -360.0; dy <= 360; dy += 60 {
					if max(dx, -dx) < 240 && max(dy, -dy) < 200 {
						continue // too near for the route to go between
					}
					b := geo.Box{X: dx, Y: dy, W: 120, H: 80}
					label := fmt.Sprintf("%v to %v at %v,%v", from, to, dx, dy)
					pts := routeElbowEnds(boxEnd("a", a, from, anchorFace(from, 0)), boxEnd("b", b, to, anchorFace(to, 0)), 0.5, 36, 10.5)
					if len(pts) < 2 {
						t.Errorf("%s: %v has no legs", label, pts)
						continue
					}
					for i := 1; i < len(pts); i++ {
						if d := pts[i].Sub(pts[i-1]); d.X != 0 && d.Y != 0 {
							t.Errorf("%s: %v has a slanted leg", label, pts)
						}
						if crossesBox(pts[i-1], pts[i], a.Expand(-1)) || crossesBox(pts[i-1], pts[i], b.Expand(-1)) {
							t.Errorf("%s: %v enters a shape", label, pts)
						}
					}
					if CrossesItself(pts) {
						t.Errorf("%s: %v crosses itself", label, pts)
					}
				}
			}
		}
	}
}

// From one side of a shape to the top of one above it: out and around, leaving room
// at the end for its head, and none at the start, which has none.
func TestElbowGoesAroundToReachAFarSide(t *testing.T) {
	h := newHarness(t)
	above := h.rect(180, 190, 460, 310)
	below := h.rect(135, 615, 465, 375)
	h.SetTool(ToolArrow)
	h.drag(300, 800, 400, 300)
	id := h.Selection()[0]
	if err := h.Batch(func() error {
		c := h.shape(id).Clone()
		c.Kind, c.HeadStart, c.HeadEnd = "elbow", "none", "arrow"
		h.Put(c)
		h.Bind(id, "start", below.ID, geo.V(1, 0.5))
		h.Bind(id, "end", above.ID, geo.V(0.5, 0))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m := h.PageMat(id)
	var got []geo.Vec
	for _, p := range h.Arrow(h.shape(id)).Elbow {
		got = append(got, m.Apply(p))
	}
	want := []geo.Vec{geo.V(600, 802.5), geo.V(676, 802.5), geo.V(676, 154), geo.V(410, 154), geo.V(410, 176.5)}
	if !sameVia(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// An elbow's corners are round, by as much as its legs leave room for, and its head
// looks along its last leg however short that is.
func TestElbowCornersAreRound(t *testing.T) {
	path := roundedElbow([]geo.Vec{geo.V(0, 0), geo.V(100, 0), geo.V(100, 8), geo.V(200, 8)}, 7)
	quads := 0
	for _, seg := range path.Segs {
		if seg.Op == geo.OpQuad {
			quads++
		}
	}
	if quads != 2 {
		t.Errorf("%d rounded corners, want 2", quads)
	}
	line, _ := path.Points(1)
	if got, want := line[1], geo.V(93, 0); got.Dist(want) > 1e-9 {
		t.Errorf("the long leg ends at %v, not %v", got, want)
	}
	if got, want := line[2], geo.V(100, 2); got.Dist(want) > 1e-9 {
		t.Errorf("the short leg starts at %v, not %v", got, want)
	}
	straight := roundedElbow([]geo.Vec{geo.V(0, 0), geo.V(100, 0)}, 7)
	if len(straight.Segs) != 2 {
		t.Errorf("a straight one has %d pieces", len(straight.Segs))
	}
	head := arrowhead("arrow", []geo.Vec{geo.V(0, 0), geo.V(100, 0), geo.V(100, 6)}, false, 3.5, Color{}, true)
	if len(head) == 0 {
		t.Fatal("no head")
	}
	pts, _ := head[0].Path.Points(1)
	if len(pts) != 3 || pts[0].Y != pts[2].Y || pts[0].Y >= 6 {
		t.Errorf("head doesn't look down its leg: %v", pts)
	}
}

// Dragged into a shape with nothing held, an arrow's end crosses its edge: the line
// stops there, but the end holds on to the middle, and its handle shows that. Taken
// up again from there, it stays, rather than start from the edge.
func TestArrowEndCrossesTheEdgeToTheMiddle(t *testing.T) {
	h := newHarness(t)
	box := h.rect(400, 300, 200, 120)
	bounds := h.PageBounds(box.ID)
	center := bounds.Center()

	h.SetTool(ToolArrow)
	h.PointerMove(100, 100, Mods{}, 0)
	h.PointerDown(100, 100, 0, 1, Mods{}, 0)
	h.PointerMove(300, 200, Mods{}, 0)
	if h.Aim != nil {
		t.Errorf("aiming at %v over nothing", *h.Aim)
	}
	h.PointerMove(center.X+20, center.Y-10, Mods{}, 0)
	if h.Aim == nil || h.Aim.Dist(center) > 1e-6 {
		t.Errorf("drawing it, aiming at %v, not %v", h.Aim, center)
	}
	h.PointerUp(center.X+20, center.Y-10, Mods{}, 0)
	if h.Aim != nil {
		t.Errorf("still aiming at %v when it's drawn", *h.Aim)
	}
	id := h.Selection()[0]
	end := func() Handle {
		for _, handle := range h.Handles() {
			if handle.Kind == HandleArrowEnd && handle.Term == "end" {
				return handle
			}
		}
		t.Fatal("no end handle")
		return Handle{}
	}
	if got := end().Point; got.Dist(center) > 1e-6 {
		t.Errorf("handle at %v, not %v", got, center)
	}
	tip := h.PageMat(id).Apply(h.Arrow(h.shape(id)).End)
	if bounds.Expand(-1).Contains(tip) {
		t.Errorf("the line went in, to %v", tip)
	}
	if b := h.bindingOf(id, "end"); b.IsPrecise || b.IsExact {
		t.Errorf("bound by %+v", b)
	}

	if got := h.HandleAt(h.ToScreen(center)); got.Kind != HandleArrowEnd || got.Term != "end" {
		t.Errorf("at the middle, handle %+v", got)
	}
	h.PointerMove(center.X, center.Y, Mods{}, 0)
	h.PointerDown(center.X, center.Y, 0, 1, Mods{}, 0)
	h.PointerMove(center.X+15, center.Y+15, Mods{}, 0)
	h.PointerUp(center.X+15, center.Y+15, Mods{}, 0)
	if got := end().Point; got.Dist(center) > 1e-6 {
		t.Errorf("taken up again, handle at %v, not %v", got, center)
	}

	// Out by an edge, it holds on where it's dropped, and the handle goes with it.
	near := geo.V(590, 310)
	h.PointerMove(center.X, center.Y, Mods{}, 0)
	h.PointerDown(center.X, center.Y, 0, 1, Mods{}, 0)
	h.PointerMove(near.X, near.Y, Mods{}, 0)
	h.PointerUp(near.X, near.Y, Mods{}, 0)
	if got := end().Point; got.Dist(near) > 1e-6 {
		t.Errorf("near the corner, handle at %v, not %v", got, near)
	}
}

// Dragged right onto the middle of a shape, an end takes its line across the edge and
// all the way there, whatever its kind. Near the middle it only aims there, and a
// press there to start an arrow leaves from the edge.
func TestArrowDraggedOntoTheMiddleGoesIn(t *testing.T) {
	for _, kind := range []string{"arc", "bent", "elbow"} {
		h := newHarness(t)
		box := h.rect(400, 300, 200, 120)
		bounds := h.PageBounds(box.ID)
		center := bounds.Center()
		tipAfter := func(x, y float64) (geo.Vec, doc.ID) {
			h.SetTool(ToolArrow)
			h.PointerMove(100, 100, Mods{}, 0)
			h.PointerDown(100, 100, 0, 1, Mods{}, 0)
			h.PointerMove(x, y, Mods{}, 0)
			h.PointerUp(x, y, Mods{}, 0)
			id := h.Selection()[0]
			if kind != "arc" {
				c := h.shape(id).Clone()
				if kind == "elbow" {
					c.Kind = "elbow"
				} else {
					c.Bend = 40
				}
				if err := h.Batch(func() error { h.Put(c); return nil }); err != nil {
					t.Fatal(err)
				}
			}
			return h.PageMat(id).Apply(h.Arrow(h.shape(id)).End), id
		}
		tip, id := tipAfter(center.X+5, center.Y-5)
		if tip.Dist(center) > 1e-6 {
			t.Errorf("%s: dropped on the middle, it ended at %v, not %v", kind, tip, center)
		}
		if b := h.bindingOf(id, "end"); !b.IsExact || b.Anchor != geo.V(0.5, 0.5) {
			t.Errorf("%s: bound by %+v", kind, b)
		}
		if tip, _ = tipAfter(center.X+30, center.Y-20); bounds.Expand(-1).Contains(tip) {
			t.Errorf("%s: dropped beside the middle, it went in, to %v", kind, tip)
		}
	}
	h := newHarness(t)
	box := h.rect(400, 300, 200, 120)
	center := h.PageBounds(box.ID).Center()
	h.SetTool(ToolArrow)
	h.drag(center.X, center.Y, 900, 360)
	id := h.Selection()[0]
	if b := h.bindingOf(id, "start"); b == nil || b.IsExact {
		t.Errorf("pressed on the middle, bound by %+v", b)
	}
	if start := h.PageMat(id).Apply(h.Arrow(h.shape(id)).Start); h.PageBounds(box.ID).Expand(-1).Contains(start) {
		t.Errorf("pressed on the middle, it starts inside, at %v", start)
	}
}
