package geo

import (
	"fmt"
	"math"
	"strconv"
)

// ParseSVGPath reads the d attribute of an SVG path: M L H V C S Q T A Z, in
// either case, with SVG's implicit repeats.
func ParseSVGPath(d string) (*Path, error) {
	r := &svgReader{s: d}
	p := &Path{}
	var cur, start, ctrl Vec
	var last byte
	cmd := byte(0)
	pendingMove := false // drawing after a Z starts a subpath where it closed
	line := func(v Vec) {
		if pendingMove {
			p.Move(cur)
			pendingMove = false
		}
		p.Line(v)
		cur = v
	}
	for {
		r.space()
		if r.done() {
			break
		}
		if c := r.s[r.i]; isCommand(c) {
			cmd = c
			r.i++
		} else if cmd == 0 {
			return nil, fmt.Errorf("path: expected a command at %d", r.i)
		} else if cmd == 'Z' || cmd == 'z' {
			return nil, fmt.Errorf("path: expected a command at %d", r.i)
		}
		if len(p.Segs) == 0 && cmd != 'M' && cmd != 'm' {
			return nil, fmt.Errorf("path: must start with M")
		}
		rel := cmd >= 'a'
		abs := func(v Vec) Vec {
			if rel {
				return cur.Add(v)
			}
			return v
		}
		switch cmd | 0x20 {
		case 'z':
			if !pendingMove {
				p.Close()
			}
			cur = start
			pendingMove = true
		case 'm':
			v, err := r.vec()
			if err != nil {
				return nil, err
			}
			cur = abs(v)
			start = cur
			p.Move(cur)
			pendingMove = false
			// Further pairs are lines.
			if rel {
				cmd = 'l'
			} else {
				cmd = 'L'
			}
		case 'l':
			v, err := r.vec()
			if err != nil {
				return nil, err
			}
			line(abs(v))
		case 'h':
			x, err := r.num()
			if err != nil {
				return nil, err
			}
			if rel {
				x += cur.X
			}
			line(V(x, cur.Y))
		case 'v':
			y, err := r.num()
			if err != nil {
				return nil, err
			}
			if rel {
				y += cur.Y
			}
			line(V(cur.X, y))
		case 'c', 's':
			var a Vec
			if cmd|0x20 == 'c' {
				v, err := r.vec()
				if err != nil {
					return nil, err
				}
				a = abs(v)
			} else {
				a = cur
				if last|0x20 == 'c' || last|0x20 == 's' {
					a = cur.Add(cur.Sub(ctrl))
				}
			}
			bv, err := r.vec()
			if err != nil {
				return nil, err
			}
			ev, err := r.vec()
			if err != nil {
				return nil, err
			}
			b, end := abs(bv), abs(ev)
			if pendingMove {
				p.Move(cur)
				pendingMove = false
			}
			p.Cube(a, b, end)
			ctrl, cur = b, end
		case 'q', 't':
			var c Vec
			if cmd|0x20 == 'q' {
				v, err := r.vec()
				if err != nil {
					return nil, err
				}
				c = abs(v)
			} else {
				c = cur
				if last|0x20 == 'q' || last|0x20 == 't' {
					c = cur.Add(cur.Sub(ctrl))
				}
			}
			ev, err := r.vec()
			if err != nil {
				return nil, err
			}
			end := abs(ev)
			if pendingMove {
				p.Move(cur)
				pendingMove = false
			}
			p.Quad(c, end)
			ctrl, cur = c, end
		case 'a':
			rx, err := r.num()
			if err != nil {
				return nil, err
			}
			ry, err := r.num()
			if err != nil {
				return nil, err
			}
			turn, err := r.num()
			if err != nil {
				return nil, err
			}
			large, err := r.flag()
			if err != nil {
				return nil, err
			}
			sweep, err := r.flag()
			if err != nil {
				return nil, err
			}
			ev, err := r.vec()
			if err != nil {
				return nil, err
			}
			end := abs(ev)
			if pendingMove {
				p.Move(cur)
				pendingMove = false
			}
			arcTo(p, cur, end, rx, ry, turn*math.Pi/180, large, sweep)
			cur = end
		default:
			return nil, fmt.Errorf("path: unknown command %q at %d", cmd, r.i-1)
		}
		last = cmd
	}
	if len(p.Segs) == 0 {
		return nil, fmt.Errorf("path: empty")
	}
	return p, nil
}

func isCommand(c byte) bool {
	switch c | 0x20 {
	case 'm', 'l', 'h', 'v', 'c', 's', 'q', 't', 'a', 'z':
		return true
	}
	return false
}

// arcTo adds an SVG arc as cubic curves, by the conversion in the SVG spec's
// implementation notes.
func arcTo(p *Path, from, to Vec, rx, ry, turn float64, large, sweep bool) {
	rx, ry = math.Abs(rx), math.Abs(ry)
	if rx == 0 || ry == 0 || from == to {
		p.Line(to)
		return
	}
	sin, cos := math.Sincos(turn)
	dx, dy := (from.X-to.X)/2, (from.Y-to.Y)/2
	x1, y1 := cos*dx+sin*dy, -sin*dx+cos*dy
	// Radii too small to reach grow until they do.
	if k := x1*x1/(rx*rx) + y1*y1/(ry*ry); k > 1 {
		s := math.Sqrt(k)
		rx, ry = rx*s, ry*s
	}
	num := rx*rx*ry*ry - rx*rx*y1*y1 - ry*ry*x1*x1
	den := rx*rx*y1*y1 + ry*ry*x1*x1
	co := math.Sqrt(math.Max(num/den, 0))
	if large == sweep {
		co = -co
	}
	cx1, cy1 := co*rx*y1/ry, -co*ry*x1/rx
	cx := cos*cx1 - sin*cy1 + (from.X+to.X)/2
	cy := sin*cx1 + cos*cy1 + (from.Y+to.Y)/2
	a0 := math.Atan2((y1-cy1)/ry, (x1-cx1)/rx)
	a1 := math.Atan2((-y1-cy1)/ry, (-x1-cx1)/rx)
	delta := a1 - a0
	if sweep && delta < 0 {
		delta += 2 * math.Pi
	} else if !sweep && delta > 0 {
		delta -= 2 * math.Pi
	}
	n := int(math.Ceil(math.Abs(delta) / (math.Pi / 2)))
	step := delta / float64(n)
	k := 4.0 / 3 * math.Tan(step/4)
	at := func(a float64) (pos, tan Vec) {
		s, c := math.Sincos(a)
		x, y := rx*c, ry*s
		tx, ty := -rx*s, ry*c
		return V(cos*x-sin*y+cx, sin*x+cos*y+cy), V(cos*tx-sin*ty, sin*tx+cos*ty)
	}
	for i := range n {
		a := a0 + float64(i)*step
		p0, t0 := at(a)
		p1, t1 := at(a + step)
		if i == n-1 {
			p1 = to // exactly where it was asked to end
		}
		p.Cube(p0.Add(t0.Mul(k)), p1.Sub(t1.Mul(k)), p1)
	}
}

type svgReader struct {
	s string
	i int
}

func (r *svgReader) done() bool { return r.i >= len(r.s) }

func (r *svgReader) space() {
	for !r.done() {
		switch r.s[r.i] {
		case ' ', '\t', '\n', '\r', '\f', ',':
			r.i++
		default:
			return
		}
	}
}

func (r *svgReader) num() (float64, error) {
	r.space()
	start := r.i
	if !r.done() && (r.s[r.i] == '+' || r.s[r.i] == '-') {
		r.i++
	}
	digits := 0
	for !r.done() && isDigit(r.s[r.i]) {
		r.i++
		digits++
	}
	if !r.done() && r.s[r.i] == '.' {
		r.i++
		for !r.done() && isDigit(r.s[r.i]) {
			r.i++
			digits++
		}
	}
	if digits == 0 {
		r.i = start
		return 0, fmt.Errorf("path: expected a number at %d", start)
	}
	if !r.done() && (r.s[r.i] == 'e' || r.s[r.i] == 'E') {
		j := r.i + 1
		if j < len(r.s) && (r.s[j] == '+' || r.s[j] == '-') {
			j++
		}
		if j < len(r.s) && isDigit(r.s[j]) {
			for j < len(r.s) && isDigit(r.s[j]) {
				j++
			}
			r.i = j
		}
	}
	v, err := strconv.ParseFloat(r.s[start:r.i], 64)
	if err != nil || math.IsInf(v, 0) {
		return 0, fmt.Errorf("path: bad number at %d", start)
	}
	return v, nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func (r *svgReader) vec() (Vec, error) {
	x, err := r.num()
	if err != nil {
		return Vec{}, err
	}
	y, err := r.num()
	if err != nil {
		return Vec{}, err
	}
	return V(x, y), nil
}

// flag reads an arc flag, a single digit that needs no space after it.
func (r *svgReader) flag() (bool, error) {
	r.space()
	if r.done() || (r.s[r.i] != '0' && r.s[r.i] != '1') {
		return false, fmt.Errorf("path: expected an arc flag at %d", r.i)
	}
	v := r.s[r.i] == '1'
	r.i++
	return v, nil
}

// Transform returns p with its points under m.
func (p *Path) Transform(m Mat) *Path {
	out := &Path{Segs: make([]Seg, len(p.Segs))}
	for i, s := range p.Segs {
		for k, v := range s.P {
			s.P[k] = m.Apply(v)
		}
		out.Segs[i] = s
	}
	return out
}

// Sampled returns the subpaths of p as polylines with points about step apart,
// for a freehand stroke to follow. A stroke rounds what it follows and strays
// where its points are far apart, so straight pieces get points along them too,
// and close to their ends, which keeps their corners sharp.
func (p *Path) Sampled(step float64) (subs [][]Vec, closed []bool) {
	const corner = 1.5
	var cur []Vec
	var start Vec
	flush := func(c bool) {
		if len(cur) > 0 {
			subs = append(subs, cur)
			closed = append(closed, c)
		}
		cur = nil
	}
	straight := func(from, to Vec) {
		d := from.Dist(to)
		if d > 3*corner {
			u := to.Sub(from).Uni()
			cur = append(cur, from.Add(u.Mul(corner)))
			inner := d - 2*corner
			n := min(int(inner/(2*step)), 256)
			for i := 1; i < n; i++ {
				cur = append(cur, from.Add(u.Mul(corner+inner*float64(i)/float64(n))))
			}
			cur = append(cur, to.Sub(u.Mul(corner)))
		}
		cur = append(cur, to)
	}
	for _, s := range p.Segs {
		switch s.Op {
		case OpMove:
			flush(false)
			start = s.P[0]
			cur = append(cur, start)
		case OpLine:
			straight(cur[len(cur)-1], s.P[0])
		case OpQuad, OpCube:
			from := cur[len(cur)-1]
			size := from.Dist(s.P[0]) + s.P[0].Dist(s.P[1])
			if s.Op == OpCube {
				size += s.P[1].Dist(s.P[2])
			}
			n := max(min(int(size/step), 256), 2)
			one := (&Path{}).Move(from)
			one.Segs = append(one.Segs, s)
			flat, _ := one.Points(n)
			cur = append(cur, flat[1:]...)
		case OpClose:
			// The way back to the start, unless it is there already.
			if len(cur) > 0 && cur[len(cur)-1].Dist(start) > 1e-6 {
				straight(cur[len(cur)-1], start)
			}
			if n := len(cur); n > 1 {
				if u := cur[0].Sub(cur[n-1]); u.X == 0 && u.Y == 0 {
					cur = cur[:n-1]
				}
			}
			flush(true)
		}
	}
	flush(false)
	return subs, closed
}
