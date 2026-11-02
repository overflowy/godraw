// Package geo is the plane geometry of the canvas: vectors, boxes, affine
// matrices, paths, and the intersection tests hit-testing and arrows use.
package geo

import "math"

// Vec is a point or a direction.
type Vec struct{ X, Y float64 }

// V returns the vector (x, y).
func V(x, y float64) Vec { return Vec{x, y} }

// Add returns a+b.
func (a Vec) Add(b Vec) Vec { return Vec{a.X + b.X, a.Y + b.Y} }

// Sub returns a-b.
func (a Vec) Sub(b Vec) Vec { return Vec{a.X - b.X, a.Y - b.Y} }

// Mul returns a scaled by k.
func (a Vec) Mul(k float64) Vec { return Vec{a.X * k, a.Y * k} }

// Dot returns the dot product of a and b.
func (a Vec) Dot(b Vec) float64 { return a.X*b.X + a.Y*b.Y }

// Cross returns the z of the cross product of a and b.
func (a Vec) Cross(b Vec) float64 { return a.X*b.Y - a.Y*b.X }

// Len returns the length of a.
func (a Vec) Len() float64 { return math.Hypot(a.X, a.Y) }

// Dist returns the distance from a to b.
func (a Vec) Dist(b Vec) float64 { return math.Hypot(a.X-b.X, a.Y-b.Y) }

// Uni returns a scaled to length 1, or zero for a zero vector.
func (a Vec) Uni() Vec {
	l := a.Len()
	if l == 0 {
		return Vec{}
	}
	return Vec{a.X / l, a.Y / l}
}

// Per returns a turned a quarter clockwise on the screen, where y goes down.
func (a Vec) Per() Vec { return Vec{a.Y, -a.X} }

// Rot returns a turned by r radians.
func (a Vec) Rot(r float64) Vec {
	s, c := math.Sincos(r)
	return Vec{a.X*c - a.Y*s, a.X*s + a.Y*c}
}

// RotAround returns a turned by r radians around o.
func (a Vec) RotAround(o Vec, r float64) Vec { return a.Sub(o).Rot(r).Add(o) }

// Lerp returns the point t of the way from a to b.
func (a Vec) Lerp(b Vec, t float64) Vec {
	return Vec{a.X + (b.X-a.X)*t, a.Y + (b.Y-a.Y)*t}
}

// Angle returns the direction from a to b, in radians.
func (a Vec) Angle(b Vec) float64 { return math.Atan2(b.Y-a.Y, b.X-a.X) }

// Med returns the point halfway between a and b.
func Med(a, b Vec) Vec { return Vec{(a.X + b.X) / 2, (a.Y + b.Y) / 2} }

// Box is an axis-aligned rectangle.
type Box struct{ X, Y, W, H float64 }

// MaxX returns the box's right edge.
func (b Box) MaxX() float64 { return b.X + b.W }

// MaxY returns the box's bottom edge.
func (b Box) MaxY() float64 { return b.Y + b.H }

// Center returns the box's center.
func (b Box) Center() Vec { return Vec{b.X + b.W/2, b.Y + b.H/2} }

// Corners returns the box's corners, clockwise from the top left.
func (b Box) Corners() [4]Vec {
	return [4]Vec{{b.X, b.Y}, {b.X + b.W, b.Y}, {b.X + b.W, b.Y + b.H}, {b.X, b.Y + b.H}}
}

// Contains reports whether p is in the box.
func (b Box) Contains(p Vec) bool {
	return p.X >= b.X && p.X <= b.X+b.W && p.Y >= b.Y && p.Y <= b.Y+b.H
}

// ContainsBox reports whether o is wholly in the box.
func (b Box) ContainsBox(o Box) bool {
	return o.X >= b.X && o.Y >= b.Y && o.MaxX() <= b.MaxX() && o.MaxY() <= b.MaxY()
}

// Overlaps reports whether the boxes touch.
func (b Box) Overlaps(o Box) bool {
	return b.X <= o.MaxX() && o.X <= b.MaxX() && b.Y <= o.MaxY() && o.Y <= b.MaxY()
}

// Union returns the smallest box around both.
func (b Box) Union(o Box) Box {
	x0, y0 := math.Min(b.X, o.X), math.Min(b.Y, o.Y)
	x1, y1 := math.Max(b.MaxX(), o.MaxX()), math.Max(b.MaxY(), o.MaxY())
	return Box{x0, y0, x1 - x0, y1 - y0}
}

// Expand returns the box grown by d on every side.
func (b Box) Expand(d float64) Box { return Box{b.X - d, b.Y - d, b.W + 2*d, b.H + 2*d} }

// BoxOf returns the smallest box around points, or a zero box for none.
func BoxOf(pts []Vec) Box {
	if len(pts) == 0 {
		return Box{}
	}
	x0, y0, x1, y1 := pts[0].X, pts[0].Y, pts[0].X, pts[0].Y
	for _, p := range pts[1:] {
		x0, y0 = math.Min(x0, p.X), math.Min(y0, p.Y)
		x1, y1 = math.Max(x1, p.X), math.Max(y1, p.Y)
	}
	return Box{x0, y0, x1 - x0, y1 - y0}
}

// BoxFrom returns the box with corners a and b, in any order.
func BoxFrom(a, b Vec) Box {
	return Box{math.Min(a.X, b.X), math.Min(a.Y, b.Y), math.Abs(a.X - b.X), math.Abs(a.Y - b.Y)}
}

// Mat is the affine transform x' = A x + C y + E, y' = B x + D y + F.
type Mat struct{ A, B, C, D, E, F float64 }

// Identity is the transform that moves nothing.
var Identity = Mat{A: 1, D: 1}

// Translate returns a transform by (x, y).
func Translate(x, y float64) Mat { return Mat{1, 0, 0, 1, x, y} }

// Rotate returns a transform turning by r radians around the origin.
func Rotate(r float64) Mat {
	s, c := math.Sincos(r)
	return Mat{c, s, -s, c, 0, 0}
}

// Scale returns a transform scaling by (x, y).
func Scale(x, y float64) Mat { return Mat{x, 0, 0, y, 0, 0} }

// Compose returns the transform of a shape at (x, y) turned by r.
func Compose(x, y, r float64) Mat {
	s, c := math.Sincos(r)
	return Mat{c, s, -s, c, x, y}
}

// Mul returns the transform applying o first, then m.
func (m Mat) Mul(o Mat) Mat {
	return Mat{
		A: m.A*o.A + m.C*o.B,
		B: m.B*o.A + m.D*o.B,
		C: m.A*o.C + m.C*o.D,
		D: m.B*o.C + m.D*o.D,
		E: m.A*o.E + m.C*o.F + m.E,
		F: m.B*o.E + m.D*o.F + m.F,
	}
}

// Apply returns p transformed.
func (m Mat) Apply(p Vec) Vec {
	return Vec{m.A*p.X + m.C*p.Y + m.E, m.B*p.X + m.D*p.Y + m.F}
}

// ApplyDir returns direction p transformed, without the translation.
func (m Mat) ApplyDir(p Vec) Vec {
	return Vec{m.A*p.X + m.C*p.Y, m.B*p.X + m.D*p.Y}
}

// Invert returns the inverse of m, or the identity if m has none.
func (m Mat) Invert() Mat {
	d := m.A*m.D - m.B*m.C
	if d == 0 {
		return Identity
	}
	return Mat{
		A: m.D / d,
		B: -m.B / d,
		C: -m.C / d,
		D: m.A / d,
		E: (m.C*m.F - m.D*m.E) / d,
		F: (m.B*m.E - m.A*m.F) / d,
	}
}

// Rotation returns the angle m turns by, in radians.
func (m Mat) Rotation() float64 { return math.Atan2(m.B, m.A) }

// ApplyBox returns the corners of b transformed.
func (m Mat) ApplyBox(b Box) [4]Vec {
	c := b.Corners()
	for i := range c {
		c[i] = m.Apply(c[i])
	}
	return c
}

// BoundsOf returns the box around b transformed.
func (m Mat) BoundsOf(b Box) Box {
	c := m.ApplyBox(b)
	return BoxOf(c[:])
}

// DistToSegment returns the distance from p to the segment ab.
func DistToSegment(p, a, b Vec) float64 {
	return p.Dist(NearestOnSegment(p, a, b))
}

// NearestOnSegment returns the point of the segment ab closest to p.
func NearestOnSegment(p, a, b Vec) Vec {
	ab := b.Sub(a)
	l := ab.Dot(ab)
	if l == 0 {
		return a
	}
	t := p.Sub(a).Dot(ab) / l
	t = math.Max(0, math.Min(1, t))
	return a.Lerp(b, t)
}

// DistToPolyline returns the distance from p to the polyline, closing it
// if closed, or +Inf for no points.
func DistToPolyline(p Vec, pts []Vec, closed bool) float64 {
	if len(pts) == 0 {
		return math.Inf(1)
	}
	if len(pts) == 1 {
		return p.Dist(pts[0])
	}
	best := math.Inf(1)
	for i := 1; i < len(pts); i++ {
		best = math.Min(best, DistToSegment(p, pts[i-1], pts[i]))
	}
	if closed {
		best = math.Min(best, DistToSegment(p, pts[len(pts)-1], pts[0]))
	}
	return best
}

// PointInPolygon reports whether p is inside the polygon, by the non-zero
// winding rule.
func PointInPolygon(p Vec, poly []Vec) bool {
	wn := 0
	n := len(poly)
	for i := 0; i < n; i++ {
		a, b := poly[i], poly[(i+1)%n]
		isLeft := (b.X-a.X)*(p.Y-a.Y) - (p.X-a.X)*(b.Y-a.Y)
		if a.Y <= p.Y {
			if b.Y > p.Y && isLeft > 0 {
				wn++
			}
		} else if b.Y <= p.Y && isLeft < 0 {
			wn--
		}
	}
	return wn != 0
}

// SegmentsCross returns where the segments ab and cd cross, if they do.
func SegmentsCross(a, b, c, d Vec) (Vec, bool) {
	r, s := b.Sub(a), d.Sub(c)
	den := r.Cross(s)
	if den == 0 {
		return Vec{}, false
	}
	ca := c.Sub(a)
	t, u := ca.Cross(s)/den, ca.Cross(r)/den
	if t < 0 || t > 1 || u < 0 || u > 1 {
		return Vec{}, false
	}
	return a.Lerp(b, t), true
}

// SegmentPolyline returns where the segment ab crosses the polyline, in
// the order of the polyline.
func SegmentPolyline(a, b Vec, pts []Vec, closed bool) []Vec {
	var out []Vec
	n := len(pts)
	if n < 2 {
		return nil
	}
	last := n - 1
	if closed {
		last = n
	}
	for i := 0; i < last; i++ {
		if p, ok := SegmentsCross(a, b, pts[i], pts[(i+1)%n]); ok {
			out = append(out, p)
		}
	}
	return out
}

// SegmentHitsPolyline reports whether the segment ab crosses the polyline
// or comes within margin of it.
func SegmentHitsPolyline(a, b Vec, pts []Vec, closed bool, margin float64) bool {
	n := len(pts)
	if n == 0 {
		return false
	}
	if n == 1 {
		return DistToSegment(pts[0], a, b) <= margin
	}
	last := n - 1
	if closed {
		last = n
	}
	for i := 0; i < last; i++ {
		c, d := pts[i], pts[(i+1)%n]
		if _, ok := SegmentsCross(a, b, c, d); ok {
			return true
		}
		if margin > 0 {
			if DistToSegment(a, c, d) <= margin || DistToSegment(b, c, d) <= margin ||
				DistToSegment(c, a, b) <= margin || DistToSegment(d, a, b) <= margin {
				return true
			}
		}
	}
	return false
}

// BoxHitsPolyline reports whether the polyline crosses the box or lies
// inside it.
func BoxHitsPolyline(b Box, pts []Vec, closed bool) bool {
	for _, p := range pts {
		if b.Contains(p) {
			return true
		}
	}
	c := b.Corners()
	for i := 0; i < 4; i++ {
		if SegmentHitsPolyline(c[i], c[(i+1)%4], pts, closed, 0) {
			return true
		}
	}
	return false
}

// Circle returns the center and radius of the circle through three
// points, and false if they are in a line.
func Circle(a, b, c Vec) (Vec, float64, bool) {
	d := 2 * (a.X*(b.Y-c.Y) + b.X*(c.Y-a.Y) + c.X*(a.Y-b.Y))
	if math.Abs(d) < 1e-6 {
		return Vec{}, 0, false
	}
	a2, b2, c2 := a.Dot(a), b.Dot(b), c.Dot(c)
	o := Vec{
		(a2*(b.Y-c.Y) + b2*(c.Y-a.Y) + c2*(a.Y-b.Y)) / d,
		(a2*(c.X-b.X) + b2*(a.X-c.X) + c2*(b.X-a.X)) / d,
	}
	return o, o.Dist(a), true
}

// ShortAngle returns the signed shortest turn from angle a to angle b.
func ShortAngle(a, b float64) float64 {
	d := math.Mod(b-a, 2*math.Pi)
	if d > math.Pi {
		d -= 2 * math.Pi
	} else if d < -math.Pi {
		d += 2 * math.Pi
	}
	return d
}

// NormAngle returns r in the range [0, 2π).
func NormAngle(r float64) float64 {
	r = math.Mod(r, 2*math.Pi)
	if r < 0 {
		r += 2 * math.Pi
	}
	return r
}

// Simplify returns pts without those within tol of the line through their
// neighbors (Ramer–Douglas–Peucker).
func Simplify(pts []Vec, tol float64) []Vec {
	if len(pts) < 3 {
		return pts
	}
	keep := make([]bool, len(pts))
	keep[0], keep[len(pts)-1] = true, true
	var walk func(i, j int)
	walk = func(i, j int) {
		if j <= i+1 {
			return
		}
		best, at := 0.0, -1
		for k := i + 1; k < j; k++ {
			if d := DistToSegment(pts[k], pts[i], pts[j]); d > best {
				best, at = d, k
			}
		}
		if best > tol {
			keep[at] = true
			walk(i, at)
			walk(at, j)
		}
	}
	walk(0, len(pts)-1)
	out := make([]Vec, 0, len(pts)/2)
	for i, p := range pts {
		if keep[i] {
			out = append(out, p)
		}
	}
	return out
}

// PolyLength returns the length of a polyline.
func PolyLength(pts []Vec) float64 {
	var l float64
	for i := 1; i < len(pts); i++ {
		l += pts[i-1].Dist(pts[i])
	}
	return l
}

// Hash returns a stable number from s, to seed the wobble of hand-drawn
// strokes: a shape looks the same each time it draws.
func Hash(s string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

// Rng is a small deterministic generator (xorshift32).
type Rng uint32

// NewRng returns a generator seeded from s.
func NewRng(s string) *Rng {
	r := Rng(Hash(s) | 1)
	return &r
}

// Next returns a number in [-1, 1).
func (r *Rng) Next() float64 {
	x := uint32(*r)
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	*r = Rng(x)
	return float64(x)/float64(1<<31) - 1
}
