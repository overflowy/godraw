package editor

import (
	"fmt"
	"math"

	"godraw/internal/doc"
	"godraw/internal/geo"
)

// PrimKind is the kind of a Prim.
type PrimKind uint8

// Kinds of primitive.
const (
	PrimFill PrimKind = iota
	PrimStroke
	PrimText
	PrimImage
	PrimClipRect // reserved for what clips its own children
)

// Prim is one thing a shape draws, in its own space. A list of them is all
// that the window and SVG export need of a shape, and the list does not
// change with the camera, so the shape can keep it.
type Prim struct {
	Kind  PrimKind
	Path  *geo.Path
	Color Color
	Width float64
	Dash  []float64
	Butt  bool // flat ends and pointed corners rather than round ones

	// Ink, on a hand-drawn stroke's fill, is the line the fill outlines: what
	// a view with very many strokes draws in its place.
	Ink *Ink
	// Rounded, on a fill or a frame's border, says that its path is this box, which
	// the GPU can draw as it is rather than from a mask of the path. It draws a border
	// on whole pixels, which suits a frame's and not a shape's, whose weight would
	// differ from that of a shape of another form.
	Rounded *RoundedBox
	// Hatched, on the lines of a pattern, is what they fill, for a view too far away
	// to make them out.
	Hatched *geo.Path
	// Gradient, on a fill, is what it fills with in place of Color.
	Gradient *GradientFill

	// Text.
	Block *TextBlock
	X, Y  float64
	Size  float64
	Font  string
	Label bool // hidden while the shape's text is edited

	// Image.
	Asset doc.ID
	Box   geo.Box
	FlipX bool
	FlipY bool
	Crop  *doc.Crop
}

// GradientFill blends colors, evenly spaced, along a line in a shape's space.
type GradientFill struct {
	From, To geo.Vec
	Colors   []Color
}

// Along returns how far along the line a point lies, 0 at its start and 1 at its
// end, and past those beyond them.
func (g *GradientFill) Along(p geo.Vec) float64 {
	d := g.To.Sub(g.From)
	l := d.Dot(d)
	if l == 0 {
		return 0
	}
	return p.Sub(g.From).Dot(d) / l
}

// At returns the color a part of the way along the line.
func (g *GradientFill) At(t float64) Color {
	k := min(max(t, 0), 1) * float64(len(g.Colors)-1)
	i := min(int(k), len(g.Colors)-2)
	return g.Colors[i].Mix(g.Colors[i+1], k-float64(i))
}

// gradientOf returns the fill of a shape with a gradient, over the box of its
// outline, as CSS lays one over a box: its line reaches both far corners.
func gradientOf(s *doc.Shape, poly []geo.Vec, pal *Palette) *GradientFill {
	g := s.Gradient()
	if g == nil || len(poly) == 0 {
		return nil
	}
	colors := make([]Color, len(g.Colors))
	for i, name := range g.Colors {
		if IsHex(name) {
			colors[i] = Hex(name)
		} else {
			colors[i] = pal.Swatch(name).Fill
		}
	}
	box := geo.BoxOf(poly)
	sin, cos := math.Sincos(g.Angle * math.Pi / 180)
	dir := geo.V(sin, -cos)
	half := dir.Mul((math.Abs(box.W*dir.X) + math.Abs(box.H*dir.Y)) / 2)
	c := box.Center()
	return &GradientFill{From: c.Sub(half), To: c.Add(half), Colors: colors}
}

// FillAt returns what a shape's fill paints at a point in its space: Alpha 0 for
// nothing, and known false for what can't be told without looking, as an image.
func (e *Editor) FillAt(s *doc.Shape, pal *Palette, q geo.Vec) (c Color, known bool) {
	switch s.Type {
	case doc.TypeGeo, doc.TypeDraw, doc.TypeNote, doc.TypeFrame, doc.TypeImage:
	case doc.TypeText, doc.TypeHighlight, doc.TypeLine, doc.TypeArrow, doc.TypeGroup:
		return Color{}, true
	default:
		if e.Geometry(s).Bounds.Contains(q) {
			return pal.Swatch("grey").Semi, true
		}
		return Color{}, true
	}
	g := e.Geometry(s)
	if !g.Closed || !geo.PointInPolygon(q, g.Outline) {
		return Color{}, true
	}
	sw := pal.SwatchOf(s)
	switch s.Type {
	case doc.TypeNote:
		return sw.NoteFill, true
	case doc.TypeFrame:
		return sw.FrameFill, true
	case doc.TypeImage:
		return Color{}, false
	}
	if f := gradientOf(s, g.Outline, pal); f != nil {
		return f.At(f.Along(q)), true
	}
	if c, ok := fillHexOf(s); ok {
		return c, true
	}
	switch s.Fill {
	case "semi":
		return pal.Solid, true
	case "solid", "pattern":
		return sw.Semi, true
	case "fill":
		return sw.Fill, true
	}
	return Color{}, true
}

// RoundedBox is a box with rounded corners.
type RoundedBox struct {
	geo.Box
	Radius float64
}

// Ink is a line through points, with its width at each.
type Ink struct {
	Pts []geo.Vec
	W   []float64
}

// Display returns what a shape draws, in its own space. Arrows follow what
// they are bound to, and groups draw nothing of their own.
func (e *Editor) Display(s *doc.Shape, pal *Palette) []Prim {
	i := 0
	if pal.Dark {
		i = 1
	}
	if s.Type == doc.TypeArrow {
		e.Arrow(s) // brings its memo up to date with what it is bound to
		m := e.arrows[s.ID]
		if !m.drawn[i] {
			m.display[i], m.drawn[i] = e.arrowPrims(s, pal), true
		}
		return m.display[i]
	}
	c := cacheOf(s)
	if !c.ok[i] {
		c.display[i], c.ok[i] = e.prims(s, pal), true
	}
	return c.display[i]
}

func (e *Editor) prims(s *doc.Shape, pal *Palette) []Prim {
	var out []Prim
	switch s.Type {
	case doc.TypeGeo:
		out = e.geoPrims(s, pal)
	case doc.TypeDraw:
		out = e.drawPrims(s, pal)
	case doc.TypeHighlight:
		out = e.highlightPrims(s, pal)
	case doc.TypeLine:
		out = e.linePrims(s, pal)
	case doc.TypeText:
		out = e.textPrims(s, pal)
	case doc.TypeNote:
		out = e.notePrims(s, pal)
	case doc.TypeFrame:
		out = e.framePrims(s, pal)
	case doc.TypeImage:
		out = e.imagePrims(s, pal)
	case doc.TypeGroup:
		return nil
	default:
		out = e.unknownPrims(s, pal)
	}
	if sh := s.Shadow(); sh != nil {
		out = append(e.shadowPrims(s, sh, pal), out...)
	}
	return out
}

func strokeWith(path *geo.Path, width float64, c Color) Prim {
	return Prim{Kind: PrimStroke, Path: path, Width: width, Color: c}
}

func fillWith(path *geo.Path, c Color) Prim {
	return Prim{Kind: PrimFill, Path: path, Color: c}
}

// --- ink ---

// noise is smooth noise along a stroke: a sum of slow waves with phases
// from the seed.
type noise struct{ p [4]float64 }

func newNoise(r *geo.Rng) noise {
	return noise{[4]float64{r.Next() * math.Pi, r.Next() * math.Pi, r.Next() * math.Pi, r.Next() * math.Pi}}
}

func (n noise) at(d float64) float64 {
	return 0.5*math.Sin(d/47+n.p[0]) + 0.3*math.Sin(d/23+n.p[1]) + 0.2*math.Sin(d/11+n.p[2])
}

// resample walks a polyline, returning points about step apart, and every
// original point closer than that to a corner, so that corners stay.
func resample(pts []geo.Vec, step float64) []geo.Vec {
	if len(pts) < 2 {
		return pts
	}
	out := []geo.Vec{pts[0]}
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		l := a.Dist(b)
		n := int(l / step)
		for k := 1; k <= n; k++ {
			out = append(out, a.Lerp(b, float64(k)/float64(n+1)))
		}
		out = append(out, b)
	}
	return out
}

// ribbon returns the filled outline of a line through pts, widths wide,
// with round ends.
func ribbon(pts []geo.Vec, widths []float64) Prim {
	return Prim{Kind: PrimFill, Path: ribbonPath(pts, widths), Ink: &Ink{pts, widths}}
}

func ribbonPath(pts []geo.Vec, widths []float64) *geo.Path {
	n := len(pts)
	p := &geo.Path{}
	if n == 0 {
		return p
	}
	if n == 1 {
		r := widths[0] / 2
		return geo.EllipsePath(geo.Box{X: pts[0].X - r, Y: pts[0].Y - r, W: 2 * r, H: 2 * r})
	}
	left := make([]geo.Vec, n)
	right := make([]geo.Vec, n)
	var prevN geo.Vec
	for i := range pts {
		a, b := pts[max(i-1, 0)], pts[min(i+1, n-1)]
		nrm := b.Sub(a).Uni().Per()
		if nrm == (geo.Vec{}) {
			nrm = prevN
		}
		prevN = nrm
		h := widths[i] / 2
		left[i] = pts[i].Add(nrm.Mul(h))
		right[i] = pts[i].Sub(nrm.Mul(h))
	}
	outline := make([]geo.Vec, 0, 2*n+12)
	outline = append(outline, left...)
	outline = append(outline, capPoints(pts[n-1], left[n-1], pts[n-2])...)
	for i := n - 1; i >= 0; i-- {
		outline = append(outline, right[i])
	}
	outline = append(outline, capPoints(pts[0], right[0], pts[1])...)
	return p.Smooth(outline, true)
}

// capPoints returns the points of the half circle around the end of a
// stroke at c, going from the point from, away from where the stroke came.
func capPoints(c, from, back geo.Vec) []geo.Vec {
	r := from.Sub(c)
	dir := c.Sub(back)
	// Turn the way that goes through the outside of the end.
	sign := 1.0
	if r.Rot(math.Pi/2).Dot(dir) < 0 {
		sign = -1
	}
	out := make([]geo.Vec, 0, 4)
	for i := 1; i <= 4; i++ {
		out = append(out, c.Add(r.Rot(sign*math.Pi*float64(i)/5)))
	}
	return out
}

// inkLine returns a hand-drawn stroke along pts: its center wanders a
// little and its width breathes. Closed lines overlap where they meet, as a
// pen coming round does.
func inkLine(pts []geo.Vec, closed bool, width float64, seed string) Prim {
	r := geo.NewRng(seed)
	wander, breathe := newNoise(r), newNoise(r)
	if closed && len(pts) > 2 {
		// Start partway along an edge, and go round a little past it.
		overlap := []geo.Vec{}
		dist := 0.0
		maxOver := math.Max(width*3, 8)
		for i := 1; i <= len(pts); i++ {
			a, b := pts[i-1], pts[i%len(pts)]
			l := a.Dist(b)
			if dist+l >= maxOver {
				overlap = append(overlap, a.Lerp(b, (maxOver-dist)/math.Max(l, 1e-9)))
				break
			}
			overlap = append(overlap, b)
			dist += l
		}
		pts = append(append(append([]geo.Vec{}, pts...), pts[0]), overlap...)
	}
	line := resample(pts, 10)
	out := make([]geo.Vec, len(line))
	ws := make([]float64, len(line))
	d := 0.0
	amp := math.Min(width*0.7, 2.4)
	for i, p := range line {
		if i > 0 {
			d += line[i-1].Dist(p)
		}
		a, b := line[max(i-1, 0)], line[min(i+1, len(line)-1)]
		nrm := b.Sub(a).Uni().Per()
		out[i] = p.Add(nrm.Mul(wander.at(d) * amp))
		ws[i] = width * (1 + 0.2*breathe.at(d*1.7))
	}
	// The pen eases on and off the paper, so that where a closed line meets
	// itself shows no lump.
	if closed {
		total := d
		ease := math.Min(width*4, total/4)
		d = 0
		for i := range line {
			if i > 0 {
				d += line[i-1].Dist(line[i])
			}
			k := math.Min(d, total-d) / math.Max(ease, 1e-9)
			if k < 1 {
				ws[i] *= 0.35 + 0.65*k
			}
		}
	}
	return ribbon(out, ws)
}

// dashOf returns the dashes of a dash style for a stroke width.
func dashOf(dash string, w float64) []float64 {
	switch dash {
	case "dashed":
		return []float64{w * 3, w * 3}
	case "dotted":
		return []float64{0, w * 2.5}
	}
	return nil
}

// outlinePrims draws a path in a dash style. base is the true path, pts its
// flattened form for the ink.
func outlinePrims(dash string, base *geo.Path, pts []geo.Vec, closed bool, w float64, c Color, seed string) []Prim {
	switch dash {
	case "none":
		return nil
	case "draw":
		p := inkLine(pts, closed, w, seed)
		p.Color = c
		return []Prim{p}
	case "dashed", "dotted":
		p := strokeWith(base, w, c)
		p.Dash = dashOf(dash, w)
		return []Prim{p}
	}
	return []Prim{strokeWith(base, w, c)}
}

// --- fills ---

// hatch returns the diagonal lines of a pattern fill inside a polygon.
func hatch(poly []geo.Vec, spacing float64) *geo.Path {
	box := geo.BoxOf(poly)
	p := &geo.Path{}
	// Lines x + y = k, every spacing·√2 along x.
	step := spacing * math.Sqrt2
	start := math.Floor((box.X+box.Y)/step) * step
	end := box.MaxX() + box.MaxY()
	far := box.W + box.H + 10
	for k := start; k <= end; k += step {
		a := geo.V(k-box.Y+far, box.Y-far)
		b := geo.V(k-box.Y-far, box.Y+far)
		cross := geo.SegmentPolyline(a, b, poly, true)
		sortAlong(cross, a)
		for i := 0; i+1 < len(cross); i += 2 {
			p.Move(cross[i]).Line(cross[i+1])
		}
	}
	return p
}

func sortAlong(pts []geo.Vec, from geo.Vec) {
	for i := 1; i < len(pts); i++ {
		for j := i; j > 0 && pts[j].Dist(from) < pts[j-1].Dist(from); j-- {
			pts[j], pts[j-1] = pts[j-1], pts[j]
		}
	}
}

// fillPrims draws what a shape fills with.
func fillPrims(s *doc.Shape, base *geo.Path, poly []geo.Vec, sw Swatch, pal *Palette) []Prim {
	if g := gradientOf(s, poly, pal); g != nil {
		return []Prim{{Kind: PrimFill, Path: base, Color: g.Colors[0], Gradient: g}}
	}
	if c, ok := fillHexOf(s); ok {
		return []Prim{fillWith(base, c)}
	}
	switch s.Fill {
	case "semi":
		return []Prim{fillWith(base, pal.Solid)}
	case "solid":
		return []Prim{fillWith(base, sw.Semi)}
	case "fill":
		return []Prim{fillWith(base, sw.Fill)}
	case "pattern":
		return []Prim{
			fillWith(base, sw.Semi),
			{Kind: PrimStroke, Path: hatch(poly, 8), Width: 1.6, Color: sw.Pattern, Hatched: base},
		}
	case "lined-fill":
		return []Prim{{Kind: PrimStroke, Path: hatch(poly, 8), Width: 1.6, Color: sw.Lined, Hatched: base}}
	}
	return nil
}

// --- geo ---

func geoBasePath(s *doc.Shape, w, h float64, poly []geo.Vec) *geo.Path {
	switch s.Geo {
	case "ellipse":
		return geo.EllipsePath(geo.Box{W: w, H: h})
	case "oval":
		return ovalPath(w, h)
	case "heart":
		return heartPath(w, h)
	case "cloud":
		return cloudPath(w, h, string(s.ID))
	}
	if r := radiusOf(s, w, h); r > 0 {
		c := geo.Box{W: w, H: h}.Corners()
		return geo.RoundedPoly(c[:], r)
	}
	if s.Dash == "draw" {
		return geo.RoundedPoly(poly, math.Min(strokeOf(s.Size)*s.Scale*2, 10))
	}
	return (&geo.Path{}).Poly(poly, true)
}

// radiusOf returns how round a shape's corners were asked to be, as far as its
// size allows.
func radiusOf(s *doc.Shape, w, h float64) float64 {
	if s.Geo != "rectangle" {
		return 0
	}
	return math.Min(s.Radius(), math.Min(w, h)/2)
}

// geoBox returns the box that a geo shape's path is, if it is one.
func geoBox(s *doc.Shape, w, h float64) *RoundedBox {
	switch s.Geo {
	case "rectangle", "x-box", "check-box":
	default:
		return nil
	}
	// The GPU's corners on a Mac aren't quite round, which a stroke along a path that
	// is would show at any size worth asking for.
	if radiusOf(s, w, h) > 0 {
		return nil
	}
	box := &RoundedBox{W: w, H: h}
	if s.Dash == "draw" {
		box.Radius = math.Min(math.Min(strokeOf(s.Size)*s.Scale*2, 10), math.Min(w, h)/2)
	}
	return box
}

func (e *Editor) geoPrims(s *doc.Shape, pal *Palette) []Prim {
	w, h := s.W, s.H+s.GrowY
	g := e.Geometry(s)
	sw := pal.SwatchOf(s)
	stroke := strokeOf(s.Size) * s.Scale
	base := geoBasePath(s, w, h, g.Outline)
	flat, _ := base.Points(10)
	box := geoBox(s, w, h)
	out := fillPrims(s, base, flat, sw, pal)
	for i := range out {
		if out[i].Kind == PrimFill {
			out[i].Rounded = box
		}
	}
	if !s.NoOutline() {
		out = append(out, outlinePrims(s.Dash, base, flat, true, stroke, sw.Solid, string(s.ID))...)
	}
	inner := &geo.Path{}
	switch s.Geo {
	case "x-box":
		inner.Move(geo.V(0, 0)).Line(geo.V(w, h)).Move(geo.V(w, 0)).Line(geo.V(0, h))
	case "check-box":
		k := math.Min(w, h)
		inner.Move(geo.V(w/2-k*0.27, h/2)).Line(geo.V(w/2-k*0.07, h/2+k*0.2)).Line(geo.V(w/2+k*0.27, h/2-k*0.2))
	}
	if len(inner.Segs) > 0 {
		out = append(out, strokeWith(inner, stroke, sw.Solid))
	}
	if s.Text != "" {
		out = append(out, e.labelPrim(s, g.Label, labelSizeOf(s.Size)*s.Scale, pal.InkOf(s), pal))
	}
	return out
}

// labelPrim draws a shape's label in its box.
func (e *Editor) labelPrim(s *doc.Shape, box *geo.Box, size float64, c Color, pal *Palette) Prim {
	b := inked(e.TextBlockOf(s, s.Text, size, box.W), pal)
	b = alignBlock(b, box.W, s.Align)
	return Prim{Kind: PrimText, Block: &b, X: box.X, Y: box.Y, Size: size, Font: s.Font, Color: c, Label: true}
}

// alignBlock moves the lines of a block within width by an alignment.
func alignBlock(b TextBlock, width float64, align string) TextBlock {
	lines := make([]TextLine, len(b.Lines))
	for i, l := range b.Lines {
		switch align {
		case "middle":
			l.X = (width - l.W) / 2
		case "end":
			l.X = width - l.W
		default:
			l.X = 0
		}
		lines[i] = l
	}
	b.Lines = lines
	return b
}

// --- freehand ---

// smoothStroke pulls points toward their neighbors, a pen's weight taking
// out the hand's tremor.
func smoothStroke(pts []geo.Vec, amount float64) []geo.Vec {
	if len(pts) < 3 {
		return pts
	}
	out := make([]geo.Vec, 0, len(pts))
	out = append(out, pts[0])
	cur := pts[0]
	for _, p := range pts[1:] {
		cur = cur.Lerp(p, 1-amount)
		out = append(out, cur)
	}
	out = append(out, pts[len(pts)-1])
	return out
}

// freehandPath returns the filled outline of a pen stroke through pts: as
// wide as size where the pen goes slowly or presses, thinner where it goes
// fast.
func freehandPath(s *doc.Shape, pts []geo.Vec, pressure []float64, size float64) Prim {
	if len(pts) == 1 {
		r := size / 2
		return Prim{Kind: PrimFill, Path: geo.EllipsePath(geo.Box{X: pts[0].X - r, Y: pts[0].Y - r, W: 2 * r, H: 2 * r})}
	}
	sm := smoothStroke(pts, 0.45)
	// Points too close together turn the outline into noise.
	clean := make([]geo.Vec, 0, len(sm))
	cleanP := make([]float64, 0, len(sm))
	for i, p := range sm {
		if len(clean) > 0 && p.Dist(clean[len(clean)-1]) < size*0.12 && i < len(sm)-1 {
			continue
		}
		clean = append(clean, p)
		pr := 0.5
		if len(pressure) > 0 {
			pr = pressure[min(i, len(pressure)-1)]
		}
		cleanP = append(cleanP, pr)
	}
	if len(clean) == 1 {
		clean = append(clean, sm[len(sm)-1].Add(geo.V(0.01, 0)))
		cleanP = append(cleanP, cleanP[0])
	}
	w := make([]float64, len(clean))
	sim := 0.75
	for i := range clean {
		var target float64
		if s.IsPen {
			target = cleanP[i]
		} else {
			speed := 0.0
			if i > 0 {
				speed = clean[i].Dist(clean[i-1]) / size
			}
			target = 1 - math.Min(speed*0.32, 0.75)
		}
		sim += (target - sim) * 0.28
		w[i] = size * (0.55 + 0.6*sim)
	}
	return ribbon(clean, w)
}

func (e *Editor) drawPrims(s *doc.Shape, pal *Palette) []Prim {
	pts := strokePoints(s)
	if len(pts) == 0 {
		return nil
	}
	sw := pal.SwatchOf(s)
	stroke := strokeOf(s.Size) * s.Scale
	var pressure []float64
	if s.IsPen {
		for _, seg := range s.Segments {
			for _, p := range seg.Points {
				pressure = append(pressure, p.Z)
			}
		}
	}
	var out []Prim
	smooth := (&geo.Path{}).Smooth(pts, s.IsClosed)
	if s.IsClosed && s.Fill != "none" {
		out = append(out, fillPrims(s, smooth, pts, sw, pal)...)
	}
	if s.NoOutline() {
		return out
	}
	if s.Dash == "draw" {
		size := 1 + stroke*1.5
		ink := freehandPath(s, pts, pressure, size)
		ink.Color = sw.Solid
		return append(out, ink)
	}
	if len(pts) == 1 {
		r := stroke
		out = append(out, fillWith(geo.EllipsePath(geo.Box{X: pts[0].X - r, Y: pts[0].Y - r, W: 2 * r, H: 2 * r}), sw.Solid))
		return out
	}
	p := strokeWith(smooth, stroke, sw.Solid)
	p.Dash = dashOf(s.Dash, stroke)
	return append(out, p)
}

func (e *Editor) highlightPrims(s *doc.Shape, pal *Palette) []Prim {
	pts := strokePoints(s)
	if len(pts) == 0 {
		return nil
	}
	size := fontSizeOf(s.Size) * 1.12 * s.Scale
	c := pal.SwatchOf(s).Highlight.Alpha(0.75)
	sm := smoothStroke(pts, 0.5)
	if len(sm) == 1 {
		r := size / 2
		return []Prim{fillWith(geo.EllipsePath(geo.Box{X: pts[0].X - r, Y: pts[0].Y - r, W: 2 * r, H: 2 * r}), c)}
	}
	return []Prim{strokeWith((&geo.Path{}).Smooth(sm, false), size, c)}
}

// --- line ---

func (e *Editor) linePrims(s *doc.Shape, pal *Palette) []Prim {
	base := linePath(s)
	pts, _ := base.Points(16)
	if len(pts) == 0 {
		return nil
	}
	return outlinePrims(s.Dash, base, pts, false, strokeOf(s.Size)*s.Scale, pal.SwatchOf(s).Solid, string(s.ID))
}

// --- text, note, frame, image ---

func (e *Editor) textPrims(s *doc.Shape, pal *Palette) []Prim {
	size := fontSizeOf(s.Size) * s.Scale
	g := e.Geometry(s)
	var b TextBlock
	if s.AutoSize {
		b = e.TextBlockOf(s, s.Text, size, 0)
	} else {
		b = e.TextBlockOf(s, s.Text, size, g.Bounds.W)
	}
	b = alignBlock(inked(b, pal), g.Bounds.W, s.TextAlign)
	return []Prim{{Kind: PrimText, Block: &b, Size: size, Font: s.Font, Color: pal.InkOf(s), Label: true}}
}

func (e *Editor) notePrims(s *doc.Shape, pal *Palette) []Prim {
	g := e.Geometry(s)
	sw := pal.SwatchOf(s)
	box := g.Bounds
	var out []Prim
	// A soft shadow, built of paths so that it turns with the shape.
	if !pal.Dark {
		for i := range 6 {
			d := float64(6-i) * s.Scale
			sb := geo.Box{X: box.X + 3*s.Scale - d/2, Y: box.Y + d*0.9, W: box.W - 6*s.Scale + d, H: box.H}
			c := sb.Corners()
			shadow := fillWith(geo.RoundedPoly(c[:], 6*s.Scale), Color{A: 0.022})
			shadow.Rounded = &RoundedBox{sb, 6 * s.Scale}
			out = append(out, shadow)
		}
	}
	c := box.Corners()
	body := fillWith(geo.RoundedPoly(c[:], 6*s.Scale), sw.NoteFill)
	body.Rounded = &RoundedBox{box, 6 * s.Scale}
	out = append(out, body)
	if s.Text != "" {
		out = append(out, e.labelPrim(s, g.Label, labelSizeOf(s.Size)*s.Scale, pal.InkOf(s), pal))
	}
	return out
}

// FrameHeading returns the font of a frame's heading, and its size in the page
// when it was given one, to read as what the frame holds does. Without one, a
// heading stays the same size on the screen, as tldraw's does.
func FrameHeading(s *doc.Shape) (size float64, font string) {
	var style string
	if s.MetaValue(doc.MetaFrameSize, &style) {
		size = fontSizeOf(style) // as a card's title, which is a text shape
		font = "draw"
	}
	s.MetaValue(doc.MetaFrameFont, &font)
	return size, font
}

// FrameHeadingFloor is the size of a frame's heading on the screen, which a
// heading with a size of its own doesn't go below.
const FrameHeadingFloor = 12.0

// FrameHeadingBox returns what a frame's heading covers in the page, in a picture
// or view where px of the page make a pixel.
func (e *Editor) FrameHeadingBox(s *doc.Shape, px float64) geo.Box {
	size, font := FrameHeading(s)
	size = math.Max(size, FrameHeadingFloor*px)
	w := s.W
	if s.Name != "" && e.Measure != nil {
		w = math.Min(w, e.Measure(s.Name, font, size, 0).W)
	}
	h := size * 1.6
	return e.PageMat(s.ID).BoundsOf(geo.Box{Y: -h, W: w, H: h})
}

func (e *Editor) framePrims(s *doc.Shape, pal *Palette) []Prim {
	box := geo.Box{W: s.W, H: s.H}
	c := box.Corners()
	path := (&geo.Path{}).Poly(c[:], true)
	sw := pal.SwatchOf(s)
	stroke := strokeWith(path, 1, sw.FrameStroke)
	if size, _ := FrameHeading(s); size > 0 {
		stroke = strokeWith(path, strokeOf("s"), sw.Solid)
	}
	stroke.Butt = true
	fill := fillWith(path, sw.FrameFill)
	fill.Rounded = &RoundedBox{Box: box}
	stroke.Rounded = fill.Rounded
	return []Prim{fill, stroke}
}

func (e *Editor) imagePrims(s *doc.Shape, pal *Palette) []Prim {
	box := geo.Box{W: s.W, H: s.H}
	if s.AssetID == "" || e.Doc.Assets[s.AssetID] == nil {
		c := box.Corners()
		return []Prim{fillWith((&geo.Path{}).Poly(c[:], true), pal.Swatch("grey").Semi)}
	}
	return []Prim{{Kind: PrimImage, Asset: s.AssetID, Box: box, FlipX: s.FlipX, FlipY: s.FlipY, Crop: s.Crop}}
}

// unknownPrims draws a shape of a type this app keeps but does not know,
// as a labeled box.
func (e *Editor) unknownPrims(s *doc.Shape, pal *Palette) []Prim {
	g := e.Geometry(s)
	c := g.Bounds.Corners()
	path := (&geo.Path{}).Poly(c[:], true)
	label := e.Measure(s.Type, "sans", 14, 0)
	return []Prim{
		fillWith(path, pal.Swatch("grey").Semi),
		strokeWith(path, 1, pal.Swatch("grey").Solid),
		{Kind: PrimText, Block: &label, X: 8, Y: 8, Size: 14, Font: "sans", Color: pal.Swatch("grey").Solid},
	}
}

// --- arrows ---

func (e *Editor) arrowPrims(s *doc.Shape, pal *Palette) []Prim {
	info := e.Arrow(s)
	sw := pal.SwatchOf(s)
	stroke := strokeWidthOf(s)
	if len(info.Line) < 2 || info.Start.Dist(info.End) < 0.5 {
		return nil
	}
	var lb *geo.Box
	if s.Text != "" {
		lb = e.arrowLabelBox(s)
	}
	// The line stops short of its label, which shows what's behind it, rather than
	// a box that would stand out on anything but the page.
	pieces := [][]geo.Vec{info.Line}
	if lb != nil && !labelAbove(s) {
		pieces = outsideBox(info.Line, lb.Expand(stroke/2))
	}
	var out []Prim
	for i, piece := range pieces {
		body := (&geo.Path{}).Poly(piece, false)
		out = append(out, outlinePrims(s.Dash, body, piece, false, stroke, sw.Solid, fmt.Sprint(s.ID, i))...)
	}
	out = append(out, arrowhead(s.HeadEnd, info.Line, false, stroke, sw.Solid)...)
	out = append(out, arrowhead(s.HeadStart, info.Line, true, stroke, sw.Solid)...)
	if lb != nil {
		size := arrowLabelSizeOf(s.Size) * s.Scale
		pad := 4.25 * s.Scale
		b := alignBlock(inked(e.arrowLabelBlock(s), pal), lb.W-2*pad, "middle")
		out = append(out, Prim{
			Kind: PrimText, Block: &b, X: lb.X + pad, Y: lb.Y + pad, Size: size, Font: s.Font,
			Color: pal.InkOf(s), Label: true,
		})
	}
	return out
}

// outsideBox returns the pieces of a line outside a box.
func outsideBox(line []geo.Vec, box geo.Box) [][]geo.Vec {
	var out [][]geo.Vec
	var cur []geo.Vec
	end := func() {
		if len(cur) > 1 {
			out = append(out, cur)
		}
		cur = nil
	}
	for i := 1; i < len(line); i++ {
		a, b := line[i-1], line[i]
		if cur == nil {
			cur = []geo.Vec{a}
		}
		t0, t1, in := inBox(a, b, box)
		if !in {
			cur = append(cur, b)
			continue
		}
		if t0 > 0 {
			cur = append(cur, a.Lerp(b, t0))
		}
		end()
		if t1 < 1 {
			cur = []geo.Vec{a.Lerp(b, t1), b}
		}
	}
	end()
	return out
}

// inBox returns the part of a segment in a box, as how far along it goes in and
// comes out, and whether any of it is.
func inBox(a, b geo.Vec, box geo.Box) (t0, t1 float64, in bool) {
	d := b.Sub(a)
	t1 = 1
	for _, side := range [4][2]float64{{-d.X, a.X - box.X}, {d.X, box.MaxX() - a.X}, {-d.Y, a.Y - box.Y}, {d.Y, box.MaxY() - a.Y}} {
		p, q := side[0], side[1]
		switch {
		case p == 0:
			if q < 0 {
				return 0, 0, false
			}
		case p < 0:
			t0 = math.Max(t0, q/p)
		default:
			t1 = math.Min(t1, q/p)
		}
	}
	return t0, t1, t0 < t1
}

// arrowhead draws a head at an end of a line.
func arrowhead(kind string, line []geo.Vec, atStart bool, w float64, c Color) []Prim {
	if kind == "none" || kind == "" || len(line) < 2 {
		return nil
	}
	tip, back := line[len(line)-1], line[len(line)-2]
	if atStart {
		tip, back = line[0], line[1]
	}
	// The direction the head points, taken a little back along the line,
	// so that a curve's head follows its body.
	reach := 0.0
	for i := 1; i < len(line) && reach < w*4; i++ {
		var a, b geo.Vec
		if atStart {
			a, b = line[i-1], line[i]
		} else {
			a, b = line[len(line)-i], line[len(line)-1-i]
		}
		reach += a.Dist(b)
		back = b
	}
	total := geo.PolyLength(line)
	u := tip.Sub(back).Uni()
	size := math.Min(w*3+6, math.Max(total/3, w))
	sideAt := func(a float64, d float64) geo.Vec { return tip.Add(u.Rot(math.Pi + a).Mul(d)) }
	switch kind {
	case "triangle":
		a, b := sideAt(0.52, size), sideAt(-0.52, size)
		path := (&geo.Path{}).Poly([]geo.Vec{tip, a, b}, true)
		return []Prim{fillWith(path, c), strokeWith(path, w*0.6, c)}
	case "diamond":
		a, b := sideAt(0.6, size*0.6), sideAt(-0.6, size*0.6)
		far := tip.Sub(u.Mul(size))
		path := (&geo.Path{}).Poly([]geo.Vec{tip, a, far, b}, true)
		return []Prim{fillWith(path, c), strokeWith(path, w*0.6, c)}
	case "dot":
		r := math.Max(w*1.3, size*0.3)
		cen := tip.Sub(u.Mul(r * 0.5))
		return []Prim{fillWith(geo.EllipsePath(geo.Box{X: cen.X - r, Y: cen.Y - r, W: 2 * r, H: 2 * r}), c)}
	case "square":
		r := math.Max(w*1.3, size*0.3)
		cen := tip.Sub(u.Mul(r * 0.5))
		n := u.Per().Mul(r)
		t := u.Mul(r)
		path := (&geo.Path{}).Poly([]geo.Vec{cen.Add(t).Add(n), cen.Add(t).Sub(n), cen.Sub(t).Sub(n), cen.Sub(t).Add(n)}, true)
		return []Prim{fillWith(path, c)}
	case "inverted":
		a, b := tip.Add(u.Per().Mul(size*0.55)).Sub(u.Mul(0)), tip.Sub(u.Per().Mul(size*0.55))
		far := tip.Sub(u.Mul(size))
		path := (&geo.Path{}).Poly([]geo.Vec{far, a, b}, true)
		return []Prim{fillWith(path, c), strokeWith(path, w*0.6, c)}
	case "bar", "pipe":
		n := u.Per().Mul(size * 0.55)
		path := (&geo.Path{}).Move(tip.Add(n)).Line(tip.Sub(n))
		return []Prim{strokeWith(path, w, c)}
	}
	// "arrow": two strokes back from the tip.
	path := (&geo.Path{}).Move(sideAt(0.5, size)).Line(tip).Line(sideAt(-0.5, size))
	return []Prim{strokeWith(path, w, c)}
}
