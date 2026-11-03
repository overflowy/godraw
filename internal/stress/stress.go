// Package stress builds pages of many shapes, for measuring how the canvas
// keeps up with them.
package stress

import (
	"fmt"
	"math"
	"math/rand/v2"

	"godraw/internal/doc"
	"godraw/internal/geo"
)

// Diagram returns a drawing of about n shapes laid out as an agent or a person
// lays out a large diagram: cards and notes in a grid, in the default hand-drawn
// style or otherwise, arrows between neighbours, some scribbles and a few frames.
// The same n gives the same drawing.
func Diagram(n int) *doc.Drawing {
	d := doc.New()
	r := rand.New(rand.NewPCG(1, uint64(n)))
	page := d.Pages[0].ID
	index := ""
	next := 0
	add := func(s *doc.Shape, parent doc.ID) *doc.Shape {
		next++
		s.Parent = parent
		index = doc.IndexBetween(index, "")
		s.Index = index
		d.Shapes[s.ID] = s
		return s
	}
	id := func(kind string) doc.ID { return doc.ID(fmt.Sprintf("shape:%s-%d", kind, next)) }

	colors := []string{"black", "blue", "red", "green", "orange", "violet", "light-blue", "grey"}
	fills := []string{"none", "none", "none", "semi", "solid", "pattern"}
	dashes := []string{"draw", "draw", "draw", "solid", "dashed", "dotted"}
	geos := []string{"rectangle", "rectangle", "rectangle", "ellipse", "diamond", "cloud", "hexagon"}
	words := []string{"Ingest", "Parser", "Queue", "Worker", "Cache", "Store", "Gateway", "Auth", "Index", "Search"}

	// Of each ten shapes: four boxes, three arrows, one text, one note, one scribble.
	cols := max(int(math.Sqrt(float64(n)*0.5)), 1)
	const stepX, stepY = 300.0, 220.0
	frame := doc.ID("")
	var boxes []doc.ID
	for i := 0; next < n; i++ {
		x, y := float64(i%cols)*stepX, float64(i/cols)*stepY
		if i%cols == 0 && i/cols%6 == 0 {
			s := doc.NewShape(id("frame"), doc.TypeFrame)
			s.X, s.Y, s.W, s.H, s.Name = x-30, y-30, float64(cols)*stepX, 6*stepY-10, fmt.Sprintf("Region %d", i/cols/6+1)
			add(s, page)
			frame = s.ID
		}
		_ = frame
		switch i % 10 {
		case 8:
			s := doc.NewShape(id("note"), doc.TypeNote)
			s.X, s.Y, s.Color = x, y, colors[r.IntN(len(colors))]
			s.SetText(words[r.IntN(len(words))] + " notes")
			add(s, page)
			continue
		case 9:
			s := doc.NewShape(id("draw"), doc.TypeDraw)
			s.X, s.Y, s.ScaleX, s.ScaleY = x, y, 1, 1
			s.Color = colors[r.IntN(len(colors))]
			pts := make([]doc.PointZ, 60+r.IntN(80))
			phase := r.Float64() * 6
			for k := range pts {
				t := float64(k)
				pts[k] = doc.PointZ{X: t * 1.5, Y: 50 + 40*math.Sin(t/6+phase), Z: 0.5}
			}
			s.Segments = []doc.Segment{{Type: "free", Points: pts}}
			add(s, page)
			continue
		case 7:
			s := doc.NewShape(id("text"), doc.TypeText)
			s.X, s.Y, s.AutoSize, s.Size = x, y+40, true, "s"
			s.SetText(words[r.IntN(len(words))] + " handles the " + words[r.IntN(len(words))])
			add(s, page)
			continue
		}
		s := doc.NewShape(id("box"), doc.TypeGeo)
		s.X, s.Y, s.W, s.H = x, y, 200, 120
		s.Geo = geos[r.IntN(len(geos))]
		s.Color = colors[r.IntN(len(colors))]
		s.Fill = fills[r.IntN(len(fills))]
		s.Dash = dashes[r.IntN(len(dashes))]
		s.SetText(words[r.IntN(len(words))])
		add(s, page)
		boxes = append(boxes, s.ID)
		if len(boxes) > 1 && next < n && i%10 < 3 {
			arrow(d, add, id("arrow"), boxes[len(boxes)-2], s.ID, page, i%3 == 0)
		}
	}
	return d
}

// arrow adds an arrow bound at both ends.
func arrow(d *doc.Drawing, add func(*doc.Shape, doc.ID) *doc.Shape, id, from, to, page doc.ID, elbow bool) {
	s := doc.NewShape(id, doc.TypeArrow)
	if elbow {
		s.Kind = "elbow"
	}
	add(s, page)
	for term, target := range map[string]doc.ID{"start": from, "end": to} {
		b := &doc.Binding{
			ID: doc.ID("binding:" + string(id) + term), Type: "arrow", From: id, To: target,
			Terminal: term, Anchor: geo.V(0.5, 0.5),
		}
		d.Bindings[b.ID] = b
	}
}
