package agent

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"

	"godraw/internal/doc"
	"godraw/internal/editor"
	"godraw/internal/geo"
)

// An op can work and still spoil the drawing: a box grows to hold its text and
// runs into the next one. What it answers says so, for an agent not to have to
// compare numbers or look at a picture to find out. Warnings speak only of what
// the batch touched, unless read is asked to check the page, and stay quiet about
// what is usually meant: shapes that overlap in an illustration, or a box wholly
// inside another.

const (
	maxWarnings = 12
	// How many a check looks for before it stops, which is more than an answer
	// shows, for a long list to say how long it is and to come last.
	maxFound = maxLimit
	// How much of an arrow has to show either side of its label, in all.
	minShownLine = 32.0
	minLine      = 20.0
	// How far off level an arrow looks like a slip, not a choice.
	maxSlant    = 8.0
	minSlantRun = 40.0
	// How near a line has to run to an edge or another line to look like it, and
	// for how long before it's more than a corner or the start of a branch.
	alongGap    = 4.0
	minAlong    = 24.0
	minSharedAt = 48.0
)

func (r *run) warn(format string, args ...any) {
	if w := fmt.Sprintf(format, args...); !slices.Contains(r.warnings, w) {
		r.warnings = append(r.warnings, w)
	}
}

// name is what warnings call a shape: the id an agent chose, not a card's part.
func (r *run) name(id doc.ID) string { return shortID(ownerOf(r.e, id)) }

// warned says that a check has found all it looks for.
func (r *run) warned() bool { return len(r.warnings) >= maxFound }

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
	aa, ab, ok := r.levelAnchors(s, axis, bs, be)
	if !ok {
		return
	}
	a, b := r.e.PageBounds(bs.To), r.e.PageBounds(be.To)
	r.e.Bind(s.ID, "start", bs.To, aa)
	r.e.Bind(s.ID, "end", be.To, ab)
	r.setEnd(s.ID, "start", geo.V(a.X+aa.X*a.W, a.Y+aa.Y*a.H))
	r.setEnd(s.ID, "end", geo.V(b.X+ab.X*b.W, b.Y+ab.Y*b.H))
}

// levelAnchors returns the anchors that run an arrow level, or warns of why none
// do.
func (r *run) levelAnchors(s *doc.Shape, axis string, bs, be *doc.Binding) (aa, ab geo.Vec, ok bool) {
	name := shortID(s.ID)
	if turned(r.e, bs.To) || turned(r.e, be.To) {
		r.warn("%s can't stay straight: what it joins is turned", name)
		return aa, ab, false
	}
	a, b := r.e.PageBounds(bs.To), r.e.PageBounds(be.To)
	at := 0.5
	s.MetaValue(doc.MetaStraightAt, &at)
	ax, bx := anchorX(bs), anchorX(be)
	if axis == "h" {
		lo, hi := math.Max(a.Y, b.Y), math.Min(a.MaxY(), b.MaxY())
		if hi < lo {
			r.warn("%s can't be level: %s and %s share no height", name, r.name(bs.To), r.name(be.To))
			return aa, ab, false
		}
		y := lo + (hi-lo)*at
		aa, ab = geo.V(ax.X, (y-a.Y)/a.H), geo.V(bx.X, (y-b.Y)/b.H)
	} else {
		lo, hi := math.Max(a.X, b.X), math.Min(a.MaxX(), b.MaxX())
		if hi < lo {
			r.warn("%s can't be level: %s and %s share no width", name, r.name(bs.To), r.name(be.To))
			return aa, ab, false
		}
		x := lo + (hi-lo)*at
		aa, ab = geo.V((x-a.X)/a.W, ax.Y), geo.V((x-b.X)/b.W, bx.Y)
	}
	return aa, ab, true
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
		r.checkPage(ids, touched)
	}
	r.reportGrowth()
	r.warnings = slices.Concat(r.warnings, r.shared, r.trunks)
}

func (r *run) checkPage(ids []doc.ID, touched map[doc.ID]bool) {
	items := r.items(ids, touched)
	r.checkOverlaps(items)
	r.checkArrows(ids, items, touched)
	r.checkFrames(ids, touched)
	r.checkHeadings(ids, items, touched)
	r.checkShared(ids, touched)
}

// checkWholePage warns of all there is on the current page, as if a batch had
// touched it all. It changes nothing, so it only asks whether straight arrows can
// stay level.
func checkWholePage(e *editor.Editor) []string {
	r := &run{e: e}
	ids := e.Descendants(e.Page)
	touched := make(map[doc.ID]bool, len(ids))
	for _, id := range ids {
		touched[id] = true
		s := e.Doc.Shapes[id]
		var axis string
		if s.Type == doc.TypeArrow && s.MetaValue(doc.MetaStraight, &axis) {
			if bs, be := e.BindingOf(id, "start"), e.BindingOf(id, "end"); bs != nil && be != nil {
				r.levelAnchors(s, axis, bs, be)
			}
		}
	}
	r.checkPage(ids, touched)
	return slices.Concat(r.warnings, r.shared, r.trunks)
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
			if b.box.X >= a.box.MaxX() || len(order) >= maxFound {
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
		if !r.arrowTouched(id, touched) {
			continue
		}
		r.checkArrow(s, r.e.BindingOf(id, "start"), r.e.BindingOf(id, "end"), items)
	}
}

// arrowTouched says whether a batch touched an arrow or what it joins.
func (r *run) arrowTouched(id doc.ID, touched map[doc.ID]bool) bool {
	if touched[id] {
		return true
	}
	for _, term := range []string{"start", "end"} {
		if b := r.e.BindingOf(id, term); b != nil && touched[b.To] {
			return true
		}
	}
	return false
}

// pageLine returns the line an arrow draws, in the page.
func (r *run) pageLine(id doc.ID, info *editor.ArrowInfo) []geo.Vec {
	m := r.e.PageMat(id)
	pts := make([]geo.Vec, len(info.Line))
	for i, p := range info.Line {
		pts[i] = m.Apply(p)
	}
	return pts
}

func (r *run) checkArrow(s *doc.Shape, bs, be *doc.Binding, items []*item) {
	name := shortID(s.ID)
	info := r.e.Arrow(s)
	if len(info.Line) < 2 || info.Start.Dist(info.End) < 0.5 {
		r.warn("%s doesn't show: its ends meet", name)
		return
	}
	m := r.e.PageMat(s.ID)
	pts := r.pageLine(s.ID, info)
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
	if n := r.e.NeedlessVia(s); n > 0 {
		r.warn("%s's via adds %d turns, and the way without it is clear; via:[] clears it", name, n)
	} else if s.Kind == "elbow" && crossesItself(pts) {
		if len(s.Via()) > 0 {
			r.warn("%s's route crosses itself; its via may be out of place: set via again, or via:[] clears it", name)
		} else {
			r.warn("%s's route crosses itself; another side or via reroutes it", name)
		}
	}
	r.checkCrossings(s, name, m, info, pts, bs, be, items)
}

// crossesItself says whether a line crosses or runs back along its own path.
func crossesItself(pts []geo.Vec) bool {
	for i := 1; i < len(pts); i++ {
		a := pts[i-1 : i+1]
		for j := i + 1; j < len(pts); j++ {
			b := pts[j-1 : j+1]
			if j > i+1 {
				if _, ok := geo.SegmentsCross(a[0], a[1], b[0], b[1]); ok {
					return true
				}
			}
			if sharedLength(a, b) >= minAlong {
				return true
			}
		}
	}
	return false
}

// checkCrossings tells of an arrow running through a box it doesn't join, or
// along an edge of one, where it can't be told from the edge. One that starts or
// ends inside the box is leaving a container, which is fine.
func (r *run) checkCrossings(s *doc.Shape, name string, m geo.Mat, info *editor.ArrowInfo, pts []geo.Vec, bs, be *doc.Binding, items []*item) {
	hint := ""
	if s.Kind == "elbow" {
		hint = "; another side or via routes it around"
	}
	rawStart, rawEnd := m.Apply(info.RawStart), m.Apply(info.RawEnd)
	joins := func(it *item, b *doc.Binding) bool {
		return b != nil && (b.To == it.s.ID || it.owner == ownerOf(r.e, b.To) || r.e.IsAncestor(it.s.ID, b.To))
	}
	crosses := func(it *item) bool {
		corners := it.box.Expand(-2).Corners()
		for i := 1; i < len(pts); i++ {
			if len(geo.SegmentPolyline(pts[i-1], pts[i], corners[:], true)) > 0 {
				return true
			}
		}
		return false
	}
	line := geo.BoxOf(pts).Expand(alongGap)
	for _, it := range items {
		if !line.Overlaps(it.box) || r.warned() {
			continue
		}
		inside := it.box.Contains(rawStart) || it.box.Contains(rawEnd)
		if it.content && it.s.Type != doc.TypeFrame && !joins(it, bs) && !joins(it, be) && !inside && crosses(it) {
			r.warn("%s runs through %s%s", name, shortID(it.owner), hint)
			continue
		}
		// A text shape has no edge to run along, and those of a card's text are
		// inside its box.
		if it.s.Type == doc.TypeText {
			continue
		}
		if edge, run := runAlong(pts, it.box); run >= minAlong {
			r.warn("%s runs along %s's %s edge for %s%s", name, shortID(it.owner), edge, num(run), hint)
		}
	}
}

// runAlong returns the edge of a box that a line keeps closest to, and for how
// long.
func runAlong(pts []geo.Vec, b geo.Box) (string, float64) {
	sides := [4]float64{}
	names := [4]string{"top", "bottom", "left", "right"}
	for i := 1; i < len(pts); i++ {
		p, q := pts[i-1], pts[i]
		for side, y := range []float64{b.Y, b.MaxY()} {
			if math.Abs(p.Y-y) <= alongGap && math.Abs(q.Y-y) <= alongGap {
				sides[side] += overlap(p.X, q.X, b.X, b.MaxX())
			}
		}
		for side, x := range []float64{b.X, b.MaxX()} {
			if math.Abs(p.X-x) <= alongGap && math.Abs(q.X-x) <= alongGap {
				sides[2+side] += overlap(p.Y, q.Y, b.Y, b.MaxY())
			}
		}
	}
	best := 0
	for i, run := range sides {
		if run > sides[best] {
			best = i
		}
	}
	return names[best], sides[best]
}

// overlap returns how much two ranges share, each given by its ends in any order.
func overlap(a, b, lo, hi float64) float64 {
	return max(min(max(a, b), max(lo, hi))-max(min(a, b), min(lo, hi)), 0)
}

// checkShared tells of two arrows drawn over each other for so long that they
// read as one. Arcs are left out: they only meet where they cross. Arrows that
// leave or reach one anchor make a trunk, as a tree does, which is said once for
// them all and last, however many it has: that may well be what is meant, and it
// mustn't crowd out what isn't.
func (r *run) checkShared(ids []doc.ID, touched map[doc.ID]bool) {
	type way struct {
		id     doc.ID
		pts    []geo.Vec
		box    geo.Box
		new    bool
		bs, be *doc.Binding
	}
	var ways []way
	for _, id := range ids {
		s := r.e.Doc.Shapes[id]
		if s.Type != doc.TypeArrow {
			continue
		}
		info := r.e.Arrow(s)
		if info.Arc || len(info.Line) < 2 {
			continue
		}
		pts := r.pageLine(id, info)
		ways = append(ways, way{
			id: id, pts: pts, box: geo.BoxOf(pts).Expand(alongGap), new: r.arrowTouched(id, touched),
			bs: r.e.BindingOf(id, "start"), be: r.e.BindingOf(id, "end"),
		})
	}
	from, into := newSets(len(ways)), newSets(len(ways))
	for i, a := range ways {
		for j := i + 1; j < len(ways); j++ {
			b := ways[j]
			if (!a.new && !b.new) || !a.box.Overlaps(b.box) {
				continue
			}
			// Sharing both ends isn't a tree: it's one arrow drawn twice.
			sameFrom, sameInto := sameAnchor(a.bs, b.bs), sameAnchor(a.be, b.be)
			trunk := from
			if sameInto {
				trunk = into
			}
			if sameFrom != sameInto {
				if trunk.find(i) != trunk.find(j) && sharedLength(a.pts, b.pts) >= minSharedAt {
					trunk.join(i, j)
				}
				continue
			}
			if len(r.shared) >= maxFound {
				continue
			}
			if n := sharedLength(a.pts, b.pts); n >= minSharedAt {
				r.shared = append(r.shared, fmt.Sprintf("%s and %s share a line for %s, so they read as one arrow; fromAt, toAt or via parts them", shortID(a.id), shortID(b.id), num(n)))
			}
		}
	}
	tell := func(trunk sets, word string, end func(way) *doc.Binding) {
		members := map[int][]doc.ID{}
		for i, w := range ways {
			root := trunk.find(i)
			members[root] = append(members[root], w.id)
		}
		for i, w := range ways {
			if list := members[i]; len(list) > 1 {
				r.trunks = append(r.trunks, fmt.Sprintf("%s share a trunk %s %s; fromAt, toAt or via parts them if it isn't meant", nameList(list), word, r.name(end(w).To)))
			}
		}
	}
	tell(from, "from", func(w way) *doc.Binding { return w.bs })
	tell(into, "into", func(w way) *doc.Binding { return w.be })
}

// sameAnchor says whether two ends are bound to one point of one shape.
func sameAnchor(a, b *doc.Binding) bool {
	return a != nil && b != nil && a.To == b.To && anchorX(a) == anchorX(b)
}

// nameList names shapes, in a line that stays short.
func nameList(ids []doc.ID) string {
	const shown = 8
	if len(ids) == 2 {
		return shortID(ids[0]) + " and " + shortID(ids[1])
	}
	var names []string
	for _, id := range ids[:min(len(ids), shown)] {
		names = append(names, shortID(id))
	}
	out := strings.Join(names, ", ")
	if len(ids) > shown {
		out += fmt.Sprintf(" and %d more", len(ids)-shown)
	}
	return out
}

// sets tells which of n things have been joined together. Each holds the one it
// was joined to, and the first of a set holds itself.
type sets []int

func newSets(n int) sets {
	s := make(sets, n)
	for i := range s {
		s[i] = i
	}
	return s
}

func (s sets) find(i int) int {
	for s[i] != i {
		s[i] = s[s[i]]
		i = s[i]
	}
	return i
}

func (s sets) join(i, j int) {
	a, b := s.find(i), s.find(j)
	s[max(a, b)] = min(a, b)
}

// sharedLength returns how long two lines run along each other.
func sharedLength(a, b []geo.Vec) float64 {
	total := 0.0
	for i := 1; i < len(a); i++ {
		p, d := a[i-1], a[i].Sub(a[i-1])
		l := d.Len()
		if l < 1 {
			continue
		}
		u := d.Mul(1 / l)
		for j := 1; j < len(b); j++ {
			q0, q1 := b[j-1].Sub(p), b[j].Sub(p)
			if math.Abs(q0.Dot(u.Per())) > alongGap || math.Abs(q1.Dot(u.Per())) > alongGap {
				continue
			}
			total += overlap(q0.Dot(u), q1.Dot(u), 0, l)
		}
	}
	return total
}

// checkFrames tells of what a frame cuts off.
func (r *run) checkFrames(ids []doc.ID, touched map[doc.ID]bool) {
	for _, id := range ids {
		s := r.e.Doc.Shapes[id]
		arrow := s.Type == doc.TypeArrow
		if (!touched[id] && !(arrow && r.arrowTouched(id, touched))) || s.Type == doc.TypeGroup || r.warned() {
			continue
		}
		f := r.frameOf(s)
		if f == nil {
			continue
		}
		b := r.e.PageMat(f.ID).Invert().Mul(r.e.PageMat(id)).BoundsOf(r.e.Geometry(s).Bounds)
		const slack = 1.0
		if b.X < -slack || b.Y < -slack || b.MaxX() > f.W+slack || b.MaxY() > f.H+slack {
			if arrow {
				r.warn(`%s sticks out of frame %s, which clips it; in:"" takes it out of the frame`, r.name(id), shortID(f.ID))
			} else {
				r.warn("%s sticks out of frame %s, which clips it", r.name(id), shortID(f.ID))
			}
		}
	}
}

// checkHeadings tells of a frame's name running into what is above the frame, or
// of an arrow crossing it. A name without a size of its own is small enough for a
// shape to be left to overlap it, and changes size with the zoom, but a line
// through it is plain at any size.
func (r *run) checkHeadings(ids []doc.ID, items []*item, touched map[doc.ID]bool) {
	for _, id := range ids {
		s := r.e.Doc.Shapes[id]
		if s.Type != doc.TypeFrame || s.Name == "" || r.warned() {
			continue
		}
		box := r.e.FrameHeadingBox(s, 1)
		r.checkHeadingArrows(s, box, ids, touched)
		if size, _ := editor.FrameHeading(s); size == 0 {
			continue
		}
		for _, it := range items {
			if it.s.ID == id || r.e.IsAncestor(id, it.s.ID) || !it.content {
				continue
			}
			if !it.touched && !touched[id] {
				continue
			}
			if w, h := depth(box, it.box); w > 1 && h > 1 {
				r.warn("frame %s's name runs into %s", shortID(id), shortID(it.owner))
			}
		}
	}
}

// checkHeadingArrows tells of arrows through a frame's name. One in the frame
// can't reach it: the frame clips it first.
func (r *run) checkHeadingArrows(f *doc.Shape, box geo.Box, ids []doc.ID, touched map[doc.ID]bool) {
	inner := box.Expand(-1)
	corners := inner.Corners()
	for _, id := range ids {
		s := r.e.Doc.Shapes[id]
		if s.Type != doc.TypeArrow || r.e.IsAncestor(f.ID, id) || (!touched[f.ID] && !r.arrowTouched(id, touched)) {
			continue
		}
		pts := r.pageLine(id, r.e.Arrow(s))
		if !lineHitsBox(pts, inner, corners[:]) {
			continue
		}
		hint := ""
		if s.Kind == "elbow" {
			hint = "; another side or via routes it around"
		}
		r.warn("%s crosses frame %s's name%s", shortID(id), shortID(f.ID), hint)
	}
}

// lineHitsBox says whether any of a line is in a box, given also as its corners.
func lineHitsBox(pts []geo.Vec, box geo.Box, corners []geo.Vec) bool {
	for i, p := range pts {
		if box.Contains(p) {
			return true
		}
		if i > 0 && len(geo.SegmentPolyline(pts[i-1], p, corners, true)) > 0 {
			return true
		}
	}
	return false
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
