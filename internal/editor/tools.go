package editor

import (
	"math"
	"slices"

	"godraw/internal/doc"
	"godraw/internal/geo"
)

// gesture is what the pointer is doing between its press and its release.
type gesture interface {
	// move follows the pointer, or a change of the modifier keys.
	move(e *Editor)
	// up ends the gesture.
	up(e *Editor)
	// cancel abandons it, as Escape does.
	cancel(e *Editor)
}

// Cursor is the pointer the canvas asks for.
type Cursor int

// Cursors.
const (
	CursorDefault Cursor = iota
	CursorCross
	CursorText
	CursorGrab
	CursorGrabbing
	CursorEW
	CursorNS
	CursorNESW
	CursorNWSE
	CursorRotate
	CursorPointer
	CursorMove
)

const dragDistance = 4 // in DIPs of the screen

// Handle is a place on the selection that the pointer can take.
type Handle struct {
	Kind HandleKind
	// Which is the side or corner of a resize, as (0,0) the top left and
	// (1,1) the bottom right, with 0.5 for the middle of a side.
	Which geo.Vec
	// Point is where the handle is, in the page.
	Point geo.Vec
	// Index is the vertex of a line (or, with Virtual, the one after the
	// midpoint), and Term the end of an arrow.
	Index   int
	Virtual bool
	Term    string
}

// HandleKind is the kind of a Handle.
type HandleKind int

// Kinds of handle.
const (
	HandleNone HandleKind = iota
	HandleResize
	HandleRotate
	HandleArrowEnd
	HandleArrowBend
	HandleLinePoint
)

// PointerDown takes a press at a point of the canvas, in DIPs.
func (e *Editor) PointerDown(x, y float64, button, clicks int, m Mods, t float64) {
	screen := geo.V(x, y)
	page := e.ToPage(screen)
	e.pointer = Pointer{Screen: screen, Page: page, DownScreen: screen, DownPage: page, Down: true, Button: button, Mods: m, Time: t}
	e.Now = t
	e.cancelGesture()
	if e.Editing != "" {
		// A press outside the text being edited ends editing, and goes on.
		if id := e.Hit(page, e.margin(), false).ID; id != e.Editing {
			e.StopEditing()
		} else {
			return
		}
	}
	if button == 2 || e.spaceHeld || (button == 0 && e.Tool == ToolHand) {
		e.gesture = &panGesture{start: e.Cam()}
		return
	}
	if button == 1 {
		e.rightClick(page)
		return
	}
	if e.ReadOnly && e.Tool != ToolSelect && e.Tool != ToolLaser {
		e.gesture = &panGesture{start: e.Cam()}
		return
	}
	switch e.Tool {
	case ToolSelect:
		e.selectDown(clicks)
	case ToolDraw, ToolHighlight:
		e.gesture = newFreehand(e)
	case ToolEraser:
		e.gesture = newEraser(e)
	case ToolLaser:
		e.gesture = newLaser(e)
	case ToolGeo, ToolFrame:
		e.gesture = &createBox{}
	case ToolArrow:
		e.gesture = newArrowCreate(e)
	case ToolLine:
		e.gesture = newLineCreate(e)
	case ToolText:
		e.gesture = &clickCreate{kind: doc.TypeText}
	case ToolNote:
		e.gesture = &clickCreate{kind: doc.TypeNote}
	}
}

// PointerMove takes a move of the pointer.
func (e *Editor) PointerMove(x, y float64, m Mods, t float64) {
	screen := geo.V(x, y)
	e.pointer.Screen, e.pointer.Page, e.pointer.Mods, e.pointer.Time = screen, e.ToPage(screen), m, t
	e.Now = t
	if e.gesture != nil {
		e.gesture.move(e)
		return
	}
	e.updateHover()
}

// PointerUp takes a release.
func (e *Editor) PointerUp(x, y float64, m Mods, t float64) {
	screen := geo.V(x, y)
	e.pointer.Screen, e.pointer.Page, e.pointer.Mods, e.pointer.Time = screen, e.ToPage(screen), m, t
	e.pointer.Down = false
	e.Now = t
	if g := e.gesture; g != nil {
		e.gesture = nil
		g.up(e)
	}
	e.updateHover()
}

// ModsChanged takes a change of the modifier keys.
func (e *Editor) ModsChanged(m Mods) {
	if m == e.pointer.Mods {
		return
	}
	e.pointer.Mods = m
	if e.gesture != nil {
		e.gesture.move(e)
	}
}

// SetSpace tells whether the space bar is held, which pans.
func (e *Editor) SetSpace(down bool) { e.spaceHeld = down }

// SpaceHeld tells whether the space bar is held.
func (e *Editor) SpaceHeld() bool { return e.spaceHeld }

// InGesture reports whether the pointer is in the middle of something.
func (e *Editor) InGesture() bool { return e.gesture != nil }

func (e *Editor) cancelGesture() {
	if g := e.gesture; g != nil {
		e.gesture = nil
		g.cancel(e)
		e.Brush, e.Snaps, e.Hint, e.DropOn, e.Erasing = nil, nil, nil, "", nil
	}
}

// Cancel handles Escape: it ends what is going on, one thing at a time.
func (e *Editor) Cancel() {
	switch {
	case e.gesture != nil:
		e.cancelGesture()
	case e.Editing != "":
		e.StopEditing()
	case e.FocusedGroup != "":
		id := e.FocusedGroup
		e.FocusedGroup = ""
		e.Select(id)
	case len(e.sel) > 0:
		e.SelectNone()
	case e.Tool != ToolSelect:
		e.SetTool(ToolSelect)
	}
}

// SetTool chooses a tool.
func (e *Editor) SetTool(t Tool) {
	e.StopEditing()
	e.cancelGesture()
	e.Tool = t
	if t != ToolSelect {
		e.Hovered = ""
		if t != ToolHand && t != ToolLaser {
			e.SelectNone()
		}
	}
}

// SetGeoTool chooses the geo tool with a shape.
func (e *Editor) SetGeoTool(kind string) {
	e.SetTool(ToolGeo)
	e.Next.Geo = kind
}

func (e *Editor) finishTool() {
	if !e.ToolLocked {
		e.Tool = ToolSelect
	}
}

func (e *Editor) margin() float64 { return 8 / e.Cam().Z }

func (e *Editor) dragged() bool {
	return e.pointer.Screen.Dist(e.pointer.DownScreen) > dragDistance
}

func (e *Editor) updateHover() {
	if e.Tool != ToolSelect || e.Editing != "" {
		e.Hovered = ""
		return
	}
	if h := e.HandleAt(e.pointer.Screen); h.Kind != HandleNone {
		e.Hovered = ""
		return
	}
	r := e.Hit(e.pointer.Page, e.margin(), false)
	if r.Hollow {
		e.Hovered = ""
		return
	}
	if r.ID == "" {
		e.Hovered = ""
		return
	}
	e.Hovered = e.outermostSelectable(r.ID)
}

// CursorNow returns the pointer the canvas should show.
func (e *Editor) CursorNow() Cursor {
	switch g := e.gesture.(type) {
	case *panGesture:
		return CursorGrabbing
	case *resizeGesture:
		return resizeCursor(g.handle.Which, g.rotation)
	case *rotateGesture:
		return CursorRotate
	}
	if e.spaceHeld {
		return CursorGrab
	}
	switch e.Tool {
	case ToolHand:
		return CursorGrab
	case ToolDraw, ToolHighlight, ToolEraser, ToolArrow, ToolGeo, ToolFrame, ToolLine, ToolNote, ToolLaser:
		return CursorCross
	case ToolText:
		return CursorText
	}
	h := e.HandleAt(e.pointer.Screen)
	switch h.Kind {
	case HandleResize:
		_, r := e.SelectionBox()
		return resizeCursor(h.Which, r)
	case HandleRotate:
		return CursorRotate
	case HandleArrowEnd, HandleArrowBend, HandleLinePoint:
		return CursorPointer
	}
	return CursorDefault
}

func resizeCursor(which geo.Vec, rotation float64) Cursor {
	d := which.Sub(geo.V(0.5, 0.5))
	a := geo.NormAngle(math.Atan2(d.Y, d.X) + rotation)
	// Eight directions, folded to the four cursors.
	k := int(math.Round(a/(math.Pi/4))) % 4
	switch k {
	case 0:
		return CursorEW
	case 1:
		return CursorNWSE
	case 2:
		return CursorNS
	}
	return CursorNESW
}

// --- selection geometry ---

// SelectionBox returns the box of the selection in its own frame, and that
// frame's rotation: a shape's for one, or those shapes' if they all turn
// alike, else none.
func (e *Editor) SelectionBox() (geo.Box, float64) {
	ids := e.topSelected()
	if len(ids) == 0 {
		return geo.Box{}, 0
	}
	r := e.PageMat(ids[0]).Rotation()
	for _, id := range ids[1:] {
		if math.Abs(geo.ShortAngle(r, e.PageMat(id).Rotation())) > 1e-3 {
			r = 0
			break
		}
	}
	inv := geo.Rotate(-r)
	var box geo.Box
	for i, id := range ids {
		s := e.Doc.Shapes[id]
		b := inv.Mul(e.PageMat(id)).BoundsOf(e.Geometry(s).Bounds)
		if i == 0 {
			box = b
		} else {
			box = box.Union(b)
		}
	}
	return box, r
}

// SelectionCorners returns the corners of the selection box, in the page.
func (e *Editor) SelectionCorners() [4]geo.Vec {
	box, r := e.SelectionBox()
	return geo.Rotate(r).ApplyBox(box)
}

// singleLinear returns the one selected shape if it is a line or an arrow,
// which show their handles rather than a box.
func (e *Editor) singleLinear() *doc.Shape {
	if len(e.sel) != 1 {
		return nil
	}
	s := e.Doc.Shapes[e.sel[0]]
	if s != nil && (s.Type == doc.TypeArrow || s.Type == doc.TypeLine) {
		return s
	}
	return nil
}

// ShowsBox reports whether the selection shows its box and resize handles.
func (e *Editor) ShowsBox() bool {
	return len(e.sel) > 0 && e.singleLinear() == nil && e.Editing == ""
}

// Handles returns what the pointer can take of the selection.
func (e *Editor) Handles() []Handle {
	if len(e.sel) == 0 || e.Editing != "" || e.ReadOnly {
		return nil
	}
	if s := e.singleLinear(); s != nil {
		if s.Locked {
			return nil
		}
		return e.linearHandles(s)
	}
	if e.allLocked() {
		return nil
	}
	box, r := e.SelectionBox()
	m := geo.Rotate(r)
	at := func(fx, fy float64) geo.Vec { return m.Apply(geo.V(box.X+box.W*fx, box.Y+box.H*fy)) }
	z := e.Cam().Z
	var hs []Handle
	// Corners come first: they win over the sides where they overlap.
	for _, c := range [][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}} {
		hs = append(hs, Handle{Kind: HandleResize, Which: geo.V(c[0], c[1]), Point: at(c[0], c[1])})
	}
	if box.W*z > 6 && box.H*z > 6 {
		for _, c := range [][2]float64{{0.5, 0}, {1, 0.5}, {0.5, 1}, {0, 0.5}} {
			hs = append(hs, Handle{Kind: HandleResize, Which: geo.V(c[0], c[1]), Point: at(c[0], c[1])})
		}
	}
	for _, c := range [][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}} {
		hs = append(hs, Handle{Kind: HandleRotate, Which: geo.V(c[0], c[1]), Point: at(c[0], c[1])})
	}
	return hs
}

func (e *Editor) allLocked() bool {
	for _, id := range e.sel {
		if s := e.Doc.Shapes[id]; s != nil && !s.Locked {
			return false
		}
	}
	return true
}

func (e *Editor) linearHandles(s *doc.Shape) []Handle {
	m := e.PageMat(s.ID)
	var hs []Handle
	if s.Type == doc.TypeArrow {
		info := e.Arrow(s)
		hs = append(hs,
			Handle{Kind: HandleArrowEnd, Term: "start", Point: m.Apply(info.Start)},
			Handle{Kind: HandleArrowEnd, Term: "end", Point: m.Apply(info.End)},
		)
		if s.Kind != "elbow" {
			hs = append(hs, Handle{Kind: HandleArrowBend, Point: m.Apply(info.Mid)})
		}
		return hs
	}
	for i, p := range s.Points {
		hs = append(hs, Handle{Kind: HandleLinePoint, Index: i, Point: m.Apply(geo.V(p.X, p.Y))})
	}
	for i := 1; i < len(s.Points); i++ {
		a, b := s.Points[i-1], s.Points[i]
		hs = append(hs, Handle{Kind: HandleLinePoint, Index: i, Virtual: true, Point: m.Apply(geo.Med(geo.V(a.X, a.Y), geo.V(b.X, b.Y)))})
	}
	return hs
}

// HandleAt returns the handle at a point of the screen.
func (e *Editor) HandleAt(screen geo.Vec) Handle {
	handles := e.Handles()
	if len(handles) == 0 {
		return Handle{}
	}
	if s := e.singleLinear(); s != nil {
		for _, h := range handles {
			if e.ToScreen(h.Point).Dist(screen) <= 10 {
				return h
			}
		}
		return Handle{}
	}
	box, r := e.SelectionBox()
	z := e.Cam().Z
	inv := geo.Rotate(-r)
	// The point in the selection's frame, in DIPs of the screen from the
	// box, so that handles are a size on the screen.
	q := inv.Apply(e.ToPage(screen))
	dx := func(at float64) float64 { return (q.X - at) * z }
	dy := func(at float64) float64 { return (q.Y - at) * z }
	const near = 7
	// A text resizes by its sides alone unless it wraps.
	for _, h := range handles {
		if h.Kind != HandleResize {
			continue
		}
		cx, cy := box.X+box.W*h.Which.X, box.Y+box.H*h.Which.Y
		switch {
		case h.Which.X != 0.5 && h.Which.Y != 0.5:
			if math.Abs(dx(cx)) <= near && math.Abs(dy(cy)) <= near {
				return h
			}
		case h.Which.X == 0.5:
			if math.Abs(dy(cy)) <= near-2 && q.X > box.X && q.X < box.MaxX() {
				return h
			}
		default:
			if math.Abs(dx(cx)) <= near-2 && q.Y > box.Y && q.Y < box.MaxY() {
				return h
			}
		}
	}
	// Rotate zones lie just beyond the corners.
	for _, h := range handles {
		if h.Kind != HandleRotate {
			continue
		}
		cx, cy := box.X+box.W*h.Which.X, box.Y+box.H*h.Which.Y
		ox, oy := dx(cx), dy(cy)
		// Outward from the corner.
		sx, sy := h.Which.X*2-1, h.Which.Y*2-1
		ox, oy = ox*sx, oy*sy
		if ox >= -near && oy >= -near && ox <= 22 && oy <= 22 && (ox > near || oy > near) {
			return h
		}
	}
	return Handle{}
}

// --- select tool ---

func (e *Editor) selectDown(clicks int) {
	p := e.pointer.Page
	if clicks >= 2 {
		if e.doubleClick() {
			return
		}
	}
	if h := e.HandleAt(e.pointer.Screen); h.Kind != HandleNone {
		switch h.Kind {
		case HandleResize:
			e.gesture = newResize(e, h)
		case HandleRotate:
			e.gesture = newRotate(e)
		case HandleArrowEnd:
			e.gesture = newArrowEndDrag(e, h.Term)
		case HandleArrowBend:
			e.gesture = newArrowBendDrag(e)
		case HandleLinePoint:
			e.gesture = newLinePointDrag(e, h)
		}
		return
	}
	hit := e.Hit(p, e.margin(), false)
	var target doc.ID
	if hit.ID != "" {
		target = e.outermostSelectable(hit.ID)
	}
	inBox := false
	if len(e.sel) > 1 || (len(e.sel) == 1 && e.singleLinear() == nil) {
		box, r := e.SelectionBox()
		inBox = box.Contains(geo.Rotate(-r).Apply(p))
	}
	switch {
	case target != "" && !hit.Hollow:
		wasSelected := e.IsSelected(target)
		if !wasSelected && !e.pointer.Mods.Shift {
			if !(inBox && len(e.sel) > 1) {
				e.Select(target)
			}
		} else if !wasSelected {
			e.Select(append(slices.Clone(e.sel), target)...)
		}
		e.gesture = &pressShape{target: target, wasSelected: wasSelected}
	case inBox:
		e.gesture = &pressShape{target: target, wasSelected: true, inBoxOnly: true, hollow: hit.Hollow}
	default:
		// Hollow shapes take clicks, but a drag starts a brush.
		e.gesture = &pressCanvas{hollow: target, additive: e.pointer.Mods.Shift, before: slices.Clone(e.sel)}
	}
}

// doubleClick handles a double click, reporting whether it did something.
func (e *Editor) doubleClick() bool {
	p := e.pointer.Page
	r := e.Hit(p, e.margin(), false)
	if r.ID == "" {
		if e.ReadOnly {
			return false
		}
		// On the canvas, a double click writes.
		e.createTextAt(p)
		return true
	}
	target := e.outermostSelectable(r.ID)
	s := e.Doc.Shapes[target]
	if s == nil {
		return false
	}
	// In the body of a frame, it writes there; only its heading renames it.
	if s.Type == doc.TypeFrame && r.Hollow && !e.ReadOnly {
		e.createTextAt(p)
		return true
	}
	if s.Type == doc.TypeGroup {
		e.FocusedGroup = target
		e.Select(e.outermostSelectable(r.ID))
		inner := e.outermostSelectable(r.ID)
		if t := e.Doc.Shapes[inner]; t != nil && textEditable(t) {
			e.StartEditing(inner)
		}
		return true
	}
	if textEditable(s) && !e.ReadOnly {
		e.Select(target)
		e.StartEditing(target)
		return true
	}
	return false
}

func textEditable(s *doc.Shape) bool {
	switch s.Type {
	case doc.TypeGeo, doc.TypeText, doc.TypeNote, doc.TypeArrow, doc.TypeFrame:
		return !s.Locked
	}
	return false
}

// pressShape is the press on a shape, or inside the selection, before it is
// known to be a click or a drag.
type pressShape struct {
	target      doc.ID
	wasSelected bool
	inBoxOnly   bool
	hollow      bool
}

func (g *pressShape) move(e *Editor) {
	if !e.dragged() || e.ReadOnly {
		return
	}
	if e.allLocked() {
		return
	}
	e.gesture = newTranslate(e)
	e.gesture.move(e)
}

func (g *pressShape) up(e *Editor) {
	switch {
	case g.inBoxOnly:
		// A click in the box: on nothing, or a hollow shape in it.
		switch {
		case g.target != "" && g.hollow && !e.pointer.Mods.Shift:
			e.Select(g.target)
		case !e.pointer.Mods.Shift:
			e.SelectNone()
		}
	case e.pointer.Mods.Shift && g.wasSelected:
		e.Select(slices.DeleteFunc(slices.Clone(e.sel), func(id doc.ID) bool { return id == g.target })...)
	case !e.pointer.Mods.Shift:
		e.Select(g.target)
	}
}

func (g *pressShape) cancel(e *Editor) {}

// pressCanvas is the press on nothing, or inside a hollow shape.
type pressCanvas struct {
	hollow   doc.ID
	additive bool
	before   []doc.ID
}

func (g *pressCanvas) move(e *Editor) {
	if !e.dragged() {
		return
	}
	e.gesture = &brushGesture{additive: g.additive, before: g.before}
	e.gesture.move(e)
}

func (g *pressCanvas) up(e *Editor) {
	switch {
	case g.hollow != "":
		if g.additive {
			if !e.IsSelected(g.hollow) {
				e.Select(append(slices.Clone(e.sel), g.hollow)...)
			}
		} else {
			e.Select(g.hollow)
		}
	case !g.additive:
		e.SelectNone()
	}
}

func (g *pressCanvas) cancel(e *Editor) {}

// brushGesture selects what a dragged box touches.
type brushGesture struct {
	additive bool
	before   []doc.ID
}

func (g *brushGesture) move(e *Editor) {
	box := geo.BoxFrom(e.pointer.DownPage, e.pointer.Page)
	e.Brush = &box
	wrap := e.pointer.Mods.Cmd // with Cmd, only what it wraps whole
	parent := e.Page
	if e.FocusedGroup != "" {
		parent = e.FocusedGroup
	}
	var hits []doc.ID
	var test func(id doc.ID) bool
	test = func(id doc.ID) bool {
		s := e.Doc.Shapes[id]
		if s.Locked {
			return false
		}
		if s.Type == doc.TypeGroup {
			for _, k := range e.Children(id) {
				if test(k) {
					return true
				}
			}
			return false
		}
		bounds := e.PageBounds(id)
		if !box.Overlaps(bounds) {
			return false
		}
		if box.ContainsBox(bounds) {
			return true
		}
		if wrap {
			return false
		}
		if s.Type == doc.TypeFrame {
			// A frame takes a brush that crosses its edge, not one inside it.
			return !bounds.ContainsBox(box)
		}
		return geo.BoxHitsPolyline(box, e.PageOutline(id), e.Geometry(s).Closed)
	}
	for _, id := range e.Children(parent) {
		if test(id) {
			hits = append(hits, id)
			continue
		}
		// What is inside a frame that the brush doesn't take as a whole.
		if s := e.Doc.Shapes[id]; s.Type == doc.TypeFrame {
			for _, k := range e.Children(id) {
				if test(k) {
					hits = append(hits, k)
				}
			}
		}
	}
	if g.additive {
		for _, id := range g.before {
			if !slices.Contains(hits, id) {
				hits = append(hits, id)
			}
		}
	}
	e.Select(hits...)
}

func (g *brushGesture) up(e *Editor) { e.Brush = nil }

func (g *brushGesture) cancel(e *Editor) {
	e.Brush = nil
	e.Select(g.before...)
}

// --- pan ---

type panGesture struct{ start Camera }

func (g *panGesture) move(e *Editor) {
	d := e.pointer.Screen.Sub(e.pointer.DownScreen)
	e.SetCam(Camera{g.start.X + d.X/g.start.Z, g.start.Y + d.Y/g.start.Z, g.start.Z})
}
func (g *panGesture) up(e *Editor)     {}
func (g *panGesture) cancel(e *Editor) { e.SetCam(g.start) }

// --- translate ---

type translateGesture struct {
	ids       []doc.ID // what moves: the selection without what its shapes hold
	initial   map[doc.ID]*doc.Shape
	cloned    []doc.ID
	wasAlt    bool
	selBefore []doc.ID
	box       geo.Box
	targets   snapTargets
}

func newTranslate(e *Editor) *translateGesture {
	e.begin()
	g := &translateGesture{initial: map[doc.ID]*doc.Shape{}, selBefore: slices.Clone(e.sel)}
	for _, id := range e.topSelected() {
		if s := e.Doc.Shapes[id]; s != nil && !s.Locked {
			g.ids = append(g.ids, id)
			g.initial[id] = s
		}
	}
	box, r := e.SelectionBox()
	g.box = geo.Rotate(r).BoundsOf(box)
	g.targets = e.collectSnapTargets(g.ids)
	return g
}

func (g *translateGesture) move(e *Editor) {
	p := e.pointer
	if p.Mods.Alt != g.wasAlt {
		g.wasAlt = p.Mods.Alt
		g.setCloned(e, p.Mods.Alt)
	}
	d := p.Page.Sub(p.DownPage)
	if p.Mods.Shift {
		if math.Abs(d.X) > math.Abs(d.Y) {
			d.Y = 0
		} else {
			d.X = 0
		}
	}
	e.Snaps = nil
	if p.Mods.Cmd != e.Snap {
		d = e.snapTranslate(g.box, d, g.targets)
	} else if e.Grid {
		size := e.gridSize()
		d.X = math.Round((g.box.X+d.X)/size)*size - g.box.X
		d.Y = math.Round((g.box.Y+d.Y)/size)*size - g.box.Y
	}
	for _, id := range g.moving() {
		init := g.initial[id]
		if init == nil {
			init = e.Doc.Shapes[id]
			g.initial[id] = init
		}
		pm := e.parentMat(init.Parent).Invert()
		local := pm.ApplyDir(d)
		c := init.Moved()
		c.X, c.Y = init.X+local.X, init.Y+local.Y
		e.put(c)
	}
	e.DropOn = g.dropTarget(e)
}

// moving returns what moves now: the clones while Alt is held, else the
// shapes.
func (g *translateGesture) moving() []doc.ID {
	if len(g.cloned) > 0 {
		return g.cloned
	}
	return g.ids
}

func (g *translateGesture) setCloned(e *Editor, on bool) {
	if on {
		// The originals go back where they were, and copies move instead.
		for _, id := range g.ids {
			e.put(g.initial[id].Moved())
		}
		clones := e.cloneShapes(g.ids, geo.Vec{})
		g.cloned = clones
		e.Select(clones...)
		return
	}
	for _, id := range g.cloned {
		e.deleteTree(id)
		delete(g.initial, id)
	}
	g.cloned = nil
	e.Select(g.selBefore...)
}

// dropTarget returns the frame what moves would drop into.
func (g *translateGesture) dropTarget(e *Editor) doc.ID {
	skip := map[doc.ID]bool{}
	for _, id := range g.moving() {
		skip[id] = true
		for _, k := range e.Descendants(id) {
			skip[k] = true
		}
	}
	return e.frameAt(e.pointer.Page, skip)
}

func (g *translateGesture) up(e *Editor) {
	target := g.dropTarget(e)
	e.DropOn, e.Snaps = "", nil
	e.reparentMoved(g.moving(), target)
	// Arrows moved without what they point at let go of it.
	moving := g.moving()
	for _, id := range moving {
		s := e.Doc.Shapes[id]
		if s == nil || s.Type != doc.TypeArrow {
			continue
		}
		for _, term := range []string{"start", "end"} {
			b := e.bindingOf(id, term)
			if b != nil && !slices.Contains(moving, b.To) {
				e.unbind(id, term)
			}
		}
	}
	e.commit()
}

func (g *translateGesture) cancel(e *Editor) {
	e.rollback()
	e.DropOn, e.Snaps = "", nil
}

func (e *Editor) gridSize() float64 {
	if e.Doc.GridSize > 0 {
		return e.Doc.GridSize
	}
	return 10
}

// reparentMoved moves shapes into a frame they were dropped on, or out of
// the frame they were dropped outside of, keeping them where they are.
func (e *Editor) reparentMoved(ids []doc.ID, target doc.ID) {
	dest := target
	if dest == "" {
		dest = e.Page
	}
	for _, id := range ids {
		s := e.Doc.Shapes[id]
		if s == nil {
			continue
		}
		parent := e.Doc.Shapes[s.Parent]
		if parent != nil && parent.Type == doc.TypeGroup {
			continue // shapes in groups stay in them
		}
		if s.Parent == dest {
			continue
		}
		e.reparent(id, dest)
	}
}

// reparent moves a shape to another parent, at the top, keeping where it is
// in the page.
func (e *Editor) reparent(id, parent doc.ID) {
	s := e.Doc.Shapes[id]
	if s == nil {
		return
	}
	m := e.parentMat(parent).Invert().Mul(e.PageMat(id))
	kids := e.Children(parent)
	last := ""
	if len(kids) > 0 {
		last = e.Doc.Shapes[kids[len(kids)-1]].Index
	}
	c := s.Moved()
	c.Parent = parent
	c.Index = doc.IndexBetween(last, "")
	o := m.Apply(geo.Vec{})
	c.X, c.Y = o.X, o.Y
	c.Rotation = m.Rotation()
	e.put(c)
}

// --- snapping ---

// SnapLine is a guide showing what a moved shape lines up with, in the page.
type SnapLine struct{ A, B geo.Vec }

type snapTargets struct {
	xs, ys []snapStop
}

type snapStop struct {
	at     float64
	lo, hi float64 // how far it spans across
}

func (e *Editor) collectSnapTargets(skip []doc.ID) snapTargets {
	var t snapTargets
	view := e.ViewBox().Expand(200)
	skipping := map[doc.ID]bool{}
	for _, id := range skip {
		skipping[id] = true
		for _, k := range e.Descendants(id) {
			skipping[k] = true
		}
	}
	var walk func(parent doc.ID)
	walk = func(parent doc.ID) {
		for _, id := range e.Children(parent) {
			if skipping[id] {
				continue
			}
			s := e.Doc.Shapes[id]
			b := e.PageBounds(id)
			if !view.Overlaps(b) {
				continue
			}
			t.xs = append(t.xs, snapStop{b.X, b.Y, b.MaxY()}, snapStop{b.X + b.W/2, b.Y, b.MaxY()}, snapStop{b.MaxX(), b.Y, b.MaxY()})
			t.ys = append(t.ys, snapStop{b.Y, b.X, b.MaxX()}, snapStop{b.Y + b.H/2, b.X, b.MaxX()}, snapStop{b.MaxY(), b.X, b.MaxX()})
			if s.Type == doc.TypeFrame || s.Type == doc.TypeGroup {
				walk(id)
			}
		}
	}
	walk(e.Page)
	return t
}

// snapTranslate adjusts a move of box by d so that its edges or center line
// up with those of other shapes, and sets the guides.
func (e *Editor) snapTranslate(box geo.Box, d geo.Vec, t snapTargets) geo.Vec {
	moved := geo.Box{X: box.X + d.X, Y: box.Y + d.Y, W: box.W, H: box.H}
	th := 8 / e.Cam().Z
	bestX, atX := th, math.NaN()
	sx := [3]float64{moved.X, moved.X + moved.W/2, moved.MaxX()}
	for _, x := range sx {
		for _, st := range t.xs {
			if dd := st.at - x; math.Abs(dd) < bestX {
				bestX, atX = math.Abs(dd), dd
			}
		}
	}
	bestY, atY := th, math.NaN()
	sy := [3]float64{moved.Y, moved.Y + moved.H/2, moved.MaxY()}
	for _, y := range sy {
		for _, st := range t.ys {
			if dd := st.at - y; math.Abs(dd) < bestY {
				bestY, atY = math.Abs(dd), dd
			}
		}
	}
	if !math.IsNaN(atX) {
		d.X += atX
	}
	if !math.IsNaN(atY) {
		d.Y += atY
	}
	moved = geo.Box{X: box.X + d.X, Y: box.Y + d.Y, W: box.W, H: box.H}
	var lines []SnapLine
	sx = [3]float64{moved.X, moved.X + moved.W/2, moved.MaxX()}
	for _, x := range sx {
		lo, hi := moved.Y, moved.MaxY()
		found := false
		for _, st := range t.xs {
			if math.Abs(st.at-x) < 0.01 {
				lo, hi, found = math.Min(lo, st.lo), math.Max(hi, st.hi), true
			}
		}
		if found {
			lines = append(lines, SnapLine{geo.V(x, lo), geo.V(x, hi)})
		}
	}
	sy = [3]float64{moved.Y, moved.Y + moved.H/2, moved.MaxY()}
	for _, y := range sy {
		lo, hi := moved.X, moved.MaxX()
		found := false
		for _, st := range t.ys {
			if math.Abs(st.at-y) < 0.01 {
				lo, hi, found = math.Min(lo, st.lo), math.Max(hi, st.hi), true
			}
		}
		if found {
			lines = append(lines, SnapLine{geo.V(lo, y), geo.V(hi, y)})
		}
	}
	e.Snaps = lines
	return d
}

// --- rotate ---

type rotateGesture struct {
	ids      []doc.ID
	initial  map[doc.ID]*doc.Shape
	mats     map[doc.ID]geo.Mat
	center   geo.Vec
	start    float64
	rotation float64
}

func newRotate(e *Editor) *rotateGesture {
	e.begin()
	g := &rotateGesture{initial: map[doc.ID]*doc.Shape{}, mats: map[doc.ID]geo.Mat{}}
	for _, id := range e.topSelected() {
		g.ids = append(g.ids, id)
		g.initial[id] = e.Doc.Shapes[id]
		g.mats[id] = e.PageMat(id)
	}
	box, r := e.SelectionBox()
	g.center = geo.Rotate(r).Apply(box.Center())
	g.start = g.center.Angle(e.pointer.Page)
	g.rotation = r
	return g
}

func (g *rotateGesture) move(e *Editor) {
	a := g.center.Angle(e.pointer.Page) - g.start
	if e.pointer.Mods.Shift {
		// Steps of 15° of the shape's final angle.
		const step = math.Pi / 12
		a = math.Round((g.rotation+a)/step)*step - g.rotation
	}
	turn := geo.Translate(g.center.X, g.center.Y).Mul(geo.Rotate(a)).Mul(geo.Translate(-g.center.X, -g.center.Y))
	for _, id := range g.ids {
		init := g.initial[id]
		m := e.parentMat(init.Parent).Invert().Mul(turn).Mul(g.mats[id])
		c := init.Moved()
		o := m.Apply(geo.Vec{})
		c.X, c.Y = o.X, o.Y
		c.Rotation = init.Rotation + a
		e.put(c)
	}
}

func (g *rotateGesture) up(e *Editor)     { e.commit() }
func (g *rotateGesture) cancel(e *Editor) { e.rollback() }

// --- resize ---

type resizeItem struct {
	id   doc.ID
	init *doc.Shape
	mat  geo.Mat
	// rel is the shape's turn from the selection's frame.
	rel float64
	// bounds is its box in the selection's frame.
	bounds geo.Box
}

type resizeGesture struct {
	handle   Handle
	box      geo.Box
	rotation float64
	items    []resizeItem
	uniform  bool
}

func newResize(e *Editor, h Handle) *resizeGesture {
	e.begin()
	box, r := e.SelectionBox()
	g := &resizeGesture{handle: h, box: box, rotation: r}
	var add func(id doc.ID)
	add = func(id doc.ID) {
		s := e.Doc.Shapes[id]
		if s == nil || s.Locked {
			return
		}
		if s.Type == doc.TypeGroup {
			for _, k := range e.Children(id) {
				add(k)
			}
			return
		}
		m := e.PageMat(id)
		item := resizeItem{id: id, init: s, mat: m, rel: geo.ShortAngle(r, m.Rotation())}
		item.bounds = geo.Rotate(-r).Mul(m).BoundsOf(e.Geometry(s).Bounds)
		g.items = append(g.items, item)
		// A shape turned off the frame's axes cannot stretch on them.
		if math.Mod(math.Abs(item.rel), math.Pi/2) > 1e-3 && math.Abs(math.Mod(math.Abs(item.rel), math.Pi/2)-math.Pi/2) > 1e-3 {
			g.uniform = true
		}
		if s.Type == doc.TypeNote {
			g.uniform = true
		}
		if s.Type == doc.TypeImage {
			g.uniform = true // images keep their proportions unless Shift says otherwise
		}
	}
	for _, id := range e.topSelected() {
		add(id)
	}
	return g
}

func (g *resizeGesture) move(e *Editor) {
	p := e.pointer
	inv := geo.Rotate(-g.rotation)
	q := inv.Apply(p.Page)
	w := g.handle.Which
	// The fixed point: the opposite side, or the center with Alt.
	anchor := geo.V(g.box.X+g.box.W*(1-w.X), g.box.Y+g.box.H*(1-w.Y))
	if p.Mods.Alt {
		anchor = g.box.Center()
	}
	moving := geo.V(g.box.X+g.box.W*w.X, g.box.Y+g.box.H*w.Y)
	dx, dy := moving.X-anchor.X, moving.Y-anchor.Y
	sx, sy := 1.0, 1.0
	if w.X != 0.5 && dx != 0 {
		sx = (q.X - anchor.X) / dx
	}
	if w.Y != 0.5 && dy != 0 {
		sy = (q.Y - anchor.Y) / dy
	}
	uniform := g.uniform != p.Mods.Shift
	if g.uniform && p.Mods.Shift {
		// Images stretch with Shift; what cannot stretch stays uniform.
		uniform = false
		for _, it := range g.items {
			if it.init.Type != doc.TypeImage {
				uniform = true
			}
		}
	}
	if uniform {
		switch {
		case w.X == 0.5:
			sx = math.Abs(sy) * signOf(sx)
		case w.Y == 0.5:
			sy = math.Abs(sx) * signOf(sy)
		default:
			k := math.Max(math.Abs(sx), math.Abs(sy))
			sx, sy = k*signOrKeep(sx), k*signOrKeep(sy)
		}
	}
	if math.Abs(sx) < 1e-4 {
		sx = 1e-4
	}
	if math.Abs(sy) < 1e-4 {
		sy = 1e-4
	}
	fwd := geo.Rotate(g.rotation)
	for _, it := range g.items {
		e.resizeItem(it, sx, sy, anchor, fwd, inv, w)
	}
}

func signOrKeep(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}

// resizeItem scales a shape, by (sx, sy) on the selection's axes around
// anchor, in the selection's frame.
func (e *Editor) resizeItem(it resizeItem, sx, sy float64, anchor geo.Vec, fwd, inv geo.Mat, which geo.Vec) {
	// The scale on the shape's own axes.
	lx, ly := sx, sy
	rel := geo.NormAngle(it.rel)
	if q := math.Mod(rel+math.Pi/4, math.Pi); q > math.Pi/2 {
		lx, ly = sy, sx
	}
	init := it.init
	// Where the shape's origin goes.
	o := inv.Apply(it.mat.Apply(geo.Vec{}))
	moved := geo.V(anchor.X+(o.X-anchor.X)*sx, anchor.Y+(o.Y-anchor.Y)*sy)
	c := init.Clone()
	turn := it.mat.Rotation()
	offset := geo.Vec{} // to the top left of the new box, for flipped boxes
	abs := func(v float64) float64 { return math.Abs(v) }
	switch init.Type {
	case doc.TypeGeo:
		c.W, c.H = math.Max(abs(init.W*lx), 1), math.Max(abs((init.H+init.GrowY)*ly), 1)
		c.GrowY = 0
		offset = boxFlipOffset(c.W, c.H, lx, ly)
		c.FlipX, c.FlipY = init.FlipX != (lx < 0), init.FlipY != (ly < 0)
	case doc.TypeFrame:
		c.W, c.H = math.Max(abs(init.W*lx), 1), math.Max(abs(init.H*ly), 1)
		offset = boxFlipOffset(c.W, c.H, lx, ly)
	case doc.TypeImage:
		c.W, c.H = math.Max(abs(init.W*lx), 1), math.Max(abs(init.H*ly), 1)
		offset = boxFlipOffset(c.W, c.H, lx, ly)
		c.FlipX, c.FlipY = init.FlipX != (lx < 0), init.FlipY != (ly < 0)
	case doc.TypeNote:
		k := abs(lx)
		c.Scale = math.Max(init.Scale*k, 0.05)
		offset = boxFlipOffset(noteSize*c.Scale, (noteSize+init.GrowY)*c.Scale, lx, ly)
	case doc.TypeText:
		if init.AutoSize && which.X != 0.5 && which.Y != 0.5 {
			k := abs(lx)
			c.Scale = math.Max(init.Scale*k, 0.05)
			g := e.Geometry(init)
			offset = boxFlipOffset(g.Bounds.W*k, g.Bounds.H*k, lx, ly)
		} else {
			// Pulling a side wraps it.
			g := e.Geometry(init)
			c.AutoSize = false
			c.W = math.Max(abs(g.Bounds.W*lx)/init.Scale, 8)
			offset = boxFlipOffset(c.W*init.Scale, g.Bounds.H, lx, 1)
			if ly < 0 {
				offset.Y = 0
			}
		}
	case doc.TypeDraw, doc.TypeHighlight:
		c.ScaleX, c.ScaleY = orOne(init.ScaleX)*lx, orOne(init.ScaleY)*ly
	case doc.TypeLine:
		pts := make([]doc.LinePoint, len(init.Points))
		for i, p := range init.Points {
			p.X, p.Y = p.X*lx, p.Y*ly
			pts[i] = p
		}
		c.Points = pts
	case doc.TypeArrow:
		c.Start = geo.V(init.Start.X*lx, init.Start.Y*ly)
		c.End = geo.V(init.End.X*lx, init.End.Y*ly)
		c.Bend = init.Bend * math.Sqrt(abs(lx*ly))
		if lx*ly < 0 {
			c.Bend = -c.Bend
		}
	default:
		c.W, c.H = math.Max(abs(init.W*lx), 1), math.Max(abs(init.H*ly), 1)
		offset = boxFlipOffset(c.W, c.H, lx, ly)
	}
	// In the page, then in the parent.
	page := fwd.Apply(moved)
	page = page.Add(geo.Rotate(turn).Apply(offset))
	inP := e.parentMat(init.Parent).Invert().Apply(page)
	c.X, c.Y = inP.X, inP.Y
	e.put(c)
}

// boxFlipOffset returns how far from the scaled origin a box w×h lies on
// its own axes: flipped, it lies on the origin's other side.
func boxFlipOffset(w, h, lx, ly float64) geo.Vec {
	var o geo.Vec
	if lx < 0 {
		o.X = -w
	}
	if ly < 0 {
		o.Y = -h
	}
	return o
}

func (g *resizeGesture) up(e *Editor)     { e.commit() }
func (g *resizeGesture) cancel(e *Editor) { e.rollback() }

// --- creating boxes ---

// createBox is a drag that makes a geo shape or a frame, or a click that
// makes one of a default size.
type createBox struct {
	id     doc.ID
	origin geo.Vec
}

func (g *createBox) move(e *Editor) {
	if g.id == "" {
		if !e.dragged() {
			return
		}
		e.begin()
		g.origin = e.snapPoint(e.pointer.DownPage)
		g.id = e.newBoxShape(g.origin, 1, 1)
	}
	p := e.snapPoint(e.pointer.Page)
	a, b := g.origin, p
	if e.pointer.Mods.Alt {
		a = g.origin.Sub(p.Sub(g.origin))
	}
	box := geo.BoxFrom(a, b)
	if e.pointer.Mods.Shift {
		k := math.Max(box.W, box.H)
		if p.X < g.origin.X && !e.pointer.Mods.Alt {
			box.X = g.origin.X - k
		}
		if p.Y < g.origin.Y && !e.pointer.Mods.Alt {
			box.Y = g.origin.Y - k
		}
		box.W, box.H = k, k
	}
	e.update(g.id, func(s *doc.Shape) {
		// A shape started in a frame lives in the frame's space.
		o := e.parentMat(s.Parent).Invert().Apply(geo.V(box.X, box.Y))
		s.X, s.Y, s.W, s.H = o.X, o.Y, math.Max(box.W, 1), math.Max(box.H, 1)
	})
}

func (g *createBox) up(e *Editor) {
	if g.id == "" {
		// A click: a shape of its default size, centered on the pointer.
		w, h := 100.0, 100.0
		if e.Tool == ToolFrame {
			w, h = 320, 180
		}
		e.begin()
		p := e.pointer.Page
		g.id = e.newBoxShape(geo.V(p.X-w/2, p.Y-h/2), w, h)
	}
	if e.Tool == ToolFrame {
		e.adoptIntoFrame(g.id)
	}
	e.Select(g.id)
	e.commit()
	e.finishTool()
}

func (g *createBox) cancel(e *Editor) {
	if g.id != "" {
		e.rollback()
	}
}

func (e *Editor) snapPoint(p geo.Vec) geo.Vec {
	if e.Grid {
		g := e.gridSize()
		return geo.V(math.Round(p.X/g)*g, math.Round(p.Y/g)*g)
	}
	return p
}

// topIndex returns an index above a parent's children.
func (e *Editor) topIndex(parent doc.ID) string {
	kids := e.Children(parent)
	if len(kids) == 0 {
		return doc.FirstIndex
	}
	return doc.IndexBetween(e.Doc.Shapes[kids[len(kids)-1]].Index, "")
}

// dropParent returns the parent of a shape made at a point: the frame there,
// or the page.
func (e *Editor) dropParent(p geo.Vec) doc.ID {
	if f := e.frameAt(p, nil); f != "" {
		return f
	}
	return e.Page
}

// place puts a new shape under parent, at a point of the page.
func (e *Editor) place(s *doc.Shape, p geo.Vec) {
	s.Parent = e.dropParent(p)
	if s.Parent == "" {
		s.Parent = e.Page
	}
	s.Index = e.topIndex(s.Parent)
	q := e.parentMat(s.Parent).Invert().Apply(p)
	s.X, s.Y = q.X, q.Y
	s.Rotation = -e.parentMat(s.Parent).Rotation()
	s.Opacity = e.Next.Opacity
}

func (e *Editor) newBoxShape(at geo.Vec, w, h float64) doc.ID {
	id := doc.NewID("shape")
	if e.Tool == ToolFrame {
		s := doc.NewShape(id, doc.TypeFrame)
		s.W, s.H, s.X, s.Y = w, h, at.X, at.Y
		s.Parent, s.Index = e.Page, e.topIndex(e.Page)
		s.Opacity = e.Next.Opacity
		e.put(s)
		return id
	}
	s := doc.NewShape(id, doc.TypeGeo)
	s.Geo = e.Next.Geo
	s.W, s.H = w, h
	e.applyNext(s)
	e.place(s, at)
	e.put(s)
	return id
}

// applyNext gives a new shape the current style.
func (e *Editor) applyNext(s *doc.Shape) {
	n := e.Next
	s.Color, s.Fill, s.Dash, s.Size, s.Font = n.Color, n.Fill, n.Dash, n.Size, n.Font
	s.LabelColor = "black"
	s.Align, s.VAlign = n.Align, n.VAlign
	s.Kind, s.HeadStart, s.HeadEnd, s.Spline = n.Kind, n.HeadStart, n.HeadEnd, n.Spline
	if s.Type == doc.TypeText {
		s.TextAlign = n.TextAlign
	}
	s.Opacity = n.Opacity
}

// adoptIntoFrame moves what a new frame wraps into it.
func (e *Editor) adoptIntoFrame(frame doc.ID) {
	box := e.PageBounds(frame)
	for _, id := range slices.Clone(e.Children(e.Page)) {
		if id == frame {
			continue
		}
		if box.ContainsBox(e.PageBounds(id)) {
			e.reparent(id, frame)
		}
	}
}

// clickCreate is a click that makes a text or a note there.
type clickCreate struct{ kind string }

func (g *clickCreate) move(e *Editor) {}
func (g *clickCreate) up(e *Editor) {
	if g.kind == doc.TypeText {
		e.createTextAt(e.pointer.Page)
	} else {
		e.createNoteAt(e.pointer.Page)
	}
	e.finishTool()
}
func (g *clickCreate) cancel(e *Editor) {}

func (e *Editor) createTextAt(p geo.Vec) doc.ID {
	e.begin()
	s := doc.NewShape(doc.NewID("shape"), doc.TypeText)
	e.applyNext(s)
	h := fontSizeOf(s.Size) * LineHeight
	e.place(s, geo.V(p.X, p.Y-h/2))
	e.put(s)
	e.Select(s.ID)
	e.StartEditing(s.ID)
	return s.ID
}

func (e *Editor) createNoteAt(p geo.Vec) doc.ID {
	e.begin()
	s := doc.NewShape(doc.NewID("shape"), doc.TypeNote)
	e.applyNext(s)
	e.place(s, geo.V(p.X-noteSize/2, p.Y-noteSize/2))
	e.put(s)
	e.Select(s.ID)
	e.StartEditing(s.ID)
	return s.ID
}

// --- text editing ---

// StartEditing starts editing a shape's text.
func (e *Editor) StartEditing(id doc.ID) {
	s := e.Doc.Shapes[id]
	if s == nil || e.ReadOnly {
		return
	}
	e.begin()
	e.Editing = id
	if s.Type == doc.TypeFrame {
		e.EditText = s.Name
	} else {
		e.EditText = s.Text
	}
	e.Hovered = ""
}

// EditChanged takes the text being edited, as it is typed.
func (e *Editor) EditChanged() {
	id := e.Editing
	s := e.Doc.Shapes[id]
	if s == nil {
		return
	}
	text := e.EditText
	e.update(id, func(c *doc.Shape) {
		if c.Type == doc.TypeFrame {
			c.Name = text
			return
		}
		c.SetText(text)
		e.fitLabel(c)
	})
}

// fitLabel grows a shape to hold its label.
func (e *Editor) fitLabel(s *doc.Shape) {
	switch s.Type {
	case doc.TypeGeo:
		size := labelSizeOf(s.Size) * s.Scale
		pad := labelPadding * s.Scale
		text := s.Text
		if text == "" {
			s.GrowY = 0
			return
		}
		b := e.Measure(text, s.Font, size, math.Max(s.W-2*pad, 8))
		s.GrowY = math.Max(0, b.H+2*pad-s.H)
	case doc.TypeNote:
		size := labelSizeOf(s.Size) * s.Scale
		pad := labelPadding * s.Scale
		b := e.Measure(s.Text+" ", s.Font, size, noteSize*s.Scale-2*pad)
		s.GrowY = math.Max(0, (b.H+2*pad-noteSize*s.Scale)/s.Scale)
	}
}

// StopEditing ends editing. An empty text shape goes away.
func (e *Editor) StopEditing() {
	id := e.Editing
	if id == "" {
		return
	}
	e.Editing = ""
	s := e.Doc.Shapes[id]
	if s != nil && s.Type == doc.TypeText && len(s.Text) == 0 {
		before := e.tx != nil && e.tx.shapes[id].before == nil
		if before {
			e.rollback()
			e.sel = slices.DeleteFunc(e.sel, func(i doc.ID) bool { return i == id })
			return
		}
		e.deleteTree(id)
		e.sel = slices.DeleteFunc(e.sel, func(i doc.ID) bool { return i == id })
	}
	e.commit()
}

// EditBox returns where the text being edited lies, in its shape's space,
// with its font size and alignment.
func (e *Editor) EditBox() (box geo.Box, size float64, font, align string, c Color) {
	s := e.Doc.Shapes[e.Editing]
	if s == nil {
		return
	}
	pal := e.Palette()
	font = s.Font
	switch s.Type {
	case doc.TypeText:
		size = fontSizeOf(s.Size) * s.Scale
		box = e.Geometry(s).Bounds
		box.H = math.Max(box.H, size*LineHeight)
		align = s.TextAlign
		c = pal.Swatch(s.Color).Solid
	case doc.TypeNote:
		size = labelSizeOf(s.Size) * s.Scale
		box = *e.Geometry(s).Label
		align = s.Align
		c = pal.Swatch(s.Color).NoteText
		if s.LabelColor != "black" {
			c = pal.Swatch(s.LabelColor).Solid
		}
	case doc.TypeGeo:
		size = labelSizeOf(s.Size) * s.Scale
		w, h := s.W, s.H+s.GrowY
		l := e.labelBox(s, w, h, size)
		box = *l
		align = s.Align
		c = pal.Swatch(s.LabelColor).Solid
	case doc.TypeArrow:
		size = arrowLabelSizeOf(s.Size) * s.Scale
		box = *e.arrowLabelBox(s)
		box = box.Expand(-4.25 * s.Scale)
		box.X -= 20
		box.W += 40
		box.H = math.Max(box.H, size*LineHeight)
		align = "middle"
		c = pal.Swatch(s.LabelColor).Solid
	case doc.TypeFrame:
		z := e.Cam().Z
		size = 12 / z
		font = "sans"
		box = geo.Box{X: 0, Y: -24 / z, W: math.Max(s.W, 160/z), H: 20 / z}
		align = "start"
		c = pal.Text
	}
	return
}

// --- arrows ---

// arrowCreate draws a new arrow.
type arrowCreate struct {
	id   doc.ID
	drag *arrowEndDrag
}

func newArrowCreate(e *Editor) *arrowCreate {
	e.begin()
	s := doc.NewShape(doc.NewID("shape"), doc.TypeArrow)
	e.applyNext(s)
	p := e.snapPoint(e.pointer.DownPage)
	s.Parent, s.Index = e.Page, e.topIndex(e.Page)
	s.X, s.Y = p.X, p.Y
	s.Start, s.End = geo.Vec{}, geo.V(0.1, 0.1)
	e.put(s)
	g := &arrowCreate{id: s.ID}
	// The start binds to what is under the press.
	if t := e.bindTargetAt(p, s.ID); t != "" {
		e.bindTerminal(s.ID, "start", t, p, e.pointer.Mods.Cmd)
	}
	g.drag = &arrowEndDrag{id: s.ID, term: "end", nested: true}
	return g
}

func (g *arrowCreate) move(e *Editor) { g.drag.move(e) }

func (g *arrowCreate) up(e *Editor) {
	e.Hint = nil
	s := e.Doc.Shapes[g.id]
	info := e.Arrow(s)
	if !e.dragged() || info.RawStart.Dist(info.RawEnd) < 3 {
		// A click draws a short arrow rightward, as nothing would show.
		if !e.dragged() {
			e.update(g.id, func(c *doc.Shape) { c.End = geo.V(120, 0) })
		}
	}
	e.Select(g.id)
	e.commit()
	e.finishTool()
}

func (g *arrowCreate) cancel(e *Editor) {
	e.rollback()
	e.Hint = nil
}

// bindTargetAt returns the shape an arrow's end at a point would bind to.
func (e *Editor) bindTargetAt(p geo.Vec, arrow doc.ID) doc.ID {
	var walk func(parent doc.ID) doc.ID
	walk = func(parent doc.ID) doc.ID {
		kids := e.Children(parent)
		for i := len(kids) - 1; i >= 0; i-- {
			id := kids[i]
			if id == arrow {
				continue
			}
			s := e.Doc.Shapes[id]
			if s.Locked || s.Type == doc.TypeHighlight || s.Type == doc.TypeArrow || s.Type == doc.TypeLine {
				continue
			}
			if s.Type == doc.TypeGroup {
				if k := walk(id); k != "" {
					return k
				}
				continue
			}
			q := e.PageMat(id).Invert().Apply(p)
			g := e.Geometry(s)
			if s.Type == doc.TypeFrame {
				if !g.Bounds.Contains(q) {
					continue
				}
				if k := walk(id); k != "" {
					return k
				}
				return id
			}
			if s.Type == doc.TypeDraw {
				if !g.Closed && geo.DistToPolyline(q, g.Outline, false) > e.margin() {
					continue
				}
				if g.Closed && !geo.PointInPolygon(q, g.Outline) && geo.DistToPolyline(q, g.Outline, true) > e.margin() {
					continue
				}
				return id
			}
			if g.Bounds.Expand(e.margin()).Contains(q) && (geo.PointInPolygon(q, g.Outline) || geo.DistToPolyline(q, g.Outline, true) <= e.margin()) {
				return id
			}
		}
		return ""
	}
	return walk(e.Page)
}

// bindTerminal ties an end of an arrow to a shape at a point of the page.
// Near the shape's middle it aims at the middle; elsewhere, or if exact, at
// the point itself.
func (e *Editor) bindTerminal(arrow doc.ID, term string, target doc.ID, p geo.Vec, exact bool) {
	t := e.Doc.Shapes[target]
	if t == nil {
		return
	}
	box := e.Geometry(t).Bounds
	q := e.PageMat(target).Invert().Apply(p)
	a := geo.V((q.X-box.X)/math.Max(box.W, 1e-6), (q.Y-box.Y)/math.Max(box.H, 1e-6))
	precise := exact || math.Abs(a.X-0.5) > 0.25 || math.Abs(a.Y-0.5) > 0.25
	b := e.bindingOf(arrow, term)
	nb := &doc.Binding{ID: doc.NewID("binding"), Type: "arrow", From: arrow, To: target, Terminal: term, Anchor: a, IsExact: exact, IsPrecise: precise}
	if b != nil {
		nb.ID, nb.Meta, nb.Raw = b.ID, b.Meta, b.Raw
	}
	e.putBinding(nb)
}

// unbind lets an end of an arrow go, leaving it where it shows.
func (e *Editor) unbind(arrow doc.ID, term string) {
	s := e.Doc.Shapes[arrow]
	b := e.bindingOf(arrow, term)
	if s == nil || b == nil {
		return
	}
	info := e.Arrow(s)
	e.update(arrow, func(c *doc.Shape) {
		if term == "start" {
			c.Start = info.Start
		} else {
			c.End = info.End
		}
	})
	e.removeBinding(b.ID)
}

// arrowEndDrag moves an end of an arrow, binding it to what it is over.
type arrowEndDrag struct {
	id     doc.ID
	term   string
	nested bool
	init   *doc.Shape
}

func newArrowEndDrag(e *Editor, term string) *arrowEndDrag {
	e.begin()
	return &arrowEndDrag{id: e.sel[0], term: term, init: e.Doc.Shapes[e.sel[0]]}
}

func (g *arrowEndDrag) move(e *Editor) {
	p := e.snapPoint(e.pointer.Page)
	s := e.Doc.Shapes[g.id]
	if s == nil {
		return
	}
	// Cmd (the snap modifier) keeps it free of what it is over.
	target := doc.ID("")
	if !e.pointer.Mods.Cmd {
		target = e.bindTargetAt(p, g.id)
	}
	e.Hint = nil
	if target != "" {
		e.Hint = map[doc.ID]bool{target: true}
		e.bindTerminal(g.id, g.term, target, p, e.pointer.Mods.Alt)
		return
	}
	if b := e.bindingOf(g.id, g.term); b != nil {
		e.removeBinding(b.ID)
	}
	local := e.PageMat(g.id).Invert().Apply(p)
	e.update(g.id, func(c *doc.Shape) {
		if g.term == "start" {
			c.Start = local
		} else {
			c.End = local
		}
	})
}

func (g *arrowEndDrag) up(e *Editor) {
	e.Hint = nil
	e.commit()
}

func (g *arrowEndDrag) cancel(e *Editor) {
	e.Hint = nil
	e.rollback()
}

// arrowBendDrag bends an arrow by its middle handle, or moves the corner
// of an elbow arrow.
type arrowBendDrag struct{ id doc.ID }

func newArrowBendDrag(e *Editor) *arrowBendDrag {
	e.begin()
	return &arrowBendDrag{id: e.sel[0]}
}

func (g *arrowBendDrag) move(e *Editor) {
	s := e.Doc.Shapes[g.id]
	if s == nil {
		return
	}
	info := e.Arrow(s)
	q := e.PageMat(g.id).Invert().Apply(e.pointer.Page)
	bend := bendFromPoint(info.RawStart, info.RawEnd, q)
	if math.Abs(bend) < 8/e.Cam().Z {
		bend = 0
	}
	e.update(g.id, func(c *doc.Shape) { c.Bend = bend })
}

func (g *arrowBendDrag) up(e *Editor)     { e.commit() }
func (g *arrowBendDrag) cancel(e *Editor) { e.rollback() }

// --- lines ---

type linePointDrag struct {
	id    doc.ID
	index int
}

func newLineCreate(e *Editor) *lineCreate {
	e.begin()
	s := doc.NewShape(doc.NewID("shape"), doc.TypeLine)
	e.applyNext(s)
	p := e.snapPoint(e.pointer.DownPage)
	s.Parent, s.Index = e.Page, e.topIndex(e.Page)
	s.X, s.Y = p.X, p.Y
	e.put(s)
	return &lineCreate{id: s.ID}
}

// lineCreate draws a new line by its end.
type lineCreate struct{ id doc.ID }

func (g *lineCreate) move(e *Editor) {
	s := e.Doc.Shapes[g.id]
	q := e.PageMat(g.id).Invert().Apply(e.snapPoint(e.pointer.Page))
	if e.pointer.Mods.Shift {
		// In steps of 45°.
		a := math.Round(math.Atan2(q.Y, q.X)/(math.Pi/4)) * (math.Pi / 4)
		q = geo.V(q.Len(), 0).Rot(a)
	}
	e.update(s.ID, func(c *doc.Shape) {
		pts := slices.Clone(c.Points)
		pts[1].X, pts[1].Y = q.X, q.Y
		c.Points = pts
	})
}

func (g *lineCreate) up(e *Editor) {
	if !e.dragged() {
		e.update(g.id, func(c *doc.Shape) {
			pts := slices.Clone(c.Points)
			pts[1].X, pts[1].Y = 100, 0
			c.Points = pts
		})
	}
	e.Select(g.id)
	e.commit()
	e.finishTool()
}

func (g *lineCreate) cancel(e *Editor) { e.rollback() }

func newLinePointDrag(e *Editor, h Handle) *linePointDrag {
	e.begin()
	id := e.sel[0]
	index := h.Index
	if h.Virtual {
		// A midpoint taken becomes a vertex.
		e.update(id, func(c *doc.Shape) {
			pts := slices.Clone(c.Points)
			a, b := pts[index-1], pts[index]
			n := doc.LinePoint{ID: string(doc.NewID("p")), Index: doc.IndexBetween(a.Index, b.Index), X: (a.X + b.X) / 2, Y: (a.Y + b.Y) / 2}
			c.Points = slices.Insert(pts, index, n)
		})
	}
	return &linePointDrag{id: id, index: index}
}

func (g *linePointDrag) move(e *Editor) {
	q := e.PageMat(g.id).Invert().Apply(e.snapPoint(e.pointer.Page))
	e.update(g.id, func(c *doc.Shape) {
		pts := slices.Clone(c.Points)
		pts[g.index].X, pts[g.index].Y = q.X, q.Y
		c.Points = pts
	})
}

func (g *linePointDrag) up(e *Editor)     { e.commit() }
func (g *linePointDrag) cancel(e *Editor) { e.rollback() }

// --- freehand ---

type freehand struct {
	id       doc.ID
	last     geo.Vec
	straight bool
}

func newFreehand(e *Editor) *freehand {
	e.begin()
	typ := doc.TypeDraw
	if e.Tool == ToolHighlight {
		typ = doc.TypeHighlight
	}
	s := doc.NewShape(doc.NewID("shape"), typ)
	e.applyNext(s)
	if typ == doc.TypeHighlight {
		s.Dash, s.Fill = "solid", "none"
	}
	p := e.pointer.Page
	s.Segments = []doc.Segment{{Type: "free", Points: []doc.PointZ{{Z: 0.5}}}}
	e.place(s, p)
	e.put(s)
	return &freehand{id: s.ID, last: p}
}

func (g *freehand) move(e *Editor) {
	p := e.pointer.Page
	z := e.Cam().Z
	if p.Dist(g.last) < 0.8/z && !e.pointer.Mods.Shift {
		return
	}
	s := e.Doc.Shapes[g.id]
	q := e.PageMat(g.id).Invert().Apply(p)
	pt := doc.PointZ{X: q.X, Y: q.Y, Z: 0.5}
	e.update(g.id, func(c *doc.Shape) {
		// Segments are copied as they grow: earlier records keep theirs.
		segs := slices.Clone(s.Segments)
		last := &segs[len(segs)-1]
		if e.pointer.Mods.Shift {
			// Shift draws a straight line from where the freehand part ended.
			if last.Type == "free" {
				from := last.Points[len(last.Points)-1]
				segs = append(segs, doc.Segment{Type: "straight", Points: []doc.PointZ{from, pt}})
			} else {
				last.Points = []doc.PointZ{last.Points[0], pt}
			}
		} else if last.Type == "straight" {
			segs = append(segs, doc.Segment{Type: "free", Points: []doc.PointZ{last.Points[len(last.Points)-1], pt}})
		} else {
			// append may share storage past the end of the old record's
			// slice, which no one reads.
			last.Points = append(last.Points, pt)
		}
		c.Segments = segs
	})
	g.last = p
}

func (g *freehand) up(e *Editor) {
	s := e.Doc.Shapes[g.id]
	e.update(g.id, func(c *doc.Shape) {
		c.IsComplete = true
		if s.Type == doc.TypeDraw {
			pts := strokePoints(s)
			// A stroke that ends where it began closes, and can take a fill.
			if len(pts) > 8 && pts[0].Dist(pts[len(pts)-1]) < math.Max(strokeOf(s.Size)*2, 12) && geo.PolyLength(pts) > 60 {
				c.IsClosed = true
			}
		}
	})
	e.commit()
}

func (g *freehand) cancel(e *Editor) { e.rollback() }

// --- eraser ---

type eraserGesture struct {
	last  geo.Vec
	trail *Trail
}

func newEraser(e *Editor) *eraserGesture {
	g := &eraserGesture{last: e.pointer.Page, trail: &Trail{Live: true}}
	e.Trails = append(e.Trails, g.trail)
	e.Erasing = map[doc.ID]bool{}
	g.hit(e, e.pointer.Page, e.pointer.Page)
	return g
}

func (g *eraserGesture) hit(e *Editor, a, b geo.Vec) {
	margin := 4 / e.Cam().Z
	var walk func(parent doc.ID)
	walk = func(parent doc.ID) {
		for _, id := range e.Children(parent) {
			s := e.Doc.Shapes[id]
			if s.Locked {
				continue
			}
			if s.Type == doc.TypeGroup {
				walk(id)
				continue
			}
			if s.Type == doc.TypeFrame {
				walk(id)
				if e.Erasing[id] {
					continue
				}
			}
			bounds := e.PageBounds(id).Expand(margin + 12)
			if !bounds.Contains(a) && !bounds.Contains(b) {
				continue
			}
			g0 := e.Geometry(s)
			pts := e.PageOutline(id)
			hit := geo.SegmentHitsPolyline(a, b, pts, g0.Closed, margin+strokeOf(s.Size)*s.Scale/2)
			if !hit && g0.Filled && g0.Closed && geo.PointInPolygon(b, pts) {
				hit = true
			}
			if hit {
				e.Erasing[e.outermostSelectable(id)] = true
			}
		}
	}
	walk(e.Page)
}

func (g *eraserGesture) move(e *Editor) {
	p := e.pointer.Page
	g.trail.Points = append(g.trail.Points, TrailPoint{p, e.Now})
	g.hit(e, g.last, p)
	g.last = p
}

func (g *eraserGesture) up(e *Editor) {
	g.trail.Live = false
	ids := keysOf(e.Erasing)
	e.Erasing = nil
	if len(ids) > 0 {
		e.transact(func() { e.deleteShapes(ids) })
	}
}

func (g *eraserGesture) cancel(e *Editor) {
	g.trail.Live = false
	e.Erasing = nil
}

// --- laser ---

type laserGesture struct{ trail *Trail }

func newLaser(e *Editor) *laserGesture {
	g := &laserGesture{trail: &Trail{Laser: true, Live: true}}
	g.trail.Points = append(g.trail.Points, TrailPoint{e.pointer.Page, e.Now})
	e.Trails = append(e.Trails, g.trail)
	return g
}

func (g *laserGesture) move(e *Editor) {
	g.trail.Points = append(g.trail.Points, TrailPoint{e.pointer.Page, e.Now})
}
func (g *laserGesture) up(e *Editor)     { g.trail.Live = false }
func (g *laserGesture) cancel(e *Editor) { g.trail.Live = false }

// TrailLife is how long a trail's points last, in seconds.
const TrailLife = 1.0

// AgeTrails drops what has faded of the trails, reporting whether any are
// left to animate.
func (e *Editor) AgeTrails(now float64) bool {
	e.Now = now
	out := e.Trails[:0]
	for _, t := range e.Trails {
		life := TrailLife
		if !t.Laser {
			life = 0.25
		}
		i := 0
		for i < len(t.Points) && now-t.Points[i].T > life {
			i++
		}
		t.Points = t.Points[i:]
		if t.Live || len(t.Points) > 0 {
			out = append(out, t)
		}
	}
	e.Trails = out
	return len(out) > 0
}

// --- right click ---

func (e *Editor) rightClick(page geo.Vec) {
	r := e.Hit(page, e.margin(), true)
	if r.ID == "" {
		box, rot := e.SelectionBox()
		if len(e.sel) > 0 && box.Contains(geo.Rotate(-rot).Apply(page)) {
			return
		}
		e.SelectNone()
		return
	}
	target := e.outermostSelectable(r.ID)
	if !e.IsSelected(target) {
		e.Select(target)
	}
}
