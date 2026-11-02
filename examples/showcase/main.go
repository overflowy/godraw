// Showcase writes examples/showcase.tldr, a drawing that shows what godraw
// does. Everything in it is ordinary shapes: select, drag and edit any of it.
//
//	go run ./examples/showcase
package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"math/cmplx"
	"math/rand/v2"
	"os"

	"godraw/internal/doc"
	"godraw/internal/geo"
)

// builder adds shapes to a drawing, keeping their paint order and giving them
// names, so that the file comes out the same each time.
type builder struct {
	d    *doc.Drawing
	page doc.ID
	last map[doc.ID]string
	n    int
	r    *rand.Rand
}

func (b *builder) id(prefix string) doc.ID {
	b.n++
	return doc.ID(fmt.Sprintf("shape:%s-%d", prefix, b.n))
}

func (b *builder) add(s *doc.Shape, parent doc.ID) *doc.Shape {
	if parent == "" {
		parent = b.page
	}
	s.Parent = parent
	s.Index = doc.IndexBetween(b.last[parent], "")
	b.last[parent] = s.Index
	b.d.Shapes[s.ID] = s
	return s
}

func (b *builder) newPage(id doc.ID, name string) {
	index := doc.FirstIndex
	if n := len(b.d.Pages); n > 0 {
		index = doc.IndexBetween(b.d.Pages[n-1].Index, "")
	}
	b.d.Pages = append(b.d.Pages, &doc.Page{ID: id, Name: name, Index: index})
	b.page = id
}

// catmull samples a smooth curve through the points, about step apart.
func catmull(pts []geo.Vec, closed bool, step float64) []geo.Vec {
	n := len(pts)
	at := func(i int) geo.Vec {
		if closed {
			return pts[((i%n)+n)%n]
		}
		return pts[max(0, min(n-1, i))]
	}
	segs := n - 1
	if closed {
		segs = n
	}
	var out []geo.Vec
	for i := 0; i < segs; i++ {
		p0, p1, p2, p3 := at(i-1), at(i), at(i+1), at(i+2)
		k := max(int(p1.Dist(p2)/step), 1)
		for j := 0; j < k; j++ {
			t := float64(j) / float64(k)
			t2, t3 := t*t, t*t*t
			out = append(out, geo.V(
				0.5*(2*p1.X+(p2.X-p0.X)*t+(2*p0.X-5*p1.X+4*p2.X-p3.X)*t2+(3*p1.X-p0.X-3*p2.X+p3.X)*t3),
				0.5*(2*p1.Y+(p2.Y-p0.Y)*t+(2*p0.Y-5*p1.Y+4*p2.Y-p3.Y)*t2+(3*p1.Y-p0.Y-3*p2.Y+p3.Y)*t3),
			))
		}
	}
	if !closed {
		out = append(out, pts[n-1])
	}
	return out
}

func oval(c geo.Vec, rx, ry float64) []geo.Vec {
	n := max(int(2*math.Pi*math.Max(rx, ry)/5), 14)
	out := make([]geo.Vec, n)
	for i := range out {
		s, co := math.Sincos(2 * math.Pi * float64(i) / float64(n))
		out[i] = geo.V(c.X+rx*co, c.Y+ry*s)
	}
	return out
}

// pen is how a stroke is drawn.
type pen struct {
	color, size, fill, dash string
	closed                  bool
	opacity                 float64
}

// stroke adds a freehand shape through points of the page or of its parent.
func (b *builder) stroke(name string, pts []geo.Vec, p pen, parent doc.ID) *doc.Shape {
	typ := doc.TypeDraw
	s := doc.NewShape(b.id(name), typ)
	o := pts[0]
	s.X, s.Y = o.X, o.Y
	points := make([]doc.PointZ, len(pts))
	for i, q := range pts {
		points[i] = doc.PointZ{X: q.X - o.X, Y: q.Y - o.Y, Z: 0.5}
	}
	if p.closed {
		points = append(points, points[0])
	}
	s.Segments = []doc.Segment{{Type: "free", Points: points, Dim: 2}}
	s.IsComplete, s.IsClosed = true, p.closed
	s.Color, s.Size = p.color, or(p.size, "m")
	s.Fill, s.Dash = or(p.fill, "none"), or(p.dash, "draw")
	if p.opacity > 0 {
		s.Opacity = p.opacity
	}
	return b.add(s, parent)
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// blob adds a filled shape with a dark outline: a shape of its color, and
// the outline over it.
func (b *builder) blob(name string, pts []geo.Vec, fill string, parent doc.ID) {
	b.stroke(name, pts, pen{color: fill, fill: "fill", closed: true}, parent)
	b.stroke(name+"-line", pts, pen{color: "black", closed: true}, parent)
}

func (b *builder) group(name string) doc.ID {
	return b.add(doc.NewShape(b.id(name), doc.TypeGroup), "").ID
}

type box struct {
	x, y, w, h                    float64
	kind, text, color, fill, dash string
	size, font, labelColor        string
	rotation                      float64
}

func (b *builder) geoShape(name string, g box, parent doc.ID) *doc.Shape {
	s := doc.NewShape(b.id(name), doc.TypeGeo)
	s.X, s.Y, s.W, s.H, s.Rotation = g.x, g.y, g.w, g.h, g.rotation
	s.Geo, s.Text = or(g.kind, "rectangle"), g.text
	s.Color, s.Fill, s.Dash = or(g.color, "black"), or(g.fill, "none"), or(g.dash, "draw")
	s.Size, s.Font, s.LabelColor = or(g.size, "m"), or(g.font, "draw"), or(g.labelColor, "black")
	return b.add(s, parent)
}

// around adds a shape turned by r around its center.
func around(c geo.Vec, w, h, r float64) (x, y float64) {
	o := c.Sub(geo.V(w/2, h/2).Rot(r))
	return o.X, o.Y
}

type words struct {
	x, y              float64
	text              string
	size, font, color string
	scale, rotation   float64
	align             string
}

func (b *builder) text(t words, parent doc.ID) *doc.Shape {
	s := doc.NewShape(b.id("text"), doc.TypeText)
	s.X, s.Y, s.Rotation = t.x, t.y, t.rotation
	s.Text = t.text
	s.Size, s.Font, s.Color = or(t.size, "m"), or(t.font, "draw"), or(t.color, "black")
	s.TextAlign = or(t.align, "start")
	if t.scale > 0 {
		s.Scale = t.scale
	}
	return b.add(s, parent)
}

func (b *builder) note(x, y, rotation float64, col, text string) {
	s := doc.NewShape(b.id("note"), doc.TypeNote)
	s.X, s.Y, s.Rotation = x, y, rotation
	s.Color, s.Text, s.Size = col, text, "m"
	b.add(s, "")
}

type link struct {
	from, to          *doc.Shape // nil leaves that end free
	start, end        geo.Vec    // free ends, in the parent's space
	bend              float64
	kind, color, dash string
	label             string
	headStart, head   string
	size              string
}

func (b *builder) arrow(l link, parent doc.ID) *doc.Shape {
	s := doc.NewShape(b.id("arrow"), doc.TypeArrow)
	s.Color, s.Dash, s.Size = or(l.color, "black"), or(l.dash, "draw"), or(l.size, "m")
	s.Kind, s.Bend, s.Text = or(l.kind, "arc"), l.bend, l.label
	s.HeadStart, s.HeadEnd = or(l.headStart, "none"), or(l.head, "arrow")
	s.Start, s.End = l.start, l.end
	centerOf := func(t *doc.Shape) geo.Vec { return geo.V(t.X+t.W/2, t.Y+t.H/2) }
	if l.from != nil {
		s.Start = centerOf(l.from)
	}
	if l.to != nil {
		s.End = centerOf(l.to)
	}
	b.add(s, parent)
	tie := func(term string, t *doc.Shape) {
		if t == nil {
			return
		}
		id := doc.ID(fmt.Sprintf("binding:%s-%s", s.ID[len("shape:"):], term))
		b.d.Bindings[id] = &doc.Binding{
			ID: id, Type: "arrow", From: s.ID, To: t.ID, Terminal: term,
			Anchor: geo.V(0.5, 0.5), Snap: "none",
		}
	}
	tie("start", l.from)
	tie("end", l.to)
	return s
}

// --- the gopher ---

// gopher draws a gopher centered on c, holding a pencil out to its left with
// the point at tip, looking at it. After the Go gopher by Renée French (CC BY).
func (b *builder) gopher(c geo.Vec, parent doc.ID) (tip geo.Vec) {
	at := func(x, y float64) geo.Vec { return c.Add(geo.V(x, y)) }
	path := func(pts ...[2]float64) []geo.Vec {
		out := make([]geo.Vec, len(pts))
		for i, p := range pts {
			out[i] = at(p[0], p[1])
		}
		return catmull(out, true, 5)
	}
	// Ears, behind the head.
	b.blob("ear", oval(at(-98, -168), 30, 30), "light-blue", parent)
	b.blob("ear", oval(at(98, -168), 30, 30), "light-blue", parent)
	// Feet, under the body.
	b.blob("foot", oval(at(-66, 206), 48, 20), "yellow", parent)
	b.blob("foot", oval(at(66, 206), 48, 20), "yellow", parent)
	// The waving arm, behind the body.
	b.blob("arm", path([2]float64{110, 30}, [2]float64{150, -10}, [2]float64{172, -66}, [2]float64{198, -58}, [2]float64{190, 0}, [2]float64{150, 60}, [2]float64{118, 80}), "light-blue", parent)
	b.blob("hand", oval(at(186, -74), 21, 21), "yellow", parent)
	// The body.
	b.blob("body", path(
		[2]float64{0, -192}, [2]float64{72, -180}, [2]float64{114, -122}, [2]float64{124, -30},
		[2]float64{130, 80}, [2]float64{120, 160}, [2]float64{72, 198}, [2]float64{0, 208},
		[2]float64{-72, 198}, [2]float64{-120, 160}, [2]float64{-130, 80}, [2]float64{-124, -30},
		[2]float64{-114, -122}, [2]float64{-72, -180},
	), "light-blue", parent)
	// Eyes, looking down at the pencil.
	for _, x := range []float64{-50, 50} {
		// The "semi" fill is the theme's paper, the opposite of its "black"
		// ink: eyes and pupils stay apart in light and dark alike.
		eye := oval(at(x, -102), 44, 46)
		b.stroke("eye", eye, pen{color: "black", fill: "semi", closed: true}, parent)
		pupil := at(x-17, -88)
		b.stroke("pupil", oval(pupil, 16, 16), pen{color: "black", fill: "fill", closed: true}, parent)
		b.stroke("glint", oval(pupil.Add(geo.V(-5, -6)), 4.5, 4.5), pen{color: "black", fill: "semi", dash: "dotted", size: "s", closed: true}, parent)
	}
	// Muzzle, tooth and nose.
	b.blob("muzzle", oval(at(0, -36), 42, 27), "yellow", parent)
	b.blob("tooth", path([2]float64{-13, -22}, [2]float64{13, -22}, [2]float64{14, 12}, [2]float64{0, 15}, [2]float64{-14, 12}), "white", parent)
	b.stroke("nose", oval(at(0, -54), 19, 12), pen{color: "black", fill: "fill", closed: true}, parent)
	b.stroke("tooth-gap", catmull([]geo.Vec{at(0, -20), at(0, 13)}, false, 5), pen{color: "grey", size: "s"}, parent)

	// The pencil, from the hand down to its point.
	hand := at(-196, -6)
	tip = at(-292, 72)
	u := tip.Sub(hand).Uni()
	n := u.Per().Mul(10)
	tail := hand.Sub(u.Mul(52))
	shoulder := tip.Sub(u.Mul(30))
	b.blob("eraser", catmull([]geo.Vec{tail.Add(n), tail.Add(u.Mul(16)).Add(n), tail.Add(u.Mul(16)).Sub(n), tail.Sub(n)}, true, 6), "light-red", parent)
	b.blob("pencil", []geo.Vec{tail.Add(u.Mul(16)).Add(n), shoulder.Add(n), shoulder.Sub(n), tail.Add(u.Mul(16)).Sub(n)}, "orange", parent)
	b.blob("wood", []geo.Vec{shoulder.Add(n), tip, shoulder.Sub(n)}, "white", parent)
	b.stroke("lead", []geo.Vec{tip.Sub(u.Mul(11)).Add(n.Mul(0.36)), tip, tip.Sub(u.Mul(11)).Sub(n.Mul(0.36))}, pen{color: "grey", fill: "fill", size: "s", closed: true}, parent)

	// The arm holding it, in front of the body.
	b.blob("arm", path([2]float64{-110, 20}, [2]float64{-150, -6}, [2]float64{-190, -22}, [2]float64{-200, 8}, [2]float64{-160, 40}, [2]float64{-118, 72}), "light-blue", parent)
	b.blob("hand", oval(hand, 23, 23), "yellow", parent)
	return tip
}

// --- page 1: the poster ---

func (b *builder) poster() {
	b.newPage("page:poster", "godraw")

	// Title. Its underline is what the gopher is drawing.
	b.text(words{x: 40, y: 0, text: "godraw", size: "xl", scale: 3.1, color: "black"}, "")
	const titleEnd = 548.0
	gopherAt := geo.V(titleEnd+292, 196-72)
	gg := b.group("gopher")
	tip := b.gopher(gopherAt, gg)
	var line []geo.Vec
	const start = 44.0
	for x := start; x < tip.X; x += 7 {
		f := (x - start) / (tip.X - start)
		line = append(line, geo.V(x, tip.Y+7*math.Sin(f*17)*(1-f*0.6)+10*(1-f)))
	}
	line = append(line, tip)
	b.stroke("underline", line, pen{color: "red", size: "l"}, "")

	// What it is.
	mark := doc.NewShape(b.id("marker"), doc.TypeHighlight)
	mark.X, mark.Y, mark.Color, mark.Size, mark.IsComplete = 216, 318, "yellow", "l", true
	mark.Segments = []doc.Segment{{Type: "free", Dim: 2, Points: []doc.PointZ{{Z: .5}, {X: 80, Y: -2, Z: .5}, {X: 170, Y: 1, Z: .5}, {X: 248, Y: -1, Z: .5}}}}
	b.add(mark, "")
	b.text(words{x: 46, y: 228, text: "tldraw, rebuilt in Go.", size: "l", scale: 1.25}, "")
	b.text(words{x: 46, y: 284, text: "Native. No browser.", size: "l", scale: 1.25}, "")

	// A bloom of ellipses in every color, drawn by hand.
	bloom := b.group("bloom")
	center := geo.V(1300, 215)
	spokes := []string{"violet", "light-violet", "red", "light-red", "orange", "yellow", "light-green", "green", "light-blue", "blue"}
	const petals = 30
	for i := range petals {
		r := math.Pi * float64(i) / petals
		x, y := around(center, 400, 112, r)
		g := b.geoShape("petal", box{x: x, y: y, w: 400, h: 112, kind: "ellipse", color: spokes[i%len(spokes)], size: "s"}, bloom)
		g.Rotation = r
	}
	for i := range 20 {
		a := 2 * math.Pi * float64(i) / 20
		p := center.Add(geo.V(218, 0).Rot(a))
		b.geoShape("bead", box{x: p.X - 9, y: p.Y - 9, w: 18, h: 18, kind: "ellipse", color: spokes[i%len(spokes)], fill: "fill", size: "s", dash: "solid"}, bloom)
	}
	b.geoShape("heart", box{x: center.X - 44, y: center.Y - 42, w: 88, h: 84, kind: "heart", color: "red", fill: "fill", size: "s"}, bloom)

	// A live diagram: boxes in a frame, joined by arrows bound to them.
	frame := doc.NewShape(b.id("frame"), doc.TypeFrame)
	frame.X, frame.Y, frame.W, frame.H = 40, 470, 1040, 470
	frame.Name = "What happens when you draw"
	b.add(frame, "")
	f := frame.ID
	node := func(x, y, w float64, text, col, fill, kind string) *doc.Shape {
		return b.geoShape("node", box{x: x, y: y, w: w, h: 92, kind: kind, text: text, color: col, fill: fill, size: "s"}, f)
	}
	pen0 := node(40, 110, 150, "Your pen", "violet", "solid", "oval")
	ed := node(280, 110, 170, "Editor", "blue", "solid", "rectangle")
	list := node(540, 110, 190, "Display list", "green", "solid", "rectangle")
	gpu := node(820, 110, 170, "Metal", "red", "solid", "hexagon")
	docBox := node(280, 330, 170, "Document", "orange", "solid", "rectangle")
	file := node(610, 330, 150, ".tldr file", "grey", "semi", "rectangle")
	undo := node(40, 330, 150, "Undo", "light-blue", "pattern", "cloud")
	b.arrow(link{from: pen0, to: ed}, f)
	b.arrow(link{from: ed, to: list}, f)
	b.arrow(link{from: list, to: gpu}, f)
	b.arrow(link{from: ed, to: docBox, label: "immutable", color: "orange"}, f)
	b.arrow(link{from: docBox, to: file, label: "autosave", color: "grey", dash: "dashed", headStart: "dot"}, f)
	b.arrow(link{from: docBox, to: undo, color: "light-blue", bend: 40, head: "triangle"}, f)
	b.arrow(link{from: gpu, to: pen0, color: "light-red", dash: "dotted", bend: 128, label: "next frame", head: "triangle"}, f)
	// A nudge to try it, in the margin.
	b.text(words{x: 800, y: 330, text: "grab this box,\nthe arrows follow", size: "s", color: "red", rotation: -0.05}, f)
	b.arrow(link{start: geo.V(830, 324), to: list, bend: -34, color: "red", size: "s"}, f)

	// Facts, on paper.
	b.note(1130, 470, -0.045, "yellow", "11.8 MB app.\nNo Electron.")
	b.note(1362, 486, 0.03, "light-blue", "Drag all 1,700 strokes at 30 ms a frame")
	b.note(1138, 700, 0.025, "light-green", "Opens your .tldr files")
	b.note(1370, 716, -0.03, "violet", "Zero lines of JavaScript")
	b.text(words{x: 1134, y: 925, text: "gopher after Renée French, CC BY", size: "s", color: "grey", font: "sans", scale: 0.72}, "")
}

// --- page 2: a flow field ---

// flowAngle is a smooth direction at each point of the page: slow waves laid
// over a swirl.
func flowAngle(p geo.Vec) float64 {
	x, y := p.X/420, p.Y/420
	a := math.Sin(x*1.7+math.Sin(y*1.3)*1.4) + 0.7*math.Sin(y*2.3-x*0.9) + 0.35*math.Sin((x+y)*4.1)
	dx, dy := p.X-800, p.Y-470
	swirl := math.Atan2(dy, dx) + math.Pi/2
	w := math.Exp(-(dx*dx + dy*dy) / (2 * 330 * 330))
	return a*1.9*(1-w) + swirl*w
}

func (b *builder) flowField() {
	b.newPage("page:flow", "Flow field")
	ramp := []string{"violet", "light-violet", "blue", "light-blue", "green", "light-green", "yellow", "orange", "red", "light-red"}
	const count = 1700
	for range count {
		p := geo.V(b.r.Float64()*1600, b.r.Float64()*940)
		// Color drifts across the page and along the field, so that it
		// falls in bands.
		t := p.X/1600*0.65 + (math.Sin(p.Y/190)+1)*0.17 + b.r.Float64()*0.08
		col := ramp[min(int(t*float64(len(ramp))), len(ramp)-1)]
		pts := []geo.Vec{p}
		n := 18 + b.r.IntN(34)
		for range n {
			p = p.Add(geo.V(5.5, 0).Rot(flowAngle(p)))
			if p.X < 0 || p.X > 1600 || p.Y < 0 || p.Y > 940 {
				break
			}
			pts = append(pts, p)
		}
		if len(pts) < 8 {
			continue
		}
		size := "s"
		if b.r.Float64() < 0.16 {
			size = "m"
		}
		b.stroke("flow", pts, pen{color: col, size: size}, "")
	}
	// A caption on a card, so that it reads over the lines.
	b.geoShape("card", box{x: 40, y: 790, w: 660, h: 118, color: "black", fill: "semi", size: "m"}, "")
	b.text(words{x: 66, y: 804, text: "Every line here is a real pen stroke.", size: "m"}, "")
	b.text(words{x: 66, y: 850, text: "Select all, drag it, recolor it. Then undo.", size: "s", color: "grey"}, "")
}

// --- page 3: every style ---

// fractal renders a Mandelbrot set in the palette's colors: a real image, as
// you could drop on the canvas.
func fractal(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	stops := []color.RGBA{{0x1d, 0x1d, 0x4d, 255}, {0x44, 0x65, 0xe9, 255}, {0x4b, 0xa1, 0xf1, 255}, {0xf1, 0xac, 0x4b, 255}, {0xe0, 0x31, 0x31, 255}, {0xae, 0x3e, 0xc9, 255}}
	for py := range h {
		for px := range w {
			c := complex(-0.745+(float64(px)/float64(w)-0.5)*0.03, 0.111+(float64(py)/float64(h)-0.5)*0.03*float64(h)/float64(w))
			z := complex(0, 0)
			i := 0
			const limit = 400
			for ; i < limit && cmplx.Abs(z) < 4; i++ {
				z = z*z + c
			}
			if i == limit {
				img.Set(px, py, color.RGBA{0x10, 0x10, 0x18, 255})
				continue
			}
			s := float64(i) + 1 - math.Log2(math.Log(cmplx.Abs(z)))
			f := math.Mod(s/28, 1) * float64(len(stops))
			a, c2 := stops[int(f)%len(stops)], stops[(int(f)+1)%len(stops)]
			k := f - math.Floor(f)
			mix := func(x, y uint8) uint8 { return uint8(float64(x)*(1-k) + float64(y)*k) }
			img.Set(px, py, color.RGBA{mix(a.R, c2.R), mix(a.G, c2.G), mix(a.B, c2.B), 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		log.Fatal(err)
	}
	return buf.Bytes()
}

func (b *builder) styleSheet() {
	b.newPage("page:styles", "Every style")
	b.text(words{x: 40, y: 0, text: "Every style", size: "xl", scale: 1.5}, "")

	kinds := []string{
		"rectangle", "ellipse", "triangle", "diamond", "pentagon",
		"hexagon", "octagon", "star", "rhombus", "rhombus-2",
		"oval", "trapezoid", "arrow-right", "arrow-left", "arrow-up",
		"arrow-down", "x-box", "check-box", "cloud", "heart",
	}
	fills := []string{"none", "semi", "solid", "pattern", "fill"}
	dashes := []string{"draw", "dashed", "dotted", "solid"}
	cols := []string{"black", "blue", "red", "green", "orange", "violet", "light-blue", "light-red", "light-green", "yellow", "grey", "light-violet"}
	for i, k := range kinds {
		b.geoShape("kind", box{
			x: 40 + float64(i%10)*122, y: 110 + float64(i/10)*132, w: 108, h: 116, kind: k,
			color: cols[i%len(cols)], fill: fills[i%len(fills)], dash: dashes[i%len(dashes)],
		}, "")
	}

	// Sizes and fonts.
	for i, f := range []string{"draw", "sans", "serif", "mono"} {
		b.text(words{x: 40, y: 400 + float64(i)*46, text: map[string]string{
			"draw": "Hand drawn", "sans": "Sans serif", "serif": "With serifs", "mono": "Monospace",
		}[f], font: f, size: "m"}, "")
	}
	y := 410.0
	for i, s := range []string{"s", "m", "l", "xl"} {
		w := 150 + float64(i)*30
		b.stroke("size", catmull([]geo.Vec{geo.V(300, y), geo.V(300+w*0.33, y-18), geo.V(300+w*0.66, y+18), geo.V(300+w, y)}, false, 6), pen{color: "blue", size: s}, "")
		y += 42
	}

	// Arrowheads, each a straight arrow.
	for i, h := range []string{"arrow", "triangle", "square", "dot", "diamond", "inverted", "bar", "pipe"} {
		row := float64(i % 4)
		col := float64(i / 4)
		b.arrow(link{
			start: geo.V(610+col*230, 408+row*42), end: geo.V(800+col*230, 408+row*42),
			head: h, headStart: "none", color: cols[i%len(cols)], dash: "solid",
		}, "")
	}

	// A curved line, an elbow, a bent arrow.
	line := doc.NewShape(b.id("line"), doc.TypeLine)
	line.X, line.Y, line.Spline, line.Color = 40, 640, "cubic", "green"
	line.Points = []doc.LinePoint{{ID: "a", Index: "a1"}, {ID: "b", Index: "a2", X: 120, Y: -36}, {ID: "c", Index: "a3", X: 240, Y: 16}, {ID: "d", Index: "a4", X: 360, Y: -20}}
	b.add(line, "")
	a := b.geoShape("from", box{x: 450, y: 590, w: 110, h: 76, text: "from", color: "blue", fill: "solid", size: "s"}, "")
	c := b.geoShape("to", box{x: 720, y: 720, w: 110, h: 76, text: "to", color: "red", fill: "solid", size: "s"}, "")
	b.arrow(link{from: a, to: c, kind: "elbow", color: "black"}, "")
	d := b.geoShape("bent", box{x: 880, y: 590, w: 110, h: 76, text: "bent", color: "violet", fill: "solid", size: "s"}, "")
	b.arrow(link{from: c, to: d, bend: 70, color: "violet", label: "arc"}, "")

	// A turned frame clipping what it holds.
	frame := doc.NewShape(b.id("frame"), doc.TypeFrame)
	frame.X, frame.Y, frame.W, frame.H, frame.Rotation = 90, 700, 280, 190, -0.09
	frame.Name = "Frames clip"
	b.add(frame, "")
	b.geoShape("clipped", box{x: 130, y: 60, w: 260, h: 200, kind: "ellipse", color: "orange", fill: "fill"}, frame.ID)
	b.geoShape("clipped", box{x: -40, y: 90, w: 180, h: 160, kind: "star", color: "blue", fill: "pattern"}, frame.ID)

	// A real image, turned.
	data := fractal(560, 400)
	asset := &doc.Asset{
		ID: "asset:fractal", Type: "image", W: 560, H: 400, Name: "fractal.png", Mime: "image/png",
		Src: "data:image/png;base64," + base64.StdEncoding.EncodeToString(data),
	}
	b.d.Assets[asset.ID] = asset
	img := doc.NewShape(b.id("image"), doc.TypeImage)
	img.X, img.Y, img.W, img.H, img.Rotation = 1040, 600, 280, 200, 0.05
	img.AssetID = asset.ID
	b.add(img, "")

	// Highlighter over words.
	mark := doc.NewShape(b.id("mark"), doc.TypeHighlight)
	mark.X, mark.Y, mark.Color, mark.Size, mark.IsComplete = 1128, 868, "light-green", "m", true
	pts := []doc.PointZ{{}, {X: 60, Y: -2, Z: .5}, {X: 130, Y: 1, Z: .5}, {X: 212, Y: -1, Z: .5}}
	mark.Segments = []doc.Segment{{Type: "free", Points: pts, Dim: 2}}
	b.add(mark, "")
	b.text(words{x: 1128, y: 850, text: "Highlighted", size: "m"}, "")

	// Opacity falling off.
	for i := range 6 {
		s := b.geoShape("fade", box{x: 800 + float64(i)*46, y: 840, w: 64, h: 64, kind: "ellipse", color: "red", fill: "fill", size: "s"}, "")
		s.Opacity = 1 - float64(i)*0.16
	}
}

func main() {
	b := &builder{
		d: &doc.Drawing{
			Name: "godraw showcase", GridSize: 10,
			Shapes: map[doc.ID]*doc.Shape{}, Bindings: map[doc.ID]*doc.Binding{}, Assets: map[doc.ID]*doc.Asset{},
		},
		last: map[doc.ID]string{},
		r:    rand.New(rand.NewPCG(2026, 11)),
	}
	b.poster()
	b.flowField()
	b.styleSheet()

	out := "examples/showcase.tldr"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	f, err := os.Create(out)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := doc.Write(f, b.d); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s: %d shapes on %d pages", out, len(b.d.Shapes), len(b.d.Pages))
}
