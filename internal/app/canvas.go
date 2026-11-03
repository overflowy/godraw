package app

import (
	"fmt"
	"math"
	"time"

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
	// shrink is the scale of a picture of the page that comes out smaller than
	// the page: see pixel.
	shrink float64
	// lightInk draws opaque strokes as lines rather than masks: see atlasBudget.
	lightInk bool
	// small says that the shape being painted is well within one mask, for those
	// of a page's many that are not to be measured path by path.
	small bool
	// turned counts the turned frames being painted: see spansLarge.
	turned int
	// box is the canvas's box in the window.
	box ui.Rect
	// zoom is the frame of a zoom going on that this scene paints.
	zoom *zoomPass
	// moving says that this scene paints at a zoom other than the one that shows, in
	// a zoom going on, and scale what takes that to the screen.
	moving bool
	scale  ui.Transform
	// current is the shape that paintShape and paintHeading paint, which are made
	// once for the frame rather than for each shape, and only which of its prims.
	current                  *doc.Shape
	paintShape, paintHeading func()
	prims                    primFilter
	noHeading                bool
	// shown is how many pixels of the screen a unit painted comes to.
	shown   float64
	only    []doc.ID // what an export draws; all of the page without it
	path    ui.Path
	segment [2][2]float32
	span    [1]ui.Span
	transp  bool
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

// buildPath writes a path into s.path under m, as finely as it shows.
func (s *scene) buildPath(g *geo.Path, m geo.Mat) *ui.Path {
	p := &s.path
	p.Reset()
	if lines := s.detailOf(g, m); lines != nil {
		for _, l := range lines {
			v := m.Apply(l.pts[0])
			p.MoveTo(float32(v.X), float32(v.Y))
			for _, q := range l.pts[1:] {
				v = m.Apply(q)
				p.LineTo(float32(v.X), float32(v.Y))
			}
			if l.closed {
				p.Close()
			}
		}
		return p
	}
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

// A hand-drawn outline has points every few units of the page, which is what it
// needs close up and far more than it needs from far away, where the frame has to
// go through all of them only to find the mask it already has. So a path of many
// parts paints as a line as fine as the pixels can tell, kept from frame to frame.
const detailTolerance = 0.2 // how far a line may stray from the path, in device pixels

// detailKey is a path, at how fine a line.
type detailKey struct {
	path *geo.Path
	tol  float64
}

// detail is a path as a line in its own space.
type detail struct {
	lines []deviceLine
	used  int
}

// detailOf returns what to paint of a path under m, nil for all of it. In a picture
// it's all of it, as the picture may be seen closer than it's made.
func (s *scene) detailOf(g *geo.Path, m geo.Mat) []deviceLine {
	if s.export || len(g.Segs) < detailSegs {
		return nil
	}
	// Each half an octave has one line, so that near zooms share it, and it's the
	// finer one.
	tol := detailTolerance / (s.cam.Z * float64(s.p.Scale()))
	tol = math.Pow(2, math.Floor(math.Log2(tol)*2)/2)
	key := detailKey{g, tol}
	d := s.a.details[key]
	if d == nil {
		if s.a.details == nil {
			s.a.details = map[detailKey]*detail{}
		}
		d = &detail{lines: flatten(g, geo.Identity, curveTolerance/tol)}
		for i := range d.lines {
			l := &d.lines[i]
			for j := range l.pts {
				l.pts[j] = l.pts[j].Mul(tol / curveTolerance)
			}
			l.pts = geo.Simplify(l.pts, tol)
		}
		s.a.details[key] = d
	}
	d.used = s.a.cellsFrame
	return d.lines
}

// paintCanvas paints the whole canvas into a box of the window.
func (a *App) paintCanvas(p *ui.Painter, r ui.Rect) {
	e := a.ed
	cam := e.Cam()
	k := float64(p.Scale())
	s := &scene{a: a, e: e, p: p, pal: e.Palette(), cam: cam, origin: snappedOrigin(r, cam, k), box: r}
	s.view = geo.Box{X: -cam.X, Y: -cam.Y, W: float64(r.W) / cam.Z, H: float64(r.H) / cam.Z}
	a.pruneCells()
	p.Fill(r, toUI(s.pal.Background), 0)
	if e.Grid {
		s.grid(r)
	}
	if a.zoomedAt.IsZero() {
		s.lightInk = s.maskArea(e.Children(e.Page), k, false) > atlasBudget
		s.children(e.Page)
		a.rest = restView{z: cam.Z, view: s.view.Expand(40 / cam.Z)}
	} else {
		a.paintZooming(s)
	}
	s.overlays()
}

// A zoom asks for all of a page's paths and text at a new size each frame, and
// masks and fonts of a size the next frame won't ask for again are all it gets for
// that work. So while a zoom goes on, the page's masks stay at one of a few levels
// of zoom, which the GPU scales to the zoom it's at, and once it stops they come to
// the zoom it stopped at, sharp. Nor does a shape go to a new level with the frame
// that wants it: each keeps the masks it has until a frame has the time for it, so
// that no frame pays for all of the page.
const (
	// How many levels of zoom there are to a doubling: the most that a level is
	// sharper than what it shows.
	zoomLevelsPerDoubling = 4
	// How long after the last of a zoom's events it counts as stopped.
	zoomSettle = 140 * time.Millisecond
	// How long a frame may spend on bringing shapes to a level, while a zoom goes
	// on and once it stopped.
	zoomBudget, settleBudget = 3 * time.Millisecond, 6 * time.Millisecond
	// How many device pixels a side of a shape's mask may take while a zoom goes on.
	// A larger one's fills stay soft until it stops, under lines that stay sharp:
	// whole masks of large shapes are megabytes each.
	zoomMaskSide = 512.0
)

// restView is what the canvas showed last with no zoom going on, for which the
// shapes in it have masks to start a zoom from.
type restView struct {
	z    float64
	view geo.Box
}

// primFilter is which of a shape's prims a scene paints.
type primFilter uint8

const (
	allOfShape primFilter = iota
	onlyInk
	onlyText
)

// zoomState is a zoom going on.
type zoomState struct {
	level float64            // the level that shapes are going to
	light bool               // whether that many masks take too much room: see atlasBudget
	at    map[doc.ID]float64 // the zoom that a shape's masks are at, of those it changed
}

// zoomPass is a frame of a zoom going on.
type zoomPass struct {
	base    *scene
	start   time.Time
	budget  time.Duration
	scenes  []*scene // one for each zoom that masks are at
	behind  int      // shapes that aren't at the level yet
	changed int      // shapes that came to the level in this frame
}

// ZoomGesture zooms around a point of the canvas, as part of a zoom going on at a
// time, as a pinch.
func (a *App) ZoomGesture(at geo.Vec, factor float64, t time.Time) {
	a.zoomedAt = t
	a.ed.ZoomAt(at, factor)
}

// paintZooming paints the page in a zoom going on, and asks for the frames to come.
func (a *App) paintZooming(s *scene) {
	cam, p := s.cam, s.p
	left := zoomSettle - p.Now().Sub(a.zoomedAt)
	level, budget := zoomLevel(a.zoom.level, cam.Z), zoomBudget
	if left <= 0 {
		// It stopped: to the zoom it's at, sharp.
		level, budget = cam.Z, settleBudget
	}
	pass := &zoomPass{base: s, start: time.Now(), budget: budget}
	s.zoom = pass
	if a.zoom.at == nil {
		a.zoom.at = map[doc.ID]float64{}
	}
	if level != a.zoom.level {
		a.zoom.level = level
		a.zoom.light = pass.scene(level).maskArea(s.e.Children(s.e.Page), float64(p.Scale()), level != cam.Z) > atlasBudget
	}
	s.children(s.e.Page)
	if left > 0 {
		p.After(left)
	}
	switch {
	case pass.behind > 0 || pass.changed > 0:
		// A mask lasts for the first frame it shows in, and is kept from its second:
		// one frame more, for the next thing to move not to pay for them.
		p.AnimationFrame()
	case left <= 0:
		a.zoomedAt, a.zoom = time.Time{}, zoomState{}
		a.rest = restView{z: cam.Z, view: s.view.Expand(40 / cam.Z)}
	}
}

// zoomLevel returns the level for a zoom, which stays the one it was while near:
// one at or above the zoom, so that masks scale down rather than up.
func zoomLevel(was, z float64) float64 {
	step := math.Pow(2, 1.0/zoomLevelsPerDoubling)
	if was != 0 && z <= was*1.02 && z >= was/(step*1.02) {
		return was
	}
	return math.Pow(2, math.Ceil(math.Log2(z)*zoomLevelsPerDoubling)/zoomLevelsPerDoubling)
}

// scene returns the scene that paints with masks at a zoom.
func (pass *zoomPass) scene(z float64) *scene {
	for _, l := range pass.scenes {
		if l.cam.Z == z {
			return l
		}
	}
	s := pass.base
	l := &scene{a: s.a, e: s.e, p: s.p, pal: s.pal, view: s.view, box: s.box}
	if z == s.cam.Z {
		l.cam, l.origin = s.cam, s.origin
	} else {
		// On the same whole pixels as snappedOrigin has them, so that the masks of a
		// zoom the view rested at are good for it. A clip under the scale that takes
		// it to the screen would have no whole pixels to fall on, and a mask of its
		// own: turned has it paint without cells, and moving as lines what used them.
		k := float64(s.p.Scale())
		snap := func(v float64) float64 { return math.Round(v*z*k) / (z * k) }
		l.cam = editor.Camera{X: snap(s.cam.X), Y: snap(s.cam.Y), Z: z}
		l.origin = geo.V(float64(s.box.X), float64(s.box.Y))
		l.moving, l.turned = true, 1
		l.shown = s.cam.Z / z
		f := float32(l.shown)
		l.scale = ui.Scaling(f, f).Then(ui.Translation(float32((s.cam.X-l.cam.X)*s.cam.Z), float32((s.cam.Y-l.cam.Y)*s.cam.Z)))
	}
	l.paintShape = func() { l.shapeNow(l.current) }
	l.paintHeading = func() { l.heading(l.current) }
	pass.scenes = append(pass.scenes, l)
	return l
}

// sceneOf returns the scene to paint a shape with: at the level if there's time to
// bring it there, otherwise at the zoom its masks are at. A shape too large for its
// masks to go to the level has its text, whose glyphs are small, painted with another
// scene that is, which is nil otherwise.
func (pass *zoomPass) sceneOf(sh *doc.Shape) (l, text *scene) {
	s := pass.base
	z := s.a.zoom
	b := s.e.PageBounds(sh.ID)
	target := z.level
	if z.level != s.cam.Z {
		target = min(target, sideLevel(max(b.W, b.H), float64(s.p.Scale())))
	}
	at, ok := z.at[sh.ID]
	if !ok {
		at = target
		// What showed before the zoom has masks from then, and anything else none.
		if rest := s.a.rest; rest.z != 0 && rest.z <= target && rest.view.Overlaps(b) {
			at = rest.z
		}
	}
	if at != target {
		if time.Since(pass.start) < pass.budget {
			at = target
			pass.changed++
		} else {
			pass.behind++
		}
	}
	if !ok || at == target {
		z.at[sh.ID] = at
	}
	l = pass.scene(at)
	l.lightInk = z.light
	if target != z.level {
		text = pass.scene(z.level)
	}
	return l, text
}

// sideLevel returns the highest level at which something this long in the page is
// within a mask's side while a zoom goes on.
func sideLevel(side, k float64) float64 {
	if side <= 0 {
		return math.Inf(1)
	}
	return math.Pow(2, math.Floor(math.Log2(zoomMaskSide/(side*k))*zoomLevelsPerDoubling)/zoomLevelsPerDoubling)
}

// paintAt paints a shape with a scene, through what takes that scene to the screen.
func (l *scene) paintAt(sh *doc.Shape, paint func()) {
	l.current = sh
	if l.moving {
		l.p.Transform(l.scale, l.box.X, l.box.Y, paint)
		return
	}
	paint()
}

// snappedOrigin returns where the canvas's corner paints, within half a device
// pixel of where it is, so that the page moves by whole pixels. A shape then falls
// the same way on the pixels wherever the view pans: its edges don't shimmer as it
// moves, and the masks it was painted from last are good for this frame too.
func snappedOrigin(r ui.Rect, cam editor.Camera, k float64) geo.Vec {
	at := func(corner, camera float64) float64 {
		return corner + (math.Round(camera*cam.Z*k)/k - camera*cam.Z)
	}
	return geo.V(at(float64(r.X), cam.X), at(float64(r.Y), cam.Y))
}

// atlasBudget is how many device pixels of masks a frame may ask for: a frame
// that overflows the atlas loses whatever paints last. Past it the window's
// strokes draw as lines, which take no room in it, and a picture of the page
// paints in smaller tiles.
const atlasBudget = 6_000_000

// maskArea estimates the device pixels of masks the shapes in view ask for,
// stopping once it's past the budget. k is device pixels per unit painted.
func (s *scene) maskArea(ids []doc.ID, k float64, turned bool) float64 {
	var area float64
	for _, id := range ids {
		if area > atlasBudget {
			break
		}
		sh := s.e.Doc.Shapes[id]
		switch sh.Type {
		case doc.TypeGroup:
			area += s.maskArea(s.e.Children(id), k, turned)
			continue
		case doc.TypeFrame:
			inner := turned || math.Abs(s.e.PageMat(id).Rotation()) >= 1e-4
			area += s.maskArea(s.e.Children(id), k, inner)
		}
		if s.culled(sh) {
			continue
		}
		if b := s.e.PageBounds(id); s.isSmall(b, k) {
			area += (b.W*s.cam.Z*k + 8) * (b.H*s.cam.Z*k + 8)
			continue
		}
		m := s.camMat().Mul(s.e.PageMat(id))
		for _, pr := range s.e.Display(sh, s.pal) {
			area += s.primMaskArea(pr, m, k, turned)
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
func (s *scene) children(parent doc.ID) { s.paint(s.e.Children(parent)) }

func (s *scene) paint(ids []doc.ID) {
	s.layer(ids, true)
	s.layer(ids, false)
}

func (s *scene) layer(ids []doc.ID, highlights bool) {
	for _, id := range ids {
		sh := s.e.Doc.Shapes[id]
		switch sh.Type {
		case doc.TypeGroup:
			s.layer(s.e.Children(id), highlights)
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
	if s.zoom != nil {
		l, text := s.zoom.sceneOf(sh)
		if text == nil {
			l.paintAt(sh, l.paintShape)
			return
		}
		l.prims, text.prims = onlyInk, onlyText
		l.paintAt(sh, l.paintShape)
		text.paintAt(sh, text.paintShape)
		l.prims, text.prims = allOfShape, allOfShape
		return
	}
	s.shapeNow(sh)
}

func (s *scene) shapeNow(sh *doc.Shape) {
	m := s.camMat().Mul(s.e.PageMat(sh.ID))
	o := s.opacityOf(sh)
	s.small = s.isSmall(s.e.PageBounds(sh.ID), float64(s.p.Scale()))
	for _, pr := range s.e.Display(sh, s.pal) {
		if pr.Label && sh.ID == s.e.Editing && !s.export {
			continue
		}
		if s.prims != allOfShape && (s.prims == onlyText) != (pr.Kind == editor.PrimText) {
			continue
		}
		s.prim(pr, m, o, sh)
	}
	s.small = false
}

// isSmall says whether a box in the page is well within one mask, whatever its
// stroke or ink adds. k is device pixels per unit painted.
func (s *scene) isSmall(b geo.Box, k float64) bool {
	return b.W*s.cam.Z*k <= wholeMask/2 && b.H*s.cam.Z*k <= wholeMask/2
}

func (s *scene) prim(pr editor.Prim, m geo.Mat, o float64, sh *doc.Shape) {
	switch pr.Kind {
	case editor.PrimFill:
		if s.asBox(pr, m) {
			s.fillBox(pr, m, toUI(pr.Color.Alpha(o)))
			return
		}
		// Translucent, overlapping pieces of line would show: those stay whole.
		if pr.Ink != nil && pr.Color.A*o >= 1 && (s.lightInk || s.movingLarge(pr, m)) {
			s.inkLine(pr, m)
			return
		}
		s.fill(pr.Path, m, toUI(pr.Color.Alpha(o)))
	case editor.PrimStroke:
		if s.asBox(pr, m) {
			s.strokeBox(pr, m, toUI(pr.Color.Alpha(o)))
			return
		}
		if pr.Hatched != nil && pr.Width/hatchCover*s.cam.Z*float64(s.p.Scale()) < hatchFinest && !s.export {
			// Too fine to make out, it would only shimmer: what it comes to, as a tint.
			s.fill(pr.Hatched, m, toUI(pr.Color.Alpha(o*hatchCover)))
			return
		}
		if !pr.Butt && len(pr.Dash) < 2 && pr.Color.A*o >= 1 && (s.lightInk || s.movingLarge(pr, m)) {
			s.lightLines(pr, m)
			return
		}
		s.strokePrim(pr, m, toUI(pr.Color.Alpha(o)))
	case editor.PrimText:
		s.text(pr, m, o)
	case editor.PrimImage:
		s.image(pr, m, o)
	}
}

// How much of what it fills a pattern's lines cover: their width to their spacing.
const hatchCover = 1.6 / 8

// These are variables for tests, to compare with what they stand in for.
var (
	// How many device pixels apart a pattern's lines have to be to make out.
	hatchFinest = 4.0
	// Whether a prim that is a box draws as one.
	boxesAsBoxes = true
	// How many parts a path has before it paints as a line as fine as it shows.
	detailSegs = 24
)

// asBox says whether to draw a prim as the box it is, which takes neither mask nor
// cells. A picture stays as exact as its paths are.
func (s *scene) asBox(pr editor.Prim, m geo.Mat) bool {
	return pr.Rounded != nil && boxesAsBoxes && !s.export && math.Abs(m.Rotation()) < 1e-4
}

// boxOf returns where a prim's box paints, and the radius of its corners.
func (s *scene) boxOf(pr editor.Prim, m geo.Mat) (ui.Rect, float32) {
	at, z := m.Apply(geo.V(pr.Rounded.X, pr.Rounded.Y)), s.cam.Z
	return ui.Rect{X: float32(at.X), Y: float32(at.Y), W: float32(pr.Rounded.W * z), H: float32(pr.Rounded.H * z)}, float32(pr.Rounded.Radius * z)
}

func (s *scene) fillBox(pr editor.Prim, m geo.Mat, c ui.Color) {
	r, radius := s.boxOf(pr, m)
	s.p.Fill(r, c, radius)
}

// strokeBox strokes a box. A painter's Stroke goes inside its box, and a stroke of a
// path to both sides of it.
func (s *scene) strokeBox(pr editor.Prim, m geo.Mat, c ui.Color) {
	r, radius := s.boxOf(pr, m)
	w := max(float32(pr.Width*s.cam.Z), s.pixel())
	s.p.Stroke(ui.Rect{X: r.X - w/2, Y: r.Y - w/2, W: r.W + w, H: r.H + w}, c, radius, w)
}

// movingLarge says whether a prim of a zoom going on would take a large mask.
func (s *scene) movingLarge(pr editor.Prim, m geo.Mat) bool {
	return s.moving && !s.small && pr.Path != nil && s.spansLargeAt(pr.Path, m, 0)
}

// inkLine draws a hand-inked stroke as a run of short lines, as wide as it is
// at each point, a few pixels each: as much as the eye can tell.
func (s *scene) inkLine(pr editor.Prim, m geo.Mat) {
	step := 3.0 // DIPs
	if s.shown > 0 {
		step /= s.shown // of the screen
	}
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
			w := max(size/2, 0.5)
			if math.Abs(m.Rotation()) < 1e-4 {
				s.p.Fill(ui.Rect{X: float32(a.X), Y: float32(a.Y) - w/2, W: float32(b.X - a.X), H: w}, c.Alpha(0.5), w/2)
				continue
			}
			s.segment[0] = [2]float32{float32(a.X), float32(a.Y)}
			s.segment[1] = [2]float32{float32(b.X), float32(b.Y)}
			s.p.StrokePolyline(s.segment[:], w, c.Alpha(0.5))
		}
		return
	}
	font := family(pr.Font)
	nat := naturalHeight(pr.Font) * size
	drop := (float32(pr.Block.Line*z) - nat) / 2
	rot := m.Rotation()
	draw := func(q geo.Vec, text string) {
		s.span[0] = ui.Span{Text: text, Font: font, Size: size, Color: c}
		s.p.RichText(float32(q.X), float32(q.Y)+drop, 0, s.span[:]...)
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
	rot := m.Rotation()
	// In a zoom going on, what it holds finds its own scene, as it has masks of its
	// own, under a clip on the screen's whole pixels, which costs nothing. A turned
	// one's clip takes a mask anyway: it all goes at once.
	if s.zoom != nil && math.Abs(rot) >= 1e-4 {
		l, text := s.zoom.sceneOf(sh)
		l.noHeading = text != nil
		l.paintAt(sh, func() { l.frame(sh) })
		l.noHeading = false
		if text != nil {
			text.paintAt(sh, text.paintHeading)
		}
		return
	}
	for _, pr := range s.e.Display(sh, s.pal) {
		s.prim(pr, m, o, sh)
	}
	origin := m.Apply(geo.Vec{})
	z := s.cam.Z
	clip := ui.Rect{X: float32(origin.X), Y: float32(origin.Y), W: float32(sh.W * z), H: float32(sh.H * z)}
	inside := func() { s.children(sh.ID) }
	if math.Abs(rot) < 1e-4 {
		s.p.Clip(clip, 0, inside)
	} else {
		deg := float32(rot * 180 / math.Pi)
		// The clip turns with the frame; the transform inside it undoes the
		// turn, as what the frame holds has it in its own transform.
		s.turned++
		s.p.Transform(ui.Rotation(deg), clip.X, clip.Y, func() {
			s.p.Clip(clip, 0, func() {
				s.p.Transform(ui.Rotation(-deg), clip.X, clip.Y, inside)
			})
		})
		s.turned--
	}
	if s.zoom != nil {
		l, text := s.zoom.sceneOf(sh)
		if text != nil {
			l = text
		}
		l.paintAt(sh, l.paintHeading)
		return
	}
	if !s.noHeading {
		s.frameHeading(sh, origin, rot)
	}
}

// heading paints a frame's heading, for a scene of its own.
func (s *scene) heading(sh *doc.Shape) {
	m := s.camMat().Mul(s.e.PageMat(sh.ID))
	s.frameHeading(sh, m.Apply(geo.Vec{}), m.Rotation())
}

// pixel returns how much of what is painted makes a pixel of what is shown. A
// picture smaller than the page it's of scales all it paints, and what stays
// the same size on the screen has to allow for that.
func (s *scene) pixel() float32 {
	if s.shrink > 0 {
		return float32(1 / s.shrink)
	}
	return 1
}

func (s *scene) frameHeading(sh *doc.Shape, origin geo.Vec, rot float64) {
	if sh.ID == s.e.Editing && !s.export {
		return
	}
	name := sh.Name
	if name == "" {
		name = "Frame"
	}
	page, font := editor.FrameHeading(sh)
	floor := float32(editor.FrameHeadingFloor) * s.pixel()
	size := max(float32(page*s.cam.Z), floor)
	color := toUI(s.pal.Text.Alpha(0.65))
	if page > 0 {
		color = toUI(s.pal.Swatch(sh.Color).Solid)
	}
	face := s.p.Theme().Font
	if font != "" {
		face = family(font)
	}
	draw := func() {
		s.span[0] = ui.Span{Text: name, Font: face, Size: size, Color: color}
		s.p.RichText(float32(origin.X), float32(origin.Y)-size*1.6, float32(max(sh.W*s.cam.Z, 40)), s.span[:]...)
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
	s.stroke((&geo.Path{}).Poly(g.Outline, g.Closed), m, stroke{width: width, color: c})
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
		s.stroke((&geo.Path{}).Move(a.Add(s.origin)).Line(b.Add(s.origin)), geo.Identity, stroke{width: 1, color: toUI(s.pal.Snap)})
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
	s.stroke((&geo.Path{}).Poly(screen, true), geo.Identity, stroke{width: 1.5, color: c, butt: true})
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
	if math.Abs(m.Rotation()) < 1e-4 {
		at, z := m.Apply(cs[0]), s.cam.Z
		r := ui.Rect{X: float32(at.X), Y: float32(at.Y), W: float32(g.Bounds.W * z), H: float32(g.Bounds.H * z)}
		s.p.Stroke(ui.Rect{X: r.X - 0.5, Y: r.Y - 0.5, W: r.W + 1, H: r.H + 1}, c.Alpha(0.6), 0.5, 1)
		return
	}
	s.stroke((&geo.Path{}).Poly(cs[:], true), m, stroke{width: 1, color: c.Alpha(0.6)})
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
