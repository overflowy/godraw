package agent

import (
	"cmp"
	"fmt"
	"math"
	"slices"

	"godraw/internal/doc"
	"godraw/internal/editor"
	"godraw/internal/geo"
)

// An op can work and still spoil the drawing: a box grows to hold its text and
// runs into the next one. What it answers says so, for an agent not to have to
// compare numbers or look at a picture to find out. Warnings speak only of what
// the batch touched, and stay quiet about what is usually meant: shapes that
// overlap in an illustration, or a box wholly inside another.

const (
	maxWarnings = 12
	// How much of an arrow has to show either side of its label, in all.
	minShownLine = 32.0
	minLine      = 20.0
	// How far off level an arrow looks like a slip, not a choice.
	maxSlant    = 8.0
	minSlantRun = 40.0
)

func (r *run) warn(format string, args ...any) {
	if w := fmt.Sprintf(format, args...); !slices.Contains(r.warnings, w) {
		r.warnings = append(r.warnings, w)
	}
}

// name is what warnings call a shape: the id an agent chose, not a card's part.
func (r *run) name(id doc.ID) string { return shortID(ownerOf(r.e, id)) }

// warned says that there are more warnings than the answer will show.
func (r *run) warned() bool { return len(r.warnings) > maxWarnings }

// touched returns what the batch added or changed, with all that those hold.
func (r *run) touched() map[doc.ID]bool {
	out := map[doc.ID]bool{}
	add := func(id doc.ID) {
		if r.e.Doc.Shapes[id] == nil {
			return
		}
		out[id] = true
		for _, k := range r.e.Descendants(id) {
			out[k] = true
		}
	}
	for _, id := range r.added {
		add(id)
	}
	for id := range r.changed {
		add(id)
	}
	return out
}

// settleArrows keeps straight arrows level, now that what they join has its
// final place and size.
func (r *run) settleArrows() {
	seen := map[doc.ID]bool{}
	settle := func(id doc.ID) {
		s := r.e.Doc.Shapes[id]
		if seen[id] || s == nil || s.Type != doc.TypeArrow {
			return
		}
		seen[id] = true
		var axis string
		if s.MetaValue(doc.MetaStraight, &axis) {
			r.level(s, axis)
		}
	}
	ids := make([]doc.ID, 0)
	for id := range r.touched() {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		settle(id)
		for _, b := range r.e.BindingsTo(id) {
			settle(b.From)
		}
	}
}

// level gives an arrow the anchors that make it run level: in the height (or,
// for "v", the width) both of what it joins share. An arrow with an end let go
// is no longer straight.
func (r *run) level(s *doc.Shape, axis string) {
	bs, be := r.e.BindingOf(s.ID, "start"), r.e.BindingOf(s.ID, "end")
	if bs == nil || be == nil {
		c := s.Clone()
		c.SetMeta(doc.MetaStraight, nil)
		c.SetMeta(doc.MetaStraightAt, nil)
		r.e.Put(c)
		return
	}
	name := shortID(s.ID)
	if turned(r.e, bs.To) || turned(r.e, be.To) {
		r.warn("%s can't stay straight: what it joins is turned", name)
		return
	}
	a, b := r.e.PageBounds(bs.To), r.e.PageBounds(be.To)
	at := 0.5
	s.MetaValue(doc.MetaStraightAt, &at)
	ax, bx := anchorX(bs), anchorX(be)
	var aa, ab geo.Vec
	if axis == "h" {
		lo, hi := math.Max(a.Y, b.Y), math.Min(a.MaxY(), b.MaxY())
		if hi < lo {
			r.warn("%s can't be level: %s and %s share no height", name, r.name(bs.To), r.name(be.To))
			return
		}
		y := lo + (hi-lo)*at
		aa, ab = geo.V(ax.X, (y-a.Y)/a.H), geo.V(bx.X, (y-b.Y)/b.H)
	} else {
		lo, hi := math.Max(a.X, b.X), math.Min(a.MaxX(), b.MaxX())
		if hi < lo {
			r.warn("%s can't be level: %s and %s share no width", name, r.name(bs.To), r.name(be.To))
			return
		}
		x := lo + (hi-lo)*at
		aa, ab = geo.V((x-a.X)/a.W, ax.Y), geo.V((x-b.X)/b.W, bx.Y)
	}
	r.e.Bind(s.ID, "start", bs.To, aa)
	r.e.Bind(s.ID, "end", be.To, ab)
	r.setEnd(s.ID, "start", geo.V(a.X+aa.X*a.W, a.Y+aa.Y*a.H))
	r.setEnd(s.ID, "end", geo.V(b.X+ab.X*b.W, b.Y+ab.Y*b.H))
}

// anchorX returns where a binding points across its shape.
func anchorX(b *doc.Binding) geo.Vec {
	if b.IsPrecise {
		return b.Anchor
	}
	return geo.V(0.5, 0.5)
}

func turned(e *editor.Editor, id doc.ID) bool {
	return math.Abs(e.PageMat(id).Rotation()) > 1e-3
}

// item is a shape with a box of its own, in the page, as far as warnings go.
type item struct {
	s        *doc.Shape
	owner    doc.ID // the card it's part of, or the shape itself
	box      geo.Box
	ownerBox geo.Box
	text     *geo.Box // what its text covers
	content  bool     // worth keeping in sight: it has text, or is an image
	order    int      // later ones paint over earlier ones
	touched  bool
}

func (r *run) checkLayout() {
	touched := r.touched()
	r.checkGrowth()
	for _, p := range r.e.Pages() {
		ids := r.e.Descendants(p.ID)
		if !slices.ContainsFunc(ids, func(id doc.ID) bool { return touched[id] }) {
			continue
		}
		items := r.items(ids, touched)
		r.checkOverlaps(items)
		r.checkArrows(ids, items, touched)
		r.checkFrames(ids, touched)
	}
	r.reportGrowth()
}

// growth is a box that came out taller than it was.
type growth struct {
	from, to float64
	// told says that a clash already blamed it. Asked says it was asked to be
	// shorter: a card that fits new text only matters if it runs into something.
	told, asked bool
}

func (g *growth) String() string { return "grew " + num(g.from) + "→" + num(g.to) }

// checkGrowth finds boxes that came out taller than they were asked to be, and
// cards taller than they were.
func (r *run) checkGrowth() {
	for id, from := range r.asked {
		if s := r.e.Doc.Shapes[id]; s != nil {
			if h := r.e.Geometry(s).Bounds.H; h > from+0.5 {
				r.grown[ownerOf(r.e, id)] = &growth{from: from, to: h, asked: true}
			}
		}
	}
	for id, from := range r.before {
		s := r.e.Doc.Shapes[id]
		if s == nil || r.grown[id] != nil {
			continue
		}
		if h := r.e.Geometry(s).Bounds.H; h > from+0.5 {
			r.grown[id] = &growth{from: from, to: h}
		}
	}
}

// reportGrowth puts first the growth no clash has told of.
func (r *run) reportGrowth() {
	var lines []string
	for id, g := range r.grown {
		if g.asked && !g.told {
			lines = append(lines, shortID(id)+" "+g.String()+" to hold its text")
		}
	}
	slices.Sort(lines)
	r.warnings = slices.Insert(r.warnings, 0, lines...)
}

func (r *run) items(ids []doc.ID, touched map[doc.ID]bool) []*item {
	var out []*item
	for order, id := range ids {
		s := r.e.Doc.Shapes[id]
		switch s.Type {
		case doc.TypeArrow, doc.TypeGroup, doc.TypeLine, doc.TypeDraw, doc.TypeHighlight:
			continue
		}
		if turned(r.e, id) { // its box in the page says too little of it
			continue
		}
		it := &item{s: s, owner: ownerOf(r.e, id), box: r.e.PageBounds(id), order: order, touched: touched[id]}
		it.ownerBox = r.e.PageBounds(it.owner)
		it.text = r.textBox(s)
		it.content = s.Text != "" || it.owner != id || (s.Type != doc.TypeGeo && s.Type != doc.TypeText)
		out = append(out, it)
	}
	return out
}

// textBox returns what a shape's text covers in the page. A label's own box is
// as wide as the shape allows, whatever the text in it. Lines say how wide they
// are without the space that ends them.
func (r *run) textBox(s *doc.Shape) *geo.Box {
	g := r.e.Geometry(s)
	if s.Text == "" || g.Label == nil {
		return nil
	}
	b := *g.Label
	if s.Type == doc.TypeGeo || s.Type == doc.TypeNote {
		w := 0.0
		for _, l := range r.e.Measure(s.Text, s.Font, editor.LabelSize(s.Size)*s.Scale, math.Max(b.W, 8)).Lines {
			w = math.Max(w, l.W)
		}
		switch s.Align {
		case "start":
		case "end":
			b.X += b.W - w
		default:
			b.X += (b.W - w) / 2
		}
		b.W = w
	}
	box := r.e.PageMat(s.ID).BoundsOf(b)
	return &box
}

// depth returns how far two boxes overlap each way.
func depth(a, b geo.Box) (w, h float64) {
	return math.Min(a.MaxX(), b.MaxX()) - math.Max(a.X, b.X), math.Min(a.MaxY(), b.MaxY()) - math.Max(a.Y, b.Y)
}

// A clash is what two shapes do to each other. Those of a card count as one, so
// that a card running into another is told once, and of the cards.
type clash struct {
	kind    int
	up, low *item
	w, h    float64
}

const (
	clashOverlap = iota
	clashSticksOut
	clashCovers
)

func (r *run) checkOverlaps(items []*item) {
	byX := slices.Clone(items)
	slices.SortStableFunc(byX, func(a, b *item) int { return cmp.Compare(a.box.X, b.box.X) })
	clashes := map[[2]doc.ID]*clash{}
	var order [][2]doc.ID
	for i, a := range byX {
		for _, b := range byX[i+1:] {
			if b.box.X >= a.box.MaxX() || len(order) > maxWarnings {
				break
			}
			if !a.touched && !b.touched {
				continue
			}
			w, h := depth(a.box, b.box)
			if w <= 1 || h <= 1 || r.e.IsAncestor(a.s.ID, b.s.ID) || r.e.IsAncestor(b.s.ID, a.s.ID) {
				continue
			}
			low, up := a, b
			if a.order > b.order {
				low, up = b, a
			}
			same := a.owner == b.owner
			boxA, boxB := a.ownerBox, b.ownerBox
			if same {
				boxA, boxB = a.box, b.box
			}
			c := &clash{up: up, low: low}
			c.w, c.h = depth(boxA, boxB)
			switch {
			case covers(up, low):
				c.kind = clashCovers
			case up.s.Type == doc.TypeText && low.s.Type != doc.TypeText && up.owner == up.s.ID && past(up.box, low.box) > 1 && !low.box.ContainsBox(up.box) && !up.box.ContainsBox(low.box):
				c.kind = clashSticksOut
			case boxA.ContainsBox(boxB) || boxB.ContainsBox(boxA) || !a.content || !b.content:
				continue
			}
			key := [2]doc.ID{min(a.owner, b.owner), max(a.owner, b.owner)}
			if same {
				key = [2]doc.ID{min(a.s.ID, b.s.ID), max(a.s.ID, b.s.ID)}
			}
			if was := clashes[key]; was == nil {
				order = append(order, key)
				clashes[key] = c
			} else if c.kind > was.kind {
				clashes[key] = c
			}
		}
	}
	for _, key := range order {
		r.tellClash(clashes[key])
	}
}

// tellClash says what went wrong, starting from what caused it: what grew, or
// else what the batch moved.
func (r *run) tellClash(c *clash) {
	same := c.up.owner == c.low.owner
	name := func(it *item) string {
		if same {
			return shortID(it.s.ID)
		}
		return shortID(it.owner)
	}
	if c.kind == clashSticksOut {
		r.warn("%s's text sticks out of %s by %s", name(c.up), name(c.low), num(past(c.up.box, c.low.box)))
		return
	}
	subject, other := c.up, c.low
	switch {
	case r.grown[c.up.owner] != nil:
	case r.grown[c.low.owner] != nil, c.low.touched && !c.up.touched:
		subject, other = c.low, c.up
	}
	lead := name(subject)
	if g := r.grown[subject.owner]; g != nil && !g.told {
		g.told = true
		lead += " " + g.String() + ", now"
	}
	switch {
	case c.kind == clashOverlap:
		r.warn("%s overlaps %s by %sx%s", lead, name(other), num(c.w), num(c.h))
	case subject == c.up:
		r.warn("%s covers %s's text", lead, name(other))
	default:
		r.warn("%s goes under %s, which hides its text", lead, name(other))
	}
}

// past returns how far a box reaches past another, on its worst side.
func past(a, b geo.Box) float64 {
	return max(b.X-a.X, a.MaxX()-b.MaxX(), b.Y-a.Y, a.MaxY()-b.MaxY())
}

// covers says whether what paints later hides the text of what paints earlier.
// A shape without a fill only hides what its outline or its own text runs
// through.
func covers(up, low *item) bool {
	if low.text == nil {
		return false
	}
	if w, h := depth(up.box, *low.text); w <= 1 || h <= 1 {
		return false
	}
	if up.text != nil {
		if w, h := depth(*up.text, *low.text); w > 1 && h > 1 {
			return true
		}
	}
	hollow := up.s.Type == doc.TypeGeo && up.s.Fill == "none"
	return !(hollow && up.box.ContainsBox(*low.text))
}

func (r *run) checkArrows(ids []doc.ID, items []*item, touched map[doc.ID]bool) {
	for _, id := range ids {
		s := r.e.Doc.Shapes[id]
		if s.Type != doc.TypeArrow || r.warned() {
			continue
		}
		bs, be := r.e.BindingOf(id, "start"), r.e.BindingOf(id, "end")
		if !touched[id] && (bs == nil || !touched[bs.To]) && (be == nil || !touched[be.To]) {
			continue
		}
		r.checkArrow(s, bs, be, items)
	}
}

func (r *run) checkArrow(s *doc.Shape, bs, be *doc.Binding, items []*item) {
	name := shortID(s.ID)
	info := r.e.Arrow(s)
	if len(info.Line) < 2 || info.Start.Dist(info.End) < 0.5 {
		r.warn("%s doesn't show: its ends meet", name)
		return
	}
	m := r.e.PageMat(s.ID)
	pts := make([]geo.Vec, len(info.Line))
	for i, p := range info.Line {
		pts[i] = m.Apply(p)
	}
	length := geo.PolyLength(pts)
	if length < minLine {
		r.warn("%s is only %s long", name, num(length))
		return
	}
	if s.Text != "" {
		if lb := r.e.ArrowLabelBox(s); lb != nil {
			const steps = 64
			in := 0
			for i := range steps {
				if lb.Contains(info.At(float64(i) / (steps - 1))) {
					in++
				}
			}
			if shown := length * float64(steps-in) / steps; shown < minShownLine {
				r.warn(`%s's label leaves %s of its %s-long line showing; widen the gap or use label:"above"`, name, num(shown), num(length))
			}
			page := m.BoundsOf(*lb)
			for _, it := range items {
				if it.text == nil || r.warned() {
					continue
				}
				if w, h := depth(page, *it.text); w > 1 && h > 1 {
					r.warn("%s's label covers %s's text", name, shortID(it.owner))
				}
			}
		}
	}
	var axis string
	if len(pts) == 2 && !s.MetaValue(doc.MetaStraight, &axis) {
		d := pts[1].Sub(pts[0])
		dx, dy := math.Abs(d.X), math.Abs(d.Y)
		switch {
		case dy > 0.5 && dy <= maxSlant && dx >= minSlantRun:
			r.warn(`%s is %s off level; straight:"h" levels it`, name, num(dy))
		case dx > 0.5 && dx <= maxSlant && dy >= minSlantRun:
			r.warn(`%s is %s off plumb; straight:"v" levels it`, name, num(dx))
		}
	}
	r.checkCrossings(name, m, info, pts, bs, be, items)
}

// checkCrossings tells of an arrow running through a box it doesn't join. One
// that starts or ends inside the box is leaving a container, which is fine.
func (r *run) checkCrossings(name string, m geo.Mat, info *editor.ArrowInfo, pts []geo.Vec, bs, be *doc.Binding, items []*item) {
	line := geo.BoxOf(pts)
	rawStart, rawEnd := m.Apply(info.RawStart), m.Apply(info.RawEnd)
	joins := func(it *item, b *doc.Binding) bool {
		return b != nil && (b.To == it.s.ID || it.owner == ownerOf(r.e, b.To) || r.e.IsAncestor(it.s.ID, b.To))
	}
	for _, it := range items {
		if !it.content || it.s.Type == doc.TypeFrame || !line.Overlaps(it.box) || r.warned() {
			continue
		}
		if joins(it, bs) || joins(it, be) || it.box.Contains(rawStart) || it.box.Contains(rawEnd) {
			continue
		}
		corners := it.box.Expand(-2).Corners()
		for i := 1; i < len(pts); i++ {
			if len(geo.SegmentPolyline(pts[i-1], pts[i], corners[:], true)) > 0 {
				r.warn("%s runs through %s", name, shortID(it.owner))
				break
			}
		}
	}
}

// checkFrames tells of what a frame cuts off.
func (r *run) checkFrames(ids []doc.ID, touched map[doc.ID]bool) {
	for _, id := range ids {
		s := r.e.Doc.Shapes[id]
		if !touched[id] || s.Type == doc.TypeGroup || r.warned() {
			continue
		}
		f := r.frameOf(s)
		if f == nil {
			continue
		}
		b := r.e.PageMat(f.ID).Invert().Mul(r.e.PageMat(id)).BoundsOf(r.e.Geometry(s).Bounds)
		const slack = 1.0
		if b.X < -slack || b.Y < -slack || b.MaxX() > f.W+slack || b.MaxY() > f.H+slack {
			r.warn("%s sticks out of frame %s, which clips it", r.name(id), shortID(f.ID))
		}
	}
}

// frameOf returns the frame that holds a shape, at any depth, if any.
func (r *run) frameOf(s *doc.Shape) *doc.Shape {
	for p := r.e.Doc.Shapes[s.Parent]; p != nil; p = r.e.Doc.Shapes[p.Parent] {
		if p.Type == doc.TypeFrame {
			return p
		}
	}
	return nil
}
