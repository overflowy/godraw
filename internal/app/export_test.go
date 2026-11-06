package app

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"
	"runtime"
	"runtime/debug"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"godraw/internal/editor"
	"godraw/internal/geo"
	"godraw/internal/stress"
)

// A frame is painted once, at the size and scale it ends at: a frame painted at
// scale 1 first leaves its buffer behind, a thousand DIPs a side at scale 0.1 being
// a hundred times the picture. It comes out as a window of that size would show it.
func TestRenderFrameIsPaintedAtItsScale(t *testing.T) {
	a := New(stress.Diagram(120), &testHost{})
	e := a.Editor()
	box, ids, ok := e.ExportBounds()
	if !ok {
		t.Fatal("nothing to render")
	}
	pal := e.Palette()
	for _, scale := range []float64{0.05, 0.19, 0.5, 1, 2} {
		t.Run(fmt.Sprint(scale), func(t *testing.T) {
			side := int(math.Ceil(512 / scale))
			paint := func(p *ui.Painter, r ui.Rect) {
				s := &scene{a: a, e: e, p: p, pal: pal, export: true, shrink: math.Min(scale, 1)}
				s.cam = editor.Camera{X: -box.X, Y: -box.Y, Z: 1}
				s.view = geo.Box{X: box.X, Y: box.Y, W: float64(side), H: float64(side)}
				p.Fill(r, toUI(pal.Background), 0)
				s.paint(ids)
			}
			var frames []string
			got := renderFrame(side, scale, func(p *ui.Painter, r ui.Rect) {
				frames = append(frames, fmt.Sprintf("%gx%g at %g", r.W, r.H, p.Scale()))
				paint(p, r)
			})
			want := fmt.Sprintf("%dx%d at %g", side, side, float32(scale))
			if len(frames) != 1 || frames[0] != want {
				t.Errorf("painted %v, want one frame of %s", frames, want)
			}
			px := int(math.Ceil(float64(side) * scale))
			if b := got.Bounds(); b.Dx() != px || b.Dy() != px {
				t.Errorf("a frame of %v, want %d a side", b, px)
			}
			same := ui.Render(func(c *ui.Context) {
				ui.Themed(c, paintTheme(c.Theme())).Background(ui.Transparent).Fill().Children(func() {
					ui.Box(c).Fill().Draw(paint)
				})
			}, side, side, float32(scale))
			if d := blockDiff(got, same, 4); d > 2 {
				t.Errorf("a frame differs by %.1f from a window's", d)
			}
		})
	}
}

// halved returns a picture at half the size, each pixel the mean of four.
func halved(p *image.RGBA) *image.RGBA {
	b := p.Bounds()
	q := image.NewRGBA(image.Rect(0, 0, b.Dx()/2, b.Dy()/2))
	for y := range q.Rect.Dy() {
		for x := range q.Rect.Dx() {
			for c := range 4 {
				n := 0
				for _, o := range [][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
					n += int(p.Pix[p.PixOffset(2*x+o[0], 2*y+o[1])+c])
				}
				q.Pix[q.PixOffset(x, y)+c] = uint8(n / 4)
			}
		}
	}
	return q
}

// A page comes out the same at any size, in one tile or many, and with what it
// draws where it should be: no tile is left out, shifted or cut short.
func TestRenderSameAtEverySize(t *testing.T) {
	a := New(stress.Diagram(60), &testHost{})
	e := a.Editor()
	box, ids, ok := e.ExportBounds()
	if !ok {
		t.Fatal("nothing to render")
	}
	whole := math.Max(box.W, box.H)
	for _, px := range []float64{512, 768, 1400, 2048, 3000} {
		t.Run(fmt.Sprint(px), func(t *testing.T) {
			scale := px / whole
			big := a.RenderImage(box, ids, scale)
			small := a.RenderImage(box, ids, scale/2)
			if b := big.Bounds(); math.Max(float64(b.Dx()), float64(b.Dy())) != math.Ceil(px) {
				t.Fatalf("%v for %g px", b, px)
			}
			half := halved(big)
			r := half.Bounds().Intersect(small.Bounds())
			const block = 32
			if d := blockDiff(half.SubImage(r).(*image.RGBA), small.SubImage(r).(*image.RGBA), block); d > 36 {
				t.Errorf("blocks of %d px differ by %.1f across sizes", block, d)
			}
			if inked(big) < 0.02 {
				t.Errorf("only %.3f of the picture has ink", inked(big))
			}
		})
	}
}

// inked returns how much of a picture isn't its top left pixel's color.
func inked(p *image.RGBA) float64 {
	bg := [3]uint8{p.Pix[0], p.Pix[1], p.Pix[2]}
	n := 0
	for i := 0; i+3 < len(p.Pix); i += 4 {
		if [3]uint8{p.Pix[i], p.Pix[i+1], p.Pix[i+2]} != bg {
			n++
		}
	}
	return float64(n) / float64(len(p.Pix)/4)
}

// gcPercent reads the GC's setting, which only setting it can.
func gcPercent() int {
	p := debug.SetGCPercent(100)
	debug.SetGCPercent(p)
	return p
}

// forcedGCs counts the collections asked for, as debug.FreeOSMemory does.
func forcedGCs() uint32 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.NumForcedGC
}

// exporting returns a window of a page that measures text as it paints, for
// a test to see into a render.
func exporting(t *testing.T, during func()) *App {
	t.Helper()
	a := New(stress.Diagram(40), &testHost{})
	e := a.Editor()
	if _, _, ok := e.ExportBounds(); !ok {
		t.Fatal("nothing to render")
	}
	measure := e.Measure
	e.Measure = func(text, font string, size, width float64) editor.TextBlock {
		during()
		return measure(text, font, size, width)
	}
	return a
}

// An export runs the GC often, then puts it back as it was and gives the memory back.
func TestExportCollectsHard(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(150))
	during := 0
	a := exporting(t, func() { during = gcPercent() })
	forced := forcedGCs()
	if _, ok := a.RenderPNG(1); !ok {
		t.Fatal("no png")
	}
	if during != exportGCPercent {
		t.Errorf("rendering with the GC at %d", during)
	}
	if got := gcPercent(); got != 150 {
		t.Errorf("the GC left at %d", got)
	}
	if forcedGCs() == forced {
		t.Error("nothing was given back")
	}
}

// A panic in the middle of an export leaves the GC as it was too.
func TestExportRestoresGCAfterPanic(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(150))
	a := exporting(t, func() { panic("in the middle") })
	func() {
		defer func() {
			if recover() == nil {
				t.Error("the export didn't panic")
			}
		}()
		a.RenderPNG(1)
	}()
	if got := gcPercent(); got != 150 {
		t.Errorf("the GC left at %d", got)
	}
}

// Exports that overlap leave the GC tight until the last ends, and as it was then.
func TestOverlappingExportsRestoreGC(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(150))
	first := collectHard()
	second := collectHard()
	first()
	if got := gcPercent(); got != exportGCPercent {
		t.Errorf("the GC at %d with an export still rendering", got)
	}
	second()
	if got := gcPercent(); got != 150 {
		t.Errorf("the GC left at %d", got)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 4 {
				collectHard()()
			}
		})
	}
	wg.Wait()
	if got := gcPercent(); got != 150 {
		t.Errorf("the GC left at %d after a crowd of exports", got)
	}
}

// What agents view leaves the GC alone: a burst of them must not hold up the canvas.
func TestViewLeavesGCAlone(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(150))
	during := 0
	a := exporting(t, func() { during = gcPercent() })
	box, ids, _ := a.Editor().ExportBounds()
	forced := forcedGCs()
	for range 5 {
		a.RenderImage(box, ids, 0.5)
	}
	if during != 150 {
		t.Errorf("viewing with the GC at %d", during)
	}
	if got := forcedGCs() - forced; got != 0 {
		t.Errorf("%d views forced %d collections", 5, got)
	}
}

// Copy as PNG sits above Export as PNG in both menus of the canvas, and puts
// the drawing on the clipboard as a PNG.
func TestCopyAsPNGFromTheMenus(t *testing.T) {
	for _, menu := range []string{"more", "context"} {
		t.Run(menu, func(t *testing.T) {
			_, tt, h := newTest(t)
			tt.Key(0, ui.KeyR)
			drag(tt, 300, 300, 500, 400)
			tt.Key(0, ui.KeyEscape)
			tt.Frame()
			if menu == "more" {
				// A popover: its items are on the screen, not in tt.Menu.
				if err := tt.Click("More"); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
				cp, ok1 := tt.Find("Copy as PNG")
				ex, ok2 := tt.Find("Export as PNG…")
				if !ok1 || !ok2 || cp.Y >= ex.Y || ex.Y-cp.Y > 2*cp.H {
					t.Fatalf("Copy as PNG isn't just above Export as PNG: %v %v", cp, ex)
				}
				if err := tt.Click("Copy as PNG"); err != nil {
					t.Fatal(err)
				}
			} else {
				tt.RightClickAt(700, 600)
				tt.Frame()
				items := tt.Menu()
				copyAt, exportAt := slices.Index(items, "Copy as PNG"), slices.Index(items, "Export as PNG…")
				if copyAt < 0 || copyAt+1 != exportAt {
					t.Fatalf("Copy as PNG isn't just above Export as PNG: %q", items)
				}
				if err := tt.ChooseMenuItem("Copy as PNG"); err != nil {
					t.Fatal(err)
				}
			}
			data := awaitPNG(t, tt, h)
			img, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if b := img.Bounds(); b.Dx() < 400 || b.Dy() < 200 {
				t.Errorf("a %v image of a 200 by 100 rectangle at 2x", b)
			}
		})
	}
}

// awaitPNG waits for a PNG drawing on the side to reach the clipboard.
func awaitPNG(t *testing.T, tt *ui.Tester, h *testHost) []byte {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute) // -race is slow
	for {
		h.pump()
		if data, err := tt.ClipboardData().Read(transfer.PNG); err == nil {
			return data
		}
		if time.Now().After(deadline) {
			t.Fatal("no PNG reached the clipboard")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Copy as PNG draws on the side: the window goes on taking input and drawing
// frames, editing the drawing as it goes, and what lands on the clipboard is
// the drawing as it was when it was asked for. Run with -race.
func TestCopyAsPNGLeavesTheWindowFree(t *testing.T) {
	a := New(stress.Diagram(400), &testHost{})
	h := a.host.(*testHost)
	tt := ui.NewTester(a.View, 1280, 820)
	tt.Frame()
	e := a.Editor()
	before := len(e.Doc.Shapes)
	// What it should come out as: the drawing and theme as they were, in tiles
	// of the same size, as gradients round a little differently in others.
	was := New(e.Doc.Snapshot(), &testHost{})
	box, ids, _ := was.Editor().ExportBounds()
	wantPic := was.newPicture(box.Expand(32), ids, 2, sideTile)
	wantPic.draw(0)

	start := time.Now()
	a.CopyPNG()
	if took := time.Since(start); took > 150*time.Millisecond {
		t.Errorf("CopyPNG held the window for %v", took)
	}
	// Meanwhile: new shapes, a pan, a theme change, frames.
	for i := range 5 {
		tt.Key(0, ui.KeyR)
		x := float32(100 + 60*i)
		drag(tt, x, 100, x+40, 140)
		e.SetCam(editor.Camera{X: e.Cam().X + 10, Y: e.Cam().Y, Z: e.Cam().Z})
		a.ToggleDark()
		tt.Frame()
	}
	if len(e.Doc.Shapes) != before+5 {
		t.Fatalf("%d shapes, not %d", len(e.Doc.Shapes), before+5)
	}
	got, err := png.Decode(bytes.NewReader(awaitPNG(t, tt, h)))
	if err != nil {
		t.Fatal(err)
	}
	wantImg := wantPic.out
	if got.Bounds() != wantImg.Bounds() {
		t.Fatalf("copied %v, not %v", got.Bounds(), wantImg.Bounds())
	}
	if n := differentPixels(got, wantImg); n > 0 {
		t.Errorf("%d pixels differ from the drawing as it was when copied", n)
	}

	// Two copies in a row: the first to finish doesn't take the clipboard from the
	// second.
	tt.SetClipboard("")
	a.CopyPNG()
	first := a.pngCopies
	a.CopyPNG()
	if a.pngCopies != first+1 {
		t.Fatal("the second copy wasn't counted")
	}
	awaitPNG(t, tt, h)
}

func differentPixels(a, b image.Image) int {
	n := 0
	r := a.Bounds()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if a.At(x, y) != b.At(x, y) {
				n++
			}
		}
	}
	return n
}
