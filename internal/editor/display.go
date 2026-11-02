package editor

import (
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
	switch s.Type {
	case doc.TypeGeo:
		return e.geoPrims(s, pal)
	case doc.TypeDraw:
		return e.drawPrims(s, pal)
	case doc.TypeHighlight:
		return e.highlightPrims(s, pal)
	case doc.TypeLine:
		return e.linePrims(s, pal)
	case doc.TypeText:
		return e.textPrims(s, pal)
	case doc.TypeNote:
		return e.notePrims(s, pal)
	case doc.TypeFrame:
		return e.framePrims(s, pal)
	case doc.TypeImage:
		return e.imagePrims(s, pal)
	case doc.TypeGroup:
		return nil
	}
	return e.unknownPrims(s, pal)
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

func fillPrims(fill string, base *geo.Path, poly []geo.Vec, sw Swatch, pal *Palette) []Prim {
	switch fill {
	case "semi":
		return []Prim{fillWith(base, pal.Solid)}
	case "solid":
		return []Prim{fillWith(base, sw.Semi)}
	case "fill":
		return []Prim{fillWith(base, sw.Fill)}
	case "pattern":
		return []Prim{
			fillWith(base, sw.Semi),
			{Kind: PrimStroke, Path: hatch(poly, 8), Width: 1.6, Color: sw.Pattern},
		}
	case "lined-fill":
		return []Prim{{Kind: PrimStroke, Path: hatch(poly, 8), Width: 1.6, Color: sw.Lined}}
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
	if s.Dash == "draw" {
		return geo.RoundedPoly(poly, math.Min(strokeOf(s.Size)*s.Scale*2, 10))
	}
	return (&geo.Path{}).Poly(poly, true)
}

func (e *Editor) geoPrims(s *doc.Shape, pal *Palette) []Prim {
	w, h := s.W, s.H+s.GrowY
	g := e.Geometry(s)
	sw := pal.Swatch(s.Color)
	stroke := strokeOf(s.Size) * s.Scale
	base := geoBasePath(s, w, h, g.Outline)
	flat, _ := base.Points(10)
	out := fillPrims(s.Fill, base, flat, sw, pal)
	out = append(out, outlinePrims(s.Dash, base, flat, true, stroke, sw.Solid, string(s.ID))...)
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
		out = append(out, e.labelPrim(s, g.Label, labelSizeOf(s.Size)*s.Scale, pal.Swatch(s.LabelColor).Solid))
	}
	return out
}

// labelPrim draws a shape's label in its box.
func (e *Editor) labelPrim(s *doc.Shape, box *geo.Box, size float64, c Color) Prim {
	b := e.Measure(s.Text, s.Font, size, box.W)
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
	sw := pal.Swatch(s.Color)
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
		out = append(out, fillPrims(s.Fill, smooth, pts, sw, pal)...)
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
	c := pal.Swatch(s.Color).Highlight.Alpha(0.75)
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
	return outlinePrims(s.Dash, base, pts, false, strokeOf(s.Size)*s.Scale, pal.Swatch(s.Color).Solid, string(s.ID))
}

// --- text, note, frame, image ---

func (e *Editor) textPrims(s *doc.Shape, pal *Palette) []Prim {
	size := fontSizeOf(s.Size) * s.Scale
	g := e.Geometry(s)
	var b TextBlock
	if s.AutoSize {
		b = e.Measure(s.Text, s.Font, size, 0)
	} else {
		b = e.Measure(s.Text, s.Font, size, g.Bounds.W)
	}
	b = alignBlock(b, g.Bounds.W, s.TextAlign)
	return []Prim{{Kind: PrimText, Block: &b, Size: size, Font: s.Font, Color: pal.Swatch(s.Color).Solid, Label: true}}
}

func (e *Editor) notePrims(s *doc.Shape, pal *Palette) []Prim {
	g := e.Geometry(s)
	sw := pal.Swatch(s.Color)
	box := g.Bounds
	var out []Prim
	// A soft shadow, built of paths so that it turns with the shape.
	if !pal.Dark {
		for i := range 6 {
			d := float64(6-i) * s.Scale
			sb := geo.Box{X: box.X + 3*s.Scale - d/2, Y: box.Y + d*0.9, W: box.W - 6*s.Scale + d, H: box.H}
			c := sb.Corners()
			out = append(out, fillWith(geo.RoundedPoly(c[:], 6*s.Scale), Color{A: 0.022}))
		}
	}
	c := box.Corners()
	out = append(out, fillWith(geo.RoundedPoly(c[:], 6*s.Scale), sw.NoteFill))
	if s.Text != "" {
		textColor := sw.NoteText
		if s.LabelColor != "black" {
			textColor = pal.Swatch(s.LabelColor).Solid
		}
		out = append(out, e.labelPrim(s, g.Label, labelSizeOf(s.Size)*s.Scale, textColor))
	}
	return out
}

func (e *Editor) framePrims(s *doc.Shape, pal *Palette) []Prim {
	box := geo.Box{W: s.W, H: s.H}
	c := box.Corners()
	path := (&geo.Path{}).Poly(c[:], true)
	sw := pal.Swatch(s.Color)
	stroke := strokeWith(path, 1, sw.FrameStroke)
	stroke.Butt = true
	return []Prim{fillWith(path, sw.FrameFill), stroke}
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
	sw := pal.Swatch(s.Color)
	stroke := strokeWidthOf(s)
	if len(info.Line) < 2 || info.Start.Dist(info.End) < 0.5 {
		return nil
	}
	body := &geo.Path{}
	if info.Arc {
		body.Poly(info.Line, false)
	} else {
		body.Poly(info.Line, false)
	}
	var out []Prim
	out = append(out, outlinePrims(s.Dash, body, info.Line, false, stroke, sw.Solid, string(s.ID))...)
	out = append(out, arrowhead(s.HeadEnd, info.Line, false, stroke, sw.Solid)...)
	out = append(out, arrowhead(s.HeadStart, info.Line, true, stroke, sw.Solid)...)
	if s.Text != "" {
		if lb := e.arrowLabelBox(s); lb != nil {
			c := lb.Corners()
			out = append(out, fillWith(geo.RoundedPoly(c[:], 4), pal.Background))
			size := arrowLabelSizeOf(s.Size) * s.Scale
			b := e.Measure(s.Text, s.Font, size, 0)
			if b.W > 280*s.Scale {
				b = e.Measure(s.Text, s.Font, size, 280*s.Scale)
			}
			pad := 4.25 * s.Scale
			b = alignBlock(b, lb.W-2*pad, "middle")
			out = append(out, Prim{
				Kind: PrimText, Block: &b, X: lb.X + pad, Y: lb.Y + pad, Size: size, Font: s.Font,
				Color: pal.Swatch(s.LabelColor).Solid, Label: true,
			})
		}
	}
	return out
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
