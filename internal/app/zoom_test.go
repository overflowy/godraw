package app

import (
	"fmt"
	"image"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"godraw/internal/doc"
	"godraw/internal/editor"
	"godraw/internal/geo"
	"godraw/internal/stress"
)

// canvasOnly returns a window of a drawing without its panels, at 2x.
func canvasOnly(d *doc.Drawing, cam editor.Camera) (*App, *ui.Tester) {
	a := New(d, &testHost{})
	a.Editor().FocusMode = true
	tt := ui.NewTester(a.View, 1280, 820)
	tt.SetScale(2)
	tt.Frame()
	a.Editor().SetCam(cam)
	tt.Frame()
	tt.Frame()
	return a, tt
}

// blockDiff returns how far the mean of a color channel of a block of pixels is
// from the other picture's, at most, for all blocks.
func blockDiff(p, q *image.RGBA, block int) float64 {
	worst := 0.0
	b := p.Bounds()
	for y := 0; y+block <= b.Dy(); y += block {
		for x := 0; x+block <= b.Dx(); x += block {
			for c := range 3 {
				d := 0
				for j := range block {
					for i := range block {
						d += int(p.Pix[p.PixOffset(x+i, y+j)+c]) - int(q.Pix[q.PixOffset(x+i, y+j)+c])
					}
				}
				worst = max(worst, math.Abs(float64(d))/float64(block*block))
			}
		}
	}
	return worst
}

// A view that pans moves the page by whole pixels, however little it's moved by: the
// same pixels come out, moved, rather than each shape's edges changing as it goes.
func TestPanMovesByWholePixels(t *testing.T) {
	cam := editor.Camera{X: 40.3, Y: 90.7, Z: 0.37}
	_, first := canvasOnly(stress.Diagram(120), cam)
	before := first.Image()
	// 13 device pixels across and 6 down, and a little that's not a whole pixel.
	moved := editor.Camera{X: cam.X + (13+0.2)/(2*cam.Z), Y: cam.Y + (6-0.2)/(2*cam.Z), Z: cam.Z}
	_, second := canvasOnly(stress.Diagram(120), moved)
	after := second.Image()
	b := before.Bounds()
	differ, total := 0, 0
	for y := 0; y+6 < b.Dy(); y++ {
		for x := 0; x+13 < b.Dx(); x++ {
			total++
			for c := range 3 {
				d := int(before.Pix[before.PixOffset(x, y)+c]) - int(after.Pix[after.PixOffset(x+13, y+6)+c])
				if d > 24 || d < -24 {
					differ++
					break
				}
			}
		}
	}
	if differ*2000 > total {
		t.Errorf("%d of %d pixels differ after a pan", differ, total)
	}
}

// settle runs a zoom to its end.
func settle(t *testing.T, a *App, tt *ui.Tester) {
	t.Helper()
	tt.Advance(zoomSettle * 2)
	for i := 0; tt.FramePending() || !a.zoomedAt.IsZero(); i++ {
		if i > 2000 {
			t.Fatal("a zoom never stopped")
		}
		tt.Frame()
		time.Sleep(time.Millisecond)
	}
}

// What a zoom paints lines up with what it comes to once it stops, all the way, and
// what it comes to is what the view paints at that zoom anyway.
func TestZoomComesToWhatItShows(t *testing.T) {
	for _, factor := range []float64{1.012, 0.988} {
		t.Run(fmt.Sprint(factor), func(t *testing.T) {
			a, tt := canvasOnly(stress.Diagram(120), editor.Camera{X: 60, Y: 90, Z: 0.5})
			for range 40 {
				a.ZoomGesture(geo.V(640, 410), factor, tt.Now())
				tt.Frame()
			}
			if a.zoomedAt.IsZero() {
				t.Fatal("no zoom going on")
			}
			zooming := tt.Image()
			moving := image.NewRGBA(zooming.Bounds())
			copy(moving.Pix, zooming.Pix)
			settle(t, a, tt)
			if a.zoom.at != nil {
				t.Error("a zoom that stopped kept what it knew of shapes")
			}
			sharp := tt.Image()
			if d := blockDiff(moving, sharp, 32); d > 40 {
				t.Errorf("a block of the zoom is off by %.1f from what it comes to", d)
			}
			_, fresh := canvasOnly(stress.Diagram(120), a.Editor().Cam())
			if d := blockDiff(sharp, fresh.Image(), 8); d > 12 {
				t.Errorf("a block of a stopped zoom is off by %.1f from the view at that zoom", d)
			}
		})
	}
}

// A window goes quiet after a zoom, rather than painting on.
func TestZoomGoesQuiet(t *testing.T) {
	a, tt := canvasOnly(stress.Diagram(60), editor.Camera{X: 60, Y: 90, Z: 0.5})
	tt.Pinch(640, 410, 1.2)
	if a.zoomedAt.IsZero() {
		t.Fatal("a pinch isn't a zoom going on")
	}
	settle(t, a, tt)
	if tt.FramePending() {
		t.Error("a frame is pending after a zoom stopped")
	}
}

// A zoom past a large shape, turned frames and all, keeps its parts where they go.
func TestZoomOfLargeAndTurnedShapes(t *testing.T) {
	d := doc.New()
	index := ""
	add := func(s *doc.Shape, parent doc.ID) *doc.Shape {
		s.Parent = parent
		index = doc.IndexBetween(index, "")
		s.Index = index
		d.Shapes[s.ID] = s
		return s
	}
	big := doc.NewShape(doc.NewID("shape"), doc.TypeGeo)
	big.X, big.Y, big.W, big.H, big.Geo, big.Fill, big.Color = 100, 100, 1200, 800, "ellipse", "solid", "blue"
	big.SetText("Large")
	add(big, "page:page")
	frame := doc.NewShape(doc.NewID("shape"), doc.TypeFrame)
	frame.X, frame.Y, frame.W, frame.H, frame.Rotation = 400, 300, 300, 200, 0.2
	add(frame, "page:page")
	inside := doc.NewShape(doc.NewID("shape"), doc.TypeGeo)
	inside.X, inside.Y, inside.W, inside.H, inside.Fill, inside.Color = 20, 20, 120, 80, "solid", "red"
	add(inside, frame.ID)
	a, tt := canvasOnly(d, editor.Camera{X: 0, Y: 0, Z: 0.8})
	for range 30 {
		a.ZoomGesture(geo.V(640, 410), 1.02, tt.Now())
		tt.Frame()
	}
	moving := image.NewRGBA(tt.Image().Bounds())
	copy(moving.Pix, tt.Image().Pix)
	settle(t, a, tt)
	if d := blockDiff(moving, tt.Image(), 32); d > 40 {
		t.Errorf("a block of the zoom is off by %.1f from what it comes to", d)
	}
}

// Painting a box as a box, and a path as a line as fine as it shows, comes out as
// the paths they stand in for, as does a tint for a pattern too fine to make out.
func TestStandInsMatchPaths(t *testing.T) {
	for _, st := range []struct {
		name string
		off  func() func()
	}{
		{"boxes", func() func() {
			was := boxesAsBoxes
			boxesAsBoxes = false
			return func() { boxesAsBoxes = was }
		}},
		{"detail", func() func() {
			was := detailSegs
			detailSegs = 1 << 30
			return func() { detailSegs = was }
		}},
		{"hatching", func() func() {
			was := hatchFinest
			hatchFinest = 0
			return func() { hatchFinest = was }
		}},
	} {
		for _, z := range []float64{0.12, 0.3, 1} {
			t.Run(fmt.Sprintf("%s at %v", st.name, z), func(t *testing.T) {
				cam := editor.Camera{X: 40 / z, Y: 60 / z, Z: z}
				_, with := canvasOnly(stress.Diagram(160), cam)
				restore := st.off()
				defer restore()
				_, without := canvasOnly(stress.Diagram(160), cam)
				if d := blockDiff(with.Image(), without.Image(), 16); d > 24 {
					t.Errorf("a block is off by %.1f", d)
				}
			})
		}
	}
}

// A view that pans keeps the cells it cut, and what's made for a frame leaves none.
func TestCellPlansAreKept(t *testing.T) {
	d, _ := nested(6, "none", "solid")
	a, tt := canvasOnly(d, editor.Camera{X: 40, Y: 30, Z: 0.5})
	kept := map[*cellPlan]bool{}
	for _, plan := range a.cells {
		kept[plan] = true
	}
	if len(kept) == 0 {
		t.Fatal("nothing painted in cells")
	}
	e := a.Editor()
	for range 60 {
		e.Pan(1.37, 0.61)
		tt.Frame()
	}
	again := 0
	for _, plan := range a.cells {
		if kept[plan] {
			again++
		}
	}
	if again != len(kept) || len(a.cells) != len(kept) {
		t.Errorf("%d plans at first, %d now, of which %d the same", len(kept), len(a.cells), again)
	}
	// An outline in the way of a large shape is a path made anew each frame.
	e.Select(e.Children(e.Page)[0])
	e.Hovered = e.Children(e.Page)[1]
	before := len(a.cells)
	for range 30 {
		tt.Frame()
	}
	if len(a.cells) != before {
		t.Errorf("%d plans grew to %d with nothing moving", before, len(a.cells))
	}
}
