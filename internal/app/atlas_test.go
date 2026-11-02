package app

import (
	"fmt"
	"image"
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"

	"godraw/internal/doc"
	"godraw/internal/editor"
	"godraw/internal/geo"
)

// nested returns a drawing of n rectangles, each inside the last, as thin lines.
func nested(n int, fill, dash string) (*doc.Drawing, []geo.Box) {
	d := doc.New()
	index := ""
	var boxes []geo.Box
	for i := range n {
		s := doc.NewShape(doc.NewID("shape"), doc.TypeGeo)
		at := float64(i) * 25
		s.X, s.Y, s.W, s.H = at, at, 3000-2*at, 2000-2*at
		s.Size, s.Dash, s.Fill, s.Color = "s", dash, fill, "black"
		s.Parent = "page:page"
		index = doc.IndexBetween(index, "")
		s.Index = index
		d.Shapes[s.ID] = s
		boxes = append(boxes, geo.Box{X: s.X, Y: s.Y, W: s.W, H: s.H})
	}
	return d, boxes
}

// inkAt says whether a horizontal run of pixels across x holds anything darker
// than the page.
func inkAt(img *image.RGBA, x, y int) bool {
	for dy := -3; dy <= 3; dy++ {
		for dx := -3; dx <= 3; dx++ {
			r, _, _, _ := img.At(x+dx, y+dy).RGBA()
			if r>>8 < 200 {
				return true
			}
		}
	}
	return false
}

// A picture of a page shows every stroke on it, however many long ones there are.
func TestRenderKeepsEveryStroke(t *testing.T) {
	for _, scale := range []float64{0.34, 0.68, 2} {
		for _, n := range []int{6, 40} {
			t.Run(fmt.Sprintf("scale %v of %d", scale, n), func(t *testing.T) {
				d, boxes := nested(n, "none", "solid")
				a := New(d, &testHost{})
				e := a.Editor()
				box := geo.Box{X: -10, Y: -10, W: 3020, H: 2020}
				img := a.RenderImage(box, e.Children(e.Page), scale)
				for i, b := range boxes {
					for _, p := range []geo.Vec{
						geo.V(b.X+b.W/2, b.Y), geo.V(b.X+b.W/2, b.MaxY()),
						geo.V(b.X, b.Y+b.H/2), geo.V(b.MaxX(), b.Y+b.H/2),
					} {
						x, y := int((p.X-box.X)*scale), int((p.Y-box.Y)*scale)
						if !inkAt(img, x, y) {
							t.Errorf("rectangle %d has no edge at %v", i, p)
						}
					}
				}
			})
		}
	}
}

// Two pictures of one page are the same picture.
func TestRenderIsStable(t *testing.T) {
	d, _ := nested(30, "none", "solid")
	a := New(d, &testHost{})
	e := a.Editor()
	box := geo.Box{X: -10, Y: -10, W: 3020, H: 2020}
	first := a.RenderImage(box, e.Children(e.Page), 0.68)
	for range 3 {
		again := a.RenderImage(box, e.Children(e.Page), 0.68)
		if string(first.Pix) != string(again.Pix) {
			t.Fatal("two renders of one page differ")
		}
	}
}

// The window keeps its icons and every stroke, whatever the zoom.
func TestWindowKeepsStrokesAndIcons(t *testing.T) {
	for _, z := range []float64{0.3, 0.48, 0.6, 1} {
		t.Run(fmt.Sprint(z), func(t *testing.T) {
			d, boxes := nested(40, "none", "solid")
			a := New(d, &testHost{})
			e := a.Editor()
			tt := ui.NewTester(a.View, 1280, 820)
			tt.SetScale(2)
			tt.Frame()
			e.SetCam(editor.Camera{X: 40 / z, Y: 90 / z, Z: z})
			tt.Frame()
			tt.Frame()
			img := tt.Image()
			shown := 0
			for i, b := range boxes {
				p := e.ToScreen(geo.V(b.X+b.W/4, b.Y))
				if p.X < 100 || p.Y < 100 || p.X > 1200 || p.Y > 700 {
					continue
				}
				shown++
				if !inkAt(img, int(p.X*2), int(p.Y*2)) {
					t.Errorf("rectangle %d has no top edge at %v", i, p)
				}
			}
			if shown == 0 {
				t.Fatal("no rectangle in view")
			}
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
		})
	}
}

// variety returns a page with every way there is of stroking and filling.
func variety() *doc.Drawing {
	d := doc.New()
	index := ""
	add := func(s *doc.Shape) {
		s.Parent = "page:page"
		index = doc.IndexBetween(index, "")
		s.Index = index
		d.Shapes[s.ID] = s
	}
	i := 0
	for _, g := range []string{"rectangle", "ellipse", "diamond", "star", "cloud", "x-box", "hexagon", "arrow-right"} {
		for _, st := range []struct {
			dash, fill string
			opacity    float64
		}{{"solid", "none", 1}, {"dashed", "semi", 1}, {"dotted", "pattern", 1}, {"draw", "solid", 1}, {"solid", "fill", 0.5}, {"dashed", "lined-fill", 0.5}} {
			s := doc.NewShape(doc.NewID("shape"), doc.TypeGeo)
			s.Geo, s.Dash, s.Fill, s.Opacity = g, st.dash, st.fill, st.opacity
			s.Color, s.Size = "blue", "l"
			s.X, s.Y, s.W, s.H = float64(i%6)*700, float64(i/6)*520, 640, 460
			if i%5 == 0 {
				s.Rotation = 0.3
			}
			add(s)
			i++
		}
	}
	return d
}

// Cells paint what one mask would, and nothing shows where they meet: blocks of
// pixels come out the same, antialiasing aside. Only at sizes where one mask per
// path still fits the atlas, for it to compare with.
func TestCellsMatchOneMask(t *testing.T) {
	for _, scale := range []float64{0.5, 1} {
		t.Run(fmt.Sprint(scale), func(t *testing.T) {
			a := New(variety(), &testHost{})
			e := a.Editor()
			box, ids, ok := e.ExportBounds()
			if !ok {
				t.Fatal("nothing to render")
			}
			cells := a.RenderImage(box, ids, scale)
			was := wholeMask
			wholeMask = 1e9
			defer func() { wholeMask = was }()
			whole := a.RenderImage(box, ids, scale)
			const block = 16
			b := cells.Bounds()
			for y := 0; y+block <= b.Dy(); y += block {
				for x := 0; x+block <= b.Dx(); x += block {
					for c := range 3 {
						d := 0
						for j := range block {
							for i := range block {
								d += int(cells.Pix[cells.PixOffset(x+i, y+j)+c]) - int(whole.Pix[whole.PixOffset(x+i, y+j)+c])
							}
						}
						if d/(block*block) > 14 || d/(block*block) < -14 {
							t.Fatalf("block at %d,%d is off by %d", x, y, d/(block*block))
						}
					}
				}
			}
		})
	}
}

// Fills cost what shows of them, so a pile of hatching is where cells can't help:
// a picture splits its tiles, and the window draws lines.
func hatched(n int) *doc.Drawing {
	d := doc.New()
	index := ""
	for i := range n + 1 {
		s := doc.NewShape(doc.NewID("shape"), doc.TypeGeo)
		s.X, s.Y, s.W, s.H = 0, 0, 1200, 760
		s.Dash, s.Fill, s.Color = "solid", "lined-fill", "blue"
		if i == n {
			s.X, s.Y, s.W, s.H, s.Fill, s.Color = 1000, 600, 100, 100, "fill", "red"
		}
		s.Parent = "page:page"
		index = doc.IndexBetween(index, "")
		s.Index = index
		d.Shapes[s.ID] = s
	}
	return d
}

func TestPileOfHatchingLosesNothing(t *testing.T) {
	red := func(c color.Color) bool {
		r, g, b, _ := c.RGBA()
		return r>>8 > 180 && g>>8 < 110 && b>>8 < 110
	}
	d := hatched(80)
	a := New(d, &testHost{})
	e := a.Editor()
	img := a.RenderImage(geo.Box{W: 1200, H: 760}, e.Children(e.Page), 2)
	if !red(img.At(2100, 1300)) {
		t.Errorf("picture: the last shape didn't paint: %v", img.At(2100, 1300))
	}
	tt := ui.NewTester(a.View, 1280, 820)
	tt.SetScale(2)
	e.SetCam(editor.Camera{X: 40, Y: 30, Z: 1})
	tt.Frame()
	tt.Frame()
	shot := tt.Image()
	p := e.ToScreen(geo.V(1050, 650))
	if !red(shot.At(int(p.X*2), int(p.Y*2))) {
		t.Errorf("window: the last shape didn't paint: %v", shot.At(int(p.X*2), int(p.Y*2)))
	}
}

// BenchmarkPaintLongStrokes paints a page of long strokes, each frame moved, so that masks
// aren't all cached. Compare with -bench and wholeMask at 1e9.
func BenchmarkPaintLongStrokes(b *testing.B) {
	for _, w := range []float64{256, 1e9} {
		b.Run(fmt.Sprint(w), func(b *testing.B) {
			was := wholeMask
			wholeMask = w
			defer func() { wholeMask = was }()
			d, _ := nested(5, "none", "solid")
			a := New(d, &testHost{})
			e := a.Editor()
			tt := ui.NewTester(a.View, 1280, 820)
			tt.SetScale(2)
			for i := 0; b.Loop(); i++ {
				e.SetCam(editor.Camera{X: float64(i%40) + 0.3, Y: 30, Z: 0.5})
				tt.Frame()
			}
		})
	}
}
