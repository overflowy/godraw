package editor

import (
	"math"

	"godraw/internal/doc"
	"godraw/internal/geo"
)

const (
	// What a shadow is, of what it doesn't say: dark in either theme, as a shadow
	// is, whichever way the palette's black goes.
	shadowColor   = "#000000"
	shadowOpacity = 0.35
	// How many layers a blur is made of: enough not to see them.
	shadowMinLayers = 6
	shadowMaxLayers = 20
	shadowMiter     = 0.55
)

// ShadowReach returns how far past a shape's box its shadow can reach.
func ShadowReach(s *doc.Shape) float64 {
	sh := s.Shadow
	if sh == nil {
		return 0
	}
	return sh.Blur + math.Max(math.Abs(sh.X), math.Abs(sh.Y))
}

// shadowPrims draws a shape's shadow, built of paths as a note's is, so that it
// turns with the shape and comes out the same on the canvas and in pictures and SVG.
// Layers grow from what casts it out to its blur, each adding what makes them come to
// a falloff that fades to nothing, for its outer edge not to show. A shape that fills is
// under what it casts, and one that doesn't glows inside its line as well as out.
func (e *Editor) shadowPrims(s *doc.Shape, sh *doc.Shadow, pal *Palette) []Prim {
	base, filled, width, rounded := e.shadowCaster(s)
	if base == nil {
		return nil
	}
	hex := sh.Color
	if hex == "" {
		hex = shadowColor
	}
	opacity := sh.Opacity
	if opacity == 0 {
		opacity = shadowOpacity
	}
	tint := pal.colorOf(hex)
	n := min(max(int(sh.Blur/2)+2, shadowMinLayers), shadowMaxLayers)
	if sh.Blur == 0 {
		n = 1
	}
	at := geo.Translate(sh.X, sh.Y)
	var flat []geo.Vec
	if filled && rounded == nil {
		flat, _ = base.Points(10)
	}
	out := make([]Prim, 0, n)
	reached := 0.0 // what the layers so far come to
	for i := range n {
		t := 1 - float64(i+1)/float64(n)
		d := sh.Blur * t
		// What it takes for this layer to bring them to the falloff at its edge.
		want := opacity * (1 - t) * (1 - t)
		c := tint.Alpha(1 - (1-want)/(1-reached))
		reached = want
		switch {
		case rounded != nil:
			box := rounded.Expand(d)
			box.X += sh.X
			box.Y += sh.Y
			corners := box.Corners()
			p := fillWith(geo.RoundedPoly(corners[:], rounded.Radius+d), c)
			p.Rounded = &RoundedBox{box, rounded.Radius + d}
			out = append(out, p)
		case filled:
			out = append(out, fillWith((&geo.Path{}).Poly(offsetPoly(flat, d), true).Transform(at), c))
		default:
			out = append(out, strokeWith(base.Transform(at), width+2*d, c))
		}
	}
	return out
}

// shadowCaster returns what casts a shape's shadow: its path, whether it fills, the
// width of its line, and the box it is, if a shape's fill is one.
func (e *Editor) shadowCaster(s *doc.Shape) (base *geo.Path, filled bool, width float64, rounded *RoundedBox) {
	box := e.Geometry(s).Bounds
	corners := box.Corners()
	switch s.Type {
	case doc.TypeGeo:
		w, h := s.W, s.H+s.GrowY
		base = geoBasePath(s, w, h, e.Geometry(s).Outline)
		filled = s.Fill != "none"
		width = strokeOf(s.Size) * s.Scale
		if filled && s.Geo == "rectangle" {
			rounded = &RoundedBox{geo.Box{W: w, H: h}, radiusOf(s, w, h)}
		}
		return base, filled, width, rounded
	case doc.TypeDraw:
		pts := strokePoints(s)
		if len(pts) < 2 {
			return nil, false, 0, nil
		}
		return (&geo.Path{}).Smooth(pts, s.IsClosed), s.IsClosed && s.Fill != "none", strokeOf(s.Size) * s.Scale, nil
	case doc.TypeLine:
		return linePath(s), false, strokeOf(s.Size) * s.Scale, nil
	case doc.TypeNote:
		return geo.RoundedPoly(corners[:], 6*s.Scale), true, 0, &RoundedBox{box, 6 * s.Scale}
	case doc.TypeImage:
		return (&geo.Path{}).Poly(corners[:], true), true, 0, &RoundedBox{Box: box}
	}
	return nil, false, 0, nil
}

// offsetPoly returns a closed polygon moved out from its line by d, whichever way
// round it runs. A sharp corner goes no farther out than d over shadowMiter, for a
// star's glow not to shoot out spikes.
func offsetPoly(pts []geo.Vec, d float64) []geo.Vec {
	// A path that closes can end where it began, which gives that point no direction.
	if n := len(pts); n > 1 && pts[0].Dist(pts[n-1]) < 1e-6 {
		pts = pts[:n-1]
	}
	n := len(pts)
	if n < 3 || d == 0 {
		return pts
	}
	area := 0.0
	for i, p := range pts {
		q := pts[(i+1)%n]
		area += p.X*q.Y - q.X*p.Y
	}
	sign := 1.0
	if area < 0 {
		sign = -1
	}
	out := make([]geo.Vec, n)
	for i, p := range pts {
		before, after := p.Sub(pts[(i+n-1)%n]).Uni(), pts[(i+1)%n].Sub(p).Uni()
		nrm := before.Per().Add(after.Per()).Uni()
		// A corner's edges move out by d when its point moves by d over the half-angle's
		// cosine.
		cos := math.Max(nrm.Dot(after.Per()), shadowMiter)
		out[i] = p.Add(nrm.Mul(sign * d / cos))
	}
	return out
}
