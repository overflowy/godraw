package editor

import (
	"fmt"
	"testing"

	"godraw/internal/geo"
)

var elbowSides = map[string]geo.Vec{
	"left": geo.V(-1, 0), "right": geo.V(1, 0), "top": geo.V(0, -1), "bottom": geo.V(0, 1),
}

// sideOf returns the middle of a side of a box.
func sideOf(b geo.Box, out geo.Vec) geo.Vec {
	return b.Center().Add(geo.V(out.X*b.W/2, out.Y*b.H/2))
}

// However two shapes lie, a route between any two of their sides leaves and
// arrives the way it was told, in right angles, and stays out of both.
func TestElbowRoutesEverySide(t *testing.T) {
	a := geo.Box{X: 0, Y: 0, W: 120, H: 80}
	for name, from := range elbowSides {
		for other, to := range elbowSides {
			for dx := -480.0; dx <= 480; dx += 120 {
				for dy := -360.0; dy <= 360; dy += 120 {
					if max(dx, -dx) < 160 && max(dy, -dy) < 120 {
						continue // the boxes overlap
					}
					b := geo.Box{X: dx, Y: dy, W: 120, H: 80}
					label := fmt.Sprintf("%s to %s at %v,%v", name, other, dx, dy)
					pts := routeElbow([]geo.Vec{sideOf(a, from), sideOf(b, to)}, from, to, 0.5, []geo.Box{a, b})
					checkRoute(t, label, pts, from, to, a, b)
				}
			}
		}
	}
}

func checkRoute(t *testing.T, label string, pts []geo.Vec, from, to geo.Vec, boxes ...geo.Box) {
	t.Helper()
	if len(pts) < 2 {
		t.Errorf("%s: %v has no legs", label, pts)
		return
	}
	if d := pts[1].Sub(pts[0]).Uni(); d.Dist(from) > 1e-6 {
		t.Errorf("%s: %v leaves going %v", label, pts, d)
	}
	if d := pts[len(pts)-1].Sub(pts[len(pts)-2]).Uni(); d.Dist(to.Mul(-1)) > 1e-6 {
		t.Errorf("%s: %v arrives going %v", label, pts, d)
	}
	for i := 1; i < len(pts); i++ {
		if d := pts[i].Sub(pts[i-1]); d.X != 0 && d.Y != 0 {
			t.Errorf("%s: %v has a slanted leg", label, pts)
		}
		if i >= 2 && pts[i].Sub(pts[i-1]).Uni().Dot(pts[i-1].Sub(pts[i-2]).Uni()) < -0.5 {
			t.Errorf("%s: %v doubles back", label, pts)
		}
		for _, box := range boxes {
			if crossesBox(pts[i-1], pts[i], box.Expand(-1)) {
				t.Errorf("%s: %v enters %v", label, pts, box)
			}
		}
	}
}

// Sides that face each other in line take the straight way, and those that
// don't, no more turns than they need.
func TestElbowRoutesAreSimple(t *testing.T) {
	a := geo.Box{X: 0, Y: 0, W: 100, H: 100}
	for _, c := range []struct {
		name     string
		b        geo.Box
		from, to string
		legs     int
	}{
		{"facing", geo.Box{X: 300, Y: 0, W: 100, H: 100}, "right", "left", 1},
		{"step", geo.Box{X: 300, Y: 200, W: 100, H: 100}, "right", "left", 3},
		{"corner", geo.Box{X: 300, Y: 200, W: 100, H: 100}, "right", "top", 2},
		{"over", geo.Box{X: 300, Y: 0, W: 100, H: 100}, "top", "top", 3},
		{"under", geo.Box{X: 300, Y: 0, W: 100, H: 100}, "bottom", "bottom", 3},
	} {
		from, to := elbowSides[c.from], elbowSides[c.to]
		pts := routeElbow([]geo.Vec{sideOf(a, from), sideOf(c.b, to)}, from, to, 0.5, []geo.Box{a, c.b})
		if len(pts)-1 != c.legs {
			t.Errorf("%s: %d legs, want %d: %v", c.name, len(pts)-1, c.legs, pts)
		}
	}
}

// One point is enough to take a route over something, keeping its sides.
func TestElbowPassesThroughVia(t *testing.T) {
	a, b := geo.Box{X: 0, Y: 0, W: 100, H: 100}, geo.Box{X: 600, Y: 0, W: 100, H: 100}
	up := elbowSides["top"]
	via := geo.V(350, -60)
	pts := routeElbow([]geo.Vec{sideOf(a, up), via, sideOf(b, up)}, up, up, 0.5, []geo.Box{a, b})
	want := []geo.Vec{geo.V(50, 0), geo.V(50, -60), geo.V(650, -60), geo.V(650, 0)}
	if fmt.Sprint(pts) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", pts, want)
	}
}
