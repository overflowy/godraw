package editor

import (
	"encoding/xml"
	"io"
	"math"
	"slices"
	"strings"
	"testing"

	"godraw/internal/doc"
	"godraw/internal/geo"
)

// boxDist is how far two boxes differ, at the worst of their sides.
func boxDist(a, b geo.Box) float64 {
	return max(math.Abs(a.X-b.X), math.Abs(a.Y-b.Y), math.Abs(a.W-b.W), math.Abs(a.H-b.H))
}

// geoShape draws a geo shape of a kind, and returns it selected.
func (h *harness) geoShape(kind string, x, y, w, ht float64) *doc.Shape {
	h.SetGeoTool(kind)
	h.drag(x, y, x+w, y+ht)
	return h.Doc.Shapes[h.Selection()[0]]
}

// add puts a shape on the page, as one step of undo.
func (h *harness) add(s *doc.Shape) *doc.Shape {
	if s.Parent == "" {
		s.Parent = h.Page
	}
	s.Index = h.topIndex(s.Parent)
	h.transact(func() { h.put(s) })
	return s
}

func (h *harness) lock(id doc.ID) {
	h.transact(func() { h.update(id, func(c *doc.Shape) { c.Locked = true }) })
}

// groupOf groups rectangles it draws, and returns the group and what it holds.
func (h *harness) groupOf(n int) (doc.ID, []doc.ID) {
	var ids []doc.ID
	for i := range n {
		ids = append(ids, h.rect(100+float64(i)*150, 100, 100, 100).ID)
	}
	h.Select(ids...)
	h.Group()
	return h.Selection()[0], ids
}

// paintSelection does what a frame does with the selection, which is where a stale
// one panics.
func (h *harness) paintSelection() {
	h.t.Helper()
	if len(h.Selection()) > 0 {
		h.SelectionCorners()
	}
	for _, id := range h.Selection() {
		if h.Doc.Shapes[id] == nil {
			h.t.Errorf("%s is selected, and gone", id)
		}
	}
}

func TestEscapeAfterDeletingFromAFocusedGroup(t *testing.T) {
	for _, n := range []int{2, 3} {
		h := newHarness(t)
		g, ids := h.groupOf(n)
		// In by a double click, which writes in the shape, out of the writing again.
		h.PointerDown(150, 150, 0, 2, Mods{}, 1)
		h.PointerUp(150, 150, Mods{}, 1)
		h.Cancel()
		if h.FocusedGroup != g || !h.IsSelected(ids[0]) {
			t.Fatalf("%d shapes: focus %q, selection %v", n, h.FocusedGroup, h.Selection())
		}
		h.Delete()
		h.Cancel()
		h.paintSelection()
		if h.Doc.Shapes[h.FocusedGroup] == nil && h.FocusedGroup != "" {
			t.Errorf("%d shapes: focus stays on %s, which is gone", n, h.FocusedGroup)
		}
	}
}

func TestUndoingAGroupWhileFocusedInIt(t *testing.T) {
	h := newHarness(t)
	g, ids := h.groupOf(2)
	h.FocusedGroup = g
	h.Select(ids[0])
	h.Undo()
	if h.FocusedGroup != "" {
		t.Errorf("focus stays on %s, which undo took away", h.FocusedGroup)
	}
	h.Cancel()
	h.paintSelection()
}

func TestDeletingWhatEmptiesASelectedGroup(t *testing.T) {
	h := newHarness(t)
	g, ids := h.groupOf(2)
	// A locked group stays selected as what it holds goes, until it holds too little.
	h.lock(g)
	h.Select(g, ids[0])
	h.Delete()
	h.paintSelection()
	if h.Doc.Shapes[g] != nil {
		t.Error("a group of one stayed")
	}
}

func TestInsertedImageOutlivesAnEmptyText(t *testing.T) {
	h := newHarness(t)
	h.SetTool(ToolText)
	h.click(300, 300)
	if h.Editing == "" {
		t.Fatal("no text being written")
	}
	h.InsertImage("a.png", "image/png", []byte("x"), 40, 40, nil)
	h.click(800, 700)
	var images int
	for _, s := range h.Doc.Shapes {
		if s.Type == doc.TypeImage {
			images++
		}
		if s.Type == doc.TypeText {
			t.Errorf("the empty text stayed: %+v", s)
		}
	}
	if images != 1 || len(h.Doc.Assets) != 1 {
		t.Fatalf("%d images and %d assets, after writing nothing", images, len(h.Doc.Assets))
	}
	h.Undo()
	if len(h.Doc.Shapes) != 0 {
		t.Errorf("undo left %d shapes", len(h.Doc.Shapes))
	}
}

func TestPastedTextOutlivesAnEmptyText(t *testing.T) {
	h := newHarness(t)
	h.SetTool(ToolText)
	h.click(300, 300)
	h.PasteText("kept")
	h.click(800, 700)
	if len(h.Doc.Shapes) != 1 {
		t.Fatalf("%d shapes after pasting while writing nothing", len(h.Doc.Shapes))
	}
}

func TestMoveToPageLetsGoOfWhatStays(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 100, 100)
	b := h.rect(400, 100, 100, 100)
	arrow := h.add(doc.NewShape("shape:arrow", doc.TypeArrow))
	h.transact(func() {
		h.Bind(arrow.ID, "start", a.ID, geo.V(0.5, 0.5))
		h.Bind(arrow.ID, "end", b.ID, geo.V(0.5, 0.5))
	})
	first := h.Page
	before := h.Arrow(h.Doc.Shapes[arrow.ID]).End
	h.NewPage()
	second := h.Page
	h.SetPage(first)

	h.Select(b.ID)
	h.MoveToPage(second)
	if got := h.PageOf(b.ID); got != second {
		t.Fatalf("b is on %s", got)
	}
	if h.BindingOf(arrow.ID, "end") != nil {
		t.Error("an arrow stays bound across pages")
	}
	if h.BindingOf(arrow.ID, "start") == nil {
		t.Error("the arrow let go of what's still on its page")
	}
	if end := h.Arrow(h.Doc.Shapes[arrow.ID]).End; end.Dist(before) > 0.01 {
		t.Errorf("the arrow's free end went from %v to %v", before, end)
	}

	h.Undo()
	if h.BindingOf(arrow.ID, "end") == nil || h.PageOf(b.ID) != first {
		t.Error("undo didn't bring the binding and the shape back")
	}

	// An arrow going by itself lets go of both, and what goes together stays bound.
	h.Select(arrow.ID)
	h.MoveToPage(second)
	if h.BindingOf(arrow.ID, "start") != nil || h.BindingOf(arrow.ID, "end") != nil {
		t.Error("an arrow that left stays bound to what it left")
	}
	h.Undo()
	h.Select(arrow.ID, a.ID, b.ID)
	h.MoveToPage(second)
	if h.BindingOf(arrow.ID, "start") == nil || h.BindingOf(arrow.ID, "end") == nil {
		t.Error("an arrow let go of what came with it")
	}
}

func TestMoveToPageCleansTheGroupLeft(t *testing.T) {
	h := newHarness(t)
	g, ids := h.groupOf(2)
	first := h.Page
	h.NewPage()
	second := h.Page
	h.SetPage(first)

	h.FocusedGroup = g
	h.Select(ids[0])
	h.MoveToPage(second)
	if h.Doc.Shapes[g] != nil {
		t.Error("a group of one stayed")
	}
	if s := h.Doc.Shapes[ids[1]]; s == nil || s.Parent != first {
		t.Errorf("what was left is %+v", s)
	}
	if !near(h.PageBounds(ids[1]).X, 250) {
		t.Errorf("what was left moved to %v", h.PageBounds(ids[1]))
	}
}

// A file can hold an empty group from before they were cleaned up.
func TestEmptyGroupsDontStretchBounds(t *testing.T) {
	h := newHarness(t)
	h.rect(1000, 1000, 100, 100)
	h.add(doc.NewShape("shape:empty", doc.TypeGroup))
	box, ok := h.ContentBounds()
	if !ok || !near(box.X, 1000) {
		t.Errorf("content bounds %v", box)
	}
	h.SelectNone()
	if box, _, _ := h.ExportBounds(); box.X < 900 {
		t.Errorf("export bounds %v", box)
	}
}

func TestFlippingWrappedTextKeepsItsBox(t *testing.T) {
	h := newHarness(t)
	s := doc.NewShape("shape:t", doc.TypeText)
	s.AutoSize, s.W, s.X, s.Y, s.Text = false, 200, 300, 300, "one\ntwo\nthree"
	h.add(s)
	before := h.PageBounds(s.ID)
	for _, horizontal := range []bool{false, true} {
		h.Select(s.ID)
		h.Flip(horizontal)
		if after := h.PageBounds(s.ID); boxDist(after, before) > 0.01 {
			t.Errorf("flip (horizontal %v) moved it from %v to %v", horizontal, before, after)
		}
	}
}

// svgOf returns the SVG of shapes, which has to read as XML.
func (h *harness) svgOf(ids ...doc.ID) string {
	h.t.Helper()
	h.Select(ids...)
	b, ok := h.SVG(false)
	if !ok {
		h.t.Fatal("no svg")
	}
	d := xml.NewDecoder(strings.NewReader(string(b)))
	for {
		_, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			h.t.Fatalf("not xml: %v\n%s", err, b)
		}
	}
	return string(b)
}

func TestSVGFadesWhatAFrameHolds(t *testing.T) {
	h := newHarness(t)
	f := doc.NewShape("shape:f", doc.TypeFrame)
	f.W, f.H, f.Opacity = 300, 200, 0.5
	h.add(f)
	k := doc.NewShape("shape:k", doc.TypeGeo)
	k.Parent, k.Fill, k.Opacity, k.X, k.Y = f.ID, "solid", 0.5, 10, 10
	h.add(k)
	svg := h.svgOf(f.ID)
	if !strings.Contains(svg, `opacity="0.25"`) {
		t.Errorf("a shape at half in a frame at half isn't at a quarter:\n%s", svg)
	}
}

func TestSVGHeadingsAsTheCanvasHasThem(t *testing.T) {
	h := newHarness(t)
	f := doc.NewShape("shape:f", doc.TypeFrame)
	f.W, f.H, f.Rotation = 300, 200, math.Pi/6
	h.add(f)
	svg := h.svgOf(f.ID)
	if !strings.Contains(svg, ">Frame</text>") {
		t.Errorf("no default heading:\n%s", svg)
	}
	if !strings.Contains(svg, "rotate(30.00") {
		t.Errorf("a turned frame's heading stays level:\n%s", svg)
	}
}

func TestSVGCropsImages(t *testing.T) {
	h := newHarness(t)
	h.InsertImage("a.png", "image/png", []byte("x"), 100, 100, nil)
	id := h.Selection()[0]
	h.transact(func() {
		h.update(id, func(c *doc.Shape) {
			c.Crop = &doc.Crop{TopLeft: geo.V(0.25, 0.5), BottomRight: geo.V(0.75, 1)}
		})
	})
	svg := h.svgOf(id)
	if !strings.Contains(svg, `viewBox="0.25 0.5 0.5 0.5"`) {
		t.Errorf("no crop:\n%s", svg)
	}
}

// labelFits says whether a geo shape's label lies inside its box.
func (h *harness) labelFits(id doc.ID) bool {
	g := h.Geometry(h.Doc.Shapes[id])
	return g.Label != nil && g.Label.Y >= -0.01 && g.Label.MaxY() <= g.Bounds.H+0.01
}

func TestDragResizeKeepsALabelInside(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 200, 200)
	h.transact(func() {
		h.update(a.ID, func(c *doc.Shape) { c.Text = "1\n2\n3\n4\n5" })
	})
	h.SelectNone()
	h.Select(a.ID)
	// By the bottom edge, up to well short of what five lines take.
	h.drag(200, 300, 200, 130)
	s := h.shape(a.ID)
	if !h.labelFits(a.ID) {
		t.Errorf("the label overflows %vx%v, grown by %v: %+v", s.W, s.H, s.GrowY, h.Geometry(s).Label)
	}
	if !near(s.H, 30) {
		t.Errorf("height %v, want the 30 asked for and growth to fit", s.H)
	}
	// Back up as far: it no longer needs to grow.
	h.Undo()
	if !near(h.shape(a.ID).H, 200) {
		t.Errorf("undo left height %v", h.shape(a.ID).H)
	}
}

func TestFlipLeavesGrowthAlone(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 200, 30)
	h.transact(func() {
		h.update(a.ID, func(c *doc.Shape) {
			c.Text = "1\n2\n3\n4\n5"
			h.fitLabel(c)
		})
	})
	before := h.shape(a.ID)
	if before.GrowY <= 0 {
		t.Fatal("the label didn't grow it")
	}
	box := h.PageBounds(a.ID)
	for _, horizontal := range []bool{true, false} {
		h.Select(a.ID)
		h.Flip(horizontal)
		s := h.shape(a.ID)
		if s.H != before.H || s.GrowY != before.GrowY {
			t.Errorf("flip made %v + %v of %v + %v", s.H, s.GrowY, before.H, before.GrowY)
		}
		if after := h.PageBounds(a.ID); boxDist(after, box) > 0.01 {
			t.Errorf("flip moved %v to %v", box, after)
		}
	}
}

func TestEraserCatchesAFastStroke(t *testing.T) {
	h := newHarness(t)
	a := h.rect(300, 300, 100, 100)
	far := h.rect(300, 600, 100, 100)
	h.SetTool(ToolEraser)
	// One move, from well before the shape to well past it.
	h.PointerDown(50, 350, 0, 1, Mods{}, 1)
	h.PointerMove(900, 350, Mods{}, 1)
	h.PointerUp(900, 350, Mods{}, 1)
	if h.Doc.Shapes[a.ID] != nil {
		t.Error("a stroke straight through a shape missed it")
	}
	if h.Doc.Shapes[far.ID] == nil {
		t.Error("a stroke nowhere near a shape erased it")
	}
}

func TestFlippedGeoMirrors(t *testing.T) {
	h := newHarness(t)
	tri := h.geoShape("triangle", 100, 100, 200, 100)
	h.transact(func() { h.update(tri.ID, func(c *doc.Shape) { c.Fill = "solid" }) })
	h.Select(tri.ID)
	base := h.PageBounds(tri.ID)

	// Its point at the top: near the top corners there's nothing to hit.
	cornerBottom, cornerTop := geo.V(110, 195), geo.V(110, 105)
	if h.Hit(cornerBottom, 0, false).ID != tri.ID || h.Hit(cornerTop, 0, false).ID == tri.ID {
		t.Fatal("not a triangle to begin with")
	}
	svg := h.svgOf(tri.ID)

	h.Flip(false)
	if h.Hit(cornerTop, 0, false).ID != tri.ID || h.Hit(cornerBottom, 0, false).ID == tri.ID {
		t.Error("hits go by a triangle that didn't flip")
	}
	if after := h.PageBounds(tri.ID); boxDist(after, base) > 0.01 {
		t.Errorf("flip moved it: %v to %v", base, after)
	}
	if h.svgOf(tri.ID) == svg {
		t.Error("the SVG didn't flip")
	}
	if !near(h.Geometry(h.shape(tri.ID)).Outline[0].Y, 100) {
		t.Errorf("outline %v", h.Geometry(h.shape(tri.ID)).Outline)
	}

	// Twice is what it was, and a label doesn't mirror.
	h.Select(tri.ID)
	h.Flip(false)
	if h.svgOf(tri.ID) != svg {
		t.Error("two flips aren't none")
	}
}

func TestFlipMirrorsEveryKindWithASide(t *testing.T) {
	for _, kind := range []string{"rhombus", "trapezoid", "arrow-right", "cloud", "heart"} {
		h := newHarness(t)
		a := h.geoShape(kind, 100, 100, 200, 120)
		was := slices.Clone(h.Geometry(a).Outline)
		wasPrims := h.pathsOf(a.ID)
		h.Select(a.ID)
		horizontal := kind != "trapezoid" && kind != "heart"
		h.Flip(horizontal)
		s := h.shape(a.ID)
		out := h.Geometry(s).Outline
		for i, p := range was {
			want := geo.V(200-p.X, p.Y)
			if !horizontal {
				want = geo.V(p.X, 120-p.Y)
			}
			if out[i].Dist(want) > 0.01 {
				t.Errorf("%s: point %d at %v, want %v", kind, i, out[i], want)
				break
			}
		}
		if h.pathsOf(a.ID) == wasPrims {
			t.Errorf("%s draws as it did", kind)
		}
	}
}

// pathsOf returns what a shape draws, with its theme, as SVG path data.
func (h *harness) pathsOf(id doc.ID) string {
	var b strings.Builder
	for _, p := range h.Display(h.Doc.Shapes[id], Light) {
		if p.Path != nil {
			b.WriteString(p.Path.SVG(geo.Identity))
		}
	}
	return b.String()
}

func TestFlipMirrorsACheckMark(t *testing.T) {
	h := newHarness(t)
	a := h.geoShape("check-box", 100, 100, 100, 100)
	last := func() string {
		prims := h.Display(h.Doc.Shapes[a.ID], Light)
		return prims[len(prims)-1].Path.SVG(geo.Identity)
	}
	was := last()
	h.Select(a.ID)
	h.Flip(true)
	if last() == was {
		t.Error("the check mark didn't flip")
	}
}

func TestFlippedGeoStopsArrowsAtItsOutline(t *testing.T) {
	h := newHarness(t)
	tri := h.geoShape("triangle", 100, 100, 200, 100)
	arrow := doc.NewShape("shape:arrow", doc.TypeArrow)
	arrow.X, arrow.Y = 200, 0
	h.add(arrow)
	h.transact(func() { h.Bind(arrow.ID, "end", tri.ID, geo.V(0.25, 0.5)) })
	// A precise anchor stays on the same part of the shape.
	h.Select(tri.ID)
	h.Flip(true)
	if b := h.BindingOf(arrow.ID, "end"); !near(b.Anchor.X, 0.75) || !near(b.Anchor.Y, 0.5) {
		t.Errorf("anchor %v after flipping", b.Anchor)
	}
	h.Undo()
	if b := h.BindingOf(arrow.ID, "end"); !near(b.Anchor.X, 0.25) {
		t.Errorf("anchor %v after undoing", b.Anchor)
	}

	// Coming down from above onto what's now a flat top, it stops at the top.
	h.transact(func() { h.Bind(arrow.ID, "end", tri.ID, geo.V(0.5, 0.5)) })
	h.Select(tri.ID)
	h.Flip(false)
	s := h.shape(arrow.ID)
	end := h.PageMat(s.ID).Apply(h.Arrow(s).End)
	if end.Y > 100 || end.Y < 80 {
		t.Errorf("arrow ends at %v, not at the flipped triangle's top edge", end)
	}
}

func TestFlipMirrorsAGradient(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 200, 100)
	h.transact(func() {
		h.update(a.ID, func(c *doc.Shape) {
			c.Fill, c.Gradient = "fill", &doc.Gradient{Colors: []string{"#ff0000", "#0000ff"}, Angle: 90}
		})
	})
	h.Select(a.ID)
	h.Flip(true)
	if g := h.shape(a.ID).Gradient; g.Angle != 270 {
		t.Errorf("angle %v", g.Angle)
	}
	h.Select(a.ID)
	h.Flip(true)
	if g := h.shape(a.ID).Gradient; g.Angle != 90 {
		t.Errorf("angle %v, flipped back", g.Angle)
	}
	h.Select(a.ID)
	h.Flip(false)
	if g := h.shape(a.ID).Gradient; g.Angle != 90 {
		t.Errorf("a left to right gradient flipped top to bottom has angle %v", g.Angle)
	}
}

func TestRotatingLeavesLockedShapes(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 100, 100)
	b := h.rect(300, 100, 100, 100)
	h.lock(b.ID)
	h.Select(a.ID, b.ID)
	// Just outside the box's top right corner, round it.
	h.drag(410, 90, 500, 300)
	if h.shape(a.ID).Rotation == 0 {
		t.Fatal("what isn't locked didn't turn")
	}
	if s := h.shape(b.ID); s.Rotation != 0 || s.X != 300 || s.Y != 100 {
		t.Errorf("a locked shape went to %v,%v turned %v", s.X, s.Y, s.Rotation)
	}
}

func TestLockedShapesTakeNoWriting(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 100, 100)
	h.lock(a.ID)
	// Right click is how a locked shape is reached.
	h.PointerDown(100, 150, 1, 1, Mods{}, 1)
	h.PointerUp(100, 150, Mods{}, 1)
	if !h.IsSelected(a.ID) {
		t.Fatal("right click doesn't reach a locked shape")
	}
	h.StartEditing(a.ID)
	if h.Editing != "" {
		t.Error("a locked shape took writing")
	}
	if h.tx != nil {
		t.Error("a refused edit left a transaction open")
	}
}

func TestLockedShapesKeepTheirPlace(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 100, 100)
	b := h.rect(150, 100, 100, 100)
	h.lock(a.ID)
	first := h.Page
	h.Select(a.ID)
	h.Reorder("front")
	if kids := h.Children(first); kids[0] != a.ID {
		t.Errorf("a locked shape went to the front: %v", kids)
	}
	h.NewPage()
	second := h.Page
	h.SetPage(first)
	h.Select(a.ID, b.ID)
	h.MoveToPage(second)
	if h.PageOf(a.ID) != first {
		t.Error("a locked shape changed page")
	}
	if h.PageOf(b.ID) != second {
		t.Error("what wasn't locked stayed")
	}
}

func TestAltDragKeepsTheOrder(t *testing.T) {
	h := newHarness(t)
	bottom := h.rect(100, 100, 100, 100)
	top := h.rect(120, 120, 100, 100)
	h.transact(func() {
		h.update(bottom.ID, func(c *doc.Shape) { c.Color = "red" })
		h.update(top.ID, func(c *doc.Shape) { c.Color = "blue" })
	})
	// The top one first.
	h.Select(top.ID, bottom.ID)
	h.dragMods(160, 160, 560, 460, Mods{Alt: true})
	kids := h.Children(h.Page)
	if len(kids) != 4 {
		t.Fatalf("%d shapes after an alt drag", len(kids))
	}
	if a, b := h.shape(kids[2]).Color, h.shape(kids[3]).Color; a != "red" || b != "blue" {
		t.Errorf("copies stack %s under %s", a, b)
	}
}

func TestCrossesItselfSaysOneThing(t *testing.T) {
	for _, c := range []struct {
		name string
		pts  []geo.Vec
		want bool
	}{
		{"a staircase", []geo.Vec{geo.V(0, 0), geo.V(100, 0), geo.V(100, 100), geo.V(200, 100)}, false},
		{"a loop", []geo.Vec{geo.V(0, 0), geo.V(100, 0), geo.V(100, 100), geo.V(50, 100), geo.V(50, -50)}, true},
		{"back along itself", []geo.Vec{geo.V(0, 0), geo.V(100, 0), geo.V(20, 0)}, true},
		{"back beside itself", []geo.Vec{geo.V(0, 0), geo.V(100, 0), geo.V(100, 3), geo.V(0, 3)}, true},
		{"back well clear of itself", []geo.Vec{geo.V(0, 0), geo.V(100, 0), geo.V(100, 40), geo.V(0, 40)}, false},
		{"a short step back", []geo.Vec{geo.V(0, 0), geo.V(100, 0), geo.V(90, 0)}, false},
	} {
		if got := CrossesItself(c.pts); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

func TestGrowthGoesAwayFromWhatHoldsStill(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 200, 200)
	h.transact(func() {
		h.update(a.ID, func(c *doc.Shape) { c.Text = "1\n2\n3\n4\n5" })
	})
	h.Select(a.ID)
	// By the top edge, down to well short of what five lines take: the bottom stays.
	h.drag(200, 100, 200, 270)
	box := h.PageBounds(a.ID)
	if !near(box.MaxY(), 300) {
		t.Errorf("the bottom went to %v in %v, dragging the top", box.MaxY(), box)
	}
	if !h.labelFits(a.ID) {
		t.Errorf("the label overflows %v", box)
	}
	// And the same by the bottom edge, where the top stays.
	h.Undo()
	h.Select(a.ID)
	h.drag(200, 300, 200, 130)
	if box := h.PageBounds(a.ID); !near(box.Y, 100) {
		t.Errorf("the top went to %v, dragging the bottom", box.Y)
	}
	// Past what holds still, turned over: it stays on the far side of it.
	h.Undo()
	h.Select(a.ID)
	h.drag(200, 100, 200, 330)
	if box := h.PageBounds(a.ID); !near(box.Y, 300) {
		t.Errorf("past the bottom, it starts at %v, not at the bottom it passed", box.Y)
	}
}

func TestArrowsStayOnWhatDoesntTurnOver(t *testing.T) {
	h := newHarness(t)
	n := doc.NewShape("shape:n", doc.TypeNote)
	n.X, n.Y = 100, 100
	h.add(n)
	arrow := doc.NewShape("shape:arrow", doc.TypeArrow)
	arrow.X, arrow.Y = 0, 0
	h.add(arrow)
	h.transact(func() { h.Bind(arrow.ID, "end", n.ID, geo.V(0, 0.5)) })
	h.Select(n.ID)
	h.Flip(true)
	if b := h.BindingOf(arrow.ID, "end"); !near(b.Anchor.X, 0) {
		t.Errorf("a note that reads as it did moved an arrow's anchor to %v", b.Anchor)
	}
	// Flipped together, the arrow comes at it from the other side.
	h.Select(n.ID, arrow.ID)
	h.Flip(true)
	if b := h.BindingOf(arrow.ID, "end"); !near(b.Anchor.X, 1) {
		t.Errorf("anchor %v, flipped with its arrow", b.Anchor)
	}
}

func TestNestedEmptyGroupsHaveNoBox(t *testing.T) {
	h := newHarness(t)
	h.rect(1000, 1000, 100, 100)
	g := doc.NewShape("shape:g", doc.TypeGroup)
	g.X, g.Y = 5000, 5000
	h.add(g)
	k := doc.NewShape("shape:k", doc.TypeGroup)
	k.Parent = g.ID
	h.add(k)
	if box, _ := h.ContentBounds(); !near(box.MaxX(), 1100) || !near(box.X, 1000) {
		t.Errorf("content bounds %v", box)
	}
	h.SelectNone()
	if box, _, _ := h.ExportBounds(); box.MaxX() > 1200 {
		t.Errorf("export bounds %v", box)
	}
	// In a group that holds something too.
	h2 := newHarness(t)
	h2.groupOf(2)
	outer := h2.Selection()[0]
	e := doc.NewShape("shape:e", doc.TypeGroup)
	e.Parent, e.X, e.Y = outer, 5000, 5000
	h2.add(e)
	if b := h2.PageBounds(outer); b.MaxX() > 1000 {
		t.Errorf("a group stretched by an empty one: %v", b)
	}
}

func TestPastingKeepsWhatIsBeingDrawn(t *testing.T) {
	h := newHarness(t)
	h.SetTool(ToolDraw)
	h.PointerDown(100, 100, 0, 1, Mods{}, 1)
	h.PointerMove(200, 200, Mods{}, 1)
	h.PasteText("kept")
	h.PointerUp(200, 200, Mods{}, 1)
	if len(h.Doc.Shapes) != 2 {
		t.Fatalf("%d shapes after pasting while drawing", len(h.Doc.Shapes))
	}
	h.Undo()
	if len(h.Doc.Shapes) != 1 {
		t.Errorf("%d shapes: undo didn't take the paste alone", len(h.Doc.Shapes))
	}
}

func TestPastingIsAStepOfItsOwn(t *testing.T) {
	h := newHarness(t)
	a := h.rect(100, 100, 100, 100)
	h.Select(a.ID)
	h.SetOpacity(0.5, true) // a slider, still held
	h.PasteText("kept")
	h.Undo()
	if len(h.Doc.Shapes) != 1 {
		t.Fatalf("%d shapes after undoing a paste", len(h.Doc.Shapes))
	}
	if o := h.shape(a.ID).Opacity; o != 0.5 {
		t.Errorf("undoing the paste took the opacity back to %v too", o)
	}
}
