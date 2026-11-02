package geo

import (
	"math"
	"testing"
)

func TestParseSVGPath(t *testing.T) {
	ends := func(d string) []Vec {
		t.Helper()
		p, err := ParseSVGPath(d)
		if err != nil {
			t.Fatalf("%q: %v", d, err)
		}
		var out []Vec
		for _, s := range p.Segs {
			switch s.Op {
			case OpMove, OpLine:
				out = append(out, s.P[0])
			case OpQuad:
				out = append(out, s.P[1])
			case OpCube:
				out = append(out, s.P[2])
			}
		}
		return out
	}
	cases := []struct {
		d    string
		want []Vec
	}{
		{"M1 2 3 4", []Vec{V(1, 2), V(3, 4)}},
		{"m1 2 3 4", []Vec{V(1, 2), V(4, 6)}},
		{"M0,0L10,0 10,10", []Vec{V(0, 0), V(10, 0), V(10, 10)}},
		{"M0 0H5V5h-5v-5", []Vec{V(0, 0), V(5, 0), V(5, 5), V(0, 5), V(0, 0)}},
		{"M0 0C1 1 2 2 3 3 4 4 5 5 6 6", []Vec{V(0, 0), V(3, 3), V(6, 6)}},
		{"M0 0c1 1 2 2 3 3", []Vec{V(0, 0), V(3, 3)}},
		{"M0 0C0 1 1 1 1 0S2-1 2 0", []Vec{V(0, 0), V(1, 0), V(2, 0)}},
		{"M0 0Q1 1 2 0T4 0", []Vec{V(0, 0), V(2, 0), V(4, 0)}},
		{"M-1.5-2.5.5.5", []Vec{V(-1.5, -2.5), V(.5, .5)}},
		{"M1e1 0L2E1 1", []Vec{V(10, 0), V(20, 1)}},
		// After Z, drawing goes on from where the subpath started.
		{"M1 1L2 2ZL3 3", []Vec{V(1, 1), V(2, 2), V(1, 1), V(3, 3)}},
	}
	for _, c := range cases {
		got := ends(c.d)
		if len(got) != len(c.want) {
			t.Errorf("%q: %v, want %v", c.d, got, c.want)
			continue
		}
		for i := range got {
			if got[i].Dist(c.want[i]) > 1e-9 {
				t.Errorf("%q: %v, want %v", c.d, got, c.want)
				break
			}
		}
	}
	for _, d := range []string{"", "  ", "L1 1", "M", "M1", "M1 2 3", "M0 0 X1 1", "hello", "M0 0ZZ1", "M0 0A1 1 0 2 0 1 1", "M0 0C1 1"} {
		if _, err := ParseSVGPath(d); err == nil {
			t.Errorf("%q parsed", d)
		}
	}
}

func TestSVGSmoothCurveReflects(t *testing.T) {
	p, err := ParseSVGPath("M0 0C0 1 1 1 1 0S2-1 2 0")
	if err != nil {
		t.Fatal(err)
	}
	// S mirrors the last control point through the current point.
	if c := p.Segs[2].P[0]; c.Dist(V(1, -1)) > 1e-9 {
		t.Errorf("first control %v", c)
	}
}

func TestSVGArcs(t *testing.T) {
	// A half circle of radius 50 from (0,0) to (100,0). Sweep 1 runs clockwise
	// on the screen, over the top, where y is smaller.
	p, err := ParseSVGPath("M0 0A50 50 0 0 1 100 0")
	if err != nil {
		t.Fatal(err)
	}
	pts, _ := p.Points(16)
	for _, q := range pts {
		if r := q.Dist(V(50, 0)); math.Abs(r-50) > 0.05 {
			t.Fatalf("point %v is %v from the center", q, r)
		}
	}
	if top := BoxOf(pts); math.Abs(top.Y+50) > 0.05 || top.MaxY() > 1e-6 {
		t.Errorf("arc bounds %+v", top)
	}
	if last := pts[len(pts)-1]; last.Dist(V(100, 0)) > 1e-9 {
		t.Errorf("ends at %v", last)
	}
	// Flags may run together with what follows them, as minifiers leave them.
	if _, err := ParseSVGPath("M0 0a50 50 0 01100 0"); err != nil {
		t.Errorf("tight flags: %v", err)
	}
	// A radius too small to reach grows until it does.
	p, _ = ParseSVGPath("M0 0A1 1 0 0 1 100 0")
	pts, _ = p.Points(16)
	if last := pts[len(pts)-1]; last.Dist(V(100, 0)) > 1e-9 {
		t.Errorf("small radius ends at %v", last)
	}
	// No radius: a straight line, as SVG says.
	p, _ = ParseSVGPath("M0 0A0 5 0 0 1 10 0")
	if p.Segs[1].Op != OpLine {
		t.Errorf("zero radius gave %v", p.Segs[1].Op)
	}
}

func TestSampled(t *testing.T) {
	p, err := ParseSVGPath("M0 0H100V100Z M200 0C200 100 300 100 300 0")
	if err != nil {
		t.Fatal(err)
	}
	subs, closed := p.Sampled(4)
	if len(subs) != 2 || !closed[0] || closed[1] {
		t.Fatalf("%d subpaths, closed %v", len(subs), closed)
	}
	if subs[0][0] != V(0, 0) {
		t.Errorf("starts at %v", subs[0][0])
	}
	if len(subs[1]) < 20 {
		t.Errorf("curve sampled to %d points", len(subs[1]))
	}
	if b := BoxOf(subs[1]); math.Abs(b.W-100) > 0.01 || math.Abs(b.H-75) > 1 {
		t.Errorf("curve bounds %+v", b)
	}
}

func TestTransform(t *testing.T) {
	p := (&Path{}).Move(V(1, 1)).Line(V(2, 2))
	q := p.Transform(Translate(10, 0).Mul(Scale(2, 2)))
	if q.Segs[1].P[0] != V(14, 4) {
		t.Errorf("got %v", q.Segs[1].P[0])
	}
	if p.Segs[1].P[0] != V(2, 2) {
		t.Error("changed the original")
	}
}
