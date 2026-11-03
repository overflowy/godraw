package editor

import (
	"math"
	"slices"

	"godraw/internal/doc"
	"godraw/internal/geo"
)

// How much room a head leaves between its tip and the shape it points at, on top of
// the widths of their lines.
const elbowHeadGap = 10.0

// elbowFace is a side of a box a route can leave or reach, or what's known of it.
type elbowFace int

const (
	faceRight elbowFace = iota
	faceBottom
	faceLeft
	faceTop
	faceAcross // left or right, whichever looks to the other end
	faceUpDown // top or bottom, whichever looks to the other end
	faceAny
)

var elbowFaces = [...]elbowFace{faceRight, faceBottom, faceLeft, faceTop}

func (f elbowFace) opposite() elbowFace { return (f + 2) % 4 }

func (f elbowFace) dir() geo.Vec {
	return [...]geo.Vec{geo.V(1, 0), geo.V(0, 1), geo.V(-1, 0), geo.V(0, -1)}[f]
}

// anchorFace returns the side nearest an anchor, for a shape turned by rot from the
// arrow. Its middle says nothing. It leans to left and right, for the route not to
// flicker where the two are as near.
func anchorFace(anchor geo.Vec, rot float64) elbowFace {
	const near = 1e-6
	d := anchor.Sub(geo.V(0.5, 0.5)).Rot(rot)
	switch {
	case math.Abs(d.X) < near && math.Abs(d.Y) < near:
		return faceAny
	case math.Abs(d.X) > math.Abs(d.Y)-0.0001:
		if d.X < 0 {
			return faceLeft
		}
		return faceRight
	case d.Y < 0:
		return faceTop
	}
	return faceBottom
}

// elbowEnd is one end of an elbow arrow, in the arrow's space: what it holds on to,
// and how it wants to get there.
type elbowEnd struct {
	id      doc.ID // what it's bound to, if anything
	box     geo.Box
	target  geo.Vec
	outline []geo.Vec
	closed  bool
	side    elbowFace
	point   bool    // it heads for its target alone, with no box to go around
	exact   bool    // it stops at its target, rather than at the outline
	onEdge  bool    // its anchor is on the edge of its shape
	head    float64 // how far off the outline it stops, for its head
	minEnd  float64 // how short its last leg can be and still read as one
}

// elbowEndOf takes down an end of an arrow, at where its anchor is, or was left.
func (e *Editor) elbowEndOf(s *doc.Shape, b *doc.Binding, t *doc.Shape, at geo.Vec, head string, inv geo.Mat, outlineIn func(*doc.Shape) []geo.Vec) elbowEnd {
	end := elbowEnd{box: geo.Box{X: at.X, Y: at.Y}, target: at, side: faceAny, point: true, minEnd: strokeWidthOf(s) * 1.5}
	if b == nil || t == nil {
		return end
	}
	end.id, end.point, end.exact = t.ID, false, b.IsExact
	end.outline, end.closed = outlineIn(t), e.Geometry(t).Closed
	end.box = geo.BoxOf(end.outline)
	end.head = e.elbowHeadRoom(s, head, t)
	if b.IsPrecise {
		end.side = anchorFace(b.Anchor, inv.Mul(e.PageMat(t.ID)).Rotation())
		end.onEdge = b.Anchor.X <= elbowOnEdge || b.Anchor.X >= 1-elbowOnEdge || b.Anchor.Y <= elbowOnEdge || b.Anchor.Y >= 1-elbowOnEdge
	}
	return end
}

// elbowHeadRoom returns how far off a shape a route stops for the head at that end.
// One with no head goes up to it.
func (e *Editor) elbowHeadRoom(s *doc.Shape, head string, t *doc.Shape) float64 {
	if head == "none" || head == "" {
		return 0
	}
	room := strokeWidthOf(s)/2 + elbowHeadGap*s.Scale
	switch t.Type {
	case doc.TypeFrame, doc.TypeGroup, doc.TypeImage:
	default:
		room += strokeWidthOf(t) / 2
	}
	return room
}

// asPoint gives up on going around a box. An anchor on the edge still keeps to the
// axis of its side, and leaves room for its head.
func (end elbowEnd) asPoint() elbowEnd {
	if end.point {
		return end
	}
	side, head := faceAny, 0.0
	if end.onEdge {
		head = end.head
		switch end.side {
		case faceLeft, faceRight, faceAcross:
			side = faceAcross
		case faceTop, faceBottom, faceUpDown:
			side = faceUpDown
		}
	}
	end.side, end.head, end.point = side, head, true
	end.box = geo.Box{X: end.target.X, Y: end.target.Y}
	return end
}

// settleOnOpenLine picks the way an end comes to a line that doesn't close: across
// the line where it's aiming, from the side with less of the shape on it.
func (end elbowEnd) settleOnOpenLine(other elbowEnd, expand float64) elbowEnd {
	if end.point || end.closed || len(end.outline) < 2 {
		return end
	}
	nearest, normal := math.Inf(1), geo.V(1, 0)
	for i := 1; i < len(end.outline); i++ {
		p, q := end.outline[i-1], end.outline[i]
		if d := distToSegment(end.target, p, q); d < nearest && p != q {
			nearest, normal = d, q.Sub(p).Per()
		}
	}
	across := math.Abs(normal.X) > math.Abs(normal.Y)
	axis, lo, hi, reach := faceUpDown, faceTop, faceBottom, end.box.H*2
	if across {
		axis, lo, hi, reach = faceAcross, faceLeft, faceRight, end.box.W*2
	}
	if end.box.Expand(expand).Contains(other.target) {
		end.side = axis
		return end.asPoint()
	}
	ray := geo.V(0, reach)
	if across {
		ray = geo.V(reach, 0)
	}
	before, after := 0.0, 0.0
	for _, p := range geo.SegmentPolyline(end.target.Sub(ray), end.target.Add(ray), end.outline, false) {
		d := p.Sub(end.target).Dot(ray.Uni())
		if math.Abs(d) < 1 {
			continue
		}
		if d < 0 {
			before = max(before, -d)
		} else {
			after = max(after, d)
		}
	}
	switch {
	case before == 0 && after == 0:
		end.side = axis
	case before > after:
		end.side = hi
	default:
		end.side = lo
	}
	return end
}

// reaches says whether a point is on what an end holds on to, or as near as margin.
func (end elbowEnd) reaches(p geo.Vec, margin float64) bool {
	return end.closed && geo.PointInPolygon(p, end.outline) || geo.DistToPolyline(p, end.outline, end.closed) <= margin
}

// sideToUse returns the side an end has settled on, if it has.
func (end elbowEnd) sideToUse(other elbowEnd, edges [4]elbowEdge) (elbowFace, bool) {
	switch end.side {
	case faceAny:
		return 0, false
	case faceAcross:
		if end.box.Center().X > other.box.Center().X && edges[faceLeft].ok {
			return faceLeft, true
		}
		return faceRight, edges[faceRight].ok
	case faceUpDown:
		if end.box.Center().Y > other.box.Center().Y && edges[faceTop].ok {
			return faceTop, true
		}
		return faceBottom, edges[faceBottom].ok
	}
	return end.side, true
}

// elbowEdge is a side a route can use.
type elbowEdge struct {
	ok       bool
	point    bool    // of a point, so with no room around it
	value    float64 // where the side is along its axis
	expanded float64 // where a route clears it
	cross    float64 // where on it the route meets it, across its axis
}

// out returns where a route has cleared the side.
func (g elbowEdge) out() float64 {
	if g.point {
		return g.value
	}
	return g.expanded
}

// usableEdge finds out whether a route can use a side of an end with the other where
// it is. A side too near the other shape keeps what's out of that shape's way, and
// it's no use if that doesn't have what the end's aiming at.
func usableEdge(a, b elbowEnd, f elbowFace, expand, least float64) elbowEdge {
	lo, hi, crossLo, crossHi := a.box.X, a.box.MaxX(), a.box.Y, a.box.MaxY()
	bLo, bHi, bCrossLo, bCrossHi := b.box.X, b.box.MaxX(), b.box.Y, b.box.MaxY()
	cross := a.target.Y
	if f == faceTop || f == faceBottom {
		lo, hi, crossLo, crossHi = crossLo, crossHi, lo, hi
		bLo, bHi, bCrossLo, bCrossHi = bCrossLo, bCrossHi, bLo, bHi
		cross = a.target.X
	}
	value, sign := hi, 1.0
	if f == faceLeft || f == faceTop {
		value, sign = lo, -1
	}
	if !b.point {
		if sign > 0 {
			bLo -= least * 2
		} else {
			bHi += least * 2
		}
	}
	self := a.id != "" && a.id == b.id && a.onEdge && b.onEdge
	if value >= bLo && value <= bHi && !a.point && !b.point && !self {
		var ok bool
		crossLo, crossHi, ok = outsideRange(crossLo, crossHi, bCrossLo-expand, bCrossHi+expand)
		if !ok {
			return elbowEdge{}
		}
	}
	if cross < crossLo || cross > crossHi {
		return elbowEdge{}
	}
	return elbowEdge{ok: true, point: a.point, value: value, expanded: value + sign*expand, cross: cross}
}

// outsideRange returns the most of the range lo to hi that's out of the range blo to
// bhi, if any is.
func outsideRange(lo, hi, blo, bhi float64) (float64, float64, bool) {
	switch {
	case lo <= blo && bhi <= hi:
		if blo-lo > hi-bhi {
			return lo, blo, true
		}
		return bhi, hi, true
	case bhi <= lo || blo >= hi:
		return lo, hi, true
	case blo <= lo && hi <= bhi:
		return 0, 0, false
	case lo >= blo && lo <= bhi:
		return bhi, hi, true
	}
	return lo, blo, true
}

func usableEdges(a, b elbowEnd, expand, least float64) [4]elbowEdge {
	var edges [4]elbowEdge
	for _, f := range elbowFaces {
		edges[f] = usableEdge(a, b, f, expand, least)
	}
	return edges
}

func hasUsableEdge(edges [4]elbowEdge, side elbowFace) bool {
	switch side {
	case faceAny:
		return edges[faceRight].ok || edges[faceBottom].ok || edges[faceLeft].ok || edges[faceTop].ok
	case faceAcross:
		return edges[faceLeft].ok || edges[faceRight].ok
	case faceUpDown:
		return edges[faceTop].ok || edges[faceBottom].ok
	}
	return edges[side].ok
}

// elbowTurn is a way of looking at a route: mirrored, then with its axes exchanged.
// It's how a few shapes of route do for every pair of sides.
type elbowTurn struct{ flipX, flipY, swap bool }

var (
	turnFlipY = elbowTurn{flipY: true}
	// What brings each side round to the right.
	turnToRight = [...]elbowTurn{
		faceRight:  {},
		faceBottom: {flipX: true, swap: true},
		faceLeft:   {flipX: true, flipY: true},
		faceTop:    {flipY: true, swap: true},
	}
)

func (t elbowTurn) vec(p geo.Vec) geo.Vec {
	if t.flipX {
		p.X = -p.X
	}
	if t.flipY {
		p.Y = -p.Y
	}
	if t.swap {
		p.X, p.Y = p.Y, p.X
	}
	return p
}

func (t elbowTurn) box(b geo.Box) geo.Box {
	if t.flipX {
		b.X = -b.MaxX()
	}
	if t.flipY {
		b.Y = -b.MaxY()
	}
	if t.swap {
		b.X, b.Y, b.W, b.H = b.Y, b.X, b.H, b.W
	}
	return b
}

func (t elbowTurn) face(f elbowFace) elbowFace {
	d := t.vec(f.dir())
	for _, o := range elbowFaces {
		if o.dir() == d {
			return o
		}
	}
	return f
}

func (t elbowTurn) inverse() elbowTurn {
	if t.swap {
		return elbowTurn{flipX: t.flipY, flipY: t.flipX, swap: true}
	}
	return t
}

// then returns t followed by n.
func (t elbowTurn) then(n elbowTurn) elbowTurn {
	if t.swap {
		return elbowTurn{flipX: n.flipY != t.flipX, flipY: n.flipX != t.flipY, swap: !n.swap}
	}
	return elbowTurn{flipX: n.flipX != t.flipX, flipY: n.flipY != t.flipY, swap: n.swap}
}

func (t elbowTurn) edges(es [4]elbowEdge) [4]elbowEdge {
	flipCross := func(g *elbowEdge) { g.cross = -g.cross }
	flipValue := func(g *elbowEdge) { g.value, g.expanded = -g.value, -g.expanded }
	if t.flipX {
		es[faceLeft], es[faceRight] = es[faceRight], es[faceLeft]
		flipCross(&es[faceTop])
		flipCross(&es[faceBottom])
		flipValue(&es[faceLeft])
		flipValue(&es[faceRight])
	}
	if t.flipY {
		es[faceTop], es[faceBottom] = es[faceBottom], es[faceTop]
		flipCross(&es[faceLeft])
		flipCross(&es[faceRight])
		flipValue(&es[faceTop])
		flipValue(&es[faceBottom])
	}
	if t.swap {
		es[faceLeft], es[faceTop] = es[faceTop], es[faceLeft]
		es[faceRight], es[faceBottom] = es[faceBottom], es[faceRight]
	}
	return es
}

// elbowBox is what a route goes around, as a view sees it.
type elbowBox struct {
	box, expanded geo.Box
	edges         [4]elbowEdge
	point         bool
}

// elbowView is two ends as a turn sees them. gapX and gapY are how far the second box
// is past the first, midX and midY the lines between them a route takes, if there's
// room for them.
type elbowView struct {
	turn             elbowTurn
	a, b             elbowBox
	common           geo.Box
	gapX, gapY       float64
	midX, midY       float64
	hasMidX, hasMidY bool
	bias             geo.Vec // which way it leans when two ways are as long
	expand           float64
}

// then returns the view turned further.
func (v elbowView) then(t elbowTurn) elbowView {
	v.turn = v.turn.then(t)
	v.a.box, v.a.expanded, v.a.edges = t.box(v.a.box), t.box(v.a.expanded), t.edges(v.a.edges)
	v.b.box, v.b.expanded, v.b.edges = t.box(v.b.box), t.box(v.b.expanded), t.edges(v.b.edges)
	v.common = t.box(v.common)
	v.bias = t.vec(v.bias)
	if t.flipX {
		v.gapX, v.midX = -v.gapX, -v.midX
	}
	if t.flipY {
		v.gapY, v.midY = -v.gapY, -v.midY
	}
	if t.swap {
		v.gapX, v.gapY = v.gapY, v.gapX
		v.midX, v.midY = v.midY, v.midX
		v.hasMidX, v.hasMidY = v.hasMidY, v.hasMidX
	}
	return v
}

// elbowPath is a route between two ends, and how long it is.
type elbowPath struct {
	pts    []geo.Vec
	length float64
}

// path brings points of the view back to the arrow's space, and drops those that
// repeat and those a straight run passes through.
func (v elbowView) path(pts ...geo.Vec) *elbowPath {
	const near = 0.01
	undo := v.turn.inverse()
	out := make([]geo.Vec, 0, len(pts))
	for _, q := range pts {
		p := undo.vec(q)
		n := len(out)
		if n < 2 {
			out = append(out, p)
			continue
		}
		last, before := out[n-1], out[n-2]
		dx, dy := math.Abs(p.X-last.X), math.Abs(p.Y-last.Y)
		switch {
		case dx < near && dy < near:
		case dx < near && math.Abs(p.X-before.X) < near:
			out[n-1].Y = p.Y
		case dy < near && math.Abs(p.Y-before.Y) < near:
			out[n-1].X = p.X
		default:
			out = append(out, p)
		}
	}
	length := 0.0
	for i := 1; i < len(out); i++ {
		length += manhattan(out[i-1], out[i])
	}
	return &elbowPath{pts: out, length: length}
}

func manhattan(a, b geo.Vec) float64 { return math.Abs(a.X-b.X) + math.Abs(a.Y-b.Y) }

// routeRightToLeft goes from the right of A to the left of B.
//
//	1:              2:         3:          4:          5:
//	┌───┐           ┌───┐      ┌───┐       ┌───────┐   ┌───────┐ ┌───┐
//	│ A ├─┐         │ A ├─┐    │ A ├───┐   │ ┌───┐ │   │ ┌───┐ │ │ A ├─┐
//	└───┘ │ ┌───┐   └───┘ │    └───┘   │   │ │ A ├─┘   └─► B │ │ └───┘ │
//	      └─► B │    ┌────┘      ┌───┐ │   │ └───┘       └───┘ └───────┘
//	        └───┘    │ ┌───┐   ┌►│ B │ │   │   ┌───┐
//	                 └─► B │   │ └───┘ │   └───► B │
//	                   └───┘   └───────┘       └───┘
func routeRightToLeft(v elbowView) *elbowPath {
	if !v.a.edges[faceRight].ok || !v.b.edges[faceLeft].ok {
		return nil
	}
	if v.a.edges[faceRight].cross > v.b.edges[faceLeft].cross {
		v = v.then(turnFlipY)
	}
	a, b := v.a.edges[faceRight], v.b.edges[faceLeft]
	if v.gapX > 0 && v.hasMidX {
		return v.path(geo.V(a.value, a.cross), geo.V(v.midX, a.cross), geo.V(v.midX, b.cross), geo.V(b.value, b.cross))
	}
	if a.point || b.point {
		return nil
	}
	if v.hasMidY {
		return v.path(geo.V(a.value, a.cross), geo.V(a.expanded, a.cross), geo.V(a.expanded, v.midY), geo.V(b.expanded, v.midY), geo.V(b.expanded, b.cross), geo.V(b.value, b.cross))
	}
	top, bottom, left, right := v.common.Y, v.common.MaxY(), v.common.X, v.common.MaxX()
	len3 := math.Abs(a.value-right) + math.Abs(a.cross-bottom) + math.Abs(right-b.expanded) + math.Abs(bottom-b.cross) + v.expand + 6
	len4 := v.expand + math.Abs(a.cross-top) + math.Abs(a.expanded-left) + math.Abs(top-b.cross) + math.Abs(left-b.value) + 6 + v.bias.Y
	len5 := math.Inf(1)
	if v.gapX < 0 && v.hasMidX {
		len5 = v.expand + math.Abs(a.cross-v.a.expanded.MaxY()) + v.common.W + math.Abs(v.a.expanded.MaxY()-v.b.expanded.Y) + math.Abs(v.b.expanded.Y-b.cross) + v.expand + 8
	}
	switch {
	case len3 < len4 && len3 < len5:
		return v.path(geo.V(a.value, a.cross), geo.V(right, a.cross), geo.V(right, bottom), geo.V(b.expanded, bottom), geo.V(b.expanded, b.cross), geo.V(b.value, b.cross))
	case len4 < len5:
		return v.path(geo.V(a.value, a.cross), geo.V(a.expanded, a.cross), geo.V(a.expanded, top), geo.V(left, top), geo.V(left, b.cross), geo.V(b.value, b.cross))
	case v.hasMidX:
		return v.path(geo.V(a.value, a.cross), geo.V(a.expanded, a.cross), geo.V(a.expanded, v.a.expanded.MaxY()), geo.V(v.midX, v.a.expanded.MaxY()), geo.V(v.midX, v.b.expanded.Y), geo.V(b.expanded, v.b.expanded.Y), geo.V(b.expanded, b.cross), geo.V(b.value, b.cross))
	}
	return nil
}

// routeRightToTop goes from the right of A to the top of B.
//
//	1:              2:              3:
//	┌───┐                 ┌───┐     ┌───┐
//	│ A ├─────┐     ┌───┐ │ ┌─▼─┐   │ A ├─┐
//	└───┘     │     │ A ├─┘ │ B │   └───┘ │
//	        ┌─▼─┐   └───┘   └───┘     ┌───┘
//	        │ B │                   ┌─▼─┐
//	        └───┘                   │ B │
//	                                └───┘
//	4:        5:          6:
//	  ┌───┐     ┌───┐       ┌───┐ ┌───┐
//	┌─▼─┐ │     │ ┌─▼─┐   ┌─▼─┐ │ │ A ├─┐
//	│ B │ │     │ │ B │   │ B │ │ └───┘ │
//	└───┘ │     │ └───┘   └───┘ └───────┘
//	┌───┐ │     └───┐
//	│ A ├─┘   ┌───┐ │
//	└───┘     │ A ├─┘
//	          └───┘
func routeRightToTop(v elbowView) *elbowPath {
	a, b := v.a.edges[faceRight], v.b.edges[faceTop]
	if !a.ok || !b.ok {
		return nil
	}
	if a.cross < b.out() && b.cross > a.out() || v.a.point && v.b.expanded.Contains(v.a.box.Center()) {
		return v.path(geo.V(a.value, a.cross), geo.V(b.cross, a.cross), geo.V(b.cross, b.value))
	}
	if v.gapX > 0 && v.hasMidX && !b.point {
		return v.path(geo.V(a.value, a.cross), geo.V(v.midX, a.cross), geo.V(v.midX, b.expanded), geo.V(b.cross, b.expanded), geo.V(b.cross, b.value))
	}
	if v.gapY > 0 && !a.point && b.cross < a.expanded && v.hasMidY {
		return v.path(geo.V(a.value, a.cross), geo.V(a.expanded, a.cross), geo.V(a.expanded, v.midY), geo.V(b.cross, v.midY), geo.V(b.cross, b.value))
	}
	top, right := v.common.Y, v.common.MaxX()
	len4 := math.Abs(a.value-right) + math.Abs(a.cross-top) + math.Abs(b.cross-right) + math.Abs(b.value-top)
	len5, len6 := math.Inf(1), math.Inf(1)
	if !a.point && !b.point && v.hasMidY {
		len5 = math.Abs(a.value-a.expanded) + math.Abs(v.b.expanded.X-a.expanded) + math.Abs(v.b.expanded.X-b.cross) + math.Abs(a.cross-v.b.expanded.Y) + math.Abs(b.value-b.expanded)
	}
	if !a.point && !b.point && v.hasMidX {
		len6 = math.Abs(a.value-right) + math.Abs(a.cross-v.a.expanded.MaxY()) + math.Abs(a.expanded-b.cross) + math.Abs(v.a.expanded.MaxY()-b.expanded) + math.Abs(b.expanded-b.value)
	}
	switch {
	case len4 < len5 && len4 < len6:
		return v.path(geo.V(a.value, a.cross), geo.V(right, a.cross), geo.V(right, top), geo.V(b.cross, top), geo.V(b.cross, b.value))
	case !a.point && !b.point && v.hasMidY && len5 < len6:
		return v.path(geo.V(a.value, a.cross), geo.V(a.expanded, a.cross), geo.V(a.expanded, v.midY), geo.V(v.b.expanded.X, v.midY), geo.V(v.b.expanded.X, b.expanded), geo.V(b.cross, b.expanded), geo.V(b.cross, b.value))
	case !a.point && !b.point && v.hasMidX:
		return v.path(geo.V(a.value, a.cross), geo.V(a.expanded, a.cross), geo.V(a.expanded, v.a.expanded.MaxY()), geo.V(v.midX, v.a.expanded.MaxY()), geo.V(v.midX, b.expanded), geo.V(b.cross, b.expanded), geo.V(b.cross, b.value))
	}
	return nil
}

// routeRightToBottom is routeRightToTop, upside down.
func routeRightToBottom(v elbowView) *elbowPath { return routeRightToTop(v.then(turnFlipY)) }

// routeRightToRight goes from the right of A to the right of B, either way up.
//
//	1:        2:                3:
//	┌───┐     ┌───┐ ┌───────┐           ┌───┐
//	│ A ├─┐   │ A ├─┘ ┌───┐ │   ┌───┐   │ A ├─┐
//	└───┘ │   └───┘   │ B ◄─┘   │ B ◄─┐ └───┘ │
//	┌───┐ │           └───┘     └───┘ └───────┘
//	│ B ◄─┘
//	└───┘
func routeRightToRight(v elbowView) *elbowPath {
	a, b := v.a.edges[faceRight], v.b.edges[faceRight]
	if !a.ok || !b.ok {
		return nil
	}
	right := v.common.MaxX()
	if (v.gapX <= 0 || a.cross > v.b.expanded.MaxY() || a.cross < v.b.expanded.Y) &&
		(b.value > v.a.box.X || b.cross > v.a.expanded.MaxY() || b.cross < v.a.expanded.Y) {
		return v.path(geo.V(a.value, a.cross), geo.V(right, a.cross), geo.V(right, b.cross), geo.V(b.value, b.cross))
	}
	if !v.hasMidX {
		return nil
	}
	nearer := func(box geo.Box) float64 {
		viaBottom := math.Abs(b.cross-box.MaxY()) + math.Abs(a.cross-box.MaxY())
		viaTop := math.Abs(b.cross-box.Y) + math.Abs(a.cross-box.Y)
		if viaBottom < viaTop {
			return box.MaxY()
		}
		return box.Y
	}
	if !b.point && v.gapX >= 0 {
		y := nearer(v.b.expanded)
		return v.path(geo.V(a.value, a.cross), geo.V(v.midX, a.cross), geo.V(v.midX, y), geo.V(b.expanded, y), geo.V(b.expanded, b.cross), geo.V(b.value, b.cross))
	}
	if !a.point && v.gapX <= 0 {
		y := nearer(v.a.expanded)
		return v.path(geo.V(a.value, a.cross), geo.V(a.expanded, a.cross), geo.V(a.expanded, y), geo.V(v.midX, y), geo.V(v.midX, b.cross), geo.V(b.value, b.cross))
	}
	return nil
}

// routeSides tries for a route between two sides, in the shape those sides take.
func (v elbowView) routeSides(from, to elbowFace) *elbowPath {
	t := turnToRight[from]
	turned := v.then(t)
	switch t.face(to) {
	case faceLeft:
		return routeRightToLeft(turned)
	case faceTop:
		return routeRightToTop(turned)
	case faceBottom:
		return routeRightToBottom(turned)
	}
	return routeRightToRight(turned)
}

// shortestOf returns the route with the fewest turns of those between pairs of sides,
// and the shortest of those, leaning to the ones first listed.
func (v elbowView) shortestOf(pairs [][2]elbowFace) *elbowPath {
	var best *elbowPath
	lean := 0.0
	for _, pair := range pairs {
		lean++
		r := v.routeSides(pair[0], pair[1])
		if r != nil && (best == nil || len(r.pts) < len(best.pts) || len(r.pts) == len(best.pts) && r.length+lean < best.length) {
			best = r
		}
	}
	return best
}

// These say whether a side of the first box can turn straight into a side of the
// second, with room to do so.
func facesRightTop(a, b elbowEdge) bool {
	return a.ok && b.ok && a.out() <= b.cross && a.cross <= b.out()
}

func facesRightBottom(a, b elbowEdge) bool {
	return a.ok && b.ok && a.out() <= b.cross && a.cross >= b.out()
}

func facesLeftTop(a, b elbowEdge) bool {
	return a.ok && b.ok && a.out() >= b.cross && a.cross <= b.out()
}

func facesLeftBottom(a, b elbowEdge) bool {
	return a.ok && b.ok && a.out() >= b.cross && a.cross >= b.out()
}

// routeAuto picks the sides by how the boxes lie, going by the wider gap, and if
// that won't do, whichever works out best.
func (v elbowView) routeAuto() *elbowPath {
	aRight, aLeft := v.a.edges[faceRight], v.a.edges[faceLeft]
	bTop, bBottom := v.b.edges[faceTop], v.b.edges[faceBottom]
	var ideal *elbowPath
	switch {
	// One more for across, or an arrow held at 45° flickers between the two.
	case math.Abs(v.gapX)+1 > math.Abs(v.gapY) && v.hasMidX:
		if v.gapX > 0 {
			ideal = v.routeSides(faceRight, faceLeft)
		} else {
			ideal = v.routeSides(faceLeft, faceRight)
		}
	case v.a.point && v.b.point:
		if v.gapY > 0 {
			ideal = v.routeSides(faceBottom, faceTop)
		} else {
			ideal = v.routeSides(faceTop, faceBottom)
		}
	case facesRightTop(aRight, bTop):
		ideal = v.routeSides(faceRight, faceTop)
	case facesRightBottom(aRight, bBottom):
		ideal = v.routeSides(faceRight, faceBottom)
	case facesLeftTop(aLeft, bTop):
		ideal = v.routeSides(faceLeft, faceTop)
	case facesLeftBottom(aLeft, bBottom):
		ideal = v.routeSides(faceLeft, faceBottom)
	case v.gapY > 0 && v.hasMidY:
		ideal = v.routeSides(faceBottom, faceTop)
	case v.gapY < 0 && v.hasMidY:
		ideal = v.routeSides(faceTop, faceBottom)
	}
	if ideal != nil {
		return ideal
	}
	var pairs [][2]elbowFace
	for _, from := range elbowFaces {
		for _, to := range elbowFaces {
			if v.a.edges[from].ok && v.b.edges[to].ok {
				pairs = append(pairs, [2]elbowFace{from, to})
			}
		}
	}
	return v.shortestOf(pairs)
}

// routeFrom picks the far side for a route with the near one settled.
func (v elbowView) routeFrom(from elbowFace) *elbowPath {
	aRight, aLeft := v.a.edges[faceRight], v.a.edges[faceLeft]
	bTop, bBottom := v.b.edges[faceTop], v.b.edges[faceBottom]
	var ideal *elbowPath
	switch from {
	case faceRight:
		switch {
		case v.gapX > 0 && v.gapX > math.Abs(v.gapY) && v.hasMidX:
			ideal = v.routeSides(faceRight, faceLeft)
		case facesRightTop(aRight, bTop):
			ideal = v.routeSides(faceRight, faceTop)
		case facesRightBottom(aRight, bBottom):
			ideal = v.routeSides(faceRight, faceBottom)
		}
	case faceLeft:
		switch {
		case v.gapX < 0 && math.Abs(v.gapX) > math.Abs(v.gapY) && v.hasMidX:
			ideal = v.routeSides(faceLeft, faceRight)
		case facesLeftTop(aLeft, bTop):
			ideal = v.routeSides(faceLeft, faceTop)
		case facesLeftBottom(aLeft, bBottom):
			ideal = v.routeSides(faceLeft, faceBottom)
		}
	}
	if ideal != nil {
		return ideal
	}
	var order [4]elbowFace
	switch from {
	case faceTop:
		order = [4]elbowFace{faceBottom, faceRight, faceLeft, faceTop}
	case faceBottom:
		order = [4]elbowFace{faceTop, faceRight, faceLeft, faceBottom}
	case faceLeft:
		order = [4]elbowFace{faceRight, faceBottom, faceLeft, faceTop}
	default:
		order = [4]elbowFace{faceLeft, faceBottom, faceRight, faceTop}
	}
	pairs := make([][2]elbowFace, len(order))
	for i, to := range order {
		pairs[i] = [2]elbowFace{from, to}
	}
	return v.shortestOf(pairs)
}

// routeBetween keeps to both sides settled on, as far as what's a point can.
func (v elbowView) routeBetween(from, to elbowFace) *elbowPath {
	if r := v.routeSides(from, to); r != nil {
		return r
	}
	switch {
	case v.a.point && v.b.point:
		return v.shortestOf([][2]elbowFace{{from.opposite(), to.opposite()}, {from, to.opposite()}, {from.opposite(), to}})
	case v.a.point:
		return v.routeSides(from.opposite(), to)
	case v.b.point:
		return v.routeSides(from, to.opposite())
	}
	return nil
}

// signedGap returns how far the span from blo to bhi is past the span from alo to
// ahi, less if it's before it, and 0 if they meet.
func signedGap(alo, ahi, blo, bhi float64) float64 {
	if d := blo - ahi; d >= 0 {
		return d
	}
	return min(bhi-alo, 0)
}

// routeElbowEnds lays out a route between two ends. It runs from the first to the
// last, and it's nil if there's no route between them.
func routeElbowEnds(start, end elbowEnd, mid, expand, least float64) []geo.Vec {
	start = start.settleOnOpenLine(end, expand)
	end = end.settleOnOpenLine(start, expand)

	// With a side settled on only at the end, it works from there.
	swapped := start.side == faceAny && end.side != faceAny
	a, b := start, end
	if swapped {
		a, b = end, start
		mid = 1 - mid
	}
	edgesA, edgesB := usableEdges(a, b, expand, least), usableEdges(b, a, expand, least)
	aUsable, bUsable := hasUsableEdge(edgesA, a.side), hasUsableEdge(edgesB, b.side)
	if !aUsable || !bUsable {
		if !aUsable {
			b = b.asPoint()
		}
		if !bUsable {
			a = a.asPoint()
		}
		if b.box.Expand(expand).Contains(a.target) {
			b = b.asPoint()
		}
		if a.box.Expand(expand).Contains(b.target) {
			a = a.asPoint()
		}
		edgesA, edgesB = usableEdges(a, b, expand, least), usableEdges(b, a, expand, least)
	}

	grown := func(end elbowEnd) geo.Box {
		if end.point {
			return end.box
		}
		return end.box.Expand(expand)
	}
	expA, expB := grown(a), grown(b)
	v := elbowView{
		a:      elbowBox{box: a.box, expanded: expA, edges: edgesA, point: a.point},
		b:      elbowBox{box: b.box, expanded: expB, edges: edgesB, point: b.point},
		common: expA.Union(expB),
		gapX:   signedGap(a.box.X, a.box.MaxX(), b.box.X, b.box.MaxX()),
		gapY:   signedGap(a.box.Y, a.box.MaxY(), b.box.Y, b.box.MaxY()),
		bias:   geo.V(1, 1),
		expand: expand,
	}

	// The line between the boxes, if there's room for a route to turn on it.
	aMin, bMin := a.minEnd*3, b.minEnd*3
	needed := 0.0
	if a.point {
		needed += aMin
	} else {
		needed += least
	}
	if b.point {
		needed += bMin
	} else {
		needed += least
	}
	line := func(gap, aLo, aHi, bLo, bHi, expALo, expAHi, expBLo, expBHi float64) (float64, bool) {
		from, to := 0.0, 0.0
		switch {
		case gap > needed:
			from, to = expAHi, expBLo
			if a.point {
				from = aHi + aMin
			}
			if b.point {
				to = bLo - bMin
			}
		case gap < -needed:
			from, to = expALo, expBHi
			if a.point {
				from = aLo - aMin
			}
			if b.point {
				to = bHi + bMin
			}
		default:
			return 0, false
		}
		return from + (to-from)*mid, true
	}
	v.midX, v.hasMidX = line(v.gapX, a.box.X, a.box.MaxX(), b.box.X, b.box.MaxX(), expA.X, expA.MaxX(), expB.X, expB.MaxX())
	v.midY, v.hasMidY = line(v.gapY, a.box.Y, a.box.MaxY(), b.box.Y, b.box.MaxY(), expA.Y, expA.MaxY(), expB.Y, expB.MaxY())

	fromSide, hasFrom := a.sideToUse(b, edgesA)
	toSide, hasTo := b.sideToUse(a, edgesB)
	var route *elbowPath
	switch {
	case hasFrom && hasTo:
		route = v.routeBetween(fromSide, toSide)
	case hasFrom:
		route = v.routeFrom(fromSide)
	}
	if route == nil {
		route = v.routeAuto()
	}
	if route == nil {
		return nil
	}
	pts := route.pts
	a.castInto(pts, 0, 1, b)
	b.castInto(pts, len(pts)-1, len(pts)-2, a)
	pts = dropTinyNubs(pts, a.minEnd, b.minEnd)
	if swapped {
		slices.Reverse(pts)
	}
	return pts
}

// castInto brings the end of a route from the box of what it holds on to to its
// outline, and back off that for its head. With too little of a last leg to take that
// from, what's shown of the leg matters more than the room, and it may go a little
// into the shape to keep some.
func (end elbowEnd) castInto(pts []geo.Vec, at, next int, other elbowEnd) {
	if len(end.outline) == 0 {
		return
	}
	turn := pts[next]
	var hit geo.Vec
	found := false
	if end.exact {
		hit, found = end.target, true
	} else {
		from := end.target
		if end.closed {
			from = turn
		}
		cross := geo.SegmentPolyline(turn, end.target, end.outline, end.closed)
		if end.reaches(end.target, math.Max(1, end.head)) {
			cross = append(cross, end.target)
		}
		near := math.Inf(1)
		for _, p := range cross {
			if d := manhattan(from, p); d < near {
				hit, near, found = p, d, true
			}
		}
	}
	if !found {
		return
	}
	offset := end.head
	if leg := manhattan(turn, hit); leg < end.head*2 {
		offset = leg - end.head
	}
	if offset < end.minEnd {
		if geo.BoxOf(end.outline).Contains(other.target) {
			offset = max(0, offset)
		} else {
			offset = -end.head
		}
	}
	p := hit
	if !end.exact && offset != 0 {
		nudged := hit.Add(turn.Sub(hit).Uni().Mul(offset))
		if offset > 0 || end.reaches(nudged, 0) {
			p = nudged
		}
	}
	pts[at] = p
}

// dropTinyNubs takes out a first or last leg too short to carry a head, as a tiny L
// looks like a mistake, and keeps what's left in right angles if it can.
func dropTinyNubs(pts []geo.Vec, minA, minB float64) []geo.Vec {
	align := func(keep, removed geo.Vec, p *geo.Vec) {
		if math.Abs(keep.X-removed.X) < 1e-6 {
			p.Y = keep.Y
		} else {
			p.X = keep.X
		}
	}
	if len(pts) >= 3 {
		if a, b := pts[0], pts[1]; manhattan(a, b) < minA {
			pts = slices.Delete(pts, 1, 2)
			if len(pts) >= 3 {
				align(a, b, &pts[1])
			}
		}
	}
	if n := len(pts); n >= 3 {
		if a, b := pts[n-1], pts[n-2]; manhattan(a, b) < minB {
			pts = slices.Delete(pts, n-2, n-1)
			if n = len(pts); n >= 3 {
				align(a, b, &pts[n-2])
			}
		}
	}
	return pts
}

// elbowLegs returns how far a route keeps from what it goes around, and the least
// room a route needs between two boxes to turn there.
func elbowLegs(s *doc.Shape) (expand, least float64) {
	switch s.Size {
	case "s":
		expand, least = 28, 6
	case "l":
		expand, least = 44, 15
	case "xl":
		expand, least = 66, 30
	default:
		expand, least = 36, 10.5
	}
	return expand * s.Scale, least * s.Scale
}
