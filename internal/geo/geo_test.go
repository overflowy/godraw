package geo

import (
	"math"
	"testing"
)

func TestMatInverse(t *testing.T) {
	m := Compose(10, -20, 0.7).Mul(Scale(2, 3)).Mul(Translate(5, 5))
	p := V(13, 17)
	if got := m.Invert().Apply(m.Apply(p)); got.Dist(p) > 1e-9 {
		t.Errorf("round trip %v", got)
	}
	if r := Compose(0, 0, 0.7).Rotation(); math.Abs(r-0.7) > 1e-9 {
		t.Errorf("rotation %v", r)
	}
}

func TestPointInPolygon(t *testing.T) {
	sq := []Vec{{0, 0}, {10, 0}, {10, 10}, {0, 10}}
	if !PointInPolygon(V(5, 5), sq) || PointInPolygon(V(15, 5), sq) {
		t.Error("square")
	}
	// Non-zero: a self-overlapping loop is filled where it overlaps.
	twice := append(append([]Vec{}, sq...), sq...)
	if !PointInPolygon(V(5, 5), twice) {
		t.Error("winding twice around a point is inside")
	}
}

func TestSegments(t *testing.T) {
	if p, ok := SegmentsCross(V(0, 0), V(10, 10), V(0, 10), V(10, 0)); !ok || p.Dist(V(5, 5)) > 1e-9 {
		t.Errorf("cross %v %v", p, ok)
	}
	if _, ok := SegmentsCross(V(0, 0), V(1, 0), V(0, 1), V(1, 1)); ok {
		t.Error("parallel segments crossed")
	}
	if d := DistToSegment(V(5, 3), V(0, 0), V(10, 0)); d != 3 {
		t.Errorf("distance %v", d)
	}
	if d := DistToSegment(V(-3, 4), V(0, 0), V(10, 0)); d != 5 {
		t.Errorf("distance past the end %v", d)
	}
}

func TestCircle(t *testing.T) {
	c, r, ok := Circle(V(1, 0), V(0, 1), V(-1, 0))
	if !ok || c.Len() > 1e-9 || math.Abs(r-1) > 1e-9 {
		t.Errorf("circle %v %v %v", c, r, ok)
	}
	if _, _, ok := Circle(V(0, 0), V(1, 1), V(2, 2)); ok {
		t.Error("a line has no circle")
	}
}

func TestSimplifyKeepsEnds(t *testing.T) {
	var pts []Vec
	for i := range 100 {
		pts = append(pts, V(float64(i), 0))
	}
	pts = append(pts, V(99, 50))
	s := Simplify(pts, 0.5)
	if len(s) != 3 || s[0] != pts[0] || s[2] != pts[100] {
		t.Errorf("simplified to %v", s)
	}
}

func TestFlatten(t *testing.T) {
	p := EllipsePath(Box{W: 100, H: 50})
	pts, closed := p.Points(8)
	if !closed || len(pts) != 33 {
		t.Errorf("%d points, closed %v", len(pts), closed)
	}
	b := BoxOf(pts)
	if math.Abs(b.W-100) > 0.1 || math.Abs(b.H-50) > 0.1 {
		t.Errorf("box %+v", b)
	}
}
