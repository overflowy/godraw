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

// How much a warning matters, the most first: an answer shows few, so what's
// wrong comes before what may be meant. Each has its own room, for many of one
// not to keep a check from finding the rest.
const (
	rankBroken = iota // what doesn't show or goes through what it shouldn't
	rankText          // what makes text hard to read
	rankLayout        // what may well be meant
	rankGrowth        // a box that fits its text
	ranks
)

func (r *run) warn(format string, args ...any) { r.warnAt(rankBroken, format, args...) }

func (r *run) warnAt(rank int, format string, args ...any) {
	w := fmt.Sprintf(format, args...)
	if slices.Contains(r.found[rank], w) {
		return
	}
	if len(r.found[rank]) >= maxFound {
		r.capped = true
		return
	}
	r.found[rank] = append(r.found[rank], w)
}

// allWarnings returns what was found, in the order an answer tells it.
func (r *run) allWarnings() []string {
	var out []string
	for _, list := range r.found {
		out = append(out, list...)
	}
	return slices.Concat(out, r.shared, r.trunks)
}

// name is what warnings call a shape: the id an agent chose, not a card's part.
func (r *run) name(id doc.ID) string { return r.called(ownerOf(r.e, id)) }

// snippetLen is how much of a shape's text a warning shows, to say which it means.
const snippetLen = 18

// called is a shape's id as a warning says it. An id the agent chose says which shape
// it is; one the server gave it doesn't, so a little of its text goes with it.
func (r *run) called(id doc.ID) string {
	name := shortID(id)
	if !givenID(name) {
		return name
	}
	if text := r.textOf(id); text != "" {
		return fmt.Sprintf("%s %q", name, snippet(text))
	}
	return name
}

// givenID says whether an id looks like one the server gives, as s12 or s12-box.
func givenID(name string) bool {
	rest, ok := strings.CutPrefix(name, "s")
	if !ok {
		return false
	}
	digits := strings.IndexFunc(rest, func(c rune) bool { return c < '0' || c > '9' })
	return digits != 0 && rest != "" && (digits < 0 || rest[digits] == '-')
}

// textOf returns what a shape says: its text or name, or a card's title or body.
func (r *run) textOf(id doc.ID) string {
	s := r.e.Doc.Shapes[id]
	if s == nil {
		return ""
	}
	if c := cardOf(r.e, id); c != nil {
		if c.head != nil {
			return c.head.Text
		}
		if c.body != nil {
			return c.body.Text
		}
	}
	if s.Text != "" {
		return s.Text
	}
	return s.Name
}

// snippet returns the start of a text, on one line.
func snippet(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if c := []rune(text); len(c) > snippetLen {
		return string(c[:snippetLen]) + "…"
	}
	return text
}

// warned says that a check has found all it looks for, of every rank.
func (r *run) warned() bool {
	for _, list := range r.found[:rankGrowth] {
		if len(list) < maxFound {
			return false
		}
	}
	r.capped = true
	return true
}

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
	r.warnings = r.allWarnings()
}

func (r *run) checkPage(ids []doc.ID, touched map[doc.ID]bool) {
	items := r.items(ids, touched)
	r.checkOverlaps(items)
	r.checkArrows(ids, items, touched)
	r.checkFrames(ids, touched)
	r.checkHeadings(ids, items, touched)
	r.checkContrast(ids, touched)
	r.checkArrowLabels(ids, touched)
	r.checkShared(ids, touched)
}

// checkWholePage warns of all there is on the current page, as if a batch had
// touched it all. It changes nothing, so it only asks whether straight arrows can
// stay level. capped says that there are more than it lists.
func checkWholePage(e *editor.Editor) (list []string, capped bool) {
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
	return r.allWarnings(), r.capped
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

// reportGrowth tells of the growth no clash has told of.
func (r *run) reportGrowth() {
	var lines []string
	for id, g := range r.grown {
		if g.asked && !g.told {
			lines = append(lines, r.called(id)+" "+g.String()+" to hold its text"+r.paddingHint(id, g))
		}
	}
	slices.Sort(lines)
	for _, line := range lines {
		r.warnAt(rankGrowth, "%s", line)
	}
}

// paddingHint says that less padding would keep a box the height it was asked to be,
// if that's so.
func (r *run) paddingHint(id doc.ID, g *growth) string {
	s := r.e.Doc.Shapes[id]
	if s == nil || (s.Type != doc.TypeGeo && s.Type != doc.TypeNote) || editor.LabelPad(s) == 0 {
		return ""
	}
	w := r.e.Geometry(s).Bounds.W
	if r.e.TextBlockOf(s, s.Text, editor.LabelSize(s.Size)*s.Scale, w).H > g.from {
		return ""
	}
	return "; padding:0 keeps it " + num(g.from) + " high"
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
		for _, l := range r.e.TextBlockOf(s, s.Text, editor.LabelSize(s.Size)*s.Scale, math.Max(b.W, 8)).Lines {
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
scan:
	for i, a := range byX {
		for _, b := range byX[i+1:] {
			if len(order) >= maxFound {
				r.capped = true
				break scan
			}
			if b.box.X >= a.box.MaxX() {
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
			return r.called(it.s.ID)
		}
		return r.called(it.owner)
	}
	if c.kind == clashSticksOut {
		r.warnAt(rankText, "%s's text sticks out of %s by %s", name(c.up), name(c.low), num(past(c.up.box, c.low.box)))
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
		r.warnAt(rankLayout, "%s overlaps %s by %sx%s", lead, name(other), num(c.w), num(c.h))
	case subject == c.up:
		r.warnAt(rankText, "%s covers %s's text", lead, name(other))
	default:
		r.warnAt(rankText, "%s goes under %s, which hides its text", lead, name(other))
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
	return !hollow || !up.box.ContainsBox(*low.text)
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
				r.warnAt(rankText, `%s's label leaves %s of its %s-long line showing; widen the gap or use label:"above"`, name, num(shown), num(length))
			}
			page := m.BoundsOf(*lb)
			for _, it := range items {
				if it.text == nil || r.warned() {
					continue
				}
				if w, h := depth(page, *it.text); w > 1 && h > 1 {
					r.warnAt(rankText, "%s's label covers %s's text", name, r.called(it.owner))
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
			r.warnAt(rankLayout, `%s is %s off level; straight:"h" levels it`, name, num(dy))
		case dx > 0.5 && dx <= maxSlant && dy >= minSlantRun:
			r.warnAt(rankLayout, `%s is %s off plumb; straight:"v" levels it`, name, num(dx))
		}
	}
	// A knot says more than that the way without it is shorter.
	if s.Kind == "elbow" && crossesItself(pts) {
		if len(s.Via()) > 0 {
			r.warn("%s's route crosses itself; its via may be out of place: set via again, or via:[] clears it", name)
		} else {
			r.warn("%s's route crosses itself; another side or via reroutes it", name)
		}
	} else if n := r.e.NeedlessVia(s); n > 0 {
		r.warnAt(rankLayout, "%s's via adds %d turns, and the way without it is clear; via:[] clears it", name, n)
	} else if d := r.e.ViaDetour(s); d > 0 {
		r.warnAt(rankLayout, "%s's via takes it %s out of its way, and the way without it is clear; via:[] clears it", name, num(d))
	}
	if s.Kind == "elbow" && bs != nil && be != nil && bs.To == be.To {
		r.warnAt(rankLayout, "%s loops back to %s; tldraw routes a loop like this through the shape", name, r.name(bs.To))
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
// ends inside the box is leaving a container, which is fine, as long as it keeps
// off the container's text.
func (r *run) checkCrossings(s *doc.Shape, name string, m geo.Mat, info *editor.ArrowInfo, pts []geo.Vec, bs, be *doc.Binding, items []*item) {
	hint := ""
	if s.Kind == "elbow" {
		hint = "; another side or via routes it around"
	}
	rawStart, rawEnd := m.Apply(info.RawStart), m.Apply(info.RawEnd)
	joins := func(it *item, b *doc.Binding) bool {
		return b != nil && (b.To == it.s.ID || it.owner == ownerOf(r.e, b.To) || r.e.IsAncestor(it.s.ID, b.To))
	}
	crosses := func(box geo.Box) bool {
		corners := box.Expand(-2).Corners()
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
		outline := r.trueOutline(it)
		within := func(p geo.Vec) bool {
			if outline != nil {
				return geo.PointInPolygon(p, outline)
			}
			return it.box.Contains(p)
		}
		through := func() bool {
			if outline == nil {
				return crosses(it.box)
			}
			for i := 1; i < len(pts); i++ {
				if len(geo.SegmentPolyline(pts[i-1], pts[i], outline, true)) > 0 {
					return true
				}
			}
			return false
		}
		inside := within(rawStart) || within(rawEnd)
		if it.content && it.s.Type != doc.TypeFrame && !joins(it, bs) && !joins(it, be) {
			if !inside && through() {
				r.warn("%s runs through %s%s", name, r.called(it.owner), hint)
				continue
			}
			if inside && it.text != nil && crosses(*it.text) {
				r.warnAt(rankText, "%s runs over %s's text%s", name, r.called(it.owner), hint)
				continue
			}
		}
		// A text shape has no edge to run along, and those of a card's text are
		// inside its box.
		if it.s.Type == doc.TypeText || outline != nil {
			continue
		}
		if edge, run := runAlong(pts, it.box); run >= minAlong {
			r.warnAt(rankLayout, "%s runs along %s's %s edge for %s%s", name, r.called(it.owner), edge, num(run), hint)
		}
	}
}

// trueOutline returns in the page, a little inside, the outline of a shape that
// doesn't fill its box, as a diamond doesn't, or nothing for one that does.
func (r *run) trueOutline(it *item) []geo.Vec {
	if it.s.Type != doc.TypeGeo {
		return nil
	}
	switch it.s.Geo {
	case "rectangle", "x-box", "check-box":
		return nil
	}
	const inset = 2.0
	if it.box.W <= 2*inset || it.box.H <= 2*inset {
		return nil
	}
	m := r.e.PageMat(it.s.ID)
	c := it.box.Center()
	kx, ky := (it.box.W-2*inset)/it.box.W, (it.box.H-2*inset)/it.box.H
	pts := r.e.Geometry(it.s).Outline
	out := make([]geo.Vec, len(pts))
	for i, p := range pts {
		q := m.Apply(p)
		out[i] = geo.V(c.X+(q.X-c.X)*kx, c.Y+(q.Y-c.Y)*ky)
	}
	return out
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

// minContrast is how far apart a text or line and what's behind it have to stand
// for it to show. It's well below what's comfortable to read, as the palette's own
// yellow on white is, for a warning to mean that it has all but gone.
const minContrast = 1.4

// painter is a shape that may paint what's behind a text or line.
type painter struct {
	s     *doc.Shape
	box   geo.Box
	inv   geo.Mat
	order int
}

// checkContrast tells of text and lines that hardly show, in either theme: colors
// that adapt can be fine in the theme that's being looked at and lost in the other.
// A shape changing under them can do that as much as they themselves.
func (r *run) checkContrast(ids []doc.ID, touched map[doc.ID]bool) {
	var painters []painter
	var filled []geo.Box
	for order, id := range ids {
		s := r.e.Doc.Shapes[id]
		switch s.Type {
		case doc.TypeGeo, doc.TypeDraw, doc.TypeNote, doc.TypeFrame, doc.TypeImage:
		default:
			continue
		}
		box := r.e.PageBounds(id)
		painters = append(painters, painter{s, box, r.e.PageMat(id).Invert(), order})
		if touched[id] {
			filled = append(filled, box)
		}
	}
	for order, id := range ids {
		if r.warned() {
			return
		}
		s := r.e.Doc.Shapes[id]
		samples, what := r.inked(s)
		if len(samples) == 0 {
			r.checkShows(s, touched)
			continue
		}
		affected := touched[id] || (s.Type == doc.TypeArrow && r.arrowTouched(id, touched))
		if !affected {
			box := geo.BoxOf(samples)
			affected = slices.ContainsFunc(filled, box.Overlaps)
		}
		if affected {
			r.checkInk(s, order, samples, what, painters)
		}
	}
}

// checkShows tells of a shape with neither outline nor fill nor text.
func (r *run) checkShows(s *doc.Shape, touched map[doc.ID]bool) {
	if !touched[s.ID] || !s.NoOutline() || s.Text != "" {
		return
	}
	if s.Fill == "none" || (s.Type == doc.TypeDraw && !s.IsClosed) {
		r.warn("%s shows nothing: it has no outline, and nothing fills it", r.name(s.ID))
	}
}

// inked returns points of the page along what a shape inks that needs what's behind
// it to stand out from, and what to call it.
func (r *run) inked(s *doc.Shape) ([]geo.Vec, string) {
	m := r.e.PageMat(s.ID)
	along := func(b geo.Box) []geo.Vec {
		y := b.Center().Y
		return []geo.Vec{m.Apply(geo.V(b.X+b.W*0.1, y)), m.Apply(b.Center()), m.Apply(geo.V(b.X+b.W*0.9, y))}
	}
	switch s.Type {
	case doc.TypeArrow:
		info := r.e.Arrow(s)
		if len(info.Line) < 2 {
			return nil, ""
		}
		if s.Text != "" {
			if lb := r.e.ArrowLabelBox(s); lb != nil {
				return along(lb.Expand(-4.25 * s.Scale)), "label"
			}
		}
		return []geo.Vec{m.Apply(info.At(0.25)), m.Apply(info.At(0.75))}, "line"
	case doc.TypeLine:
		g := r.e.Geometry(s)
		if len(g.Outline) < 2 {
			return nil, ""
		}
		return []geo.Vec{m.Apply(g.Outline[len(g.Outline)/2])}, "line"
	case doc.TypeFrame:
		// A name of no size of its own takes the theme's color, and shows.
		if size, _ := editor.FrameHeading(s); size == 0 || s.Name == "" {
			return nil, ""
		}
		b := r.e.FrameHeadingBox(s, 1)
		y := b.Center().Y
		return []geo.Vec{geo.V(b.X+b.W*0.1, y), b.Center(), geo.V(b.X+b.W*0.9, y)}, "name"
	case doc.TypeGeo, doc.TypeNote, doc.TypeText:
		if s.Text == "" {
			return nil, ""
		}
		b := r.inkBox(s)
		switch role := ""; {
		case s.Type != doc.TypeText:
			return along(b), "label"
		case s.MetaValue(doc.MetaCard, &role) && role == "head":
			return along(b), "title"
		case role == "body":
			return along(b), "body"
		}
		return along(b), "text"
	}
	return nil, ""
}

// inkBox returns what a shape's lines of text cover, in its own space.
func (r *run) inkBox(s *doc.Shape) geo.Box {
	g := r.e.Geometry(s)
	b := *g.Label
	size := editor.LabelSize(s.Size) * s.Scale
	align := s.Align
	if s.Type == doc.TypeText {
		size, align = editor.TextSize(s.Size)*s.Scale, s.TextAlign
	}
	w := 0.0
	for _, l := range r.e.TextBlockOf(s, s.Text, size, math.Max(b.W, 8)).Lines {
		w = math.Max(w, l.W)
	}
	w = math.Min(w, b.W)
	switch align {
	case "start":
	case "end":
		b.X += b.W - w
	default:
		b.X += (b.W - w) / 2
	}
	b.W = w
	return b
}

// checkInk tells of a shape's ink where it stands out least, in each theme.
func (r *run) checkInk(s *doc.Shape, order int, samples []geo.Vec, what string, painters []painter) {
	type lost struct {
		theme   string
		on      editor.Color
		against float64
	}
	var found []lost
	for _, theme := range []struct {
		name string
		pal  *editor.Palette
	}{{"light", editor.Light}, {"dark", editor.Dark}} {
		worst := lost{theme: theme.name, against: math.Inf(1)}
		for _, p := range samples {
			on, known := r.behind(s, order, p, theme.pal, painters)
			if !known {
				continue
			}
			ink := r.inkOf(s, theme.pal)
			shown := on.Mix(ink, r.opacityOf(s))
			if c := editor.Contrast(shown, on); c < worst.against {
				worst.on, worst.against = on, c
			}
		}
		if worst.against < minContrast {
			found = append(found, worst)
		}
	}
	name := r.name(s.ID)
	switch len(found) {
	case 1:
		l := found[0]
		r.warnAt(rankText, "%s's %s hardly shows in the %s theme: %s on %s, contrast %s", name, what, l.theme, r.inkName(s), l.on.CSS(), num(l.against))
	case 2:
		r.warnAt(rankText, "%s's %s hardly shows in either theme: %s on %s and %s", name, what, r.inkName(s), found[0].on.CSS(), found[1].on.CSS())
	}
}

// inkOf returns the color a shape inks what inked says of it.
func (r *run) inkOf(s *doc.Shape, pal *editor.Palette) editor.Color {
	if s.Type == doc.TypeArrow && s.Text == "" || s.Type == doc.TypeLine || s.Type == doc.TypeFrame {
		return pal.SwatchOf(s).Solid
	}
	return pal.InkOf(s)
}

// inkName says that color as an op does.
func (r *run) inkName(s *doc.Shape) string {
	if s.Type == doc.TypeText || s.Type == doc.TypeLine || s.Type == doc.TypeFrame || (s.Type == doc.TypeArrow && s.Text == "") {
		return colorName(s)
	}
	name := s.LabelColor
	s.MetaValue(doc.MetaLabelColor, &name)
	return name
}

// opacityOf returns how opaque a shape paints, with what holds it.
func (r *run) opacityOf(s *doc.Shape) float64 {
	o := s.Opacity
	for p := r.e.Doc.Shapes[s.Parent]; p != nil; p = r.e.Doc.Shapes[p.Parent] {
		o *= p.Opacity
	}
	return o
}

// behind returns what's painted at a point of the page under a shape's ink, with
// its own fill, or known false if a picture has a part in it.
func (r *run) behind(s *doc.Shape, order int, at geo.Vec, pal *editor.Palette, painters []painter) (editor.Color, bool) {
	type layer struct {
		c editor.Color
		a float64
	}
	var layers []layer
walk:
	for _, p := range slices.Backward(painters) {
		if p.order > order || !p.box.Contains(at) {
			continue
		}
		c, known := r.e.FillAt(p.s, pal, p.inv.Apply(at))
		if !known {
			return editor.Color{}, false
		}
		if c.A == 0 {
			continue
		}
		a := c.A * r.opacityOf(p.s)
		layers = append(layers, layer{c, a})
		if a >= 1 {
			break walk
		}
	}
	under := pal.Background
	for _, layer := range slices.Backward(layers) {
		under = under.Mix(layer.c, layer.a)
	}
	return under, true
}

// checkArrowLabels tells of arrows' labels in one another's way, and of arrows run
// over another's label. A label has nothing behind it to hide what's under it.
func (r *run) checkArrowLabels(ids []doc.ID, touched map[doc.ID]bool) {
	type labeled struct {
		id  doc.ID
		box geo.Box
	}
	type drawn struct {
		id  doc.ID
		pts []geo.Vec
		box geo.Box
	}
	var labels []labeled
	var arrows []drawn
	for _, id := range ids {
		s := r.e.Doc.Shapes[id]
		if s.Type != doc.TypeArrow {
			continue
		}
		pts := r.pageLine(id, r.e.Arrow(s))
		if len(pts) > 0 {
			arrows = append(arrows, drawn{id, pts, geo.BoxOf(pts)})
		}
		if s.Text == "" {
			continue
		}
		if lb := r.e.ArrowLabelBox(s); lb != nil {
			pad := -4.25 * s.Scale // what's around its text
			labels = append(labels, labeled{id, r.e.PageMat(id).BoundsOf(lb.Expand(pad))})
		}
	}
	for i, a := range labels {
		for _, b := range labels[i+1:] {
			if !r.arrowTouched(a.id, touched) && !r.arrowTouched(b.id, touched) {
				continue
			}
			if w, h := depth(a.box, b.box); w > 1 && h > 1 {
				r.warnAt(rankText, "%s's label covers %s's label", shortID(b.id), shortID(a.id))
			}
		}
		corners := a.box.Corners()
		for _, l := range arrows {
			if l.id == a.id || !l.box.Overlaps(a.box) || (!r.arrowTouched(a.id, touched) && !r.arrowTouched(l.id, touched)) {
				continue
			}
			if lineHitsBox(l.pts, a.box, corners[:]) {
				r.warnAt(rankText, "%s runs over %s's label", shortID(l.id), shortID(a.id))
			}
		}
	}
}

// checkFrames tells of what a frame cuts off.
func (r *run) checkFrames(ids []doc.ID, touched map[doc.ID]bool) {
	for _, id := range ids {
		s := r.e.Doc.Shapes[id]
		arrow := s.Type == doc.TypeArrow
		if (!touched[id] && (!arrow || !r.arrowTouched(id, touched))) || s.Type == doc.TypeGroup || r.warned() {
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
			// A frame's name is meant to sit on what holds the frame.
			if it.s.ID == id || r.e.IsAncestor(id, it.s.ID) || r.e.IsAncestor(it.s.ID, id) || !it.content {
				continue
			}
			if !it.touched && !touched[id] {
				continue
			}
			if w, h := depth(box, it.box); w > 1 && h > 1 {
				r.warnAt(rankLayout, "frame %s's name runs into %s", shortID(id), r.called(it.owner))
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
		r.warnAt(rankText, "%s crosses frame %s's name%s", shortID(id), shortID(f.ID), hint)
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
