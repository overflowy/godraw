package app

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"

	"godraw/internal/doc"
	"godraw/internal/editor"
	"godraw/internal/geo"
)

// inkMiddle returns how far from the middle of a box the middle of the red text in
// it is, in the painter's units: down is more.
func inkMiddle(t *testing.T, z float64, scale float32, font, size string) (float64, int) {
	t.Helper()
	d := doc.New()
	s := doc.NewShape(doc.NewID("shape"), doc.TypeGeo)
	s.X, s.Y, s.W, s.H = 100, 100, 600, 300
	s.Parent, s.Index = "page:page", doc.FirstIndex
	s.Color, s.Dash, s.Fill, s.LabelColor, s.Font, s.Size = "black", "solid", "none", "red", font, size
	s.SetText("Label")
	d.Shapes[s.ID] = s
	a := New(d, &testHost{})
	e := a.Editor()
	tt := ui.NewTester(a.View, 1280, 820)
	tt.SetScale(scale)
	tt.Frame()
	// The box's middle in the window's, clear of its panels.
	e.SetCam(editor.Camera{X: 640/z - 400, Y: 410/z - 250, Z: z})
	tt.Frame()
	tt.Frame()
	img := tt.Image()
	k := float64(scale)
	tl, br := e.ToScreen(geo.V(s.X, s.Y)), e.ToScreen(geo.V(s.X+s.W, s.Y+s.H))
	lo, hi, n := 1e9, -1e9, 0
	for y := int(tl.Y*k) + 3; y < int(br.Y*k)-3; y++ {
		for x := int(440 * k); x < int(840*k); x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r>>8 > g>>8+60 && r>>8 > b>>8+60 {
				lo, hi = min(lo, float64(y)), max(hi, float64(y))
				n++
			}
		}
	}
	if n == 0 {
		return 0, 0
	}
	return (lo+hi)/2/k - (tl.Y+br.Y)/2, n
}

// A label stays in the middle of its box however far the view zooms out. Text
// smaller than the theme's used to fall down its box as it shrank.
func TestZoomKeepsTextInPlace(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		for _, font := range []string{"draw", "sans", "serif", "mono"} {
			for _, size := range []string{"s", "m", "xl"} {
				for _, z := range []float64{2, 1, 0.75, 0.5, 0.4, 0.3, 0.25, 0.2, 0.15} {
					off, n := inkMiddle(t, z, scale, font, size)
					t.Logf("scale %v %s %s zoom %v: %.2f off (%d px)", scale, font, size, z, off, n)
					if n == 0 {
						// Too small to read, it's drawn as a line in a gray of its color.
						if editor.LabelSize(size)*z >= 3.5 {
							t.Errorf("scale %v %s %s zoom %v: no text found", scale, font, size, z)
						}
						continue
					}
					if math.Abs(off) > 1.5 {
						t.Errorf("scale %v %s %s zoom %v: text is %.2f off the middle of the box", scale, font, size, z, off)
					}
				}
			}
		}
	}
}

// A frame's name stays just above the frame at any zoom, and so does what's drawn
// at the floor of its size.
func TestZoomKeepsFrameNameInPlace(t *testing.T) {
	for _, z := range []float64{1, 0.5, 0.25, 0.15} {
		d := doc.New()
		f := doc.NewShape(doc.NewID("shape"), doc.TypeFrame)
		f.X, f.Y, f.W, f.H, f.Parent, f.Index, f.Name = 100, 100, 1200, 600, "page:page", doc.FirstIndex, "Frame"
		d.Shapes[f.ID] = f
		a := New(d, &testHost{})
		e := a.Editor()
		tt := ui.NewTester(a.View, 1280, 820)
		tt.SetScale(2)
		tt.Frame()
		e.SetCam(editor.Camera{Z: z, Y: 100 / z})
		tt.Frame()
		tt.Frame()
		img := tt.Image()
		p := e.ToScreen(geo.V(f.X, f.Y))
		bg := img.At(int(p.X*2)+400, int(p.Y*2)-20)
		lo, hi := 1e9, -1e9
		for y := int(p.Y*2) - 60; y < int(p.Y*2)-3; y++ {
			for x := int(p.X * 2); x < int(p.X*2)+160; x++ {
				if img.At(x, y) != bg {
					lo, hi = min(lo, float64(y)), max(hi, float64(y))
				}
			}
		}
		if lo > hi {
			t.Errorf("zoom %v: no name above the frame", z)
			continue
		}
		// Of a 12 px name, drawn 19.2 above the frame: ink well clear of its top edge.
		gap := p.Y - hi/2
		t.Logf("zoom %v: ink %.1f to %.1f above the frame", z, p.Y-lo/2, gap)
		if gap < 3 {
			t.Errorf("zoom %v: name %.1f above the frame", z, gap)
		}
	}
}
