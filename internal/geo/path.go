package geo

import (
	"fmt"
	"math"
	"strings"
)

// Op is a path command.
type Op uint8

// Path commands.
const (
	OpMove Op = iota
	OpLine
	OpQuad
	OpCube
	OpClose
)

// Seg is one command of a Path, with its points in P: one for Move and
// Line, control and end for Quad, two controls and end for Cube.
type Seg struct {
	Op Op
	P  [3]Vec
}

// Path is a vector shape, independent of what draws it, so that the window
// and SVG export draw the same thing.
type Path struct{ Segs []Seg }

// Move starts a subpath at p.
func (p *Path) Move(v Vec) *Path {
	p.Segs = append(p.Segs, Seg{Op: OpMove, P: [3]Vec{v}})
	return p
}

// Line draws a line to v.
func (p *Path) Line(v Vec) *Path {
	p.Segs = append(p.Segs, Seg{Op: OpLine, P: [3]Vec{v}})
	return p
}

// Quad draws a quadratic curve to v through control c.
func (p *Path) Quad(c, v Vec) *Path {
	p.Segs = append(p.Segs, Seg{Op: OpQuad, P: [3]Vec{c, v}})
	return p
}

// Cube draws a cubic curve to v through controls a and b.
func (p *Path) Cube(a, b, v Vec) *Path {
	p.Segs = append(p.Segs, Seg{Op: OpCube, P: [3]Vec{a, b, v}})
	return p
}

// Close closes the subpath.
func (p *Path) Close() *Path {
	p.Segs = append(p.Segs, Seg{Op: OpClose})
	return p
}

// Poly adds the polyline pts, closing it if closed.
func (p *Path) Poly(pts []Vec, closed bool) *Path {
	for i, v := range pts {
		if i == 0 {
			p.Move(v)
		} else {
			p.Line(v)
		}
	}
	if closed && len(pts) > 0 {
		p.Close()
	}
	return p
}

// Smooth adds a curve through the midpoints of pts, the smooth line a
// freehand stroke follows.
func (p *Path) Smooth(pts []Vec, closed bool) *Path {
	n := len(pts)
	switch {
	case n == 0:
		return p
	case n < 3:
		return p.Poly(pts, closed)
	}
	if closed {
		p.Move(Med(pts[n-1], pts[0]))
		for i := 0; i < n; i++ {
			p.Quad(pts[i], Med(pts[i], pts[(i+1)%n]))
		}
		return p.Close()
	}
	p.Move(pts[0])
	for i := 1; i < n-1; i++ {
		p.Quad(pts[i], Med(pts[i], pts[i+1]))
	}
	return p.Line(pts[n-1])
}

// EllipsePath returns a closed ellipse filling box b.
func EllipsePath(b Box) *Path {
	const k = 0.5522847498
	c := b.Center()
	rx, ry := b.W/2, b.H/2
	p := &Path{}
	p.Move(V(c.X+rx, c.Y))
	p.Cube(V(c.X+rx, c.Y+k*ry), V(c.X+k*rx, c.Y+ry), V(c.X, c.Y+ry))
	p.Cube(V(c.X-k*rx, c.Y+ry), V(c.X-rx, c.Y+k*ry), V(c.X-rx, c.Y))
	p.Cube(V(c.X-rx, c.Y-k*ry), V(c.X-k*rx, c.Y-ry), V(c.X, c.Y-ry))
	p.Cube(V(c.X+k*rx, c.Y-ry), V(c.X+rx, c.Y-k*ry), V(c.X+rx, c.Y))
	return p.Close()
}

// RoundedPoly returns the closed polygon pts with its corners rounded by r.
func RoundedPoly(pts []Vec, r float64) *Path {
	n := len(pts)
	p := &Path{}
	if n < 3 {
		return p.Poly(pts, true)
	}
	for i := 0; i < n; i++ {
		prev, cur, next := pts[(i+n-1)%n], pts[i], pts[(i+1)%n]
		d := math.Min(r, math.Min(cur.Dist(prev), cur.Dist(next))/2)
		a := cur.Add(prev.Sub(cur).Uni().Mul(d))
		b := cur.Add(next.Sub(cur).Uni().Mul(d))
		if i == 0 {
			p.Move(a)
		} else {
			p.Line(a)
		}
		p.Quad(cur, b)
	}
	return p.Close()
}

// Flatten returns the subpaths of p as polylines, with whether each is
// closed, each curve as n segments.
func (p *Path) Flatten(n int) (subs [][]Vec, closed []bool) {
	var cur []Vec
	flush := func(c bool) {
		if len(cur) > 0 {
			subs = append(subs, cur)
			closed = append(closed, c)
		}
		cur = nil
	}
	for _, s := range p.Segs {
		switch s.Op {
		case OpMove:
			flush(false)
			cur = append(cur, s.P[0])
		case OpLine:
			cur = append(cur, s.P[0])
		case OpQuad:
			from := cur[len(cur)-1]
			for i := 1; i <= n; i++ {
				t := float64(i) / float64(n)
				u := 1 - t
				cur = append(cur, from.Mul(u*u).Add(s.P[0].Mul(2*u*t)).Add(s.P[1].Mul(t*t)))
			}
		case OpCube:
			from := cur[len(cur)-1]
			for i := 1; i <= n; i++ {
				t := float64(i) / float64(n)
				u := 1 - t
				cur = append(cur, from.Mul(u*u*u).Add(s.P[0].Mul(3*u*u*t)).Add(s.P[1].Mul(3*u*t*t)).Add(s.P[2].Mul(t*t*t)))
			}
		case OpClose:
			flush(true)
		}
	}
	flush(false)
	return subs, closed
}

// Points returns p's first subpath as a polyline, with whether it closes.
func (p *Path) Points(n int) ([]Vec, bool) {
	subs, closed := p.Flatten(n)
	if len(subs) == 0 {
		return nil, false
	}
	return subs[0], closed[0]
}

// SVG returns p as the d attribute of an SVG path, under m.
func (p *Path) SVG(m Mat) string {
	var b strings.Builder
	pt := func(v Vec) {
		v = m.Apply(v)
		fmt.Fprintf(&b, "%.2f %.2f ", v.X, v.Y)
	}
	for _, s := range p.Segs {
		switch s.Op {
		case OpMove:
			b.WriteString("M")
			pt(s.P[0])
		case OpLine:
			b.WriteString("L")
			pt(s.P[0])
		case OpQuad:
			b.WriteString("Q")
			pt(s.P[0])
			pt(s.P[1])
		case OpCube:
			b.WriteString("C")
			pt(s.P[0])
			pt(s.P[1])
			pt(s.P[2])
		case OpClose:
			b.WriteString("Z")
		}
	}
	return b.String()
}
