package app

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"godraw/internal/editor"
	"godraw/internal/stress"
)

// perfCase moves something each frame of a large page.
type perfCase struct {
	name string
	step func(a *App, tt *ui.Tester, i int)
}

func perfCases() []perfCase {
	return []perfCase{
		{"idle", func(a *App, tt *ui.Tester, i int) { tt.Frame() }},
		{"pan-whole-pixels", func(a *App, tt *ui.Tester, i int) { tt.Scroll(640, 410, float32(i%2*2-1)*8, 0) }},
		{"pan-trackpad", func(a *App, tt *ui.Tester, i int) { tt.Scroll(640, 410, 1.37, 0.61) }},
		{"zoom-pinch", func(a *App, tt *ui.Tester, i int) {
			f := float32(1.01)
			if i/60%2 == 1 {
				f = 1 / f
			}
			tt.Pinch(640, 410, f)
		}},
		{"hover", func(a *App, tt *ui.Tester, i int) { tt.Move(float32(100+i%1000), float32(100+i%600)) }},
	}
}

func BenchmarkCanvas(b *testing.B) {
	for _, n := range []int{300, 1000} {
		for _, zoom := range []string{"fit", "100"} {
			for _, c := range perfCases() {
				b.Run(fmt.Sprintf("%d/%s/%s", n, zoom, c.name), func(b *testing.B) {
					a := New(stress.Diagram(n), &testHost{})
					tt := ui.NewTester(a.View, 1280, 820)
					tt.SetScale(2)
					tt.Frame()
					e := a.Editor()
					if zoom == "fit" {
						e.ZoomToFit()
					} else {
						e.SetCam(editor.Camera{X: 40, Y: 90, Z: 1})
					}
					tt.Frame()
					tt.Frame()
					var times []time.Duration
					b.ResetTimer()
					for i := 0; b.Loop(); i++ {
						t0 := time.Now()
						c.step(a, tt, i)
						times = append(times, time.Since(t0))
					}
					slices.Sort(times)
					if len(times) > 0 {
						b.ReportMetric(float64(times[len(times)*95/100].Microseconds())/1000, "p95ms")
					}
				})
			}
		}
	}
}
