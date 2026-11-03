package editor

import (
	"cmp"
	"math"
	"slices"
	"time"

	"godraw/internal/doc"
	"godraw/internal/geo"
)

// viaRoute is what a bound arrow's via points were when a transaction began, and
// where they are now, in the page.
type viaRoute struct {
	from, to   doc.ID
	start, end geo.Vec
	via, now   []geo.Vec
	auto       bool // the app chose the points, to go around something
}

// RouteChanges says what SettleRoutes did to arrows.
type RouteChanges struct {
	Moved    []doc.ID // their points went along, or the app's own weren't needed
	Cleared  []doc.ID // they lost points they were given, as needless
	Replaced []doc.ID // they took another way than the points they were given
	Around   []doc.ID // the app gave them points, to go around something
}

// routesNow takes down the routes of the elbow arrows bound at both ends, and any
// others with via points. Those that run level look after themselves.
func (e *Editor) routesNow() map[doc.ID]*viaRoute {
	var out map[doc.ID]*viaRoute
	for id, s := range e.Doc.Shapes {
		if s.Type != doc.TypeArrow || (s.Kind != "elbow" && len(s.Via) == 0) {
			continue
		}
		if s.Straight != "" {
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
		out[id] = &viaRoute{from: e.bindingOf(id, "start").To, to: e.bindingOf(id, "end").To, start: start, end: end, via: via, now: via, auto: s.ViaAuto}
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
	var via []geo.Vec
	for _, p := range s.Via {
		via = append(via, m.Apply(p))
	}
	return via
}

// SettleRoutes keeps elbow arrows' routes right for what they join, as those stand
// now against when the transaction began, so a drag that comes back undoes what it
// did on the way. An arrow neither of whose ends moved stays as it is, and when both
// moved alike, its points go along. When they didn't, nothing says where points it
// was given should go, and they stay, unless they only add turns to a route that's
// clear without them, when they go, or a better way can be found, when that's taken.
// An arrow without points, or with the app's own, goes the way it would on its own
// if that's clear, and if it isn't, around what's in the way, if it can. What was told
// otherwise since the transaction began stays as it was told.
func (e *Editor) SettleRoutes() RouteChanges { return e.settleRoutes(0) }

// settleRoutes settles routes, looking for ways around for as long as budget says, if
// it says. A drag goes on looking the next time it moves, and each arrow gets its turn,
// by the order of a map; when it ends, all of them are settled.
func (e *Editor) settleRoutes(budget time.Duration) RouteChanges {
	const still = 0.01
	var out RouteChanges
	began := time.Now()
	e.pageShapes = map[doc.ID][]routeShape{}
	defer func() { e.pageShapes = nil }()
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
		want, auto, other := was.via, was.auto, false
		switch {
		case ds.Len() < still && de.Len() < still:
		case ds.Dist(de) <= still:
			want = make([]geo.Vec, len(was.via))
			for i, p := range was.via {
				want[i] = p.Add(ds)
			}
		case budget > 0 && time.Since(began) > budget:
			continue
		case len(was.via) == 0 || was.auto:
			if plain := e.withVia(s, nil); !e.routeEntersShape(plain, e.routeOf(plain)) {
				want, auto = nil, false
			} else if via, ok := e.wayAround(plain); ok {
				want, auto = via, true
			}
		default:
			c := e.withVia(s, was.via)
			if e.NeedlessVia(c) > 0 {
				want = nil
			} else if via, ok := e.betterVia(c); ok {
				want, other = via, true
			}
		}
		if sameVia(want, was.now) {
			continue
		}
		c := e.withVia(s, want)
		if auto && len(want) > 0 {
			c.ViaAuto = true
		}
		e.put(c)
		was.now = want
		switch {
		case other:
			out.Replaced = append(out.Replaced, id)
		case auto && len(want) > 0 && ds.Dist(de) > still:
			out.Around = append(out.Around, id)
		case len(want) == 0 && !was.auto:
			out.Cleared = append(out.Cleared, id)
		default:
			out.Moved = append(out.Moved, id)
		}
	}
	return out
}

// betterVia finds via points, in the page, for a clear route with fewer turns than
// an elbow arrow's, or any clear route if its own runs into something or itself.
func (e *Editor) betterVia(s *doc.Shape) ([]geo.Vec, bool) {
	route := e.routeOf(s)
	stuck := e.routeIsTaken(s, route) || crossesItself(route)
	return e.findVia(s, func(c *doc.Shape, r []geo.Vec) bool {
		return (stuck || len(r) < len(route)) && !e.routeRunsAlongArrow(c, r)
	})
}

// wayAround finds via points, in the page, for a route that keeps out of shapes. An
// arrow without points doesn't mind running along another: a tree's branches share a
// trunk, and that's for whoever draws it to part.
func (e *Editor) wayAround(s *doc.Shape) ([]geo.Vec, bool) {
	return e.findVia(s, func(*doc.Shape, []geo.Vec) bool { return true })
}

// findVia returns the first of the candidates whose route will do, keeps out of
// shapes, and neither crosses itself nor goes further than a detour should. It looks
// for one that keeps its distance first, and takes what room there is if none does.
func (e *Editor) findVia(s *doc.Shape, will func(c *doc.Shape, route []geo.Vec) bool) ([]geo.Vec, bool) {
	plain := s.Clone()
	plain.SetVia(nil)
	longest := geo.PolyLength(e.routeOf(plain)) + 2*routeReach
	for _, margin := range []float64{routeClearance, routeTouch} {
		for _, via := range e.viaCandidates(s, margin) {
			c := e.withVia(s, via)
			r := e.routeOf(c)
			if !e.routeComesWithin(c, r, margin-0.5) && will(c, r) && !crossesItself(r) && geo.PolyLength(r) <= longest {
				return via, true
			}
		}
	}
	return nil, false
}

// viaCandidates returns the via points to try for an arrow, in the page, the fewest
// first: none, then those of a route that keeps a margin from what's in its way.
func (e *Editor) viaCandidates(s *doc.Shape, margin float64) [][]geo.Vec {
	plain := s.Clone()
	plain.SetVia(nil)
	bs, be := e.bindingOf(s.ID, "start"), e.bindingOf(s.ID, "end")
	var ts, te *doc.Shape
	if bs != nil {
		ts = e.Doc.Shapes[bs.To]
	}
	if be != nil {
		te = e.Doc.Shapes[be.To]
	}
	m := e.PageMat(s.ID)
	inv := m.Invert()
	info := e.computeArrow(plain, bs, be, ts, te, m)
	a, b := info.RawStart, info.RawEnd
	out, in := e.elbowSide(bs, ts, inv, a, b), e.elbowSide(be, te, inv, b, a)
	var avoid []geo.Box
	if out != (geo.Vec{}) {
		avoid = append(avoid, inv.BoundsOf(e.PageBounds(ts.ID)))
	}
	if in != (geo.Vec{}) {
		avoid = append(avoid, inv.BoundsOf(e.PageBounds(te.ID)))
	}
	// It takes account of what it runs into, and looks again, for the fewer it keeps
	// out of the fewer ways there are to try.
	boxes := e.routeObstacles(s)
	slices.SortFunc(boxes, func(p, q geo.Box) int {
		return cmp.Or(cmp.Compare(p.X, q.X), cmp.Compare(p.Y, q.Y), cmp.Compare(p.W, q.W), cmp.Compare(p.H, q.H))
	})
	var pts []geo.Vec
	var lanes []geo.Box
	for added := 0; ; {
		pts = routeElbow([]geo.Vec{a, b}, out, in, 0.5, avoid, lanes...)
		more := false
		for i, box := range boxes {
			kept := inv.BoundsOf(box).Expand(margin)
			if added < routeAvoided && meetsBox(pts, kept) {
				avoid, lanes = append(avoid, kept), append(lanes, kept)
				boxes = slices.Delete(boxes, i, i+1)
				added++
				more = true
				break
			}
		}
		if !more {
			break
		}
	}
	list := [][]geo.Vec{nil}
	if len(pts) < 3 {
		return list
	}
	corners := make([]geo.Vec, 0, len(pts)-2)
	for _, p := range pts[1 : len(pts)-1] {
		corners = append(corners, m.Apply(p))
	}
	for _, p := range corners {
		list = append(list, []geo.Vec{p})
	}
	if len(corners) > 1 {
		list = append(list, corners)
	}
	return list
}

// crossesItself says whether a route crosses or runs back along its own way.
func crossesItself(pts []geo.Vec) bool {
	for i := 1; i < len(pts); i++ {
		for j := i + 1; j < len(pts); j++ {
			if runsAlong(pts[i-1:i+1], pts[j-1:j+1]) {
				return true
			}
			if j > i+1 {
				if _, ok := geo.SegmentsCross(pts[i-1], pts[i], pts[j-1], pts[j]); ok {
					return true
				}
			}
		}
	}
	return false
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

// How far a via can lie off the way a route would go without it before that's more
// than a choice of route.
const farOutOfTheWay = 120.0

// How much further than the direct way a route may go to get around something, by
// half, and how many shapes it may go around: routes are worked out as a shape is
// dragged, and the time they take grows fast with what they keep out of.
const (
	// How far a way around keeps from what it goes around.
	routeClearance = 12.0
	routeTouch     = 2.0 // nearer than this is running into it
	routeReach     = 200.0
	routeAvoided   = 6
	// How long a frame of a drag spends on them.
	routeFrameBudget = 6 * time.Millisecond
)

// How far along an arrow or an edge a route may run before it reads as one with it.
const routeAlong = 24.0

// NeedlessVia returns how many turns an elbow arrow's via points add to a route
// that would be clear without them, or 0 if that's not what they do.
func (e *Editor) NeedlessVia(s *doc.Shape) int {
	if s.Kind != "elbow" || len(s.Via) == 0 {
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

// ViaDetour returns how far an elbow arrow's via points lie off a route that would
// be clear without them, if that's far, or 0.
func (e *Editor) ViaDetour(s *doc.Shape) float64 {
	if s.Kind != "elbow" || len(s.Via) == 0 {
		return 0
	}
	plain := s.Clone()
	plain.SetVia(nil)
	route := e.routeOf(plain)
	far := 0.0
	for _, p := range s.Via {
		near := math.Inf(1)
		for i := 1; i < len(route); i++ {
			near = min(near, distToSegment(p, route[i-1], route[i]))
		}
		far = max(far, near)
	}
	if far < farOutOfTheWay || e.routeIsTaken(plain, route) {
		return 0
	}
	return far
}

func distToSegment(p, a, b geo.Vec) float64 {
	d := b.Sub(a)
	l := d.Dot(d)
	if l == 0 {
		return p.Dist(a)
	}
	return p.Dist(a.Add(d.Mul(math.Max(0, math.Min(1, p.Sub(a).Dot(d)/l)))))
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
// as any.
func (e *Editor) routeIsTaken(s *doc.Shape, route []geo.Vec) bool {
	return e.routeEntersShape(s, route) || e.routeRunsAlongArrow(s, route)
}

func (e *Editor) routeRunsAlongArrow(s *doc.Shape, route []geo.Vec) bool {
	m := e.PageMat(s.ID)
	pts := make([]geo.Vec, len(route))
	for i, p := range route {
		pts[i] = m.Apply(p)
	}
	page := e.PageOf(s.ID)
	for id, o := range e.Doc.Shapes {
		if o.Type != doc.TypeArrow || id == s.ID || e.PageOf(id) != page {
			continue
		}
		am := e.PageMat(id)
		other := e.Arrow(o).Line
		theirs := make([]geo.Vec, len(other))
		for i, p := range other {
			theirs[i] = am.Apply(p)
		}
		if runsAlong(pts, theirs) {
			return true
		}
	}
	return false
}

// routeEntersShape says whether a route enters or runs along a shape other than what
// its arrow joins.
func (e *Editor) routeEntersShape(s *doc.Shape, route []geo.Vec) bool {
	return e.routeComesWithin(s, route, routeTouch)
}

// routeComesWithin says whether a route comes within a distance of a shape other than
// what its arrow joins.
func (e *Editor) routeComesWithin(s *doc.Shape, route []geo.Vec, margin float64) bool {
	m := e.PageMat(s.ID)
	pts := make([]geo.Vec, len(route))
	for i, p := range route {
		pts[i] = m.Apply(p)
	}
	line := geo.BoxOf(pts).Expand(margin)
	for _, box := range e.routeObstacles(s) {
		if line.Overlaps(box) && meetsBox(pts, box.Expand(margin)) {
			return true
		}
	}
	return false
}

func meetsBox(pts []geo.Vec, box geo.Box) bool {
	for i := 1; i < len(pts); i++ {
		if crossesBox(pts[i-1], pts[i], box) {
			return true
		}
	}
	return false
}

// routeObstacles returns the boxes, in the page, of what a route should keep out
// of. What its arrow joins isn't, nor what's in the same group as that, nor a box
// that an end is inside of, which it's leaving, as a container. Frames, groups and
// strokes aren't in a route's way.
func (e *Editor) routeObstacles(s *doc.Shape) []geo.Box {
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
	var out []geo.Box
next:
	for _, o := range e.shapesOfPage(e.PageOf(s.ID)) {
		for _, end := range ends {
			if o.id == end || e.IsAncestor(end, o.id) || o.outer == e.outerGroup(end) {
				continue next
			}
		}
		for _, a := range anchors {
			if o.box.Contains(a) {
				continue next
			}
		}
		out = append(out, o.box)
	}
	return out
}

// routeShape is a shape on a page that a route may run into.
type routeShape struct {
	id, outer doc.ID
	box       geo.Box
}

// shapesOfPage returns what on a page could be in a route's way, kept for as long as
// SettleRoutes runs, which asks for each of the routes it looks at.
func (e *Editor) shapesOfPage(page doc.ID) []routeShape {
	if list, ok := e.pageShapes[page]; ok {
		return list
	}
	var list []routeShape
	for id, o := range e.Doc.Shapes {
		switch o.Type {
		case doc.TypeArrow, doc.TypeFrame, doc.TypeGroup, doc.TypeDraw, doc.TypeHighlight, doc.TypeLine:
			continue
		}
		if e.PageOf(id) == page {
			list = append(list, routeShape{id: id, outer: e.outerGroup(id), box: e.PageBounds(id)})
		}
	}
	if e.pageShapes != nil {
		e.pageShapes[page] = list
	}
	return list
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
