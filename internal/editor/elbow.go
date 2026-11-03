package editor

import (
	"math"
	"slices"

	"godraw/internal/doc"
	"godraw/internal/geo"
)

const (
	// elbowStub is how far a route runs straight out of a side before it turns,
	// and how far it keeps from the shapes it joins as it goes around them.
	elbowStub = 24.0
	// elbowEdge is how near an edge an anchor counts as being on that side.
	elbowEdge = 0.01
	// What a route pays, in pixels of length: a route that breaks a rule always
	// loses to one that doesn't.
	elbowBend    = 40.0
	elbowThrough = 1e4 // through a shape it joins
	elbowOffSide = 1e5 // not leaving or reaching a side the way it should
	elbowBack    = 1e6 // doubling back on itself
	elbowSlant   = 1e7 // not right-angled
)

// computeElbow lays out a right-angled arrow. An anchor on the edge of a shape
// says the side the arrow leaves by, and via what it passes through. With
// neither, it goes out from each end along the longer axis, with its bend
// where elbowMidPoint says.
func (e *Editor) computeElbow(s *doc.Shape, info *ArrowInfo, bs, be *doc.Binding, ts, te *doc.Shape, inv geo.Mat, outlineIn func(*doc.Shape) []geo.Vec) *ArrowInfo {
	a, b := info.RawStart, info.RawEnd
	mid := s.ElbowMidPoint
	if mid == 0 {
		mid = 0.5
	}
	stops := slices.Concat([]geo.Vec{a}, s.Via, []geo.Vec{b})
	out := e.elbowSide(bs, ts, inv, a, stops[1])
	in := e.elbowSide(be, te, inv, b, stops[len(stops)-2])
	if len(stops) == 2 && out == (geo.Vec{}) && in == (geo.Vec{}) {
		return e.plainElbow(s, info, ts, te, mid, outlineIn)
	}
	var avoid []geo.Box
	if out != (geo.Vec{}) {
		avoid = append(avoid, geo.BoxOf(outlineIn(ts)))
	}
	if in != (geo.Vec{}) {
		avoid = append(avoid, geo.BoxOf(outlineIn(te)))
	}
	pts := routeElbow(stops, out, in, mid, avoid)

	if ts != nil {
		pts = meetOutline(pts, out, outlineIn(ts))
	}
	if te != nil {
		slices.Reverse(pts)
		pts = meetOutline(pts, in, outlineIn(te))
		if d := pts[1].Sub(pts[0]); d.Len() > strokeWidthOf(s) {
			pts[0] = pts[0].Add(d.Uni().Mul(strokeWidthOf(s)))
		}
		slices.Reverse(pts)
	}
	info.Elbow, info.Line = pts, pts
	info.Start, info.End = pts[0], pts[len(pts)-1]
	info.Mid = info.at(0.5)
	return info
}

// plainElbow goes out from each end along the longer axis.
func (e *Editor) plainElbow(s *doc.Shape, info *ArrowInfo, ts, te *doc.Shape, mid float64, outlineIn func(*doc.Shape) []geo.Vec) *ArrowInfo {
	a, b := info.RawStart, info.RawEnd
	d := b.Sub(a)
	var pts []geo.Vec
	if math.Abs(d.X) >= math.Abs(d.Y) {
		x := a.X + d.X*mid
		pts = []geo.Vec{a, geo.V(x, a.Y), geo.V(x, b.Y), b}
	} else {
		y := a.Y + d.Y*mid
		pts = []geo.Vec{a, geo.V(a.X, y), geo.V(b.X, y), b}
	}
	// Clip each bound end to its target, along its first and last legs.
	if ts != nil {
		if p, ok := lastCross(pts[0], pts[1], outlineIn(ts)); ok {
			pts[0] = p
		}
	}
	if te != nil {
		if p, ok := lastCross(pts[3], pts[2], outlineIn(te)); ok {
			pts[3] = p.Add(pts[2].Sub(pts[3]).Uni().Mul(strokeWidthOf(s)))
		}
	}
	info.Elbow, info.Line = pts, pts
	info.Start, info.End = pts[0], pts[3]
	info.Mid = geo.Med(pts[1], pts[2])
	return info
}

// elbowSide returns the way out of the side of its shape that a bound end's
// anchor is on, in the arrow's space, or nothing if the anchor is within the
// shape. An anchor in a corner is on the side that faces where the arrow goes.
func (e *Editor) elbowSide(b *doc.Binding, t *doc.Shape, inv geo.Mat, at, toward geo.Vec) geo.Vec {
	if b == nil || t == nil || !b.IsPrecise {
		return geo.Vec{}
	}
	var sides []geo.Vec
	if b.Anchor.X <= elbowEdge {
		sides = append(sides, geo.V(-1, 0))
	}
	if b.Anchor.X >= 1-elbowEdge {
		sides = append(sides, geo.V(1, 0))
	}
	if b.Anchor.Y <= elbowEdge {
		sides = append(sides, geo.V(0, -1))
	}
	if b.Anchor.Y >= 1-elbowEdge {
		sides = append(sides, geo.V(0, 1))
	}
	m := inv.Mul(e.PageMat(t.ID))
	var best geo.Vec
	most := math.Inf(-1)
	for _, side := range sides {
		out := squared(m.ApplyDir(side))
		if d := toward.Sub(at).Dot(out); d > most {
			best, most = out, d
		}
	}
	return best
}

// squared returns the axis a direction is nearest, a shape that turned a little
// still having sides.
func squared(d geo.Vec) geo.Vec {
	if math.Abs(d.X) >= math.Abs(d.Y) {
		return geo.V(signOf(d.X), 0)
	}
	return geo.V(0, signOf(d.Y))
}

// meetOutline brings the start of a route to the shape it leaves. Out of a side,
// that's along the way it leaves, as the outline of an ellipse is within its
// box. The search starts outside it, as an anchor can fall just inside the edge
// it's on, and rounding in a frame's coordinates puts one there. From within the
// shape, it's where the route crosses the outline.
func meetOutline(pts []geo.Vec, out geo.Vec, outline []geo.Vec) []geo.Vec {
	if out != (geo.Vec{}) {
		box := geo.BoxOf(outline)
		from := pts[0]
		far := out.Mul(box.W + box.H)
		near := math.Inf(1)
		for _, p := range geo.SegmentPolyline(from.Add(far), from.Sub(far), outline, true) {
			if d := p.Dist(from); d < near {
				pts[0], near = p, d
			}
		}
		return pts
	}
	for i := 1; i < len(pts); i++ {
		if p, ok := lastCross(pts[i-1], pts[i], outline); ok {
			return slices.Concat([]geo.Vec{p}, pts[i:])
		}
	}
	return pts
}

// routeElbow joins stops with right angles. out and in are the ways out of the
// sides of the shapes at its ends, if they have them. It keeps out of avoid, and can
// run along the edges of lanes as well as a little way off what it avoids.
func routeElbow(stops []geo.Vec, out, in geo.Vec, mid float64, avoid []geo.Box, lanes ...geo.Box) []geo.Vec {
	pts := []geo.Vec{stops[0]}
	heading := out
	for i := 1; i < len(stops); i++ {
		// A leg reaches a point it passes through already going the way the next
		// leg goes, so that the route keeps to where that point takes it, rather
		// than go on as before and step out to it and back.
		arrive, then := geo.Vec{}, geo.Vec{}
		if i == len(stops)-1 {
			arrive = in
		} else {
			then = squared(stops[i+1].Sub(stops[i]))
		}
		pts = append(pts, elbowLeg(stops[i-1], stops[i], heading, i == 1 && out != (geo.Vec{}), arrive, then, mid, avoid, lanes)[1:]...)
		pts = tidyRoute(pts)
		if n := len(pts); n >= 2 && pts[n-1] != pts[n-2] {
			heading = pts[n-1].Sub(pts[n-2]).Uni()
		}
	}
	if len(pts) < 2 {
		pts = append(pts, stops[len(stops)-1])
	}
	return pts
}

// elbowLeg returns the cheapest way from a to b of the few that can be right,
// trying first those that look best when several cost the same. heading is how
// it's going at a, which is a rule if side says it leaves a shape's side; in is
// the way out of the side of the shape at b, and then the way it would like to
// be going there.
func elbowLeg(a, b, heading geo.Vec, side bool, in, then geo.Vec, mid float64, avoid, lanes []geo.Box) []geo.Vec {
	xs := []float64{a.X + (b.X-a.X)*mid}
	ys := []float64{a.Y + (b.Y-a.Y)*mid}
	for _, box := range avoid {
		xs = append(xs, box.X-elbowStub, box.MaxX()+elbowStub)
		ys = append(ys, box.Y-elbowStub, box.MaxY()+elbowStub)
	}
	for _, box := range lanes {
		xs = append(xs, box.X, box.MaxX())
		ys = append(ys, box.Y, box.MaxY())
	}
	starts := [][]geo.Vec{{a}}
	if side {
		starts = [][]geo.Vec{{a, a.Add(heading.Mul(elbowStub))}, {a}}
	}
	ends := [][]geo.Vec{{b}}
	if in != (geo.Vec{}) {
		ends = [][]geo.Vec{{b.Add(in.Mul(elbowStub)), b}, {b}}
	}
	var best []geo.Vec
	least := math.Inf(1)
	for _, from := range starts {
		for _, to := range ends {
			p, q := from[len(from)-1], to[0]
			try := func(corners ...geo.Vec) {
				route := tidyRoute(slices.Concat(from, corners, to))
				if c := elbowCost(route, heading, side, in, then, avoid); c < least {
					best, least = route, c
				}
			}
			try()
			try(geo.V(xs[0], p.Y), geo.V(xs[0], q.Y))
			try(geo.V(p.X, ys[0]), geo.V(q.X, ys[0]))
			try(geo.V(q.X, p.Y))
			try(geo.V(p.X, q.Y))
			for _, x := range xs[1:] {
				try(geo.V(x, p.Y), geo.V(x, q.Y))
			}
			for _, y := range ys[1:] {
				try(geo.V(p.X, y), geo.V(q.X, y))
			}
			for _, x := range xs {
				for _, y := range ys {
					try(geo.V(x, p.Y), geo.V(x, y), geo.V(q.X, y))
					try(geo.V(p.X, y), geo.V(x, y), geo.V(x, q.Y))
				}
			}
		}
	}
	return best
}

// elbowCost says what a route pays for its length, its bends and the rules it
// breaks.
func elbowCost(pts []geo.Vec, heading geo.Vec, side bool, in, then geo.Vec, avoid []geo.Box) float64 {
	if len(pts) < 2 {
		return 0
	}
	cost := 0.0
	last := heading
	for i := 1; i < len(pts); i++ {
		d := pts[i].Sub(pts[i-1])
		cost += d.Len()
		if d.X != 0 && d.Y != 0 {
			cost += elbowSlant
		}
		dir := d.Uni()
		switch turn := dir.Dot(last); {
		case last == (geo.Vec{}):
		case turn < -0.5:
			cost += elbowBack
		case turn < 0.5:
			cost += elbowBend
			if i == 1 && side {
				cost += elbowOffSide
			}
		}
		last = dir
		for _, box := range avoid {
			if crossesBox(pts[i-1], pts[i], box.Expand(-1)) {
				cost += elbowThrough
			}
		}
	}
	if in != (geo.Vec{}) {
		switch turn := last.Dot(in); {
		case turn > 0.5:
			cost += elbowBack
		case turn > -0.5:
			cost += elbowOffSide
		}
	}
	if then != (geo.Vec{}) && last.Dot(then) < 0.5 {
		cost += elbowBend
	}
	return cost
}

// crossesBox says whether a level or upright piece of line enters a box.
func crossesBox(p, q geo.Vec, b geo.Box) bool {
	return max(p.X, q.X) > b.X && min(p.X, q.X) < b.MaxX() && max(p.Y, q.Y) > b.Y && min(p.Y, q.Y) < b.MaxY()
}

// tidyRoute drops points that repeat, and those a straight run passes through.
func tidyRoute(pts []geo.Vec) []geo.Vec {
	out := make([]geo.Vec, 0, len(pts))
	for _, p := range pts {
		if n := len(out); n > 0 && out[n-1].Dist(p) < 0.01 {
			continue
		}
		if n := len(out); n >= 2 {
			a, b := out[n-1].Sub(out[n-2]).Uni(), p.Sub(out[n-1]).Uni()
			if a.Dist(b) < 1e-6 {
				out[n-1] = p
				continue
			}
		}
		out = append(out, p)
	}
	return out
}
