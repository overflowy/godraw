package editor

import (
	"math"
	"slices"

	"godraw/internal/doc"
	"godraw/internal/geo"
)

// ArrowInfo is how an arrow lies, in its own space.
type ArrowInfo struct {
	// Start and End are where it ends after its ends are clipped to the
	// shapes they are bound to; RawStart and RawEnd before.
	Start, End, RawStart, RawEnd geo.Vec
	// Mid is the point its bend handle shows at.
	Mid geo.Vec
	// Arc, when it bends: its circle, the angle it starts at and how far
	// it turns, signed.
	Arc           bool
	Center        geo.Vec
	Radius        float64
	AngleA, Sweep float64
	// Elbow is the corners of a right-angled arrow.
	Elbow []geo.Vec
	Line  []geo.Vec
}

type arrowMemo struct {
	s      *doc.Shape
	starts [2]*doc.Shape
	binds  [2]*doc.Binding
	mats   [3]geo.Mat
	info   *ArrowInfo
	// What the arrow draws, by theme, as it lies now.
	display [2][]Prim
	drawn   [2]bool
}

// bindingOf returns the binding of an end of an arrow, if any.
func (e *Editor) bindingOf(arrow doc.ID, terminal string) *doc.Binding {
	e.indexBindings()
	for _, b := range e.bindFrom[arrow] {
		if b.Terminal == terminal {
			return b
		}
	}
	return nil
}

// indexBindings finds bindings by their arrows and targets: arrows ask for
// theirs each frame.
func (e *Editor) indexBindings() {
	if e.bindFrom != nil {
		return
	}
	e.bindFrom = make(map[doc.ID][]*doc.Binding, len(e.Doc.Bindings))
	e.bindTo = make(map[doc.ID][]*doc.Binding, len(e.Doc.Bindings))
	for _, b := range e.Doc.Bindings {
		e.bindFrom[b.From] = append(e.bindFrom[b.From], b)
		e.bindTo[b.To] = append(e.bindTo[b.To], b)
	}
}

// bindingsTo returns the arrows' bindings to a shape.
func (e *Editor) bindingsTo(id doc.ID) []*doc.Binding {
	e.indexBindings()
	return slices.Clone(e.bindTo[id])
}

// Arrow returns how an arrow lies, from its props and what it is bound to.
func (e *Editor) Arrow(s *doc.Shape) *ArrowInfo {
	bs, be := e.bindingOf(s.ID, "start"), e.bindingOf(s.ID, "end")
	var ts, te *doc.Shape
	if bs != nil {
		ts = e.Doc.Shapes[bs.To]
	}
	if be != nil {
		te = e.Doc.Shapes[be.To]
	}
	am := e.PageMat(s.ID)
	mats := [3]geo.Mat{am}
	if ts != nil {
		mats[1] = e.PageMat(ts.ID)
	}
	if te != nil {
		mats[2] = e.PageMat(te.ID)
	}
	// An arrow is computed again when it, its bindings, what it is bound to
	// or where any of them are changes.
	if m := e.arrows[s.ID]; m != nil && m.s == s && m.starts == [2]*doc.Shape{ts, te} &&
		m.binds == [2]*doc.Binding{bs, be} && m.mats == mats {
		return m.info
	}
	info := e.computeArrow(s, bs, be, ts, te, am)
	e.arrows[s.ID] = &arrowMemo{s: s, starts: [2]*doc.Shape{ts, te}, binds: [2]*doc.Binding{bs, be}, mats: mats, info: info}
	return info
}

// anchorOf returns where a binding points, in the page.
func (e *Editor) anchorOf(b *doc.Binding, target *doc.Shape) geo.Vec {
	box := e.Geometry(target).Bounds
	a := geo.V(0.5, 0.5)
	if b.IsPrecise {
		a = b.Anchor
	}
	return e.PageMat(target.ID).Apply(geo.V(box.X+a.X*box.W, box.Y+a.Y*box.H))
}

func strokeWidthOf(s *doc.Shape) float64 { return strokeOf(s.Size) * s.Scale }

func (e *Editor) computeArrow(s *doc.Shape, bs, be *doc.Binding, ts, te *doc.Shape, am geo.Mat) *ArrowInfo {
	inv := am.Invert()
	info := &ArrowInfo{RawStart: s.Start, RawEnd: s.End}
	if bs != nil && ts != nil {
		info.RawStart = inv.Apply(e.anchorOf(bs, ts))
	}
	if be != nil && te != nil {
		info.RawEnd = inv.Apply(e.anchorOf(be, te))
	}
	a, b := info.RawStart, info.RawEnd

	// Each bound end stops at its target's outline, a little off it. The
	// outline comes into the arrow's space.
	outlineIn := func(t *doc.Shape) []geo.Vec {
		g := e.Geometry(t)
		m := inv.Mul(e.PageMat(t.ID))
		out := make([]geo.Vec, len(g.Outline))
		for i, p := range g.Outline {
			out[i] = m.Apply(p)
		}
		return out
	}
	sw := strokeWidthOf(s)
	offsetOf := func(head string) float64 {
		if head == "none" {
			return sw/2 + 2
		}
		return sw*1.5 + 4
	}

	if s.Kind == "elbow" {
		return e.computeElbow(s, info, ts, te, outlineIn)
	}

	dist := a.Dist(b)
	if math.Abs(s.Bend) < 1 || dist < 1 {
		// Straight.
		info.Start, info.End = a, b
		if ts != nil {
			if p, ok := lastCross(a, b, outlineIn(ts)); ok {
				info.Start = p.Add(b.Sub(a).Uni().Mul(offsetOf(s.HeadStart)))
			}
		}
		if te != nil {
			if p, ok := lastCross(b, a, outlineIn(te)); ok {
				info.End = p.Add(a.Sub(b).Uni().Mul(offsetOf(s.HeadEnd)))
			}
		}
		// Targets overlapping: ends that passed each other stay at the middle.
		if b.Sub(a).Dot(info.End.Sub(info.Start)) < 0 {
			m := geo.Med(info.Start, info.End)
			info.Start, info.End = m, m
		}
		info.Mid = geo.Med(info.Start, info.End)
		info.Line = []geo.Vec{info.Start, info.End}
		return info
	}

	u := b.Sub(a).Uni()
	mid := geo.Med(a, b).Add(u.Per().Mul(-s.Bend))
	center, radius, ok := geo.Circle(a, mid, b)
	if !ok {
		info.Start, info.End, info.Mid = a, b, geo.Med(a, b)
		info.Line = []geo.Vec{a, b}
		return info
	}
	aa, am2, ab := center.Angle(a), center.Angle(mid), center.Angle(b)
	sweep := sweepThrough(aa, am2, ab)

	at := func(th float64) geo.Vec { return center.Add(geo.V(radius, 0).Rot(th)) }
	sample := func(from, turn float64) []geo.Vec {
		n := int(math.Max(8, math.Min(96, math.Abs(turn)*radius/6)))
		pts := make([]geo.Vec, n+1)
		for i := range pts {
			pts[i] = at(from + turn*float64(i)/float64(n))
		}
		return pts
	}

	startTurn, endTurn := 0.0, sweep
	// Walk along the arc to where it leaves the start's target, and back
	// from its end to where it enters the end's target.
	if ts != nil {
		if t, ok := crossAlong(sample(aa, sweep), outlineIn(ts), false); ok {
			startTurn = sweep*t + signOf(sweep)*offsetOf(s.HeadStart)/radius
		}
	}
	if te != nil {
		if t, ok := crossAlong(sample(aa, sweep), outlineIn(te), true); ok {
			endTurn = sweep*t - signOf(sweep)*offsetOf(s.HeadEnd)/radius
		}
	}
	if signOf(sweep)*(endTurn-startTurn) < 0 {
		mid := (startTurn + endTurn) / 2
		startTurn, endTurn = mid, mid
	}
	info.Arc = true
	info.Center, info.Radius = center, radius
	info.AngleA, info.Sweep = aa+startTurn, endTurn-startTurn
	info.Start, info.End = at(info.AngleA), at(info.AngleA+info.Sweep)
	info.Mid = at(info.AngleA + info.Sweep/2)
	info.Line = sample(info.AngleA, info.Sweep)
	return info
}

func signOf(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}

// sweepThrough returns the signed turn from angle a to angle b that passes
// through angle m.
func sweepThrough(a, m, b float64) float64 {
	cw := geo.NormAngle(b - a)
	if geo.NormAngle(m-a) <= cw {
		return cw
	}
	return cw - 2*math.Pi
}

// lastCross returns where the segment from a to b last crosses a closed
// outline around a: where a line leaving the target's inside leaves it.
func lastCross(a, b geo.Vec, outline []geo.Vec) (geo.Vec, bool) {
	cross := geo.SegmentPolyline(a, b, outline, true)
	if len(cross) == 0 {
		return geo.Vec{}, false
	}
	best, bestD := cross[0], cross[0].Dist(a)
	for _, p := range cross[1:] {
		if d := p.Dist(a); d > bestD {
			best, bestD = p, d
		}
	}
	return best, true
}

// crossAlong returns how far along a polyline, from 0 to 1, it crosses an
// outline for the last time, or from its end for the first time if atEnd.
func crossAlong(line, outline []geo.Vec, atEnd bool) (float64, bool) {
	n := len(line) - 1
	if atEnd {
		for i := n; i > 0; i-- {
			if c := geo.SegmentPolyline(line[i-1], line[i], outline, true); len(c) > 0 {
				best := c[0]
				for _, p := range c[1:] {
					if p.Dist(line[i-1]) < best.Dist(line[i-1]) {
						best = p
					}
				}
				f := best.Dist(line[i-1]) / math.Max(line[i-1].Dist(line[i]), 1e-9)
				return (float64(i-1) + f) / float64(n), true
			}
		}
		return 0, false
	}
	for i := 1; i <= n; i++ {
		if c := geo.SegmentPolyline(line[i-1], line[i], outline, true); len(c) > 0 {
			best := c[0]
			for _, p := range c[1:] {
				if p.Dist(line[i]) < best.Dist(line[i]) {
					best = p
				}
			}
			f := best.Dist(line[i-1]) / math.Max(line[i-1].Dist(line[i]), 1e-9)
			return (float64(i-1) + f) / float64(n), true
		}
	}
	return 0, false
}

// computeElbow lays out a right-angled arrow: out from each end along the
// longer axis, with its bend where elbowMidPoint says.
func (e *Editor) computeElbow(s *doc.Shape, info *ArrowInfo, ts, te *doc.Shape, outlineIn func(*doc.Shape) []geo.Vec) *ArrowInfo {
	a, b := info.RawStart, info.RawEnd
	mid := s.ElbowMidPoint
	if mid == 0 {
		mid = 0.5
	}
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

func (e *Editor) arrowGeometry(s *doc.Shape) *Geometry {
	info := e.Arrow(s)
	pts := info.Line
	box := geo.BoxOf(pts)
	g := &Geometry{Bounds: box, Outline: pts}
	if s.Text != "" {
		if lb := e.arrowLabelBox(s); lb != nil {
			g.Bounds = box.Union(*lb)
			g.Label = lb
		}
	}
	return g
}

// arrowLabelAt returns the point a part of the way along the arrow.
func (i *ArrowInfo) at(t float64) geo.Vec {
	if i.Arc {
		return i.Center.Add(geo.V(i.Radius, 0).Rot(i.AngleA + i.Sweep*t))
	}
	if len(i.Line) < 2 {
		return i.Start
	}
	total := geo.PolyLength(i.Line)
	d := total * t
	for k := 1; k < len(i.Line); k++ {
		l := i.Line[k-1].Dist(i.Line[k])
		if d <= l && l > 0 {
			return i.Line[k-1].Lerp(i.Line[k], d/l)
		}
		d -= l
	}
	return i.Line[len(i.Line)-1]
}

// arrowLabelBox returns the box of an arrow's label, in the arrow's space.
func (e *Editor) arrowLabelBox(s *doc.Shape) *geo.Box {
	info := e.Arrow(s)
	text := s.Text
	if text == "" {
		text = " "
	}
	size := arrowLabelSizeOf(s.Size) * s.Scale
	b := e.Measure(text, s.Font, size, 0)
	if b.W > 280*s.Scale {
		b = e.Measure(text, s.Font, size, 280*s.Scale)
	}
	pos := s.LabelPosition
	if pos == 0 {
		pos = 0.5
	}
	c := info.at(pos)
	pad := 4.25 * s.Scale
	return &geo.Box{X: c.X - b.W/2 - pad, Y: c.Y - b.H/2 - pad, W: b.W + 2*pad, H: b.H + 2*pad}
}

// bendFromPoint returns the bend of an arrow whose middle is near p.
func bendFromPoint(a, b, p geo.Vec) float64 {
	u := b.Sub(a).Uni()
	return p.Sub(geo.Med(a, b)).Dot(u.Per().Mul(-1))
}
