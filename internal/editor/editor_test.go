package editor

import (
	"bytes"
	"math"
	"os"
	"strings"
	"testing"

	"godraw/internal/doc"
	"godraw/internal/geo"
)

// fakeMeasure lays text out in a fixed-width font, so that tests do not
// depend on the fonts of the machine.
func fakeMeasure(text, font string, size, width float64) TextBlock {
	cw := size * 0.5
	line := size * LineHeight
	b := TextBlock{Line: line}
	for i, l := range strings.Split(text, "\n") {
		w := float64(len(l)) * cw
		b.Lines = append(b.Lines, TextLine{Text: l, Y: float64(i) * line, W: w})
		b.W = math.Max(b.W, w)
	}
	b.H = float64(len(b.Lines)) * line
	return b
}

type harness struct {
	*Editor
	t       *testing.T
	changes int
	clock   float64
}

func newHarness(t *testing.T) *harness {
	h := &harness{Editor: New(doc.New(), fakeMeasure), t: t}
	h.OnChange = func(*doc.Drawing) { h.changes++ }
	return h
}

func (h *harness) dragMods(x0, y0, x1, y1 float64, m Mods) {
	h.clock += 0.1
	h.PointerMove(x0, y0, m, h.clock)
	h.PointerDown(x0, y0, 0, 1, m, h.clock)
	const n = 6
	for i := 1; i <= n; i++ {
		f := float64(i) / n
		h.PointerMove(x0+(x1-x0)*f, y0+(y1-y0)*f, m, h.clock)
	}
	h.PointerUp(x1, y1, m, h.clock)
}

func (h *harness) drag(x0, y0, x1, y1 float64) { h.dragMods(x0, y0, x1, y1, Mods{}) }

func (h *harness) click(x, y float64) { h.drag(x, y, x, y) }

func (h *harness) rect(x, y, w, ht float64) *doc.Shape {
	h.SetGeoTool("rectangle")
	h.drag(x, y, x+w, y+ht)
	return h.Doc.Shapes[h.Selection()[0]]
}

func (h *harness) shape(id doc.ID) *doc.Shape {
	h.t.Helper()
	s := h.Doc.Shapes[id]
	if s == nil {
		h.t.Fatalf("no shape %s", id)
	}
	return s
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func TestCreateAndUndo(t *testing.T) {
	h := newHarness(t)
	s := h.rect(100, 100, 200, 100)
	if !near(s.X, 100) || !near(s.W, 200) || !near(s.H, 100) {
		t.Errorf("rect %+v", s)
	}
	if h.Tool != ToolSelect {
		t.Errorf("tool %q", h.Tool)
	}
	if h.changes != 1 {
		t.Errorf("%d changes for one drag", h.changes)
	}
	h.Undo()
	if len(h.Doc.Shapes) != 0 || len(h.Selection()) != 0 {
		t.Errorf("after undo: %d shapes, selection %v", len(h.Doc.Shapes), h.Selection())
	}
	h.Redo()
	if h.Doc.Shapes[s.ID] != s {
		t.Error("redo did not restore the record")
	}
	if len(h.Selection()) != 1 {
		t.Errorf("redo selection %v", h.Selection())
	}
}

func TestDefaultSizeOnClick(t *testing.T) {
	h := newHarness(t)
	h.SetGeoTool("ellipse")
	h.click(300, 300)
	s := h.shape(h.Selection()[0])
	if s.Geo != "ellipse" || !near(s.W, 100) || !near(s.X, 250) {
		t.Errorf("clicked shape %+v", s)
	}
}

func TestToolLock(t *testing.T) {
	h := newHarness(t)
	h.ToolLocked = true
	h.SetGeoTool("rectangle")
	h.drag(10, 10, 60, 60)
	h.drag(100, 10, 160, 60)
	if h.Tool != ToolGeo || len(h.Doc.Shapes) != 2 {
		t.Errorf("tool %q, %d shapes", h.Tool, len(h.Doc.Shapes))
	}
}

func TestSelectAndHollow(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 200, 200)
	h.SelectNone()
	// A click inside a hollow shape selects it...
	h.click(200, 200)
	if !h.IsSelected(a.ID) {
		t.Error("clicking inside a hollow shape did not select it")
	}
	h.SelectNone()
	// ...but a drag from there is a brush, not a move.
	h.drag(150, 150, 170, 170)
	if s := h.shape(a.ID); !near(s.X, 100) {
		t.Errorf("hollow shape moved to %v", s.X)
	}
	// A click on nothing clears.
	h.Select(a.ID)
	h.click(600, 600)
	if len(h.Selection()) != 0 {
		t.Error("clicking the canvas did not clear the selection")
	}
}

func TestBrush(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 50, 50)
	b := h.rect(300, 100, 50, 50)
	h.SelectNone()
	h.drag(50, 50, 200, 200)
	if !h.IsSelected(a.ID) || h.IsSelected(b.ID) {
		t.Errorf("brush selected %v", h.Selection())
	}
	h.SelectNone()
	h.drag(50, 50, 400, 200)
	if len(h.Selection()) != 2 {
		t.Errorf("wide brush selected %v", h.Selection())
	}
	// With Cmd, only what it wraps whole: this box only touches the second.
	h.SelectNone()
	h.dragMods(50, 50, 320, 200, Mods{Cmd: true})
	if !h.IsSelected(a.ID) || h.IsSelected(b.ID) {
		t.Errorf("wrap brush selected %v", h.Selection())
	}
	if h.Brush != nil {
		t.Error("brush left showing")
	}
}

func TestTranslateAndCancel(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 100, 100)
	h.drag(150, 150, 250, 180)
	if s := h.shape(a.ID); !near(s.X, 200) || !near(s.Y, 130) {
		t.Errorf("moved to %v,%v", s.X, s.Y)
	}
	// Shift keeps to an axis.
	h.dragMods(250, 180, 350, 190, Mods{Shift: true})
	if s := h.shape(a.ID); !near(s.X, 300) || !near(s.Y, 130) {
		t.Errorf("shift-moved to %v,%v", s.X, s.Y)
	}
	// Escape abandons a drag, leaving nothing to undo.
	before := len(h.undo)
	h.PointerMove(350, 180, Mods{}, 0)
	h.PointerDown(350, 180, 0, 1, Mods{}, 0)
	h.PointerMove(450, 300, Mods{}, 0)
	if s := h.shape(a.ID); near(s.X, 300) {
		t.Fatal("did not move during the drag")
	}
	h.Cancel()
	if s := h.shape(a.ID); !near(s.X, 300) || !near(s.Y, 130) {
		t.Errorf("cancel left it at %v,%v", s.X, s.Y)
	}
	if len(h.undo) != before {
		t.Error("a canceled drag left a step of undo")
	}
	h.PointerUp(450, 300, Mods{}, 0)
}

func TestAltDragClones(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 100, 100)
	h.dragMods(150, 150, 400, 150, Mods{Alt: true})
	if len(h.Doc.Shapes) != 2 {
		t.Fatalf("%d shapes after alt-drag", len(h.Doc.Shapes))
	}
	if s := h.shape(a.ID); !near(s.X, 100) {
		t.Errorf("original moved to %v", s.X)
	}
	c := h.shape(h.Selection()[0])
	if c.ID == a.ID || !near(c.X, 350) {
		t.Errorf("clone %+v", c)
	}
	h.Undo()
	if len(h.Doc.Shapes) != 1 {
		t.Errorf("%d shapes after undoing a clone", len(h.Doc.Shapes))
	}
}

func TestResize(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 100, 100)
	// By the bottom right corner.
	h.drag(200, 200, 300, 250)
	if s := h.shape(a.ID); !near(s.X, 100) || !near(s.W, 200) || !near(s.H, 150) {
		t.Errorf("resized to %v,%v %vx%v", s.X, s.Y, s.W, s.H)
	}
	// By the left side, past the right: it flips, and stays a positive box.
	h.drag(100, 175, 400, 175)
	s := h.shape(a.ID)
	if !near(s.X, 300) || !near(s.W, 100) || s.W <= 0 || !s.FlipX {
		t.Errorf("flipped to x %v w %v flip %v", s.X, s.W, s.FlipX)
	}
}

func TestResizeWithShiftKeepsProportions(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 100, 50)
	h.dragMods(200, 150, 400, 160, Mods{Shift: true})
	if s := h.shape(a.ID); !near(s.W/s.H, 2) {
		t.Errorf("proportions %vx%v", s.W, s.H)
	}
}

func TestResizeRotatedShape(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 100, 100)
	h.update(a.ID, func(s *doc.Shape) { s.Rotation = math.Pi / 2 })
	h.commit()
	// A quarter turn around its origin: its opposite corner must stay put
	// as it stretches along its own axes.
	s := h.shape(a.ID)
	m := e0(h, s.ID)
	fixed := m.Apply(geo.V(0, 0))
	far := m.Apply(geo.V(s.W, s.H))
	sc := h.ToScreen(far)
	h.drag(sc.X, sc.Y, sc.X+0, sc.Y+50)
	s = h.shape(a.ID)
	m = e0(h, s.ID)
	if got := m.Apply(geo.V(0, 0)); got.Dist(fixed) > 0.01 {
		t.Errorf("anchor moved from %v to %v", fixed, got)
	}
	if !near(s.W, 150) || !near(s.H, 100) {
		t.Errorf("size %vx%v", s.W, s.H)
	}
}

func e0(h *harness, id doc.ID) geo.Mat { return h.PageMat(id) }

func TestRotate(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 100, 100)
	// Beyond the bottom right corner, then around the center.
	h.drag(212, 212, 88, 212)
	s := h.shape(a.ID)
	if math.Abs(geo.ShortAngle(s.Rotation, math.Pi/2)) > 0.05 {
		t.Errorf("rotation %v", s.Rotation)
	}
	// The center stays.
	c := h.PageMat(a.ID).Apply(geo.V(50, 50))
	if c.Dist(geo.V(150, 150)) > 0.5 {
		t.Errorf("center moved to %v", c)
	}
	// With Shift, in steps of 15°.
	h.dragMods(c.X+70, c.Y+70, c.X-17, c.Y+95, Mods{Shift: true})
	s = h.shape(a.ID)
	steps := s.Rotation / (math.Pi / 12)
	if math.Abs(steps-math.Round(steps)) > 1e-6 {
		t.Errorf("rotation %v is not a step", s.Rotation)
	}
}

func TestArrowBindsAndFollows(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 100, 100)
	b := h.rect(500, 100, 100, 100)
	h.Next.Dash = "solid"
	h.SetTool(ToolArrow)
	h.drag(150, 150, 550, 150)
	arrow := h.shape(h.Selection()[0])
	if arrow.Type != doc.TypeArrow {
		t.Fatalf("selected %s", arrow.Type)
	}
	bs, be := h.bindingOf(arrow.ID, "start"), h.bindingOf(arrow.ID, "end")
	if bs == nil || be == nil || bs.To != a.ID || be.To != b.ID {
		t.Fatalf("bindings %+v %+v", bs, be)
	}
	info := h.Arrow(arrow)
	start, end := h.PageMat(arrow.ID).Apply(info.Start), h.PageMat(arrow.ID).Apply(info.End)
	// It stops short of each shape's edge, outside each.
	if start.X < 200 || start.X > 215 || end.X > 500 || end.X < 485 {
		t.Errorf("arrow from %v to %v", start, end)
	}
	if !near(start.Y, 150) {
		t.Errorf("arrow start y %v", start.Y)
	}
	// Moved with the target; the record of the arrow itself is untouched.
	h.Select(b.ID)
	h.drag(550, 150, 550, 450)
	if h.shape(arrow.ID) != arrow {
		t.Error("moving a target changed the arrow's record")
	}
	info = h.Arrow(arrow)
	end = h.PageMat(arrow.ID).Apply(info.End)
	if end.Y < 300 {
		t.Errorf("arrow did not follow: end %v", end)
	}
	// Deleting the target leaves the arrow where it shows, unbound.
	h.Select(b.ID)
	h.Delete()
	if h.bindingOf(arrow.ID, "end") != nil {
		t.Error("binding outlived its target")
	}
	freed := h.shape(arrow.ID)
	if h.PageMat(arrow.ID).Apply(freed.End).Dist(end) > 0.01 {
		t.Errorf("freed end at %v, showed at %v", freed.End, end)
	}
	h.Undo()
	if h.bindingOf(arrow.ID, "end") == nil || h.Doc.Shapes[b.ID] == nil {
		t.Error("undo did not restore the target and its binding")
	}
}

func TestArrowCmdStaysFree(t *testing.T) {
	h := newHarness(t)
	h.rect(100, 100, 100, 100)
	h.SetTool(ToolArrow)
	h.dragMods(150, 150, 400, 150, Mods{Cmd: true})
	arrow := h.Selection()[0]
	if h.bindingOf(arrow, "end") != nil {
		t.Error("end bound over nothing")
	}
}

func TestArrowBend(t *testing.T) {
	h := newHarness(t)
	h.SetTool(ToolArrow)
	h.drag(100, 100, 300, 100)
	id := h.Selection()[0]
	// By its middle handle, downward: tldraw's sign for this is positive.
	h.drag(200, 100, 200, 150)
	s := h.shape(id)
	if !near(s.Bend, 50) {
		t.Errorf("bend %v", s.Bend)
	}
	info := h.Arrow(s)
	if !info.Arc {
		t.Fatal("not an arc")
	}
	mid := h.PageMat(id).Apply(info.Mid)
	if mid.Dist(geo.V(200, 150)) > 0.5 {
		t.Errorf("middle at %v", mid)
	}
}

func TestLineMidpointBecomesVertex(t *testing.T) {
	h := newHarness(t)
	h.SetTool(ToolLine)
	h.drag(100, 100, 300, 100)
	id := h.Selection()[0]
	if n := len(h.shape(id).Points); n != 2 {
		t.Fatalf("%d points", n)
	}
	h.drag(200, 100, 200, 200)
	s := h.shape(id)
	if len(s.Points) != 3 {
		t.Fatalf("%d points after dragging a midpoint", len(s.Points))
	}
	if !near(s.Points[1].X, 100) || !near(s.Points[1].Y, 100) {
		t.Errorf("new vertex %+v", s.Points[1])
	}
	if !(s.Points[0].Index < s.Points[1].Index && s.Points[1].Index < s.Points[2].Index) {
		t.Errorf("vertices out of order: %+v", s.Points)
	}
}

func TestFreehand(t *testing.T) {
	h := newHarness(t)
	h.SetTool(ToolDraw)
	h.drag(100, 100, 200, 150)
	if h.Tool != ToolDraw {
		t.Errorf("tool %q after a stroke", h.Tool)
	}
	var s *doc.Shape
	for _, v := range h.Doc.Shapes {
		s = v
	}
	if s == nil || !s.IsComplete || len(s.Segments) == 0 || len(s.Segments[0].Points) < 3 {
		t.Fatalf("stroke %+v", s)
	}
	if !near(s.X, 100) || !near(s.Y, 100) {
		t.Errorf("stroke at %v,%v", s.X, s.Y)
	}
	// One undo takes back the whole stroke.
	h.Undo()
	if len(h.Doc.Shapes) != 0 {
		t.Error("undo left part of a stroke")
	}
	// Resizing scales it through its scale props.
	h.Redo()
	id := s.ID
	h.SetTool(ToolSelect)
	h.Select(id)
	b := h.PageBounds(id)
	h.drag(b.MaxX(), b.MaxY(), b.X+b.W*2, b.Y+b.H*2)
	if got := h.PageBounds(id); !near(got.W, b.W*2) || !near(got.X, b.X) {
		t.Errorf("resized bounds %+v from %+v", got, b)
	}
}

func TestEraser(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 100, 100)
	b := h.rect(400, 100, 100, 100)
	h.SetTool(ToolEraser)
	h.drag(50, 150, 150, 150) // across a's left edge only
	if h.Doc.Shapes[a.ID] != nil {
		t.Error("eraser missed the shape it crossed")
	}
	if h.Doc.Shapes[b.ID] == nil {
		t.Error("eraser took a shape it did not touch")
	}
	h.Undo()
	if h.Doc.Shapes[a.ID] == nil {
		t.Error("undo did not bring back what was erased")
	}
}

func TestTextEditing(t *testing.T) {
	h := newHarness(t)
	h.SetTool(ToolText)
	h.click(100, 100)
	if h.Editing == "" {
		t.Fatal("not editing")
	}
	id := h.Editing
	h.EditText = "hi"
	h.EditChanged()
	if s := h.shape(id); s.Text != "hi" {
		t.Errorf("text %q", s.Text)
	}
	b1 := h.Geometry(h.shape(id)).Bounds.W
	h.EditText = "hello world"
	h.EditChanged()
	if b2 := h.Geometry(h.shape(id)).Bounds.W; b2 <= b1 {
		t.Errorf("text did not grow: %v then %v", b1, b2)
	}
	h.StopEditing()
	if h.Editing != "" || h.shape(id).Text != "hello world" {
		t.Error("stop editing")
	}
	h.Undo()
	if h.Doc.Shapes[id] != nil {
		t.Error("one undo should take back the whole new text")
	}
}

func TestEmptyTextGoesAwayLeavingNoHistory(t *testing.T) {
	h := newHarness(t)
	h.SetTool(ToolText)
	h.click(100, 100)
	h.StopEditing()
	if len(h.Doc.Shapes) != 0 {
		t.Errorf("%d shapes", len(h.Doc.Shapes))
	}
	if h.CanUndo() {
		t.Error("an empty text left a step of undo")
	}
	if h.changes != 0 {
		t.Errorf("%d changes", h.changes)
	}
}

func TestLabelGrowsShape(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 100, 60)
	h.StartEditing(a.ID)
	h.EditText = "a\nb\nc\nd\ne"
	h.EditChanged()
	h.StopEditing()
	s := h.shape(a.ID)
	if s.GrowY <= 0 {
		t.Errorf("growY %v", s.GrowY)
	}
	if g := h.Geometry(s); g.Bounds.H <= 60 {
		t.Errorf("bounds %+v", g.Bounds)
	}
}

func TestGroupAndUngroup(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 50, 50)
	b := h.rect(300, 300, 50, 50)
	h.Select(a.ID, b.ID)
	h.Group()
	g := h.shape(h.Selection()[0])
	if g.Type != doc.TypeGroup {
		t.Fatalf("selected %s", g.Type)
	}
	if bounds := h.PageBounds(g.ID); !near(bounds.X, 100) || !near(bounds.MaxX(), 350) {
		t.Errorf("group bounds %+v", bounds)
	}
	// Children keep where they are in the page.
	if p := h.PageMat(a.ID).Apply(geo.Vec{}); p.Dist(geo.V(100, 100)) > 0.01 {
		t.Errorf("child moved to %v", p)
	}
	// A click selects the group, not what it holds.
	h.SelectNone()
	h.click(100, 125)
	if !h.IsSelected(g.ID) {
		t.Errorf("click selected %v", h.Selection())
	}
	// Moving the group moves what it holds.
	h.drag(100, 125, 200, 125)
	if p := h.PageMat(a.ID).Apply(geo.Vec{}); p.Dist(geo.V(200, 100)) > 0.01 {
		t.Errorf("child at %v after moving the group", p)
	}
	h.Ungroup()
	if h.Doc.Shapes[g.ID] != nil {
		t.Error("group remains")
	}
	if p := h.PageMat(a.ID).Apply(geo.Vec{}); p.Dist(geo.V(200, 100)) > 0.01 {
		t.Errorf("child at %v after ungrouping", p)
	}
	if len(h.Selection()) != 2 {
		t.Errorf("selection %v", h.Selection())
	}
	// Deleting all but one of a group's shapes dissolves it.
	h.Group()
	g2 := h.Selection()[0]
	h.Select(a.ID)
	h.Delete()
	if h.Doc.Shapes[g2] != nil {
		t.Error("a group of one remains")
	}
	if s := h.shape(b.ID); s.Parent != h.Page {
		t.Errorf("survivor's parent %s", s.Parent)
	}
}

func TestReorder(t *testing.T) {
	h := newHarness(t)
	a := h.rect(10, 10, 20, 20)
	b := h.rect(40, 10, 20, 20)
	c := h.rect(70, 10, 20, 20)
	order := func() []doc.ID { return h.Children(h.Page) }
	check := func(want ...doc.ID) {
		t.Helper()
		got := order()
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("order %v, want %v", got, want)
				return
			}
		}
	}
	check(a.ID, b.ID, c.ID)
	h.Select(a.ID)
	h.Reorder("front")
	check(b.ID, c.ID, a.ID)
	h.Reorder("back")
	check(a.ID, b.ID, c.ID)
	h.Reorder("forward")
	check(b.ID, a.ID, c.ID)
	h.Reorder("backward")
	check(a.ID, b.ID, c.ID)
	h.Undo()
	check(b.ID, a.ID, c.ID)
}

func TestAlignAndDistribute(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 50, 50)
	b := h.rect(300, 200, 50, 50)
	c := h.rect(400, 300, 50, 50)
	h.Select(a.ID, b.ID, c.ID)
	h.Align("left")
	for _, id := range []doc.ID{a.ID, b.ID, c.ID} {
		if s := h.shape(id); !near(s.X, 100) {
			t.Errorf("x %v", s.X)
		}
	}
	h.Undo()
	h.Distribute(true)
	if s := h.shape(b.ID); !near(s.X, 250) {
		t.Errorf("distributed middle at %v", s.X)
	}
}

func TestFrameTakesAndGivesBack(t *testing.T) {
	h := newHarness(t)
	h.SetTool(ToolFrame)
	h.drag(500, 100, 900, 500)
	frame := h.Selection()[0]
	a := h.rect(100, 100, 50, 50)
	h.drag(125, 125, 700, 300)
	s := h.shape(a.ID)
	if s.Parent != frame {
		t.Fatalf("parent %s after dropping on a frame", s.Parent)
	}
	if p := h.PageMat(a.ID).Apply(geo.Vec{}); p.Dist(geo.V(675, 275)) > 0.01 {
		t.Errorf("at %v after dropping on a frame", p)
	}
	// Moving the frame moves it.
	h.Select(frame)
	h.drag(500, 300, 600, 300)
	if p := h.PageMat(a.ID).Apply(geo.Vec{}); p.Dist(geo.V(775, 275)) > 0.01 {
		t.Errorf("at %v after moving the frame", p)
	}
	// And out again.
	h.Select(a.ID)
	h.drag(800, 300, 100, 300)
	if s := h.shape(a.ID); s.Parent != h.Page {
		t.Errorf("parent %s after dropping outside", s.Parent)
	}
	// What is outside it doesn't show a hit through it, and the frame
	// clips: inside a frame's bounds, its child is found.
}

func TestNewFrameAdoptsWhatItWraps(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 50, 50)
	h.SetTool(ToolFrame)
	h.drag(50, 50, 300, 300)
	if s := h.shape(a.ID); s.Parent != h.Selection()[0] {
		t.Errorf("parent %s", s.Parent)
	}
}

func TestCopyPaste(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 50, 50)
	b := h.rect(400, 100, 50, 50)
	h.SetTool(ToolArrow)
	h.drag(125, 125, 425, 125)
	arrow := h.Selection()[0]
	h.Select(a.ID, b.ID, arrow)
	data, _ := h.CopyData()
	if data == nil {
		t.Fatal("no clipboard data")
	}
	at := geo.V(1000, 1000)
	if err := h.Paste(data, &at); err != nil {
		t.Fatal(err)
	}
	if len(h.Doc.Shapes) != 6 {
		t.Fatalf("%d shapes after paste", len(h.Doc.Shapes))
	}
	if len(h.Doc.Bindings) != 4 {
		t.Errorf("%d bindings after paste", len(h.Doc.Bindings))
	}
	for _, id := range h.Selection() {
		if id == a.ID || id == b.ID || id == arrow {
			t.Error("paste selected an original")
		}
	}
	box, _ := h.SelectionBox()
	if box.Center().Dist(at) > 40 {
		t.Errorf("pasted around %v", box.Center())
	}
	h.Undo()
	if len(h.Doc.Shapes) != 3 || len(h.Doc.Bindings) != 2 {
		t.Errorf("after undo: %d shapes, %d bindings", len(h.Doc.Shapes), len(h.Doc.Bindings))
	}

	// Copying an arrow without its target leaves its end where it shows.
	h.Select(arrow)
	data, _ = h.CopyData()
	if err := h.Paste(data, nil); err != nil {
		t.Fatal(err)
	}
	if len(h.Doc.Bindings) != 2 {
		t.Errorf("a lone arrow's paste made bindings: %d", len(h.Doc.Bindings))
	}
}

func TestDuplicate(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 50, 50)
	h.Duplicate()
	if len(h.Doc.Shapes) != 2 {
		t.Fatalf("%d shapes", len(h.Doc.Shapes))
	}
	d := h.shape(h.Selection()[0])
	if d.ID == a.ID || !near(d.X, 116) {
		t.Errorf("duplicate %+v", d)
	}
}

func TestPages(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 50, 50)
	first := h.Page
	h.NewPage()
	if h.Page == first || len(h.Pages()) != 2 {
		t.Fatalf("pages %v", h.Pages())
	}
	if h.Hit(geo.V(100, 125), 8, false).ID != "" {
		t.Error("a new page shows the old page's shape")
	}
	b := h.rect(10, 10, 50, 50)
	h.SetPage(first)
	h.Select(a.ID)
	if len(h.Children(first)) != 1 {
		t.Error("shape in the wrong page")
	}
	h.MoveToPage(h.PageOf(b.ID))
	if h.PageOf(a.ID) != h.PageOf(b.ID) {
		t.Error("move to page")
	}
	// Deleting a page deletes what is on it, and undo restores both.
	p2 := h.PageOf(b.ID)
	h.DeletePage(p2)
	if len(h.Pages()) != 1 || h.Doc.Shapes[b.ID] != nil {
		t.Errorf("after deleting a page: %d pages", len(h.Pages()))
	}
	h.Undo()
	if len(h.Pages()) != 2 || h.Doc.Shapes[b.ID] == nil {
		t.Error("undo did not restore a deleted page")
	}
	// The last page stays.
	h.DeletePage(h.Pages()[0].ID)
	h.DeletePage(h.Pages()[0].ID)
	if len(h.Pages()) != 1 {
		t.Errorf("%d pages", len(h.Pages()))
	}
}

func TestStyles(t *testing.T) {
	h := newHarness(t)
	a := h.rect(10, 10, 50, 50)
	b := h.rect(100, 10, 50, 50)
	h.Select(a.ID, b.ID)
	h.SetStyle(StyleColor, "red")
	if v, ok := h.SharedStyle(StyleColor); !ok || v != "red" {
		t.Errorf("shared color %q %v", v, ok)
	}
	h.Select(a.ID)
	h.SetStyle(StyleColor, "blue")
	h.Select(a.ID, b.ID)
	if v, _ := h.SharedStyle(StyleColor); v != Mixed {
		t.Errorf("shared color %q", v)
	}
	// What comes next takes the last style.
	h.SelectNone()
	c := h.rect(200, 10, 50, 50)
	if c.Color != "blue" {
		t.Errorf("new shape color %q", c.Color)
	}
	// A locked shape keeps its style.
	h.Select(a.ID)
	h.ToggleLock()
	h.SetStyle(StyleColor, "green")
	if s := h.shape(a.ID); s.Color != "blue" {
		t.Errorf("locked shape turned %q", s.Color)
	}
}

func TestLockedShapes(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 50, 50)
	h.ToggleLock()
	h.SelectNone()
	h.drag(100, 125, 300, 125)
	if s := h.shape(a.ID); !near(s.X, 100) {
		t.Errorf("locked shape moved to %v", s.X)
	}
	h.Select(a.ID)
	h.Delete()
	if h.Doc.Shapes[a.ID] == nil {
		t.Error("locked shape deleted")
	}
}

func TestCamera(t *testing.T) {
	h := newHarness(t)
	p := geo.V(300, 200)
	before := h.ToPage(p)
	h.ZoomAt(p, 2)
	if h.Cam().Z != 2 {
		t.Errorf("zoom %v", h.Cam().Z)
	}
	if h.ToPage(p).Dist(before) > 1e-6 {
		t.Error("zooming moved the point under the pointer")
	}
	h.ZoomAt(p, 1000)
	if h.Cam().Z != maxZoom {
		t.Errorf("zoom %v past the limit", h.Cam().Z)
	}
	if got := h.ToScreen(h.ToPage(geo.V(17, 23))); got.Dist(geo.V(17, 23)) > 1e-6 {
		t.Errorf("screen round trip %v", got)
	}
	// Each page keeps its view.
	first := h.Page
	cam := h.Cam()
	h.NewPage()
	if h.Cam().Z != 1 {
		t.Error("a new page should start at 100%")
	}
	h.SetPage(first)
	if h.Cam() != cam {
		t.Error("a page lost its view")
	}
}

func TestNewPageViewFitsWhatItHolds(t *testing.T) {
	h := newHarness(t)
	h.rect(5000, 5000, 200, 100)
	h.cams = map[doc.ID]Camera{} // as a file that just opened
	h.FitNewPage()
	if !h.ViewBox().ContainsBox(h.PageBounds(h.Children(h.Page)[0])) {
		t.Errorf("view %+v doesn't show the drawing", h.ViewBox())
	}
	// Once seen, a page keeps the view the user left it at.
	h.SetCam(Camera{Z: 2})
	h.FitNewPage()
	if h.Cam().Z != 2 {
		t.Errorf("zoom %v", h.Cam().Z)
	}
	// An empty page starts at 100%, at the origin.
	h.NewPage()
	h.FitNewPage()
	if h.Cam() != (Camera{Z: 1}) {
		t.Errorf("empty page camera %+v", h.Cam())
	}
}

// Gestures work under zoom: what is dragged follows the pointer.
func TestTranslateUnderZoom(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 100, 100)
	h.SetCam(Camera{X: 50, Y: -20, Z: 2})
	sc := h.ToScreen(geo.V(150, 150))
	h.drag(sc.X, sc.Y, sc.X+200, sc.Y)
	if s := h.shape(a.ID); !near(s.X, 200) {
		t.Errorf("moved to %v under zoom, want 200", s.X)
	}
}

func TestSnapping(t *testing.T) {
	h := newHarness(t)
	h.rect(100, 100, 100, 100)
	b := h.rect(400, 300, 100, 100)
	// With Cmd, the left edge snaps to the other's left, within reach.
	h.PointerMove(450, 350, Mods{}, 0)
	h.PointerDown(450, 350, 0, 1, Mods{}, 0)
	h.PointerMove(153, 350, Mods{Cmd: true}, 0)
	if s := h.shape(b.ID); !near(s.X, 100) {
		t.Errorf("snapped to %v", s.X)
	}
	if len(h.Snaps) == 0 {
		t.Error("no guide shown")
	}
	h.PointerUp(153, 350, Mods{Cmd: true}, 0)
	if len(h.Snaps) != 0 {
		t.Error("guide left showing")
	}
}

func TestDisplayOfEveryShape(t *testing.T) {
	h := newHarness(t)
	for _, k := range GeoKinds {
		for _, dash := range []string{"draw", "solid", "dashed", "dotted"} {
			for _, fill := range []string{"none", "semi", "solid", "pattern", "fill"} {
				s := doc.NewShape(doc.NewID("shape"), doc.TypeGeo)
				s.Geo, s.Dash, s.Fill, s.W, s.H, s.Parent = k, dash, fill, 80, 60, h.Page
				if len(h.Display(s, Light)) == 0 && dash != "none" {
					t.Errorf("%s %s %s draws nothing", k, dash, fill)
				}
				g := h.Geometry(s)
				if len(g.Outline) < 3 || g.Bounds.W != 80 {
					t.Errorf("%s geometry %+v", k, g.Bounds)
				}
			}
		}
	}
	for _, k := range GeoKinds {
		pts := geoOutline(k, 100, 80, "seed")
		b := geo.BoxOf(pts)
		if b.X < -8 || b.Y < -8 || b.MaxX() > 108 || b.MaxY() > 88 {
			t.Errorf("%s outline leaves its box: %+v", k, b)
		}
	}
	// A dot of a stroke, and an arrow of no length, don't break.
	s := doc.NewShape("shape:dot", doc.TypeDraw)
	s.Parent = h.Page
	s.Segments = []doc.Segment{{Type: "free", Points: []doc.PointZ{{}}}}
	if len(h.Display(s, Dark)) == 0 {
		t.Error("a dot draws nothing")
	}
	if g := h.Geometry(s); g.Bounds.W == 0 {
		t.Error("a dot has no size")
	}
	ar := doc.NewShape("shape:ar", doc.TypeArrow)
	ar.Parent = h.Page
	ar.End = ar.Start
	h.Display(ar, Light)
}

func TestSVGExport(t *testing.T) {
	h := newHarness(t)
	if _, ok := h.SVG(false); ok {
		t.Error("an empty page exported")
	}
	h.rect(100, 100, 50, 50)
	h.SetTool(ToolText)
	h.click(300, 300)
	h.EditText = "a <b> & c"
	h.EditChanged()
	h.StopEditing()
	h.SetTool(ToolFrame)
	h.drag(400, 400, 500, 500)
	h.SelectNone()
	data, ok := h.SVG(false)
	if !ok {
		t.Fatal("no svg")
	}
	if !bytes.HasPrefix(data, []byte("<svg")) || !bytes.HasSuffix(data, []byte("</svg>")) {
		t.Errorf("svg %.60s", data)
	}
	if !bytes.Contains(data, []byte("a &lt;b&gt; &amp; c")) {
		t.Error("text not escaped")
	}
	if !bytes.Contains(data, []byte("clipPath")) {
		t.Error("frame not clipped")
	}
}

func TestSaveRoundTripOfEdits(t *testing.T) {
	h := newHarness(t)
	h.rect(100, 100, 50, 50)
	h.SetTool(ToolArrow)
	h.drag(125, 125, 400, 400)
	h.SetTool(ToolDraw)
	h.drag(10, 10, 100, 50)
	h.SetTool(ToolLine)
	h.drag(10, 200, 100, 250)
	var b bytes.Buffer
	if err := doc.Write(&b, h.Doc); err != nil {
		t.Fatal(err)
	}
	d, err := doc.Read(&b)
	if err != nil {
		t.Fatal(err)
	}
	for id, s := range h.Doc.Shapes {
		r := d.Shapes[id]
		if r == nil || r.Type != s.Type || !near(r.X, s.X) || r.Index != s.Index || r.Parent != s.Parent {
			t.Errorf("%s came back as %+v", id, r)
		}
	}
	if len(d.Bindings) != len(h.Doc.Bindings) {
		t.Errorf("%d bindings, had %d", len(d.Bindings), len(h.Doc.Bindings))
	}
}

// TestWriteSample writes a drawing of every kind of record where
// GODRAW_SAMPLE says, for checking against tldraw's own validators.
func TestWriteSample(t *testing.T) {
	path := os.Getenv("GODRAW_SAMPLE")
	if path == "" {
		t.Skip("GODRAW_SAMPLE not set")
	}
	h := newHarness(t)
	for i, k := range GeoKinds {
		h.SetGeoTool(k)
		h.Next.Dash = []string{"draw", "dashed", "dotted", "solid"}[i%4]
		h.Next.Fill = []string{"none", "semi", "solid", "pattern", "fill"}[i%5]
		h.Next.Color = Colors[i%len(Colors)]
		h.drag(float64(i)*110, 0, float64(i)*110+100, 90)
	}
	first := h.Selection()[0]
	h.StartEditing(first)
	h.EditText = "label\nsecond line"
	h.EditChanged()
	h.StopEditing()
	a := h.rect(0, 200, 100, 100)
	b := h.rect(400, 200, 100, 100)
	h.SetTool(ToolArrow)
	h.Next.HeadStart, h.Next.HeadEnd, h.Next.Kind = "dot", "triangle", "arc"
	h.drag(50, 250, 450, 250)
	arrow := h.Selection()[0]
	h.StartEditing(arrow)
	h.EditText = "arrow label"
	h.EditChanged()
	h.StopEditing()
	mid := h.Arrow(h.shape(arrow)).Mid
	sc := h.ToScreen(h.PageMat(arrow).Apply(mid))
	h.drag(sc.X, sc.Y, sc.X, sc.Y+30)
	h.Next.Kind = "elbow"
	h.SetTool(ToolArrow)
	h.drag(700, 200, 900, 300)
	h.Next = DefaultStyle()
	h.SetTool(ToolDraw)
	h.drag(0, 400, 200, 450)
	h.SetTool(ToolHighlight)
	h.drag(0, 500, 200, 550)
	h.SetTool(ToolLine)
	h.drag(300, 400, 500, 500)
	h.SetTool(ToolText)
	h.click(600, 400)
	h.EditText = "text"
	h.EditChanged()
	h.StopEditing()
	h.SetTool(ToolNote)
	h.click(800, 500)
	h.EditText = "note"
	h.EditChanged()
	h.StopEditing()
	h.SetTool(ToolFrame)
	h.drag(1000, 400, 1300, 600)
	frame := h.Selection()[0]
	h.update(frame, func(s *doc.Shape) { s.Name = "My frame" })
	h.commit()
	c := h.rect(1050, 450, 50, 50)
	h.Select(a.ID, b.ID)
	h.Group()
	png1 := []byte{0x89, 'P', 'N', 'G'}
	h.InsertImage("a.png", "image/png", png1, 64, 64, nil)
	h.NewPage()
	h.rect(0, 0, 100, 100)
	_ = c
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := doc.Write(f, h.Doc); err != nil {
		t.Fatal(err)
	}
}

func TestShapeDrawnInAFrameStaysUnderThePointer(t *testing.T) {
	h := newHarness(t)
	h.SetTool(ToolFrame)
	h.drag(200, 200, 600, 600)
	frame := h.Selection()[0]
	a := h.rect(300, 300, 100, 50)
	if a.Parent != frame {
		t.Fatalf("parent %s", a.Parent)
	}
	if b := h.PageBounds(a.ID); !near(b.X, 300) || !near(b.Y, 300) || !near(b.W, 100) {
		t.Errorf("drawn at %+v", b)
	}
	h.SetTool(ToolDraw)
	h.drag(400, 450, 500, 500)
	var stroke *doc.Shape
	for _, s := range h.Doc.Shapes {
		if s.Type == doc.TypeDraw {
			stroke = s
		}
	}
	if stroke.Parent != frame {
		t.Errorf("stroke parent %s", stroke.Parent)
	}
	if b := h.PageBounds(stroke.ID); !near(b.X, 400) || !near(b.Y, 450) {
		t.Errorf("stroke at %+v", b)
	}
}
