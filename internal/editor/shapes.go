package editor

import (
	"math"

	"godraw/internal/doc"
	"godraw/internal/geo"
)

// Geometry is what the editor knows of a shape's form, in its own space.
type Geometry struct {
	Bounds geo.Box
	// Outline is the shape's line, flattened, for hit tests and for where
	// arrows end.
	Outline []geo.Vec
	Closed  bool
	// Filled says that a press inside the outline hits the shape.
	Filled bool
	// Label is the box of the shape's label, when it has text.
	Label *geo.Box
}

// shapeCache is what the editor keeps on a shape, which an edit replaces.
type shapeCache struct {
	geom    *Geometry
	display [2][]Prim
	ok      [2]bool
}

func cacheOf(s *doc.Shape) *shapeCache {
	if c, ok := s.Cache.(*shapeCache); ok {
		return c
	}
	c := &shapeCache{}
	s.Cache = c
	return c
}

// Geometry returns a shape's geometry. Groups have the bounds of what they
// hold; arrows change with what they are bound to.
func (e *Editor) Geometry(s *doc.Shape) *Geometry {
	switch s.Type {
	case doc.TypeGroup:
		return e.groupGeometry(s)
	case doc.TypeArrow:
		return e.arrowGeometry(s)
	}
	c := cacheOf(s)
	if c.geom == nil {
		c.geom = e.computeGeometry(s)
	}
	return c.geom
}

func (e *Editor) groupGeometry(s *doc.Shape) *Geometry {
	inv := e.PageMat(s.ID).Invert()
	var box geo.Box
	first := true
	for _, id := range e.Children(s.ID) {
		k := e.Doc.Shapes[id]
		b := inv.Mul(e.PageMat(id)).BoundsOf(e.Geometry(k).Bounds)
		if first {
			box, first = b, false
		} else {
			box = box.Union(b)
		}
	}
	c := box.Corners()
	return &Geometry{Bounds: box, Outline: c[:], Closed: true}
}

func (e *Editor) computeGeometry(s *doc.Shape) *Geometry {
	switch s.Type {
	case doc.TypeGeo:
		w, h := s.W, s.H+s.GrowY
		pts := geoOutline(s.Geo, w, h, string(s.ID))
		g := &Geometry{Bounds: geo.Box{W: w, H: h}, Outline: pts, Closed: true, Filled: s.Fill != "none"}
		if s.Text != "" {
			g.Label = e.labelBox(s, w, h, labelSizeOf(s.Size)*s.Scale)
		}
		return g
	case doc.TypeDraw, doc.TypeHighlight:
		pts := strokePoints(s)
		if len(pts) == 1 {
			r := s.Scale * strokeOf(s.Size)
			pts = []geo.Vec{pts[0], pts[0].Add(geo.V(r, 0))}
		}
		box := geo.BoxOf(pts)
		filled := s.Type == doc.TypeDraw && s.IsClosed && s.Fill != "none"
		return &Geometry{Bounds: box, Outline: geo.Simplify(pts, 0.5), Closed: s.IsClosed, Filled: filled}
	case doc.TypeLine:
		pts, _ := linePath(s).Points(16)
		return &Geometry{Bounds: geo.BoxOf(pts), Outline: pts}
	case doc.TypeText:
		size := fontSizeOf(s.Size) * s.Scale
		text := s.Text
		if text == "" {
			text = " "
		}
		var b TextBlock
		if s.AutoSize {
			b = e.Measure(text, s.Font, size, 0)
		} else {
			b = e.Measure(text, s.Font, size, s.W*s.Scale)
			b.W = s.W * s.Scale
		}
		box := geo.Box{W: math.Max(b.W, 8), H: b.H}
		c := box.Corners()
		return &Geometry{Bounds: box, Outline: c[:], Closed: true, Filled: true, Label: &box}
	case doc.TypeNote:
		w, h := noteSize*s.Scale, noteSize*s.Scale+s.GrowY*s.Scale
		box := geo.Box{W: w, H: h}
		c := box.Corners()
		g := &Geometry{Bounds: box, Outline: c[:], Closed: true, Filled: true}
		g.Label = e.labelBox(s, w, h, labelSizeOf(s.Size)*s.Scale)
		return g
	case doc.TypeFrame:
		box := geo.Box{W: s.W, H: s.H}
		c := box.Corners()
		return &Geometry{Bounds: box, Outline: c[:], Closed: true}
	case doc.TypeImage:
		box := geo.Box{W: s.W, H: s.H}
		c := box.Corners()
		return &Geometry{Bounds: box, Outline: c[:], Closed: true, Filled: true}
	}
	// A type this app does not know, such as an embed: a box.
	w, h := s.W, s.H
	if w == 0 {
		w = 100
	}
	if h == 0 {
		h = 100
	}
	box := geo.Box{W: w, H: h}
	c := box.Corners()
	return &Geometry{Bounds: box, Outline: c[:], Closed: true, Filled: true}
}

// labelBox places a shape's label in a box w×h, by its alignments.
func (e *Editor) labelBox(s *doc.Shape, w, h, size float64) *geo.Box {
	text := s.Text
	if text == "" {
		text = " "
	}
	pad := labelPadding * s.Scale
	b := e.Measure(text, s.Font, size, math.Max(w-2*pad, 8))
	box := geo.Box{W: math.Max(w-2*pad, 8), H: b.H}
	box.X = pad
	switch s.VAlign {
	case "start":
		box.Y = pad
	case "end":
		box.Y = h - pad - b.H
	default:
		box.Y = (h - b.H) / 2
	}
	return &box
}

// strokePoints returns the points of a freehand shape in its space.
func strokePoints(s *doc.Shape) []geo.Vec {
	n := 0
	for _, seg := range s.Segments {
		n += len(seg.Points)
	}
	pts := make([]geo.Vec, 0, n)
	sx, sy := orOne(s.ScaleX), orOne(s.ScaleY)
	for _, seg := range s.Segments {
		for _, p := range seg.Points {
			pts = append(pts, geo.V(p.X*sx, p.Y*sy))
		}
	}
	return pts
}

func orOne(v float64) float64 {
	if v == 0 {
		return 1
	}
	return v
}

// linePath returns the center line of a line shape.
func linePath(s *doc.Shape) *geo.Path {
	pts := make([]geo.Vec, len(s.Points))
	for i, p := range s.Points {
		pts[i] = geo.V(p.X, p.Y)
	}
	p := &geo.Path{}
	if s.Spline == "cubic" && len(pts) > 2 {
		// A Catmull-Rom curve through the vertices.
		p.Move(pts[0])
		for i := 0; i < len(pts)-1; i++ {
			p0, p1, p2, p3 := pts[max(i-1, 0)], pts[i], pts[i+1], pts[min(i+2, len(pts)-1)]
			c1 := p1.Add(p2.Sub(p0).Mul(1.0 / 6))
			c2 := p2.Sub(p3.Sub(p1).Mul(1.0 / 6))
			p.Cube(c1, c2, p2)
		}
		return p
	}
	return p.Poly(pts, false)
}

// geoOutline returns the outline of a geo shape in a w×h box.
func geoOutline(kind string, w, h float64, seed string) []geo.Vec {
	switch kind {
	case "ellipse":
		return ellipsePoints(w, h, 64)
	case "oval":
		pts, _ := ovalPath(w, h).Points(12)
		return pts
	case "triangle":
		return []geo.Vec{{X: w / 2, Y: 0}, {X: w, Y: h}, {X: 0, Y: h}}
	case "diamond":
		return []geo.Vec{{X: w / 2, Y: 0}, {X: w, Y: h / 2}, {X: w / 2, Y: h}, {X: 0, Y: h / 2}}
	case "pentagon":
		return regular(5, w, h, -math.Pi/2)
	case "hexagon":
		return regular(6, w, h, 0)
	case "octagon":
		return regular(8, w, h, math.Pi/8)
	case "star":
		return star(w, h)
	case "rhombus":
		o := math.Min(w*0.38, h*0.38)
		return []geo.Vec{{X: o, Y: 0}, {X: w, Y: 0}, {X: w - o, Y: h}, {X: 0, Y: h}}
	case "rhombus-2":
		o := math.Min(w*0.38, h*0.38)
		return []geo.Vec{{X: 0, Y: 0}, {X: w - o, Y: 0}, {X: w, Y: h}, {X: o, Y: h}}
	case "trapezoid":
		o := math.Min(w*0.38, h*0.38)
		return []geo.Vec{{X: o, Y: 0}, {X: w - o, Y: 0}, {X: w, Y: h}, {X: 0, Y: h}}
	case "arrow-right":
		ox, oy := math.Min(w, h)*0.38, h*0.16
		return []geo.Vec{{X: 0, Y: oy}, {X: w - ox, Y: oy}, {X: w - ox, Y: 0}, {X: w, Y: h / 2}, {X: w - ox, Y: h}, {X: w - ox, Y: h - oy}, {X: 0, Y: h - oy}}
	case "arrow-left":
		ox, oy := math.Min(w, h)*0.38, h*0.16
		return []geo.Vec{{X: ox, Y: 0}, {X: ox, Y: oy}, {X: w, Y: oy}, {X: w, Y: h - oy}, {X: ox, Y: h - oy}, {X: ox, Y: h}, {X: 0, Y: h / 2}}
	case "arrow-up":
		ox, oy := w*0.16, math.Min(w, h)*0.38
		return []geo.Vec{{X: w / 2, Y: 0}, {X: w, Y: oy}, {X: w - ox, Y: oy}, {X: w - ox, Y: h}, {X: ox, Y: h}, {X: ox, Y: oy}, {X: 0, Y: oy}}
	case "arrow-down":
		ox, oy := w*0.16, math.Min(w, h)*0.38
		return []geo.Vec{{X: ox, Y: 0}, {X: w - ox, Y: 0}, {X: w - ox, Y: h - oy}, {X: w, Y: h - oy}, {X: w / 2, Y: h}, {X: 0, Y: h - oy}, {X: ox, Y: h - oy}}
	case "heart":
		pts, _ := heartPath(w, h).Points(16)
		return pts
	case "cloud":
		pts, _ := cloudPath(w, h, seed).Points(10)
		return pts
	}
	return []geo.Vec{{X: 0, Y: 0}, {X: w, Y: 0}, {X: w, Y: h}, {X: 0, Y: h}}
}

func ellipsePoints(w, h float64, n int) []geo.Vec {
	pts := make([]geo.Vec, n)
	for i := range pts {
		s, c := math.Sincos(2 * math.Pi * float64(i) / float64(n))
		pts[i] = geo.V(w/2+w/2*c, h/2+h/2*s)
	}
	return pts
}

// regular returns a regular polygon of n sides stretched over a w×h box.
func regular(n int, w, h, start float64) []geo.Vec {
	pts := make([]geo.Vec, n)
	for i := range pts {
		s, c := math.Sincos(start + 2*math.Pi*float64(i)/float64(n))
		pts[i] = geo.V(c, s)
	}
	b := geo.BoxOf(pts)
	for i, p := range pts {
		pts[i] = geo.V((p.X-b.X)/b.W*w, (p.Y-b.Y)/b.H*h)
	}
	return pts
}

func star(w, h float64) []geo.Vec {
	pts := make([]geo.Vec, 10)
	for i := range pts {
		r := 1.0
		if i%2 == 1 {
			r = 0.42
		}
		s, c := math.Sincos(-math.Pi/2 + math.Pi*float64(i)/5)
		pts[i] = geo.V(c*r, s*r)
	}
	b := geo.BoxOf(pts)
	for i, p := range pts {
		pts[i] = geo.V((p.X-b.X)/b.W*w, (p.Y-b.Y)/b.H*h)
	}
	return pts
}

func ovalPath(w, h float64) *geo.Path {
	p := &geo.Path{}
	const k = 0.5522847498
	if w >= h {
		r := h / 2
		p.Move(geo.V(r, 0)).Line(geo.V(w-r, 0))
		p.Cube(geo.V(w-r+k*r, 0), geo.V(w, r-k*r), geo.V(w, r))
		p.Cube(geo.V(w, r+k*r), geo.V(w-r+k*r, h), geo.V(w-r, h))
		p.Line(geo.V(r, h))
		p.Cube(geo.V(r-k*r, h), geo.V(0, r+k*r), geo.V(0, r))
		p.Cube(geo.V(0, r-k*r), geo.V(r-k*r, 0), geo.V(r, 0))
		return p.Close()
	}
	r := w / 2
	p.Move(geo.V(r, 0))
	p.Cube(geo.V(r+k*r, 0), geo.V(w, r-k*r), geo.V(w, r))
	p.Line(geo.V(w, h-r))
	p.Cube(geo.V(w, h-r+k*r), geo.V(r+k*r, h), geo.V(r, h))
	p.Cube(geo.V(r-k*r, h), geo.V(0, h-r+k*r), geo.V(0, h-r))
	p.Line(geo.V(0, r))
	p.Cube(geo.V(0, r-k*r), geo.V(r-k*r, 0), geo.V(r, 0))
	return p.Close()
}

func heartPath(w, h float64) *geo.Path {
	p := &geo.Path{}
	p.Move(geo.V(w/2, h*0.28))
	p.Cube(geo.V(w*0.42, h*0.08), geo.V(w*0.22, 0), geo.V(w*0.14, h*0.04))
	p.Cube(geo.V(-w*0.02, h*0.12), geo.V(-w*0.04, h*0.42), geo.V(w*0.12, h*0.58))
	p.Cube(geo.V(w*0.26, h*0.74), geo.V(w*0.42, h*0.88), geo.V(w/2, h))
	p.Cube(geo.V(w*0.58, h*0.88), geo.V(w*0.74, h*0.74), geo.V(w*0.88, h*0.58))
	p.Cube(geo.V(w*1.04, h*0.42), geo.V(w*1.02, h*0.12), geo.V(w*0.86, h*0.04))
	p.Cube(geo.V(w*0.78, 0), geo.V(w*0.58, h*0.08), geo.V(w/2, h*0.28))
	return p.Close()
}

// cloudPath returns a closed row of bumps around the edge of an oval, sized
// by the seed, the same each time for a shape.
func cloudPath(w, h float64, seed string) *geo.Path {
	r := geo.NewRng(seed)
	count := int(math.Max(7, math.Min(16, (w+h)/44)))
	pts := make([]geo.Vec, count)
	for i := range pts {
		a := 2*math.Pi*float64(i)/float64(count) + r.Next()*0.1
		s, c := math.Sincos(a)
		pts[i] = geo.V(w/2+w/2*c, h/2+h/2*s)
	}
	p := &geo.Path{}
	center := geo.V(w/2, h/2)
	for i := range pts {
		a, b := pts[i], pts[(i+1)%count]
		if i == 0 {
			p.Move(a)
		}
		m := geo.Med(a, b)
		out := m.Sub(center).Uni()
		bulge := a.Dist(b) * (0.8 + 0.2*r.Next())
		p.Quad(m.Add(out.Mul(bulge)), b)
	}
	p.Close()
	// The bumps reach past the oval: bring the whole back into the box.
	flat, _ := p.Points(10)
	box := geo.BoxOf(flat)
	m := geo.Scale(w/box.W, h/box.H).Mul(geo.Translate(-box.X, -box.Y))
	for i := range p.Segs {
		for k := range p.Segs[i].P {
			p.Segs[i].P[k] = m.Apply(p.Segs[i].P[k])
		}
	}
	return p
}

// PageBounds returns the box around a shape in the page.
func (e *Editor) PageBounds(id doc.ID) geo.Box {
	s := e.Doc.Shapes[id]
	if s == nil {
		return geo.Box{}
	}
	return e.PageMat(id).BoundsOf(e.Geometry(s).Bounds)
}

// PageOutline returns a shape's outline in the page.
func (e *Editor) PageOutline(id doc.ID) []geo.Vec {
	s := e.Doc.Shapes[id]
	if s == nil {
		return nil
	}
	g := e.Geometry(s)
	m := e.PageMat(id)
	out := make([]geo.Vec, len(g.Outline))
	for i, p := range g.Outline {
		out[i] = m.Apply(p)
	}
	return out
}

// hitKind is how a point hits a shape.
type hitKind int

const (
	hitNone hitKind = iota
	hitEdge
	hitInside
)

// hitShape tells whether a point of the page hits a shape, within margin.
func (e *Editor) hitShape(s *doc.Shape, p geo.Vec, margin float64) hitKind {
	if s.Type == doc.TypeGroup {
		return hitNone
	}
	g := e.Geometry(s)
	q := e.PageMat(s.ID).Invert().Apply(p)
	if !g.Bounds.Expand(margin + 16).Contains(q) {
		return hitNone
	}
	if s.Type == doc.TypeHighlight {
		margin += fontSizeOf(s.Size) * s.Scale * 0.5
	}
	if s.Type == doc.TypeDraw {
		margin += strokeOf(s.Size) * s.Scale
	}
	if g.Closed && geo.PointInPolygon(q, g.Outline) {
		if g.Filled || (g.Label != nil && s.Text != "" && g.Label.Contains(q)) {
			return hitInside
		}
		if geo.DistToPolyline(q, g.Outline, g.Closed) <= margin {
			return hitEdge
		}
		// Inside, but hollow: a click selects it, but it stays clear for
		// brushes.
		return hitHollow
	}
	if geo.DistToPolyline(q, g.Outline, g.Closed) <= margin {
		return hitEdge
	}
	if g.Label != nil && g.Label.Contains(q) && s.Text != "" {
		return hitInside
	}
	if s.Type == doc.TypeArrow && s.Text != "" {
		if box := e.arrowLabelBox(s); box != nil && box.Contains(q) {
			return hitInside
		}
	}
	return hitNone
}

// hitHollow is a point inside the outline of a shape without a fill.
const hitHollow hitKind = 100

// HitResult is the shape under a point.
type HitResult struct {
	ID     doc.ID
	Hollow bool
}

// Hit returns the top shape at a point of the page. Shapes inside frames
// show only where the frame does.
func (e *Editor) Hit(p geo.Vec, margin float64, allowLocked bool) HitResult {
	var hollow doc.ID
	var hollowArea float64
	var walk func(parent doc.ID) doc.ID
	walk = func(parent doc.ID) doc.ID {
		kids := e.Children(parent)
		for i := len(kids) - 1; i >= 0; i-- {
			id := kids[i]
			s := e.Doc.Shapes[id]
			if s.Locked && !allowLocked {
				continue
			}
			if s.Type == doc.TypeFrame {
				q := e.PageMat(id).Invert().Apply(p)
				box := geo.Box{W: s.W, H: s.H}
				if box.Contains(q) {
					if k := walk(id); k != "" {
						return k
					}
				}
				if geo.DistToPolyline(q, e.Geometry(s).Outline, true) <= margin || e.hitFrameHeading(s, q) {
					return id
				}
				if box.Contains(q) {
					area := s.W * s.H
					if hollow == "" || area < hollowArea {
						hollow, hollowArea = id, area
					}
				}
				continue
			}
			if s.Type == doc.TypeGroup {
				if k := walk(id); k != "" {
					return k
				}
				continue
			}
			switch e.hitShape(s, p, margin) {
			case hitEdge, hitInside:
				return id
			case hitHollow:
				b := e.Geometry(s).Bounds
				area := b.W * b.H
				if hollow == "" || area < hollowArea {
					hollow, hollowArea = id, area
				}
			}
		}
		return ""
	}
	if id := walk(e.Page); id != "" {
		return HitResult{ID: id}
	}
	if hollow != "" {
		return HitResult{ID: hollow, Hollow: true}
	}
	return HitResult{}
}

func (e *Editor) hitFrameHeading(s *doc.Shape, q geo.Vec) bool {
	z := e.Cam().Z
	h := 26 / z
	return q.Y < 0 && q.Y >= -h && q.X >= 0 && q.X <= math.Min(s.W, 160/z)
}

// frameAt returns the top frame holding a point of the page, other than
// those in skip and their contents.
func (e *Editor) frameAt(p geo.Vec, skip map[doc.ID]bool) doc.ID {
	var walk func(parent doc.ID) doc.ID
	walk = func(parent doc.ID) doc.ID {
		kids := e.Children(parent)
		for i := len(kids) - 1; i >= 0; i-- {
			id := kids[i]
			s := e.Doc.Shapes[id]
			if skip[id] {
				continue
			}
			if s.Type == doc.TypeGroup {
				if k := walk(id); k != "" {
					return k
				}
			}
			if s.Type == doc.TypeFrame {
				q := e.PageMat(id).Invert().Apply(p)
				if (geo.Box{W: s.W, H: s.H}).Contains(q) {
					if k := walk(id); k != "" {
						return k
					}
					return id
				}
			}
		}
		return ""
	}
	return walk(e.Page)
}
