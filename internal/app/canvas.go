package app

import (
	"fmt"
	"math"

	"github.com/egoist/mygo/ui"

	"godraw/internal/doc"
	"godraw/internal/editor"
	"godraw/internal/geo"
)

// scene is one painting of the canvas: the camera it looks through, whether
// to show what only editing shows, and the path it reuses.
type scene struct {
	a      *App
	e      *editor.Editor
	p      *ui.Painter
	pal    *editor.Palette
	cam    editor.Camera
	origin geo.Vec
	view   geo.Box // what the camera shows, in the page
	export bool
	// lightInk draws hand-inked strokes as lines rather than filled masks:
	// see atlasBudget.
	lightInk bool
	only     []doc.ID // what an export draws; all of the page without it
	path     ui.Path
	segment  [2][2]float32
	transp   bool
}

func toUI(c editor.Color) ui.Color {
	return ui.RGBA(c.R, c.G, c.B, float32(c.A))
}

// camMat returns the transform from the page to the painter.
func (s *scene) camMat() geo.Mat {
	return geo.Translate(s.origin.X, s.origin.Y).
		Mul(geo.Scale(s.cam.Z, s.cam.Z)).
		Mul(geo.Translate(s.cam.X, s.cam.Y))
}

// buildPath writes a path into s.path under m.
func (s *scene) buildPath(g *geo.Path, m geo.Mat) *ui.Path {
	p := &s.path
	p.Reset()
	for _, seg := range g.Segs {
		switch seg.Op {
		case geo.OpMove:
			v := m.Apply(seg.P[0])
			p.MoveTo(float32(v.X), float32(v.Y))
		case geo.OpLine:
			v := m.Apply(seg.P[0])
			p.LineTo(float32(v.X), float32(v.Y))
		case geo.OpQuad:
			a, b := m.Apply(seg.P[0]), m.Apply(seg.P[1])
			p.QuadTo(float32(a.X), float32(a.Y), float32(b.X), float32(b.Y))
		case geo.OpCube:
			a, b, c := m.Apply(seg.P[0]), m.Apply(seg.P[1]), m.Apply(seg.P[2])
			p.CubeTo(float32(a.X), float32(a.Y), float32(b.X), float32(b.Y), float32(c.X), float32(c.Y))
		case geo.OpClose:
			p.Close()
		}
	}
	return p
}

// paintCanvas paints the whole canvas into a box of the window.
func (a *App) paintCanvas(p *ui.Painter, r ui.Rect) {
	e := a.ed
	cam := e.Cam()
	s := &scene{a: a, e: e, p: p, pal: e.Palette(), cam: cam, origin: geo.V(float64(r.X), float64(r.Y))}
	s.view = geo.Box{X: -cam.X, Y: -cam.Y, W: float64(r.W) / cam.Z, H: float64(r.H) / cam.Z}
	p.Fill(r, toUI(s.pal.Background), 0)
	if e.Grid {
		s.grid(r)
	}
	s.lightInk = s.maskArea(e.Page) > atlasBudget
	s.children(e.Page)
	s.overlays()
}

// atlasBudget is how many device pixels of masks a frame may ask for. Paths
// rasterize to a mask of their bounding box, in a 4096² atlas that also holds
// the window's icons and text, and a frame that overflows it loses whatever
// paints last. A long diagonal stroke costs its whole box however thin it is,
// so a page dense with strokes gets there quickly; past this budget strokes
// draw as lines, which take no room in it.
const atlasBudget = 6_000_000

// maskArea estimates the device pixels of masks the shapes in view ask for.
func (s *scene) maskArea(parent doc.ID) float64 {
	k := s.cam.Z * float64(s.p.Scale())
	var area float64
	for _, id := range s.e.Children(parent) {
		sh := s.e.Doc.Shapes[id]
		if sh.Type == doc.TypeGroup || sh.Type == doc.TypeFrame {
			area += s.maskArea(id)
			if sh.Type == doc.TypeGroup {
				continue
			}
		}
		b := s.e.PageBounds(id)
		if s.view.Overlaps(b) {
			area += (b.W*k + 8) * (b.H*k + 8)
		}
	}
	return area
}

// grid draws dots at the grid's steps, sparser as the view zooms out.
func (s *scene) grid(r ui.Rect) {
	step := s.e.Doc.GridSize
	if step <= 0 {
		step = 10
	}
	for step*s.cam.Z < 12 {
		step *= 5
	}
	c := toUI(s.pal.Grid)
	major := step * 5
	x0 := math.Floor(s.view.X/step) * step
	y0 := math.Floor(s.view.Y/step) * step
	for y := y0; y <= s.view.MaxY(); y += step {
		sy := (y+s.cam.Y)*s.cam.Z + s.origin.Y
		isY := math.Abs(math.Mod(y, major)) < 1e-6
		for x := x0; x <= s.view.MaxX(); x += step {
			sx := (x+s.cam.X)*s.cam.Z + s.origin.X
			d := float32(1.5)
			if isY && math.Abs(math.Mod(x, major)) < 1e-6 {
				d = 2.5
			}
			s.p.Fill(ui.Rect{X: float32(sx) - d/2, Y: float32(sy) - d/2, W: d, H: d}, c, d/2)
		}
	}
}

// children paints what a page or frame holds: highlights first, under
// everything, as ink laid over paper, then the rest.
func (s *scene) children(parent doc.ID) {
	s.layer(parent, true)
	s.layer(parent, false)
}

func (s *scene) layer(parent doc.ID, highlights bool) {
	for _, id := range s.e.Children(parent) {
		sh := s.e.Doc.Shapes[id]
		switch sh.Type {
		case doc.TypeGroup:
			s.layer(id, highlights)
		case doc.TypeHighlight:
			if highlights {
				s.shape(sh)
			}
		case doc.TypeFrame:
			if !highlights {
				s.frame(sh)
			}
		default:
			if !highlights {
				s.shape(sh)
			}
		}
	}
}

func (s *scene) culled(sh *doc.Shape) bool {
	b := s.e.PageBounds(sh.ID)
	pad := 40.0 / s.cam.Z
	if sh.Type == doc.TypeHighlight || sh.Type == doc.TypeDraw {
		pad += 60
	}
	return !s.view.Expand(pad).Overlaps(b)
}

func (s *scene) opacityOf(sh *doc.Shape) float64 {
	o := sh.Opacity
	if s.e.Erasing[sh.ID] {
		o *= 0.3
	}
	// A group's opacity fades what it holds.
	cur := sh
	for !doc.IsPageID(cur.Parent) {
		p := s.e.Doc.Shapes[cur.Parent]
		if p == nil {
			break
		}
		o *= p.Opacity
		if s.e.Erasing[p.ID] {
			o *= 0.3
		}
		cur = p
	}
	return o
}

func (s *scene) shape(sh *doc.Shape) {
	if s.culled(sh) {
		return
	}
	m := s.camMat().Mul(s.e.PageMat(sh.ID))
	o := s.opacityOf(sh)
	for _, pr := range s.e.Display(sh, s.pal) {
		if pr.Label && sh.ID == s.e.Editing && !s.export {
			continue
		}
		s.prim(pr, m, o, sh)
	}
}

func (s *scene) prim(pr editor.Prim, m geo.Mat, o float64, sh *doc.Shape) {
	switch pr.Kind {
	case editor.PrimFill:
		// Translucent, overlapping pieces of line would show: those stay whole.
		if s.lightInk && pr.Ink != nil && pr.Color.A*o >= 1 {
			s.inkLine(pr, m)
			return
		}
		s.p.FillPath(s.buildPath(pr.Path, m), toUI(pr.Color.Alpha(o)))
	case editor.PrimStroke:
		path := s.buildPath(pr.Path, m)
		if pr.Butt {
			path.Cap(ui.ButtCap).Join(ui.MiterJoin)
		}
		if len(pr.Dash) >= 2 {
			z := s.cam.Z
			path.Dash(0, float32(pr.Dash[0]*z), float32(pr.Dash[1]*z))
		}
		w := float32(pr.Width * s.cam.Z)
		if pr.Butt {
			w = max(w, 1)
		}
		s.p.StrokePath(path, w, toUI(pr.Color.Alpha(o)))
	case editor.PrimText:
		s.text(pr, m, o)
	case editor.PrimImage:
		s.image(pr, m, o)
	}
}

// inkLine draws a hand-inked stroke as a run of short lines, as wide as it is
// at each point, a few pixels each: as much as the eye can tell.
func (s *scene) inkLine(pr editor.Prim, m geo.Mat) {
	const step = 3 // DIPs
	c := toUI(pr.Color)
	z := s.cam.Z
	pts := pr.Ink.Pts
	if len(pts) == 0 {
		return
	}
	last := m.Apply(pts[0])
	seg := s.segment[:]
	for i := 1; i < len(pts); i++ {
		q := m.Apply(pts[i])
		if i < len(pts)-1 && q.Dist(last) < step {
			continue
		}
		seg[0] = [2]float32{float32(last.X), float32(last.Y)}
		seg[1] = [2]float32{float32(q.X), float32(q.Y)}
		s.p.StrokePolyline(seg, max(float32(pr.Ink.W[i]*z), 0.6), c)
		last = q
	}
}

// text draws a block of text. It lays out at its size on the screen, so
// that it is sharp at any zoom, on the line breaks it has in the page, so
// that it breaks the same at any zoom.
func (s *scene) text(pr editor.Prim, m geo.Mat, o float64) {
	if pr.Block == nil {
		return
	}
	z := s.cam.Z
	size := float32(pr.Size * z)
	c := toUI(pr.Color.Alpha(o))
	if size < 3 {
		// Too small to read: a gray line in place of each line of text.
		for _, l := range pr.Block.Lines {
			if l.Text == "" {
				continue
			}
			a := m.Apply(geo.V(pr.X+l.X, pr.Y+l.Y+pr.Block.Line/2))
			b := m.Apply(geo.V(pr.X+l.X+l.W, pr.Y+l.Y+pr.Block.Line/2))
			s.buildPath((&geo.Path{}).Move(a).Line(b), geo.Identity)
			s.p.StrokePath(&s.path, max(size/2, 0.5), c.Alpha(0.5))
		}
		return
	}
	font := family(pr.Font)
	nat := naturalHeight(pr.Font) * size
	drop := (float32(pr.Block.Line*z) - nat) / 2
	rot := m.Rotation()
	draw := func(q geo.Vec, text string) {
		s.p.RichText(float32(q.X), float32(q.Y)+drop, 0, ui.Span{Text: text, Font: font, Size: size, Color: c})
	}
	if math.Abs(rot) < 1e-4 {
		for _, l := range pr.Block.Lines {
			if l.Text != "" {
				draw(m.Apply(geo.V(pr.X+l.X, pr.Y+l.Y)), l.Text)
			}
		}
		return
	}
	// Turned, the block's corner stays where it goes, and the lines lay out
	// from it as if the block were straight.
	origin := m.Apply(geo.V(pr.X, pr.Y))
	deg := float32(rot * 180 / math.Pi)
	s.p.Transform(ui.Rotation(deg), float32(origin.X), float32(origin.Y), func() {
		for _, l := range pr.Block.Lines {
			if l.Text == "" {
				continue
			}
			draw(origin.Add(geo.V(l.X*z, l.Y*z)), l.Text)
		}
	})
}

func (s *scene) image(pr editor.Prim, m geo.Mat, o float64) {
	b := s.a.bitmapOf(pr.Asset)
	z := s.cam.Z
	origin := m.Apply(geo.V(pr.Box.X, pr.Box.Y))
	dst := ui.Rect{X: float32(origin.X), Y: float32(origin.Y), W: float32(pr.Box.W * z), H: float32(pr.Box.H * z)}
	draw := func() {
		if b == nil {
			// Decoding still, or not an image we read.
			s.p.Fill(dst, toUI(s.pal.Swatch("grey").Semi.Alpha(o)), 0)
			return
		}
		var src ui.Rect
		if pr.Crop != nil {
			w, h := b.Size()
			src = ui.Rect{
				X: float32(pr.Crop.TopLeft.X) * float32(w), Y: float32(pr.Crop.TopLeft.Y) * float32(h),
				W: float32(pr.Crop.BottomRight.X-pr.Crop.TopLeft.X) * float32(w),
				H: float32(pr.Crop.BottomRight.Y-pr.Crop.TopLeft.Y) * float32(h),
			}
		}
		opts := ui.ImageOptions{FlipX: pr.FlipX, FlipY: pr.FlipY}
		if o < 1 {
			opts.Tint = ui.RGBA(255, 255, 255, float32(o))
		}
		s.p.ImageRegion(b, src, dst, opts)
	}
	rot := m.Rotation()
	if math.Abs(rot) < 1e-4 {
		draw()
		return
	}
	s.p.Transform(ui.Rotation(float32(rot*180/math.Pi)), dst.X, dst.Y, draw)
}

// frame paints a frame, with what it holds clipped to it.
func (s *scene) frame(sh *doc.Shape) {
	if s.culled(sh) {
		return
	}
	pm := s.e.PageMat(sh.ID)
	m := s.camMat().Mul(pm)
	o := s.opacityOf(sh)
	for _, pr := range s.e.Display(sh, s.pal) {
		s.prim(pr, m, o, sh)
	}
	origin := m.Apply(geo.Vec{})
	z := s.cam.Z
	clip := ui.Rect{X: float32(origin.X), Y: float32(origin.Y), W: float32(sh.W * z), H: float32(sh.H * z)}
	rot := m.Rotation()
	inside := func() { s.children(sh.ID) }
	if math.Abs(rot) < 1e-4 {
		s.p.Clip(clip, 0, inside)
	} else {
		deg := float32(rot * 180 / math.Pi)
		// The clip turns with the frame; the transform inside it undoes the
		// turn, as what the frame holds has it in its own transform.
		s.p.Transform(ui.Rotation(deg), clip.X, clip.Y, func() {
			s.p.Clip(clip, 0, func() {
				s.p.Transform(ui.Rotation(-deg), clip.X, clip.Y, inside)
			})
		})
	}
	s.frameHeading(sh, origin, rot)
}

func (s *scene) frameHeading(sh *doc.Shape, origin geo.Vec, rot float64) {
	if sh.ID == s.e.Editing && !s.export {
		return
	}
	name := sh.Name
	if name == "" {
		name = "Frame"
	}
	const size = 12
	span := ui.Span{Text: name, Size: size, Color: toUI(s.pal.Text.Alpha(0.65))}
	draw := func() {
		s.p.RichText(float32(origin.X), float32(origin.Y)-size*1.6, float32(max(sh.W*s.cam.Z, 40)), span)
	}
	if math.Abs(rot) < 1e-4 {
		draw()
		return
	}
	s.p.Transform(ui.Rotation(float32(rot*180/math.Pi)), float32(origin.X), float32(origin.Y), draw)
}

// --- what editing shows ---

func (s *scene) outline(id doc.ID, width float32, c ui.Color) {
	sh := s.e.Doc.Shapes[id]
	if sh == nil {
		return
	}
	g := s.e.Geometry(sh)
	m := s.camMat().Mul(s.e.PageMat(id))
	path := (&geo.Path{}).Poly(g.Outline, g.Closed)
	s.buildPath(path, m)
	s.p.StrokePath(&s.path, width, c)
}

func (s *scene) overlays() {
	e := s.e
	sel := toUI(s.pal.Selection)
	// The shape under the pointer.
	if e.Hovered != "" && !e.IsSelected(e.Hovered) && !e.InGesture() {
		s.hoverOutline(e.Hovered, sel)
	}
	// What an arrow is about to bind to, or a frame about to take shapes.
	for id := range e.Hint {
		s.outline(id, 2.5, sel)
	}
	if e.DropOn != "" {
		s.outline(e.DropOn, 2.5, sel)
	}
	s.selection(sel)
	s.trails()
	if e.Brush != nil {
		b := *e.Brush
		tl := e.ToScreen(geo.V(b.X, b.Y))
		r := ui.Rect{X: float32(tl.X + s.origin.X), Y: float32(tl.Y + s.origin.Y), W: float32(b.W * s.cam.Z), H: float32(b.H * s.cam.Z)}
		s.p.Fill(r, toUI(s.pal.BrushFill), 0)
		s.p.Stroke(r, toUI(s.pal.BrushLine), 0, 1)
	}
	for _, l := range e.Snaps {
		a, b := e.ToScreen(l.A), e.ToScreen(l.B)
		s.buildPath((&geo.Path{}).Move(a.Add(s.origin)).Line(b.Add(s.origin)), geo.Identity)
		s.p.StrokePath(&s.path, 1, toUI(s.pal.Snap))
	}
}

func (s *scene) hoverOutline(id doc.ID, c ui.Color) {
	sh := s.e.Doc.Shapes[id]
	if sh == nil {
		return
	}
	if sh.Type == doc.TypeGroup {
		for _, k := range s.e.Children(id) {
			s.hoverOutline(k, c)
		}
		return
	}
	s.outline(id, 1.5, c)
}

func (s *scene) selection(c ui.Color) {
	e := s.e
	ids := e.Selection()
	if len(ids) == 0 || e.Editing != "" {
		if e.Editing != "" {
			s.outline(e.Editing, 1.5, c.Alpha(0.5))
		}
		return
	}
	gesturing := e.InGesture()
	// Each shape of a multiple selection shows its own outline, while it
	// is not being moved.
	if len(ids) > 1 && !gesturing {
		for _, id := range ids {
			if sh := e.Doc.Shapes[id]; sh != nil && !s.culled(sh) {
				s.outlineBox(id, c)
			}
		}
	}
	if !e.ShowsBox() {
		if len(ids) == 1 {
			s.outline(ids[0], 1.5, c)
		}
		s.linearHandles(c)
		return
	}
	corners := e.SelectionCorners()
	screen := make([]geo.Vec, 4)
	for i, p := range corners {
		screen[i] = e.ToScreen(p).Add(s.origin)
	}
	s.buildPath((&geo.Path{}).Poly(screen, true), geo.Identity)
	s.p.StrokePath(s.path.Cap(ui.ButtCap).Join(ui.MiterJoin), 1.5, c)
	if gesturing && !isResizing(e) {
		return
	}
	if len(e.Handles()) == 0 {
		return // locked: no handles
	}
	white := toUI(s.pal.Background)
	rot := e.PageMat(ids[0]).Rotation()
	if _, r := e.SelectionBox(); true {
		rot = r
	}
	for _, p := range screen {
		s.square(p, rot, white, c)
	}
	if !gesturing && (s.a.showSizeBadge()) {
		s.sizeBadge(screen, c)
	}
}

func isResizing(e *editor.Editor) bool {
	return e.CursorNow() >= editor.CursorEW && e.CursorNow() <= editor.CursorNWSE
}

// square draws a handle, turned with the selection.
func (s *scene) square(p geo.Vec, rot float64, fill, line ui.Color) {
	const d = 5.0
	c := [4]geo.Vec{{X: -d, Y: -d}, {X: d, Y: -d}, {X: d, Y: d}, {X: -d, Y: d}}
	m := geo.Translate(p.X, p.Y).Mul(geo.Rotate(rot))
	pts := make([]geo.Vec, 4)
	for i := range c {
		pts[i] = m.Apply(c[i])
	}
	path := (&geo.Path{}).Poly(pts, true)
	s.buildPath(path, geo.Identity)
	s.p.FillPath(&s.path, fill)
	s.buildPath(path, geo.Identity)
	s.p.StrokePath(s.path.Join(ui.MiterJoin), 1.5, line)
}

func (s *scene) outlineBox(id doc.ID, c ui.Color) {
	sh := s.e.Doc.Shapes[id]
	if sh == nil {
		return
	}
	if sh.Type == doc.TypeArrow || sh.Type == doc.TypeLine || sh.Type == doc.TypeDraw || sh.Type == doc.TypeHighlight {
		s.outline(id, 1, c.Alpha(0.6))
		return
	}
	g := s.e.Geometry(sh)
	cs := g.Bounds.Corners()
	m := s.camMat().Mul(s.e.PageMat(id))
	s.buildPath((&geo.Path{}).Poly(cs[:], true), m)
	s.p.StrokePath(&s.path, 1, c.Alpha(0.6))
}

func (s *scene) linearHandles(c ui.Color) {
	fill := toUI(s.pal.Background)
	for _, h := range s.e.Handles() {
		p := s.e.ToScreen(h.Point).Add(s.origin)
		r := float32(5)
		alpha := float32(1)
		if h.Virtual || h.Kind == editor.HandleArrowBend {
			r, alpha = 4, 0.7
		}
		s.p.FillPath((&ui.Path{}).Circle(float32(p.X), float32(p.Y), r), fill.Alpha(alpha))
		s.p.StrokePath((&ui.Path{}).Circle(float32(p.X), float32(p.Y), r), 1.5, c.Alpha(alpha))
	}
}

func (s *scene) sizeBadge(corners []geo.Vec, c ui.Color) {
	box, _ := s.e.SelectionBox()
	label := fmt.Sprintf("%.0f × %.0f", box.W, box.H)
	bottom := corners[0]
	for _, p := range corners {
		if p.Y > bottom.Y {
			bottom = p
		}
	}
	mid := geo.Med(corners[2], corners[3])
	if mid.Y < bottom.Y {
		mid.Y = bottom.Y
	}
	span := ui.Span{Text: label, Size: 11, Color: ui.RGBA(255, 255, 255, 1)}
	w, h := s.p.MeasureText(0, span)
	r := ui.Rect{X: float32(mid.X) - w/2 - 5, Y: float32(mid.Y) + 10, W: w + 10, H: h + 4}
	s.p.Fill(r, c, 4)
	s.p.RichText(r.X+5, r.Y+2, 0, span)
}

func (s *scene) trails() {
	for _, t := range s.e.Trails {
		if len(t.Points) < 2 {
			continue
		}
		life := editor.TrailLife
		col := s.pal.Laser
		width := 4.0
		if !t.Laser {
			life, col, width = 0.25, s.pal.Swatch("grey").Solid, 3
		}
		n := len(t.Points)
		for i := 1; i < n; i++ {
			a, b := t.Points[i-1], t.Points[i]
			age := (s.e.Now - b.T) / life
			if age >= 1 {
				continue
			}
			pa, pb := s.e.ToScreen(a.P).Add(s.origin), s.e.ToScreen(b.P).Add(s.origin)
			w := width * (1 - age*0.6)
			s.buildPath((&geo.Path{}).Move(pa).Line(pb), geo.Identity)
			s.p.StrokePath(&s.path, float32(w), toUI(col.Alpha(1-age)))
		}
	}
}
