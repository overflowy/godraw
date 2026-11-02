package editor

import (
	"math"
	"slices"

	"godraw/internal/doc"
	"godraw/internal/geo"
)

// viaRoute is what a bound arrow's via points were when a transaction began, and
// where they are now, in the page.
type viaRoute struct {
	from, to   doc.ID
	start, end geo.Vec
	via, now   []geo.Vec
}

// routesNow takes down the routes of the arrows bound at both ends that have via
// points.
func (e *Editor) routesNow() map[doc.ID]*viaRoute {
	var out map[doc.ID]*viaRoute
	for id, s := range e.Doc.Shapes {
		if s.Type != doc.TypeArrow || len(s.Via()) == 0 {
			continue
		}
		start, end, ok := e.routeEnds(s)
		if !ok {
			continue
		}
		if out == nil {
			out = map[doc.ID]*viaRoute{}
		}
		via := e.pageVia(s)
		out[id] = &viaRoute{from: e.bindingOf(id, "start").To, to: e.bindingOf(id, "end").To, start: start, end: end, via: via, now: via}
	}
	return out
}

// routeEnds returns where a bound arrow's ends hold on to their shapes, in the page.
func (e *Editor) routeEnds(s *doc.Shape) (start, end geo.Vec, ok bool) {
	bs, be := e.bindingOf(s.ID, "start"), e.bindingOf(s.ID, "end")
	if bs == nil || be == nil {
		return start, end, false
	}
	ts, te := e.Doc.Shapes[bs.To], e.Doc.Shapes[be.To]
	if ts == nil || te == nil {
		return start, end, false
	}
	return e.anchorOf(bs, ts), e.anchorOf(be, te), true
}

func (e *Editor) pageVia(s *doc.Shape) []geo.Vec {
	m := e.PageMat(s.ID)
	via := s.Via()
	for i, p := range via {
		via[i] = m.Apply(p)
	}
	return via
}

// SettleRoutes keeps via points with what their arrows join, as those stand now
// against when the transaction began, so a drag that comes back undoes what it
// did on the way. When both ends moved alike, the points go along. When they
// didn't, nothing says where the points should go, and they stay, unless they
// only add turns to a route that's clear without them: then they go. It returns
// the arrows whose points moved and those that lost them. What was told otherwise
// since the transaction began stays as it was told.
func (e *Editor) SettleRoutes() (moved, cleared []doc.ID) {
	const still = 0.01
	for id, was := range e.routes {
		s := e.Doc.Shapes[id]
		if s == nil {
			delete(e.routes, id)
			continue
		}
		bs, be := e.bindingOf(id, "start"), e.bindingOf(id, "end")
		start, end, ok := e.routeEnds(s)
		if !ok || bs.To != was.from || be.To != was.to || !sameVia(e.pageVia(s), was.now) {
			delete(e.routes, id)
			continue
		}
		ds, de := start.Sub(was.start), end.Sub(was.end)
		want := was.via
		switch {
		case ds.Len() < still && de.Len() < still:
		case ds.Dist(de) <= still:
			want = make([]geo.Vec, len(was.via))
			for i, p := range was.via {
				want[i] = p.Add(ds)
			}
		default:
			if e.NeedlessVia(e.withVia(s, was.via)) > 0 {
				want = nil
			}
		}
		if sameVia(want, was.now) {
			continue
		}
		e.put(e.withVia(s, want))
		was.now = want
		if len(want) == 0 {
			cleared = append(cleared, id)
		} else {
			moved = append(moved, id)
		}
	}
	return moved, cleared
}

// withVia returns a copy of an arrow that passes through points of the page.
func (e *Editor) withVia(s *doc.Shape, via []geo.Vec) *doc.Shape {
	m := e.PageMat(s.ID).Invert()
	local := make([]geo.Vec, len(via))
	for i, p := range via {
		local[i] = m.Apply(p)
	}
	c := s.Clone()
	c.SetVia(local)
	return c
}

func sameVia(a, b []geo.Vec) bool {
	return slices.EqualFunc(a, b, func(p, q geo.Vec) bool { return p.Dist(q) < 0.01 })
}

// A via that runs a route past where it's going and back adds a turn each way. One
// turn more is only another shape of route, which may well be what was meant.
const needlessTurns = 2

// How far along an arrow or an edge a route may run before it reads as one with it.
const routeAlong = 24.0

// NeedlessVia returns how many turns an elbow arrow's via points add to a route
// that would be clear without them, or 0 if that's not what they do.
func (e *Editor) NeedlessVia(s *doc.Shape) int {
	if s.Kind != "elbow" || len(s.Via()) == 0 {
		return 0
	}
	plain := s.Clone()
	plain.SetVia(nil)
	route := e.routeOf(plain)
	extra := len(e.routeOf(s)) - len(route)
	if extra < needlessTurns || e.routeIsTaken(plain, route) {
		return 0
	}
	return extra
}

// routeOf returns the corners of an elbow arrow as it would lie, in its own space.
func (e *Editor) routeOf(s *doc.Shape) []geo.Vec {
	bs, be := e.bindingOf(s.ID, "start"), e.bindingOf(s.ID, "end")
	var ts, te *doc.Shape
	if bs != nil {
		ts = e.Doc.Shapes[bs.To]
	}
	if be != nil {
		te = e.Doc.Shapes[be.To]
	}
	return e.computeArrow(s, bs, be, ts, te, e.PageMat(s.ID)).Elbow
}

// routeIsTaken says whether a route enters or runs along a shape other than what
// its arrow joins, or runs along another arrow, which is as good a reason for a via
// as any. A box that an end is inside of is one it's leaving, as a container, and
// frames, groups and strokes aren't in a route's way.
func (e *Editor) routeIsTaken(s *doc.Shape, route []geo.Vec) bool {
	m := e.PageMat(s.ID)
	pts := make([]geo.Vec, len(route))
	for i, p := range route {
		pts[i] = m.Apply(p)
	}
	var ends []doc.ID
	var anchors []geo.Vec
	for _, term := range []string{"start", "end"} {
		if b := e.bindingOf(s.ID, term); b != nil {
			if t := e.Doc.Shapes[b.To]; t != nil {
				ends = append(ends, b.To)
				anchors = append(anchors, e.anchorOf(b, t))
			}
		}
	}
	page := e.PageOf(s.ID)
	line := geo.BoxOf(pts)
next:
	for id, o := range e.Doc.Shapes {
		if id == s.ID || e.PageOf(id) != page {
			continue
		}
		switch o.Type {
		case doc.TypeArrow:
			am := e.PageMat(id)
			line := e.Arrow(o).Line
			other := make([]geo.Vec, len(line))
			for i, p := range line {
				other[i] = am.Apply(p)
			}
			if runsAlong(pts, other) {
				return true
			}
			continue
		case doc.TypeFrame, doc.TypeGroup, doc.TypeDraw, doc.TypeHighlight, doc.TypeLine:
			continue
		}
		for _, end := range ends {
			if id == end || e.IsAncestor(end, id) || e.outerGroup(id) == e.outerGroup(end) {
				continue next
			}
		}
		box := e.PageBounds(id)
		if !line.Overlaps(box) {
			continue
		}
		for _, a := range anchors {
			if box.Contains(a) {
				continue next
			}
		}
		for i := 1; i < len(pts); i++ {
			if crossesBox(pts[i-1], pts[i], box.Expand(2)) {
				return true
			}
		}
	}
	return false
}

// runsAlong says whether two lines lie on each other for long enough to read as one.
func runsAlong(a, b []geo.Vec) bool {
	const near = 2.0
	flat := func(p, q, r, u float64) bool {
		return math.Abs(p-q) <= near && math.Abs(r-u) <= near && math.Abs(p-r) <= near
	}
	shared := func(p, q, r, u float64) float64 {
		return min(max(p, q), max(r, u)) - max(min(p, q), min(r, u))
	}
	for i := 1; i < len(a); i++ {
		for j := 1; j < len(b); j++ {
			p, q, r, u := a[i-1], a[i], b[j-1], b[j]
			if flat(p.Y, q.Y, r.Y, u.Y) && shared(p.X, q.X, r.X, u.X) >= routeAlong {
				return true
			}
			if flat(p.X, q.X, r.X, u.X) && shared(p.Y, q.Y, r.Y, u.Y) >= routeAlong {
				return true
			}
		}
	}
	return false
}

// outerGroup returns the outermost group that holds a shape, or the shape.
func (e *Editor) outerGroup(id doc.ID) doc.ID {
	for {
		s := e.Doc.Shapes[id]
		if s == nil {
			return id
		}
		p := e.Doc.Shapes[s.Parent]
		if p == nil || p.Type != doc.TypeGroup {
			return id
		}
		id = p.ID
	}
}
