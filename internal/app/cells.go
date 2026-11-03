package app

import (
	"image"
	"math"

	"github.com/egoist/mygo/ui"

	"godraw/internal/editor"
	"godraw/internal/geo"
)

// A path paints from a mask of its whole bounding box, in a 4096² atlas that
// also holds the window's icons and text, and a frame that fills it loses what
// is left to paint. A thin outline around a large box costs all of the box. So
// a large path paints in cells of the device's pixels, each clipped to its cell
// and holding only the part of the path near it, which makes its cost follow its
// length rather than its box. Clips on whole pixels neither overlap nor leave a
// gap, so nothing shows where cells meet, translucent strokes included.
const (
	// How many device pixels a side a cell is.
	cellSide = 128
	// How far a curve may stray from the lines it's drawn as, in device pixels.
	curveTolerance = 0.1
)

// wholeMask is how many device pixels a path can span and still take one mask.
// Those of this size or less are most of a page, and cached as they are. It's a
// variable for tests, to compare the two ways of painting.
var wholeMask = 256.0

// deviceLine is a subpath as a polyline in device pixels.
type deviceLine struct {
	pts    []geo.Vec
	closed bool
}

// stroke is what stroking a path asks for, in the painter's units.
type stroke struct {
	width float32
	color ui.Color
	butt  bool      // flat ends and pointed corners rather than round ones
	dash  []float64 // lengths of a dash and a gap, if dashed
	// kept says that the path is one the next frame strokes again, rather than one
	// made for this frame, for its cells to be kept.
	kept bool
}

// spansLarge says whether a path under m reaches past what one mask should hold,
// reach being how far what paints it goes from its points. Control points bound
// a curve, which is all this needs. A turned frame's children stay whole: a clip
// under its transform has no whole pixels to fall on.
func (s *scene) spansLarge(g *geo.Path, m geo.Mat, reach float64) bool {
	if s.small || s.turned > 0 {
		return false
	}
	return s.spansLargeAt(g, m, reach)
}

func (s *scene) spansLargeAt(g *geo.Path, m geo.Mat, reach float64) bool {
	lo, hi := geo.V(math.Inf(1), math.Inf(1)), geo.V(math.Inf(-1), math.Inf(-1))
	add := func(p geo.Vec) {
		v := m.Apply(p)
		lo, hi = geo.V(min(lo.X, v.X), min(lo.Y, v.Y)), geo.V(max(hi.X, v.X), max(hi.Y, v.Y))
	}
	for _, seg := range g.Segs {
		switch seg.Op {
		case geo.OpMove, geo.OpLine:
			add(seg.P[0])
		case geo.OpQuad:
			add(seg.P[0])
			add(seg.P[1])
		case geo.OpCube:
			add(seg.P[0])
			add(seg.P[1])
			add(seg.P[2])
		}
	}
	k := float64(s.p.Scale())
	return (hi.X-lo.X+2*reach)*k > wholeMask || (hi.Y-lo.Y+2*reach)*k > wholeMask
}

// stroke strokes a path under m.
func (s *scene) stroke(g *geo.Path, m geo.Mat, st stroke) {
	reach := float64(st.width) / 2
	if st.butt {
		reach *= 4 // the miter limit
	}
	if !s.spansLarge(g, m, reach) {
		path := s.buildPath(g, m)
		if st.butt {
			path.Cap(ui.ButtCap).Join(ui.MiterJoin)
		}
		if len(st.dash) >= 2 {
			path.Dash(0, float32(st.dash[0]), float32(st.dash[1]))
		}
		s.p.StrokePath(path, st.width, st.color)
		return
	}
	k := float64(s.p.Scale())
	plan, shift := s.planOf(cellKey{path: g, reach: float32(reach*k + 2), on: float32(dashOn(st.dash) * k), off: float32(dashOff(st.dash) * k)}, m, k, st.kept)
	first, last, ok := plan.showing(s, shift, k)
	if !ok {
		return
	}
	path := &s.path
	draw := func() { s.p.StrokePath(path, st.width, st.color) }
	for row := first.Y; row <= last.Y; row++ {
		for col := first.X; col <= last.X; col++ {
			i := plan.grid.index(col, row)
			runs := plan.pieces[i].runs
			if len(runs) == 0 {
				continue
			}
			path.Reset()
			for _, run := range runs {
				s.addLine(path, run.pts, shift, k)
				if run.closed {
					path.Close()
				}
			}
			if st.butt {
				path.Cap(ui.ButtCap).Join(ui.MiterJoin)
			}
			s.p.Clip(plan.grid.rect(i, shift, k), 0, draw)
		}
	}
}

// dashOn and dashOff return the lengths of a stroke's dash and gap, none if whole.
func dashOn(dash []float64) float64 {
	if len(dash) >= 2 {
		return dash[0]
	}
	return 0
}

func dashOff(dash []float64) float64 {
	if len(dash) >= 2 {
		return dash[1]
	}
	return 0
}

// fill fills a path under m.
func (s *scene) fill(g *geo.Path, m geo.Mat, c ui.Color) {
	s.fillWith(g, m, brush{color: c})
}

// brush is what a path fills with: a color, or a gradient if it has one.
type brush struct {
	color    ui.Color
	gradient *gradientLine
}

// path fills a path whose bounds, as a painter finds them, are given.
func (b brush) path(p *ui.Painter, path *ui.Path, bounds ui.Rect) {
	if b.gradient != nil {
		p.FillPathGradient(path, b.gradient.over(bounds))
		return
	}
	p.FillPath(path, b.color)
}

func (b brush) rect(p *ui.Painter, r ui.Rect) {
	if b.gradient != nil {
		p.FillGradient(r, b.gradient.over(r), 0)
		return
	}
	p.Fill(r, b.color, 0)
}

// gradientLine is a shape's gradient as it lies on the painter.
type gradientLine struct {
	fill     *editor.GradientFill
	from, to geo.Vec
	opacity  float64
}

// fillGradient fills what a prim fills with its gradient.
func (s *scene) fillGradient(pr editor.Prim, m geo.Mat, o float64) {
	line := &gradientLine{fill: pr.Gradient, from: m.Apply(pr.Gradient.From), to: m.Apply(pr.Gradient.To), opacity: o}
	if s.asBox(pr, m) {
		r, radius := s.boxOf(pr, m)
		s.p.FillGradient(r, line.over(r), radius)
		return
	}
	s.fillWith(pr.Path, m, brush{gradient: line})
}

// over returns the painter's gradient for a box of it. A painter lays a gradient
// over what it fills, so what's filled in parts, each over a box of its own, needs
// the part of the line that the box's own line covers for them all to meet.
func (l *gradientLine) over(r ui.Rect) ui.LinearGradient {
	d := l.to.Sub(l.from)
	u := d.Uni()
	half := (math.Abs(float64(r.W)*u.X) + math.Abs(float64(r.H)*u.Y)) / 2
	c := geo.V(float64(r.X+r.W/2), float64(r.Y+r.H/2))
	along := func(p geo.Vec) float64 { return p.Sub(l.from).Dot(d) / d.Dot(d) }
	t0, t1 := along(c.Sub(u.Mul(half))), along(c.Add(u.Mul(half)))
	color := func(t float64) ui.Color { return toUI(l.fill.At(t).Alpha(l.opacity)) }
	g := ui.LinearGradient{Angle: float32(math.Atan2(u.X, -u.Y) * 180 / math.Pi)}
	if t1 <= t0 {
		g.From, g.To = color(t0), color(t0)
		return g
	}
	g.Stops = append(g.Stops, ui.GradientStop{Color: color(t0)})
	for i, c := range l.fill.Colors {
		if t := float64(i) / float64(len(l.fill.Colors)-1); t > t0 && t < t1 {
			g.Stops = append(g.Stops, ui.GradientStop{Color: toUI(c.Alpha(l.opacity)), At: float32((t - t0) / (t1 - t0))})
		}
	}
	g.Stops = append(g.Stops, ui.GradientStop{Color: color(t1), At: 1})
	return g
}

// boundsOf returns the box a painter finds around a path it flattens under m.
func (s *scene) boundsOf(g *geo.Path, m geo.Mat) ui.Rect {
	k := float64(s.p.Scale())
	var box geo.Box
	first := true
	for _, l := range flatten(g, m, k) {
		b := geo.BoxOf(l.pts)
		if first {
			box, first = b, false
		} else {
			box = box.Union(b)
		}
	}
	return ui.Rect{X: float32(box.X / k), Y: float32(box.Y / k), W: float32(box.W / k), H: float32(box.H / k)}
}

// fillWith fills a path under m. Cells that all of it covers have no need of a mask.
func (s *scene) fillWith(g *geo.Path, m geo.Mat, c brush) {
	if !s.spansLarge(g, m, 0) {
		path := s.buildPath(g, m)
		var bounds ui.Rect
		if c.gradient != nil {
			bounds = s.boundsOf(g, m)
		}
		c.path(s.p, path, bounds)
		return
	}
	k := float64(s.p.Scale())
	plan, shift := s.planOf(cellKey{path: g, fill: true}, m, k, true)
	first, last, ok := plan.showing(s, shift, k)
	if !ok {
		return
	}
	path := &s.path
	draw := func() { c.path(s.p, path, path.Bounds()) }
	for row := first.Y; row <= last.Y; row++ {
		from := -1
		flush := func(end int) {
			if from >= 0 {
				a, b := plan.grid.rect(plan.grid.index(from, row), shift, k), plan.grid.rect(plan.grid.index(end-1, row), shift, k)
				c.rect(s.p, ui.Rect{X: a.X, Y: a.Y, W: b.X + b.W - a.X, H: a.H})
				from = -1
			}
		}
		for col := first.X; col <= last.X; col++ {
			i := plan.grid.index(col, row)
			if polys := plan.polys[i]; len(polys) > 0 {
				flush(col)
				path.Reset()
				for _, poly := range polys {
					s.addLine(path, poly, shift, k)
					path.Close()
				}
				s.p.Clip(plan.grid.rect(i, shift, k), 0, draw)
				continue
			}
			if plan.solid[i] {
				if from < 0 {
					from = col
				}
			} else {
				flush(col)
			}
		}
		flush(last.X + 1)
	}
}

// cellKey is what a plan of cells depends on, besides how the path is placed.
type cellKey struct {
	path    *geo.Path
	fill    bool
	reach   float32 // how far from a line a cell holds it, in device pixels
	on, off float32 // the lengths of a dash and a gap, in device pixels
	a, b    float64 // the path's transform, but for where it goes
	c, d    float64
	k       float64 // device pixels per unit painted
	fx, fy  float64 // where it goes within a pixel
}

// cellPlan is the work of painting a path in cells, kept from frame to frame: a view
// that pans moves it by whole pixels, which changes nothing of it but where it goes.
// It holds the cells of a part of the path around what showed when it was made, its
// lines being in device pixels from a whole pixel near the path.
type cellPlan struct {
	fill   bool
	reach  float64 // how far from a line a cell holds it, in device pixels
	lines  []deviceLine
	bounds geo.Box // around the lines
	grid   cellGrid
	pieces []piece       // of a stroke: what each cell holds
	polys  [][][]geo.Vec // of a fill: what each cell holds, cut to it
	solid  []bool        // of a fill: the cells all inside it
	used   int           // the frame that last painted it
}

// planOf returns the plan of a path under m, and how far to move it, in device
// pixels. It has the lines of the path; showing cuts them to the cells that show.
func (s *scene) planOf(key cellKey, m geo.Mat, k float64, keep bool) (*cellPlan, geo.Vec) {
	t := geo.V(m.E*k, m.F*k)
	shift := geo.V(math.Floor(t.X), math.Floor(t.Y))
	key.a, key.b, key.c, key.d, key.k = m.A, m.B, m.C, m.D, k
	// As fine as it shows, and no finer: the rounding of adding up where a shape
	// goes differs as the view pans.
	key.fx, key.fy = math.Round((t.X-shift.X)*64)/64, math.Round((t.Y-shift.Y)*64)/64
	plan := s.a.cells[key]
	if plan == nil {
		near := m
		near.E, near.F = key.fx/k, key.fy/k
		plan = newCellPlan(key, near, k)
		if keep {
			if s.a.cells == nil {
				s.a.cells = map[cellKey]*cellPlan{}
			}
			s.a.cells[key] = plan
		}
	}
	plan.used = s.a.cellsFrame
	return plan, shift
}

// pruneCells lets go of the plans no recent frame painted.
func (a *App) pruneCells() {
	a.cellsFrame++
	if a.cellsFrame%120 != 0 {
		return
	}
	for key, plan := range a.cells {
		if a.cellsFrame-plan.used > 120 {
			delete(a.cells, key)
		}
	}
	for key, d := range a.details {
		if a.cellsFrame-d.used > 120 {
			delete(a.details, key)
		}
	}
}

func newCellPlan(key cellKey, m geo.Mat, k float64) *cellPlan {
	plan := &cellPlan{fill: key.fill, reach: float64(key.reach), lines: flatten(key.path, m, k)}
	if key.fill {
		for i := range plan.lines {
			plan.lines[i].closed = true
		}
	} else if key.off > 0 {
		plan.lines = dashed(plan.lines, float64(key.on), float64(key.off))
	}
	lo, hi := geo.V(math.Inf(1), math.Inf(1)), geo.V(math.Inf(-1), math.Inf(-1))
	for _, l := range plan.lines {
		for _, p := range l.pts {
			lo, hi = geo.V(min(lo.X, p.X), min(lo.Y, p.Y)), geo.V(max(hi.X, p.X), max(hi.Y, p.Y))
		}
	}
	if lo.X <= hi.X {
		plan.bounds = geo.Box{X: lo.X, Y: lo.Y, W: hi.X - lo.X, H: hi.Y - lo.Y}
	}
	plan.grid = cellGrid{x0: math.Floor(lo.X), y0: math.Floor(lo.Y)}
	return plan
}

// cellMargin is how far a fill's cell holds its edges: antialiasing, and rounding to
// quarters of a pixel.
const cellMargin = 2.0

// cellSlack is how many cells beyond what shows a plan cuts, for a view that pans
// not to cut them again each time another row of them comes to show.
const cellSlack = 2

// showing returns the first and last columns and rows of a plan's cells that show,
// having cut what it hasn't cut of them yet.
func (plan *cellPlan) showing(s *scene, shift geo.Vec, k float64) (first, last image.Point, ok bool) {
	if len(plan.lines) == 0 {
		return first, last, false
	}
	v := s.p.Visible()
	// The cells within a path's reach, and within what shows.
	reach := cellMargin + plan.reach
	span := func(from, to, lo, hi, origin, sh float64) (int, int) {
		a := max(math.Floor((from*k-sh-origin-1)/cellSide), math.Floor((lo-reach-origin)/cellSide))
		b := min(math.Ceil((to*k-sh-origin+1)/cellSide), math.Floor((hi+reach-origin)/cellSide)+1)
		return int(a), int(b) - 1
	}
	c0, c1 := span(float64(v.X), float64(v.X+v.W), plan.bounds.X, plan.bounds.MaxX(), plan.grid.x0, shift.X)
	r0, r1 := span(float64(v.Y), float64(v.Y+v.H), plan.bounds.Y, plan.bounds.MaxY(), plan.grid.y0, shift.Y)
	if c1 < c0 || r1 < r0 {
		return first, last, false
	}
	g := plan.grid
	if g.cols == 0 || c0 < g.c0 || r0 < g.r0 || c1 >= g.c0+g.cols || r1 >= g.r0+g.rows {
		plan.cut(c0-cellSlack, r0-cellSlack, c1+cellSlack, r1+cellSlack, reach)
	}
	return image.Pt(c0, r0), image.Pt(c1, r1), true
}

// cut cuts the path to a range of cells, as far as it reaches.
func (plan *cellPlan) cut(c0, r0, c1, r1 int, reach float64) {
	g := &plan.grid
	first := func(lo, origin float64) int { return int(math.Floor((lo - reach - origin) / cellSide)) }
	g.c0, g.r0 = max(c0, first(plan.bounds.X, g.x0)), max(r0, first(plan.bounds.Y, g.y0))
	g.cols = min(c1, first(plan.bounds.MaxX()+2*reach, g.x0)) - g.c0 + 1
	g.rows = min(r1, first(plan.bounds.MaxY()+2*reach, g.y0)) - g.r0 + 1
	if !plan.fill {
		plan.pieces = g.cut(plan.lines, plan.reach)
		return
	}
	pieces := g.cut(plan.lines, cellMargin)
	plan.polys = make([][][]geo.Vec, g.cols*g.rows)
	plan.solid = make([]bool, g.cols*g.rows)
	for row := range g.rows {
		cross := crossings(plan.lines, g.y0+float64(row+g.r0)*cellSide+0.5)
		for col := range g.cols {
			i := row*g.cols + col
			if len(pieces[i].runs) == 0 {
				plan.solid[i] = cross.windingAt(g.x0+float64(col+g.c0)*cellSide+0.5) != 0
				continue
			}
			box := g.deviceBox(i, geo.Vec{}).Expand(cellMargin)
			for _, l := range plan.lines {
				if poly := clipPolygon(l.pts, box); len(poly) >= 3 {
					plan.polys[i] = append(plan.polys[i], poly)
				}
			}
		}
	}
}

// addLine adds a polyline in device pixels, moved by shift, to a path, in the
// painter's units.
func (s *scene) addLine(path *ui.Path, pts []geo.Vec, shift geo.Vec, k float64) {
	path.MoveTo(float32((pts[0].X+shift.X)/k), float32((pts[0].Y+shift.Y)/k))
	for _, p := range pts[1:] {
		path.LineTo(float32((p.X+shift.X)/k), float32((p.Y+shift.Y)/k))
	}
}

// flatten returns a path under m as polylines in device pixels, k being device
// pixels per unit of m's result. Its curves are as fine as they show.
func flatten(g *geo.Path, m geo.Mat, k float64) []deviceLine {
	at := func(p geo.Vec) geo.Vec { return m.Apply(p).Mul(k) }
	var out []deviceLine
	var cur []geo.Vec
	flush := func(closed bool) {
		if len(cur) > 0 {
			out = append(out, deviceLine{cur, closed})
		}
		cur = nil
	}
	for _, seg := range g.Segs {
		switch seg.Op {
		case geo.OpMove:
			flush(false)
			cur = append(cur, at(seg.P[0]))
		case geo.OpLine:
			cur = append(cur, at(seg.P[0]))
		case geo.OpQuad:
			from, c, to := cur[len(cur)-1], at(seg.P[0]), at(seg.P[1])
			n := curveSteps(0.25, from.Sub(c.Mul(2)).Add(to).Len())
			for i := 1; i <= n; i++ {
				t := float64(i) / float64(n)
				u := 1 - t
				cur = append(cur, from.Mul(u*u).Add(c.Mul(2*u*t)).Add(to.Mul(t*t)))
			}
		case geo.OpCube:
			from, a, b, to := cur[len(cur)-1], at(seg.P[0]), at(seg.P[1]), at(seg.P[2])
			n := curveSteps(0.75, max(from.Sub(a.Mul(2)).Add(b).Len(), a.Sub(b.Mul(2)).Add(to).Len()))
			for i := 1; i <= n; i++ {
				t := float64(i) / float64(n)
				u := 1 - t
				cur = append(cur, from.Mul(u*u*u).Add(a.Mul(3*u*u*t)).Add(b.Mul(3*u*t*t)).Add(to.Mul(t*t*t)))
			}
		case geo.OpClose:
			flush(true)
		}
	}
	flush(false)
	return out
}

// curveSteps is Wang's formula: how many lines keep a curve within the tolerance,
// from how sharply it bends and a factor of its degree.
func curveSteps(factor, bend float64) int {
	return max(int(math.Ceil(math.Sqrt(factor*bend/curveTolerance))), 1)
}

// dashed returns the dashes of polylines, as polylines. Each subpath starts them
// anew. A dash through the start of a closed one is one dash, which turns there.
func dashed(lines []deviceLine, on, off float64) []deviceLine {
	if off <= 0 {
		return lines // no gaps: it's whole
	}
	var out []deviceLine
	for _, l := range lines {
		pts := l.pts
		if l.closed {
			pts = append(pts[:len(pts):len(pts)], pts[0])
		}
		first := len(out)
		var cur []geo.Vec
		left, inDash := on, true
		if on > 0 {
			cur = []geo.Vec{pts[0]}
		}
		for i := 1; i < len(pts); i++ {
			a, b := pts[i-1], pts[i]
			length := b.Dist(a)
			if length == 0 {
				continue
			}
			u := b.Sub(a).Mul(1 / length)
			pos := 0.0
			for {
				if left > length-pos {
					left -= length - pos
					if inDash {
						cur = append(cur, b)
					}
					break
				}
				pos += left
				p := a.Add(u.Mul(pos))
				if inDash {
					cur = append(cur, p)
					if on == 0 {
						cur = dot(p)
					}
					out = append(out, deviceLine{pts: cur})
					cur = nil
					left, inDash = off, false
				} else {
					left, inDash = on, true
					if on > 0 {
						cur = []geo.Vec{p}
					} else {
						out = append(out, deviceLine{pts: dot(p)})
						left, inDash = off, false
					}
				}
			}
		}
		if len(cur) >= 2 {
			if l.closed && on > 0 && len(out) > first && out[first].pts[0] == pts[0] {
				out[first].pts = append(cur, out[first].pts[1:]...)
			} else {
				out = append(out, deviceLine{pts: cur})
			}
		}
	}
	return out
}

// dot returns a dash of no length, which shows its caps alone: on a quarter of a
// pixel, which is where what paints it would put it, so as not to stretch it.
func dot(p geo.Vec) []geo.Vec {
	p = geo.V(math.Round(p.X*4)/4, math.Round(p.Y*4)/4)
	return []geo.Vec{p, p}
}

// cellGrid is the cells of a path that show. Its corner is on a whole pixel from
// the path's own, so that a path's cells stay its own as the view pans.
type cellGrid struct {
	x0, y0             float64
	c0, r0, cols, rows int
}

// piece is what a cell holds of a path.
type piece struct {
	runs []cellRun
	line int // the subpath its last run is of
	head int // the run of that subpath that begins where the subpath does, or -1
}

// cellRun is a part of a subpath in a cell. It's closed if it's all of a closed
// subpath, whose corner at the start needs to be a corner.
type cellRun struct {
	pts    []geo.Vec
	closed bool
}

// index returns the place in a plan of the cell of a column and a row.
func (g cellGrid) index(col, row int) int { return (row-g.r0)*g.cols + col - g.c0 }

// deviceBox returns a cell's box, in device pixels, moved by shift.
func (g cellGrid) deviceBox(i int, shift geo.Vec) geo.Box {
	col, row := i%g.cols+g.c0, i/g.cols+g.r0
	return geo.Box{X: g.x0 + shift.X + float64(col)*cellSide, Y: g.y0 + shift.Y + float64(row)*cellSide, W: cellSide, H: cellSide}
}

// rect returns a cell's box, moved by shift, in the painter's units.
func (g cellGrid) rect(i int, shift geo.Vec, k float64) ui.Rect {
	b := g.deviceBox(i, shift)
	return ui.Rect{X: float32(b.X / k), Y: float32(b.Y / k), W: float32(b.W / k), H: float32(b.H / k)}
}

// cut gives each cell the parts of polylines within margin of it, as runs that
// join where the polyline does.
func (g cellGrid) cut(lines []deviceLine, margin float64) []piece {
	pieces := make([]piece, g.cols*g.rows)
	cellRange := func(lo, hi, origin float64, first, count int) (int, int) {
		a := int(math.Floor((lo-margin-origin)/cellSide)) - first
		b := int(math.Floor((hi+margin-origin)/cellSide)) - first
		return max(a, 0), min(b, count-1)
	}
	for li, l := range lines {
		pts := l.pts
		if l.closed && len(pts) > 1 && pts[0] != pts[len(pts)-1] {
			pts = append(pts[:len(pts):len(pts)], pts[0])
		}
		for j := 1; j < len(pts); j++ {
			a, b := pts[j-1], pts[j]
			r0, r1 := cellRange(min(a.Y, b.Y), max(a.Y, b.Y), g.y0, g.r0, g.rows)
			c0, c1 := cellRange(min(a.X, b.X), max(a.X, b.X), g.x0, g.c0, g.cols)
			for row := r0; row <= r1; row++ {
				for col := c0; col <= c1; col++ {
					i := row*g.cols + col
					p, q, ok := clipSegment(a, b, g.deviceBox(i, geo.Vec{}).Expand(margin))
					if !ok {
						continue
					}
					cell := &pieces[i]
					if n := len(cell.runs); n == 0 || cell.line != li {
						cell.line, cell.head = li, -1
					} else if run := &cell.runs[n-1]; run.pts[len(run.pts)-1] == p {
						run.pts = append(run.pts, q)
						continue
					}
					if j == 1 && p == a {
						cell.head = len(cell.runs)
					}
					cell.runs = append(cell.runs, cellRun{pts: []geo.Vec{p, q}})
				}
			}
		}
		if !l.closed {
			continue
		}
		// A closed subpath goes on through its start: the run that reaches it and
		// the one that leaves it are one.
		for i := range pieces {
			cell := &pieces[i]
			n := len(cell.runs)
			if n == 0 || cell.line != li || cell.head < 0 {
				continue
			}
			last := &cell.runs[n-1]
			if last.pts[len(last.pts)-1] != pts[0] {
				continue
			}
			if cell.head == n-1 {
				last.closed = true
				continue
			}
			cell.runs[cell.head].pts = append(last.pts, cell.runs[cell.head].pts[1:]...)
			cell.runs = cell.runs[:n-1]
		}
	}
	return pieces
}

// clipSegment returns the part of a segment in a box, if any (Liang-Barsky).
func clipSegment(a, b geo.Vec, box geo.Box) (geo.Vec, geo.Vec, bool) {
	t0, t1 := 0.0, 1.0
	d := b.Sub(a)
	side := func(p, q float64) bool {
		if p == 0 {
			return q >= 0
		}
		t := q / p
		if p < 0 {
			t0 = max(t0, t)
		} else {
			t1 = min(t1, t)
		}
		return t0 <= t1
	}
	if !side(-d.X, a.X-box.X) || !side(d.X, box.MaxX()-a.X) || !side(-d.Y, a.Y-box.Y) || !side(d.Y, box.MaxY()-a.Y) {
		return a, b, false
	}
	p, q := a, b
	if t0 > 0 {
		p = a.Add(d.Mul(t0))
	}
	if t1 < 1 {
		q = a.Add(d.Mul(t1))
	}
	return p, q, true
}

// clipPolygon cuts a polygon to a box (Sutherland-Hodgman), which keeps how many
// times it winds around each point in the box.
func clipPolygon(pts []geo.Vec, box geo.Box) []geo.Vec {
	out := pts
	for _, edge := range [4]struct {
		in    func(geo.Vec) bool
		cross func(a, b geo.Vec) geo.Vec
	}{
		{func(p geo.Vec) bool { return p.X >= box.X }, func(a, b geo.Vec) geo.Vec { return alongX(a, b, box.X) }},
		{func(p geo.Vec) bool { return p.X <= box.MaxX() }, func(a, b geo.Vec) geo.Vec { return alongX(a, b, box.MaxX()) }},
		{func(p geo.Vec) bool { return p.Y >= box.Y }, func(a, b geo.Vec) geo.Vec { return alongY(a, b, box.Y) }},
		{func(p geo.Vec) bool { return p.Y <= box.MaxY() }, func(a, b geo.Vec) geo.Vec { return alongY(a, b, box.MaxY()) }},
	} {
		var next []geo.Vec
		for i, p := range out {
			last := out[(i+len(out)-1)%len(out)]
			if edge.in(p) != edge.in(last) {
				next = append(next, edge.cross(last, p))
			}
			if edge.in(p) {
				next = append(next, p)
			}
		}
		out = next
		if len(out) == 0 {
			return nil
		}
	}
	return out
}

func alongX(a, b geo.Vec, x float64) geo.Vec {
	return geo.V(x, a.Y+(b.Y-a.Y)*(x-a.X)/(b.X-a.X))
}

func alongY(a, b geo.Vec, y float64) geo.Vec {
	return geo.V(a.X+(b.X-a.X)*(y-a.Y)/(b.Y-a.Y), y)
}

// crossing is where an edge crosses a row, and which way: +1 down, -1 up.
type crossing struct {
	x   float64
	dir int
}

type rowCrossings []crossing

// crossings returns where the edges of closed polylines cross a line across.
func crossings(lines []deviceLine, y float64) rowCrossings {
	var out rowCrossings
	for _, l := range lines {
		for i, b := range l.pts {
			a := l.pts[(i+len(l.pts)-1)%len(l.pts)]
			if (a.Y <= y) != (b.Y <= y) {
				dir := 1
				if b.Y < a.Y {
					dir = -1
				}
				out = append(out, crossing{a.X + (y-a.Y)*(b.X-a.X)/(b.Y-a.Y), dir})
			}
		}
	}
	return out
}

// windingAt returns how many times the edges wind around a point of the row.
func (r rowCrossings) windingAt(x float64) int {
	n := 0
	for _, c := range r {
		if c.x < x {
			n += c.dir
		}
	}
	return n
}

// strokePrim strokes what a shape draws, at the camera's zoom.
func (s *scene) strokePrim(pr editor.Prim, m geo.Mat, c ui.Color) {
	st := stroke{width: float32(pr.Width * s.cam.Z), color: c, butt: pr.Butt, kept: true}
	if len(pr.Dash) >= 2 {
		st.dash = []float64{pr.Dash[0] * s.cam.Z, pr.Dash[1] * s.cam.Z}
	}
	if pr.Butt {
		st.width = max(st.width, s.pixel())
	}
	s.stroke(pr.Path, m, st)
}

// lightLines strokes what a shape draws as short lines, each a few pixels of mask
// at most and, on a GPU, none: as much as a view this crowded needs.
func (s *scene) lightLines(pr editor.Prim, m geo.Mat) {
	const step = 64.0 // device pixels
	k := float64(s.p.Scale())
	w, c := float32(pr.Width*s.cam.Z), toUI(pr.Color)
	v := s.p.Visible()
	box := geo.Box{X: float64(v.X) * k, Y: float64(v.Y) * k, W: float64(v.W) * k, H: float64(v.H) * k}.Expand(step)
	for _, l := range flatten(pr.Path, m, k) {
		pts := l.pts
		if l.closed {
			pts = append(pts[:len(pts):len(pts)], pts[0])
		}
		for i := 1; i < len(pts); i++ {
			a, b, ok := clipSegment(pts[i-1], pts[i], box)
			if !ok {
				continue
			}
			n := max(int(math.Ceil(b.Dist(a)/step)), 1)
			for j := range n {
				p, q := a.Add(b.Sub(a).Mul(float64(j)/float64(n))), a.Add(b.Sub(a).Mul(float64(j+1)/float64(n)))
				s.segment[0] = [2]float32{float32(p.X / k), float32(p.Y / k)}
				s.segment[1] = [2]float32{float32(q.X / k), float32(q.Y / k)}
				s.p.StrokePolyline(s.segment[:], w, c)
			}
		}
	}
}

// primMaskArea estimates the device pixels of masks a stroke or fill asks for. A
// large one's is what its cells hold near each line, up to all of what shows of
// it, as hatching comes to. k is device pixels per unit painted.
func (s *scene) primMaskArea(pr editor.Prim, m geo.Mat, k float64, turned bool) float64 {
	if pr.Path == nil || (pr.Kind != editor.PrimFill && pr.Kind != editor.PrimStroke) || s.asBox(pr, m) {
		return 0
	}
	reach := 2.0
	if pr.Kind == editor.PrimStroke {
		reach += pr.Width * s.cam.Z * k / 2
	}
	at := func(p geo.Vec) geo.Vec { return m.Apply(p).Mul(k) }
	view := geo.Box{
		X: ((s.view.X+s.cam.X)*s.cam.Z + s.origin.X) * k, Y: ((s.view.Y+s.cam.Y)*s.cam.Z + s.origin.Y) * k,
		W: s.view.W * s.cam.Z * k, H: s.view.H * s.cam.Z * k,
	}
	lo, hi := geo.V(math.Inf(1), math.Inf(1)), geo.V(math.Inf(-1), math.Inf(-1))
	var from, start geo.Vec
	cost := 0.0
	leg := func(to geo.Vec) {
		lo, hi = geo.V(min(lo.X, to.X), min(lo.Y, to.Y)), geo.V(max(hi.X, to.X), max(hi.Y, to.Y))
		if a, b, ok := clipSegment(from, to, view); ok {
			d := b.Sub(a)
			cost += d.Len()*2*reach + min(math.Abs(d.X), math.Abs(d.Y))*cellSide
		}
		from = to
	}
	for _, seg := range pr.Path.Segs {
		switch seg.Op {
		case geo.OpMove:
			from = at(seg.P[0])
			start = from
			lo, hi = geo.V(min(lo.X, from.X), min(lo.Y, from.Y)), geo.V(max(hi.X, from.X), max(hi.Y, from.Y))
		case geo.OpLine:
			leg(at(seg.P[0]))
		case geo.OpQuad:
			leg(at(seg.P[0]))
			leg(at(seg.P[1]))
		case geo.OpCube:
			leg(at(seg.P[0]))
			leg(at(seg.P[1]))
			leg(at(seg.P[2]))
		case geo.OpClose:
			leg(start)
		}
	}
	if lo.X > hi.X {
		return 0
	}
	w, h := hi.X-lo.X+2*reach, hi.Y-lo.Y+2*reach
	if turned || (w <= wholeMask && h <= wholeMask) {
		return w * h
	}
	return min(cost, min(w, view.W)*min(h, view.H))
}
