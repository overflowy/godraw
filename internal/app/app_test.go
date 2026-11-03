package app

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"godraw/internal/doc"
	"godraw/internal/editor"
	"godraw/internal/geo"
)

// testHost stands in for the window. As Window.Update does, it holds what
// workers post until the main thread runs it, here the test's (pump).
type testHost struct {
	saved  []*doc.Drawing
	mu     sync.Mutex
	queued []func()
}

func (h *testHost) Changed(d *doc.Drawing) { h.saved = append(h.saved, d) }
func (h *testHost) Window() *mygo.Window   { return nil }

func (h *testHost) Update(fn func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.queued = append(h.queued, fn)
}

func (h *testHost) pump() {
	h.mu.Lock()
	fns := h.queued
	h.queued = nil
	h.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

func newTest(t *testing.T) (*App, *ui.Tester, *testHost) {
	t.Helper()
	h := &testHost{}
	a := New(doc.New(), h)
	tt := ui.NewTester(a.View, 1280, 820)
	tt.Frame()
	return a, tt, h
}

// snapshot writes the last frame where GODRAW_SNAPSHOTS says, to look at.
func snapshot(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	dir := os.Getenv("GODRAW_SNAPSHOTS")
	if dir == "" {
		return
	}
	var b bytes.Buffer
	if err := png.Encode(&b, tt.Image()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".png"), b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func drag(tt *ui.Tester, x0, y0, x1, y1 float32) {
	tt.Press(x0, y0)
	const n = 8
	for i := 1; i <= n; i++ {
		f := float32(i) / n
		tt.Move(x0+(x1-x0)*f, y0+(y1-y0)*f)
	}
	tt.Release(x1, y1)
}

func TestToolbarAndDrawing(t *testing.T) {
	a, tt, h := newTest(t)
	e := a.Editor()

	// A rectangle, by its key and a drag.
	tt.Key(0, ui.KeyR)
	if e.Tool != editor.ToolGeo {
		t.Fatalf("tool %q after R", e.Tool)
	}
	drag(tt, 200, 200, 400, 320)
	if got := len(e.Doc.Shapes); got != 1 {
		t.Fatalf("%d shapes after dragging a rectangle", got)
	}
	if e.Tool != editor.ToolSelect {
		t.Errorf("tool %q after making a shape, want select", e.Tool)
	}
	if len(h.saved) != 1 {
		t.Errorf("%d changes after one shape", len(h.saved))
	}
	rect := e.Selection()[0]
	if s := e.Doc.Shapes[rect]; s.X != 200 || s.Y != 200 || s.W != 200 || s.H != 120 {
		t.Errorf("rectangle at %v,%v %vx%v", s.X, s.Y, s.W, s.H)
	}

	// Moved by a press inside the selection.
	drag(tt, 300, 260, 350, 310)
	if s := e.Doc.Shapes[rect]; s.X != 250 || s.Y != 250 {
		t.Errorf("rectangle moved to %v,%v", s.X, s.Y)
	}

	// Undone and redone.
	tt.Key(ui.Cmd, ui.KeyZ)
	if s := e.Doc.Shapes[rect]; s.X != 200 {
		t.Errorf("undo left x at %v", s.X)
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	if s := e.Doc.Shapes[rect]; s.X != 250 {
		t.Errorf("redo left x at %v", s.X)
	}

	// Freehand, by the toolbar's button: a click, as a user would.
	if err := tt.Click("Draw — D"); err != nil {
		t.Fatal(err)
	}
	if e.Tool != editor.ToolDraw {
		t.Fatalf("tool %q after its button", e.Tool)
	}
	before := len(e.Doc.Shapes)
	drag(tt, 500, 200, 700, 300)
	if len(e.Doc.Shapes) != before+1 {
		t.Errorf("freehand made %d shapes", len(e.Doc.Shapes)-before)
	}
	snapshot(t, tt, "tools")
}

func TestPanelsTakeThePointer(t *testing.T) {
	a, tt, _ := newTest(t)
	e := a.Editor()
	tt.Key(0, ui.KeyD)
	// A press on the toolbar's panel, in its padding, must not draw.
	box, ok := tt.Find("Select — V")
	if !ok {
		t.Fatal("no select button")
	}
	tt.Press(box.X-1, box.Y-1)
	tt.Release(box.X-1, box.Y-1)
	if len(e.Doc.Shapes) != 0 {
		t.Errorf("pressing the panel drew %d shapes", len(e.Doc.Shapes))
	}
}

func TestEditText(t *testing.T) {
	a, tt, _ := newTest(t)
	e := a.Editor()
	tt.Key(0, ui.KeyT)
	tt.ClickAt(400, 300)
	if e.Editing == "" {
		t.Fatal("not editing after a click with the text tool")
	}
	tt.Frame()
	tt.Type("hello")
	tt.Frame()
	if e.EditText != "hello" {
		t.Errorf("edit text %q", e.EditText)
	}
	s := e.Doc.Shapes[e.Editing]
	if s == nil || s.Text != "hello" {
		t.Fatalf("shape text not updated: %+v", s)
	}
	snapshot(t, tt, "editing")
	tt.Key(0, ui.KeyEscape)
	if e.Editing != "" {
		t.Error("Escape did not end editing")
	}
	// A typed letter is a tool's key again.
	tt.Key(0, ui.KeyD)
	if e.Tool != editor.ToolDraw {
		t.Errorf("tool %q after D", e.Tool)
	}

	// Empty text goes away.
	before := len(e.Doc.Shapes)
	tt.Key(0, ui.KeyT)
	tt.ClickAt(600, 500)
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	if len(e.Doc.Shapes) != before {
		t.Errorf("empty text stayed: %d shapes, had %d", len(e.Doc.Shapes), before)
	}
}

// showcase fills a page with one of everything, to look at.
func showcase(t *testing.T, a *App, tt *ui.Tester) {
	t.Helper()
	e := a.Editor()
	kinds := editor.GeoKinds
	fills := []string{"none", "semi", "solid", "pattern", "fill"}
	dashes := []string{"draw", "dashed", "dotted", "solid"}
	colors := []string{"black", "blue", "red", "green", "orange", "violet", "light-blue", "yellow"}
	for i, k := range kinds {
		tt.Key(0, ui.KeyEscape)
		e.SetGeoTool(k)
		e.Next.Fill = fills[i%len(fills)]
		e.Next.Dash = dashes[i%len(dashes)]
		e.Next.Color = colors[i%len(colors)]
		x := float32(60 + (i%10)*112)
		y := float32(80 + (i/10)*120)
		drag(tt, x, y, x+96, y+96)
	}
	e.Next = editor.DefaultStyle()
	e.SetTool(editor.ToolDraw)
	stroke := func(x0, y0 float32) {
		tt.Press(x0, y0)
		for i := range 40 {
			f := float32(i)
			tt.Move(x0+f*5, y0+30*sinf(f/5))
		}
		tt.Release(x0+200, y0)
	}
	stroke(60, 380)
	e.Next.Size, e.Next.Color = "l", "blue"
	stroke(300, 380)
	e.Next = editor.DefaultStyle()
	e.SetTool(editor.ToolHighlight)
	stroke(560, 380)
	e.SetTool(editor.ToolArrow)
	drag(tt, 60, 480, 300, 480)
	e.Next.HeadStart, e.Next.HeadEnd, e.Next.Dash = "dot", "triangle", "dashed"
	e.SetTool(editor.ToolArrow)
	drag(tt, 340, 450, 560, 520)
	e.Next = editor.DefaultStyle()
	// An arrow between two shapes, bound to them.
	e.SetGeoTool("rectangle")
	drag(tt, 640, 440, 760, 520)
	e.SetGeoTool("ellipse")
	drag(tt, 900, 440, 1040, 540)
	e.SetTool(editor.ToolArrow)
	drag(tt, 700, 480, 970, 490)
	e.SetTool(editor.ToolNote)
	tt.ClickAt(180, 660)
	tt.Frame()
	tt.Type("A sticky note in the handwriting font")
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	e.SelectNone()
	e.Next.Size, e.Next.Font = "m", "sans"
	e.SetTool(editor.ToolText)
	tt.ClickAt(330, 600)
	tt.Frame()
	tt.Type("Sans text")
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	e.Next.Font = "serif"
	e.SetTool(editor.ToolText)
	tt.ClickAt(330, 640)
	tt.Frame()
	tt.Type("Serif text")
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	e.Next.Font = "mono"
	e.SetTool(editor.ToolText)
	tt.ClickAt(330, 680)
	tt.Frame()
	tt.Type("Mono text")
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	e.Next.Font = "draw"
	e.SetTool(editor.ToolText)
	tt.ClickAt(330, 720)
	tt.Frame()
	tt.Type("Draw text")
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	e.Next = editor.DefaultStyle()
	e.SetTool(editor.ToolFrame)
	drag(tt, 560, 580, 860, 760)
	e.SelectNone()
	tt.Frame()
}

func TestShowcase(t *testing.T) {
	a, tt, _ := newTest(t)
	a.SetTheme("light")
	tt.Frame()
	showcase(t, a, tt)
	snapshot(t, tt, "showcase")
	a.SetTheme("dark")
	tt.Frame()
	snapshot(t, tt, "showcase-dark")
}

func sinf(x float32) float32 { return float32(math.Sin(float64(x))) }

func TestOpacitySliderIsOneUndoStep(t *testing.T) {
	a, tt, _ := newTest(t)
	e := a.Editor()
	tt.Key(0, ui.KeyR)
	drag(tt, 300, 300, 500, 400)
	id := e.Selection()[0]
	box, ok := tt.Find("Opacity")
	if !ok {
		t.Fatal("no opacity slider")
	}
	steps := 0
	a.ed.OnChange = func(*doc.Drawing) { steps++ }
	y := box.Y + box.H/2
	drag(tt, box.X+box.W-2, y, box.X+box.W/2, y)
	tt.Frame()
	if o := e.Doc.Shapes[id].Opacity; o > 0.8 || o < 0.3 {
		t.Errorf("opacity %v after dragging the slider", o)
	}
	if steps != 1 {
		t.Errorf("a slider drag made %d changes", steps)
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	if o := e.Doc.Shapes[id].Opacity; o != 1 {
		t.Errorf("opacity %v after one undo", o)
	}
}

func TestClipboardAndContextMenu(t *testing.T) {
	a, tt, _ := newTest(t)
	e := a.Editor()
	tt.Key(0, ui.KeyR)
	drag(tt, 300, 300, 500, 400)
	tt.Key(ui.Cmd, ui.KeyC)
	tt.Key(ui.Cmd, ui.KeyV)
	tt.Frame()
	if n := len(e.Doc.Shapes); n != 2 {
		t.Fatalf("%d shapes after copy and paste", n)
	}
	tt.Key(ui.Cmd, ui.KeyX)
	tt.Frame()
	if n := len(e.Doc.Shapes); n != 1 {
		t.Errorf("%d shapes after cut", n)
	}
	// Text from elsewhere comes in as a text shape.
	tt.SetClipboard("from elsewhere")
	tt.Key(ui.Cmd, ui.KeyV)
	tt.Frame()
	found := false
	for _, s := range e.Doc.Shapes {
		if s.Type == doc.TypeText && s.Text == "from elsewhere" {
			found = true
		}
	}
	if !found {
		t.Error("pasted text made no text shape")
	}

	// The context menu acts on what it opened over: an edge of the rectangle.
	e.SelectNone()
	tt.Frame()
	var rect doc.ID
	for id, s := range e.Doc.Shapes {
		if s.Type == doc.TypeGeo {
			rect = id
		}
	}
	tt.RightClickAt(300, 350)
	if !e.IsSelected(rect) {
		t.Fatalf("right click selected %v", e.Selection())
	}
	if err := tt.ChooseMenuItem("Duplicate"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if n := len(e.Doc.Shapes); n != 3 {
		t.Errorf("%d shapes after Duplicate from the menu", n)
	}
	tt.RightClickAt(300, 350)
	if err := tt.ChooseMenuItem("Arrange", "Bring to Front"); err != nil {
		t.Error(err)
	}
}

func TestExportPNG(t *testing.T) {
	a, tt, _ := newTest(t)
	if _, ok := a.RenderPNG(2); ok {
		t.Error("an empty page exported")
	}
	showcase(t, a, tt)
	data, ok := a.RenderPNG(1)
	if !ok {
		t.Fatal("no png")
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() < 800 || b.Dy() < 500 {
		t.Errorf("exported %v", b)
	}
	if dir := os.Getenv("GODRAW_SNAPSHOTS"); dir != "" {
		if err := os.WriteFile(filepath.Join(dir, "export.png"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// BenchmarkPaint measures a frame of a large drawing: the cost of everything
// but the GPU.
func BenchmarkPaint(b *testing.B) {
	h := &testHost{}
	d := doc.New()
	for i := range 2000 {
		s := doc.NewShape(doc.NewID("shape"), doc.TypeGeo)
		s.Parent, s.Index = "page:page", doc.FirstIndex
		s.X, s.Y = float64(i%50)*30, float64(i/50)*30
		s.W, s.H = 24, 24
		if i%2 == 0 {
			s.Type = doc.TypeDraw
			s.ScaleX, s.ScaleY = 1, 1
			pts := make([]doc.PointZ, 60)
			for k := range pts {
				pts[k] = doc.PointZ{X: float64(k) * 0.4, Y: 12 + 8*math.Sin(float64(k)/4), Z: .5}
			}
			s.Segments = []doc.Segment{{Type: "free", Points: pts}}
		}
		d.Shapes[s.ID] = s
	}
	a := New(d, h)
	tt := ui.NewTester(a.View, 1600, 1000)
	tt.Frame()
	b.ResetTimer()
	for i := range b.N {
		// Panning redraws everything; nothing is rebuilt.
		tt.Scroll(800, 500, float32(i%3), 1)
	}
}

func TestDropImage(t *testing.T) {
	a, tt, h := newTest(t)
	e := a.Editor()
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	img.Set(5, 5, color.RGBA{255, 0, 0, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "x.png")
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	// What isn't an image is left out, and reading goes on on the side.
	a.DropFiles([]string{path, filepath.Join(dir, "notes.txt")}, 400, 300)
	deadline := time.Now().Add(2 * time.Second)
	for len(e.Doc.Assets) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		h.pump()
		tt.Frame()
	}
	if len(e.Doc.Assets) != 1 || len(e.Doc.Shapes) != 1 {
		t.Fatalf("%d assets, %d shapes after dropping an image", len(e.Doc.Assets), len(e.Doc.Shapes))
	}
	var s *doc.Shape
	for _, v := range e.Doc.Shapes {
		s = v
	}
	if s.Type != doc.TypeImage || s.W != 40 || s.H != 30 {
		t.Errorf("image %+v", s)
	}
	if want := e.ToPage(geo.V(400, 300)); math.Abs(s.X+s.W/2-want.X) > 1 || math.Abs(s.Y+s.H/2-want.Y) > 1 {
		t.Errorf("image at %v,%v, dropped at %v", s.X, s.Y, want)
	}
	// It decodes, and draws.
	for a.bitmapOf(s.AssetID) == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		h.pump()
		tt.Frame()
	}
	if a.bitmapOf(s.AssetID) == nil {
		t.Error("the image never decoded")
	}
}

// Frames clip what they hold, turned or not.
func TestFrameClipsWhenTurned(t *testing.T) {
	a, tt, _ := newTest(t)
	a.SetTheme("light")
	e := a.Editor()
	tt.Frame()
	tt.Key(0, ui.KeyF)
	drag(tt, 200, 200, 500, 450)
	frame := e.Selection()[0]
	e.Next.Fill, e.Next.Color = "fill", "red"
	e.SetGeoTool("ellipse")
	drag(tt, 400, 300, 640, 420) // starts inside, so it is the frame's
	if s := e.Doc.Shapes[e.Selection()[0]]; s.Parent != frame {
		t.Fatalf("parent %s, want the frame", s.Parent)
	}
	e.SelectNone()
	tt.Frame()
	red := func(x, y int) bool {
		r, g, b, _ := tt.Image().At(x, y).RGBA()
		return r>>8 > 180 && g>>8 < 90 && b>>8 < 90
	}
	// In the frame it shows; past its edge, it doesn't.
	if !red(450, 360) || red(560, 360) {
		t.Errorf("straight: inside red %v, outside red %v", red(450, 360), red(560, 360))
	}
	snapshot(t, tt, "frame-straight")
	e.Select(frame)
	box := geo.V(350, 325) // around the frame's middle
	drag(tt, 508, 458, 508-60, 458+70)
	_ = box
	e.SelectNone()
	tt.Frame()
	snapshot(t, tt, "frame-turned")
	if r := e.Doc.Shapes[frame].Rotation; math.Abs(r) < 0.1 {
		t.Fatalf("frame did not turn: %v", r)
	}
	// The child's page position past the frame's turned edge must not show.
	m := e.PageMat(frame)
	s := e.Doc.Shapes[frame]
	out := m.Apply(geo.V(s.W+30, s.H/2))
	in := m.Apply(geo.V(s.W-40, s.H/2))
	if red(int(out.X), int(out.Y)) {
		t.Errorf("turned: child shows past the frame's edge at %v", out)
	}
	if !red(int(in.X), int(in.Y)) {
		t.Errorf("turned: child gone inside the frame at %v", in)
	}
}

// A page dense with long strokes must not push out what paints after it. Each
// stroke's mask is its whole bounding box, and they share an atlas with the
// window's icons and text.
func TestDensePageLosesNothing(t *testing.T) {
	d := doc.New()
	index := ""
	add := func(s *doc.Shape) {
		s.Parent = "page:page"
		index = doc.IndexBetween(index, "")
		s.Index = index
		d.Shapes[s.ID] = s
	}
	for i := range 1500 {
		s := doc.NewShape(doc.NewID("shape"), doc.TypeDraw)
		s.X, s.Y = float64(i%40)*20, float64(i%30)*10
		pts := make([]doc.PointZ, 60)
		for k := range pts {
			pts[k] = doc.PointZ{X: float64(k) * 6, Y: float64(k) * 5, Z: .5}
		}
		s.Segments = []doc.Segment{{Type: "free", Points: pts, Dim: 2}}
		add(s)
	}
	// The last thing the canvas paints: a solid red box, in a corner of the view.
	last := doc.NewShape(doc.NewID("shape"), doc.TypeGeo)
	last.X, last.Y, last.W, last.H = 1000, 500, 100, 100
	last.Color, last.Fill, last.Dash = "red", "fill", "solid"
	add(last)

	a := New(d, &testHost{})
	tt := ui.NewTester(a.View, 1280, 820)
	tt.SetScale(2)
	tt.Frame()
	tt.Frame()
	img := tt.Image()
	p := a.Editor().ToScreen(geo.V(1050, 550))
	r, g, b, _ := img.At(int(p.X*2), int(p.Y*2)).RGBA()
	if r>>8 <= 180 || g>>8 >= 90 || b>>8 >= 90 {
		t.Errorf("the last shape didn't paint: pixel at %v is %d,%d,%d", p, r>>8, g>>8, b>>8)
	}
	// And the window's own icons still show: the select tool's button holds ink.
	box, ok := tt.Find("Hand — H")
	if !ok {
		t.Fatal("no hand button")
	}
	bg := img.At(int(box.X+1)*2, int(box.Y+1)*2)
	inked := false
	for y := int(box.Y) * 2; y < int(box.Y+box.H)*2; y++ {
		for x := int(box.X) * 2; x < int(box.X+box.W)*2; x++ {
			if img.At(x, y) != bg {
				inked = true
			}
		}
	}
	if !inked {
		t.Error("the toolbar's icons are gone")
	}
}

// Export tiles must meet without a seam, and hold everything a dense page has.
func TestExportDensePageInTiles(t *testing.T) {
	a, tt, _ := newTest(t)
	e := a.Editor()
	e.SetGeoTool("rectangle")
	e.Next.Fill, e.Next.Color, e.Next.Dash = "fill", "blue", "solid"
	// Across where tiles meet (512 DIPs apart at scale 2, from the box's corner).
	drag(tt, 100, 100, 1100, 700)
	data, ok := a.RenderPNG(2)
	if !ok {
		t.Fatal("no png")
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	// Inside the rectangle, every pixel is its color, seams included.
	want := img.At(b.Dx()/2, b.Dy()/2)
	for y := 120; y < b.Dy()-120; y++ {
		for x := 120; x < b.Dx()-120; x++ {
			if img.At(x, y) != want {
				t.Fatalf("pixel %d,%d is %v inside a solid %v", x, y, img.At(x, y), want)
			}
		}
	}
}

func TestMCPDialog(t *testing.T) {
	const url = "http://127.0.0.1:7463/mcp"
	was := mcpCopiedFor
	mcpCopiedFor = 10 * time.Millisecond
	defer func() { mcpCopiedFor = was }()
	h := &testHost{}
	a := New(doc.New(), h)
	st := MCPStatus{}
	a.MCP = func() MCPStatus { return st }
	tt := ui.NewTester(a.View, 1280, 820)
	tt.Frame()
	if _, ok := tt.Find("MCP server: starting"); !ok {
		t.Error("no sign of a server starting")
	}

	st = MCPStatus{URL: url}
	tt.Frame()
	if err := tt.Click("MCP server: running"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	snapshot(t, tt, "mcp-dialog")
	want := "claude mcp add --scope project --transport http godraw " + url
	if !tt.HasText(want) || !tt.HasText(url) || !tt.HasText("Running") {
		t.Fatalf("dialog shows %v", tt.Texts())
	}
	before, ok := tt.Find("Copy Claude Code")
	if !ok {
		t.Fatal("no copy button")
	}
	if err := tt.Click("Copy Claude Code"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := tt.Clipboard(); got != want {
		t.Errorf("copied %q", got)
	}
	after, ok := tt.Find("Copied Claude Code")
	if !ok {
		t.Fatal("nothing says it copied")
	}
	if after != before {
		t.Errorf("the button moved as it copied: %+v to %+v", before, after)
	}
	snapshot(t, tt, "mcp-copied")
	// It goes back by itself, unless another copy came since.
	time.Sleep(50 * time.Millisecond)
	h.pump()
	tt.Frame()
	if _, ok := tt.Find("Copied Claude Code"); ok {
		t.Error("still says copied")
	}
	if err := tt.Click("Copy Other MCP clients"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := tt.Clipboard(); got != url {
		t.Errorf("copied %q", got)
	}
	a.mcpCopies++ // as another copy would
	time.Sleep(50 * time.Millisecond)
	h.pump()
	tt.Frame()
	if _, ok := tt.Find("Copied Other MCP clients"); !ok {
		t.Error("an old timer undid a newer copy")
	}
	a.mcpCopied = ""

	// Keys are the dialog's while it is open.
	tt.Key(0, ui.KeyD)
	tt.Frame()
	if a.Editor().Tool == editor.ToolDraw {
		t.Error("a key reached the canvas through the dialog")
	}
	if err := tt.Click("Close"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText(want) {
		t.Error("the dialog stayed")
	}
	tt.Key(0, ui.KeyD)
	tt.Frame()
	if a.Editor().Tool != editor.ToolDraw {
		t.Error("keys didn't come back")
	}
	a.Editor().SetTool(editor.ToolSelect)
	tt.Frame()

	// The menu opens it.
	if err := tt.Click("More"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	snapshot(t, tt, "mcp-menu")
	if err := tt.Click("MCP Server…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(want) {
		t.Error("the menu didn't open it")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tt.HasText(want) {
		t.Error("Escape didn't close it")
	}

	st = MCPStatus{Problem: "Port 7463 is in use."}
	tt.Frame()
	if err := tt.Click("MCP server: not running"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	snapshot(t, tt, "mcp-problem")
	if !tt.HasText("Port 7463 is in use.") || !tt.HasText("Not running") || tt.HasText(want) {
		t.Errorf("failed dialog shows %v", tt.Texts())
	}
}

// At any size of window, all of the dialog shows, and the command breaks at
// its space, not inside its URL.
func TestMCPDialogFitsTheWindow(t *testing.T) {
	const url = "http://127.0.0.1:7463/mcp"
	h := &testHost{}
	a := New(doc.New(), h)
	a.MCP = func() MCPStatus { return MCPStatus{URL: url} }
	tt := ui.NewTester(a.View, 1280, 820)
	a.ShowMCP()
	tt.Frame()
	whole := MCPStatus{URL: url}.ClaudeCommand()
	for _, w := range []int{1280, 900, 760, 700, 688, 660, 640} {
		tt.SetSize(w, 420)
		tt.Frame()
		for _, name := range []string{"Copy Claude Code", "Copy Other MCP clients", "Close"} {
			// Find clips to what shows, so a button cut off comes out narrower.
			box, ok := tt.Find(name)
			if !ok {
				t.Fatalf("%d: no %s", w, name)
			}
			if box.W < 32 || box.X+box.W > float32(w)-8 {
				t.Errorf("%d: %s at %+v", w, name, box)
			}
		}
		broken := false
		for _, s := range tt.Texts() {
			if strings.Contains(s, " \\\n"+url) {
				broken = true
			}
		}
		if !broken && !tt.HasText(whole) {
			t.Errorf("%d: command shows as %v", w, tt.Texts())
		}
		if w == 640 {
			snapshot(t, tt, "mcp-small")
			if !broken {
				t.Log("the command fits on one line at the smallest size")
			}
		}
		// What it copies never has the break in it.
		if err := tt.Click("Copy Claude Code"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if got := tt.Clipboard(); got != whole {
			t.Errorf("%d: copied %q", w, got)
		}
		a.mcpCopied = ""
	}
}

// The button sits where it covers nothing, at the smallest window too.
func TestMCPButtonCoversNoTools(t *testing.T) {
	h := &testHost{}
	a := New(doc.New(), h)
	a.MCP = func() MCPStatus { return MCPStatus{URL: "http://127.0.0.1:7463/mcp"} }
	tt := ui.NewTester(a.View, 640, 420)
	tt.Frame()
	mcp, ok := tt.Find("MCP server: running")
	if !ok {
		t.Fatal("no button")
	}
	for _, name := range []string{"Select — V", "Hand — H", "Draw — D", "Zoom out — ⌘-", "Black", "black"} {
		if box, ok := tt.Find(name); ok && box.X < mcp.X+mcp.W && mcp.X < box.X+box.W && box.Y < mcp.Y+mcp.H && mcp.Y < box.Y+box.H {
			t.Errorf("it covers %s: %+v and %+v", name, mcp, box)
		}
	}
	// The window's bottom panels are as they were without it: nothing there
	// grew to hold it.
	tt.SetSize(1100, 700)
	tt.Frame()
	zoom, _ := tt.Find("Keyboard shortcuts — ?")
	sel, _ := tt.Find("Select — V")
	if zoom.X+zoom.W >= sel.X {
		t.Errorf("the zoom panel reaches the toolbar at 1100: %+v, %+v", zoom, sel)
	}
}

func TestNoMCPWithoutAServer(t *testing.T) {
	_, tt, _ := newTest(t)
	for _, name := range []string{"MCP", "MCP server starting"} {
		if _, ok := tt.Find(name); ok {
			t.Errorf("found %q", name)
		}
	}
}

// A marker goes under what it marks in what an export draws, as on the canvas.
func TestExportPutsHighlightsUnder(t *testing.T) {
	a, _, _ := newTest(t)
	e := a.ed
	drag := func(x0, y0, x1, y1 float64) {
		e.PointerMove(x0, y0, editor.Mods{}, 0)
		e.PointerDown(x0, y0, 0, 1, editor.Mods{}, 0)
		e.PointerMove(x1, y1, editor.Mods{}, 0)
		e.PointerUp(x1, y1, editor.Mods{}, 0)
	}
	e.SetGeoTool("rectangle")
	e.Next.Fill = "solid"
	drag(100, 100, 300, 300)
	box := geo.Box{X: 50, Y: 50, W: 300, H: 300}
	at := func() color.Color {
		return a.RenderImage(box, e.Children(e.Page), 1).At(150, 150)
	}
	before := at()
	e.SetTool(editor.ToolHighlight)
	e.Next.Color = "yellow"
	drag(80, 200, 320, 200)
	if after := at(); after != before {
		t.Errorf("the marker shows over a solid box: %v, was %v", after, before)
	}
}
