package doc

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strings"

	"godraw/internal/geo"
)

// fileSchema names the versions of tldraw's record types that files this
// app writes hold, those of @tldraw/tlschema 5.6.0. tldraw migrates files
// from them as it opens them, so that later versions still read these.
var fileSchema = map[string]int{
	"com.tldraw.store": 5, "com.tldraw.asset": 1, "com.tldraw.camera": 1, "com.tldraw.document": 2,
	"com.tldraw.instance": 26, "com.tldraw.instance_page_state": 5, "com.tldraw.page": 1,
	"com.tldraw.instance_presence": 6, "com.tldraw.pointer": 1, "com.tldraw.shape": 4, "com.tldraw.user": 1,
	"com.tldraw.asset.image": 6, "com.tldraw.asset.video": 5, "com.tldraw.asset.bookmark": 2,
	"com.tldraw.shape.arrow": 8, "com.tldraw.shape.bookmark": 2, "com.tldraw.shape.draw": 5,
	"com.tldraw.shape.embed": 4, "com.tldraw.shape.frame": 1, "com.tldraw.shape.geo": 12,
	"com.tldraw.shape.group": 0, "com.tldraw.shape.highlight": 4, "com.tldraw.shape.image": 5,
	"com.tldraw.shape.line": 5, "com.tldraw.shape.note": 13, "com.tldraw.shape.text": 4,
	"com.tldraw.shape.video": 4, "com.tldraw.binding.arrow": 1,
}

// tldrFile is the outside of a .tldr file. Older files hold some props in
// other forms, which readShape takes where they are simple.
type tldrFile struct {
	Version int               `json:"tldrawFileFormatVersion"`
	Schema  json.RawMessage   `json:"schema"`
	Records []json.RawMessage `json:"records"`
}

// ErrNotTldraw tells that a file is not a tldraw file this app reads.
var ErrNotTldraw = errors.New("this is not a tldraw file")

// Read reads a .tldr file.
func Read(r io.Reader) (*Drawing, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return New(), nil // a file made empty, as with touch
	}
	var f tldrFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotTldraw, err)
	}
	if f.Version == 0 {
		return nil, ErrNotTldraw
	}
	if f.Version > 1 {
		return nil, fmt.Errorf("this file is from a newer version of tldraw (format %d)", f.Version)
	}
	d := &Drawing{
		GridSize: 10,
		Shapes:   map[ID]*Shape{},
		Bindings: map[ID]*Binding{},
		Assets:   map[ID]*Asset{},
	}
	for _, raw := range f.Records {
		var head struct {
			ID       ID     `json:"id"`
			TypeName string `json:"typeName"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			return nil, err
		}
		switch head.TypeName {
		case "document":
			var r struct {
				Name     string          `json:"name"`
				GridSize float64         `json:"gridSize"`
				Meta     json.RawMessage `json:"meta"`
			}
			if err := json.Unmarshal(raw, &r); err != nil {
				return nil, err
			}
			d.Name, d.Meta = r.Name, r.Meta
			if r.GridSize > 0 {
				d.GridSize = r.GridSize
			}
		case "page":
			var r struct {
				Name  string          `json:"name"`
				Index string          `json:"index"`
				Meta  json.RawMessage `json:"meta"`
			}
			if err := json.Unmarshal(raw, &r); err != nil {
				return nil, err
			}
			d.Pages = append(d.Pages, &Page{ID: head.ID, Name: r.Name, Index: r.Index, Meta: r.Meta})
		case "shape":
			s, err := readShape(raw)
			if err != nil {
				return nil, fmt.Errorf("shape %s: %w", head.ID, err)
			}
			d.Shapes[s.ID] = s
		case "binding":
			b, err := readBinding(raw)
			if err != nil {
				return nil, fmt.Errorf("binding %s: %w", head.ID, err)
			}
			d.Bindings[b.ID] = b
		case "asset":
			a, err := readAsset(raw)
			if err != nil {
				return nil, fmt.Errorf("asset %s: %w", head.ID, err)
			}
			d.Assets[a.ID] = a
		case "camera", "instance", "instance_page_state", "instance_presence", "pointer":
			// The session, which files do not keep.
		default:
			d.Others = append(d.Others, raw)
		}
	}
	if len(d.Pages) == 0 {
		d.Pages = []*Page{{ID: "page:page", Name: "Page 1", Index: FirstIndex}}
	}
	slices.SortStableFunc(d.Pages, func(a, b *Page) int { return strings.Compare(a.Index, b.Index) })
	return d, nil
}

// Write writes a .tldr file.
func Write(w io.Writer, d *Drawing) error {
	records := make([]any, 0, len(d.Shapes)+len(d.Pages)+4)
	records = append(records, obj{
		{"gridSize", orNum(d.GridSize, 10)},
		{"name", d.Name},
		{"meta", metaOf(d.Meta)},
		{"id", "document:document"},
		{"typeName", "document"},
	})
	for _, p := range d.Pages {
		records = append(records, obj{
			{"meta", metaOf(p.Meta)},
			{"id", p.ID},
			{"name", p.Name},
			{"index", p.Index},
			{"typeName", "page"},
		})
	}
	ids := make([]ID, 0, len(d.Shapes))
	for id := range d.Shapes {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		records = append(records, shapeRecord(d.Shapes[id]))
	}
	ids = ids[:0]
	for id := range d.Bindings {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		records = append(records, bindingRecord(d.Bindings[id]))
	}
	ids = ids[:0]
	for id := range d.Assets {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		records = append(records, assetRecord(d.Assets[id]))
	}
	for _, o := range d.Others {
		records = append(records, o)
	}
	schema := obj{{"schemaVersion", 2}, {"sequences", fileSchema}}
	enc := json.NewEncoder(w)
	return enc.Encode(obj{
		{"tldrawFileFormatVersion", 1},
		{"schema", schema},
		{"records", records},
	})
}

func orNum(v, def float64) float64 {
	if v == 0 {
		return def
	}
	return v
}

func metaOf(m json.RawMessage) json.RawMessage {
	if len(m) == 0 {
		return json.RawMessage("{}")
	}
	return m
}

// kv and obj are a JSON object with its keys in order, so that files are
// the same each time they are written and diff well.
type kv struct {
	K string
	V any
}

type obj []kv

// MarshalJSON writes the keys in order, later values of a key winning.
func (o obj) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	first := true
	for i, e := range o {
		last := true
		for _, l := range o[i+1:] {
			if l.K == e.K {
				last = false
				break
			}
		}
		if !last {
			continue
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		k, err := json.Marshal(e.K)
		if err != nil {
			return nil, err
		}
		v, err := json.Marshal(e.V)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func readShape(raw []byte) (*Shape, error) {
	var r struct {
		ID       ID              `json:"id"`
		Type     string          `json:"type"`
		ParentID ID              `json:"parentId"`
		Index    string          `json:"index"`
		X        float64         `json:"x"`
		Y        float64         `json:"y"`
		Rotation float64         `json:"rotation"`
		Opacity  *float64        `json:"opacity"`
		IsLocked bool            `json:"isLocked"`
		Meta     json.RawMessage `json:"meta"`
		Props    map[string]json.RawMessage
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	s := NewShape(r.ID, r.Type)
	s.Parent, s.Index, s.X, s.Y, s.Rotation, s.Locked, s.Meta = r.ParentID, r.Index, r.X, r.Y, r.Rotation, r.IsLocked, r.Meta
	if r.Opacity != nil {
		s.Opacity = *r.Opacity
	}
	s.Raw = r.Props
	p := props(r.Props)
	s.Geo = p.str("geo", s.Geo)
	s.Color = p.str("color", s.Color)
	s.LabelColor = p.str("labelColor", s.LabelColor)
	s.Fill = p.str("fill", s.Fill)
	s.Dash = p.str("dash", s.Dash)
	s.Size = p.str("size", s.Size)
	s.Font = p.str("font", s.Font)
	s.Align = legacyAlign(p.str("align", s.Align))
	s.VAlign = p.str("verticalAlign", s.VAlign)
	s.TextAlign = p.str("textAlign", s.TextAlign)
	s.AutoSize = p.boolean("autoSize", s.AutoSize)
	s.URL = p.str("url", "")
	s.Name = p.str("name", "")
	s.W = p.num("w", s.W)
	s.H = p.num("h", s.H)
	s.GrowY = p.num("growY", 0)
	s.Scale = p.num("scale", 1)
	s.FlipX = p.boolean("flipX", false)
	s.FlipY = p.boolean("flipY", false)
	s.IsComplete = p.boolean("isComplete", false)
	s.IsClosed = p.boolean("isClosed", false)
	s.IsPen = p.boolean("isPen", false)
	s.ScaleX = p.num("scaleX", 1)
	s.ScaleY = p.num("scaleY", 1)
	s.Kind = p.str("kind", s.Kind)
	s.HeadStart = p.str("arrowheadStart", s.HeadStart)
	s.HeadEnd = p.str("arrowheadEnd", s.HeadEnd)
	s.Bend = p.num("bend", 0)
	s.LabelPosition = p.num("labelPosition", s.LabelPosition)
	s.ElbowMidPoint = p.num("elbowMidPoint", s.ElbowMidPoint)
	s.Spline = p.str("spline", s.Spline)
	s.AltText = p.str("altText", "")
	s.Playing = p.boolean("playing", false)
	s.AssetID = ID(p.str("assetId", ""))
	if v, ok := p["fontSizeAdjustment"]; ok {
		var f *float64
		json.Unmarshal(v, &f)
		s.FontAdjustment = f
	}
	var v struct{ X, Y float64 }
	if b, ok := p["start"]; ok && json.Unmarshal(b, &v) == nil {
		s.Start = geo.V(v.X, v.Y)
	}
	if b, ok := p["end"]; ok && json.Unmarshal(b, &v) == nil {
		s.End = geo.V(v.X, v.Y)
	}
	if b, ok := p["crop"]; ok && string(b) != "null" {
		var c struct {
			TopLeft     struct{ X, Y float64 } `json:"topLeft"`
			BottomRight struct{ X, Y float64 } `json:"bottomRight"`
			IsCircle    bool                   `json:"isCircle"`
		}
		if json.Unmarshal(b, &c) == nil {
			s.Crop = &Crop{geo.V(c.TopLeft.X, c.TopLeft.Y), geo.V(c.BottomRight.X, c.BottomRight.Y), c.IsCircle}
		}
	}
	if b, ok := p["points"]; ok {
		var pts map[string]struct {
			ID    string  `json:"id"`
			Index string  `json:"index"`
			X     float64 `json:"x"`
			Y     float64 `json:"y"`
		}
		if json.Unmarshal(b, &pts) == nil {
			s.Points = s.Points[:0:0]
			for _, l := range pts {
				s.Points = append(s.Points, LinePoint{l.ID, l.Index, l.X, l.Y})
			}
			sort.Slice(s.Points, func(i, j int) bool { return s.Points[i].Index < s.Points[j].Index })
		}
	}
	if b, ok := p["segments"]; ok {
		segs, err := readSegments(b)
		if err != nil {
			return nil, err
		}
		s.Segments = segs
	}
	if b, ok := p["richText"]; ok {
		s.RichText = b
		s.Text = RichToPlain(b)
		s.richOf = s.Text
	} else {
		s.Text = p.str("text", "")
	}
	return s, nil
}

// legacyAlign reads the alignments tldraw kept for old files.
func legacyAlign(a string) string { return strings.TrimSuffix(a, "-legacy") }

type props map[string]json.RawMessage

func (p props) str(k, def string) string {
	if b, ok := p[k]; ok {
		var v string
		if json.Unmarshal(b, &v) == nil {
			return v
		}
	}
	return def
}

func (p props) num(k string, def float64) float64 {
	if b, ok := p[k]; ok {
		var v float64
		if json.Unmarshal(b, &v) == nil {
			return v
		}
	}
	return def
}

func (p props) boolean(k string, def bool) bool {
	if b, ok := p[k]; ok {
		var v bool
		if json.Unmarshal(b, &v) == nil {
			return v
		}
	}
	return def
}

func readSegments(b []byte) ([]Segment, error) {
	var segs []struct {
		Type   string          `json:"type"`
		Path   string          `json:"path"`
		Dim    int             `json:"dim"`
		Points json.RawMessage `json:"points"`
	}
	if err := json.Unmarshal(b, &segs); err != nil {
		return nil, err
	}
	out := make([]Segment, 0, len(segs))
	for _, s := range segs {
		seg := Segment{Type: s.Type, Dim: s.Dim}
		switch {
		case s.Path != "":
			pts, err := decodePoints(s.Path, s.Dim)
			if err != nil {
				return nil, err
			}
			seg.Points = pts
		case len(s.Points) > 0:
			// Files from before points were encoded.
			var pts []struct{ X, Y, Z float64 }
			if err := json.Unmarshal(s.Points, &pts); err == nil {
				for _, p := range pts {
					seg.Points = append(seg.Points, PointZ{p.X, p.Y, p.Z})
				}
			}
		}
		out = append(out, seg)
	}
	return out, nil
}

// float16 converts between float64 and IEEE half precision, the deltas of
// the points of .tldr strokes.
func float16From(b uint16) float64 {
	sign := 1.0
	if b&0x8000 != 0 {
		sign = -1
	}
	exp := int(b>>10) & 0x1f
	frac := float64(b & 0x3ff)
	switch exp {
	case 0:
		return sign * frac * math.Pow(2, -24)
	case 31:
		if frac == 0 {
			return sign * math.Inf(1)
		}
		return math.NaN()
	}
	return sign * (1 + frac/1024) * math.Pow(2, float64(exp-15))
}

func float16To(f float64) uint16 {
	var sign uint16
	if f < 0 || (f == 0 && math.Signbit(f)) {
		sign, f = 0x8000, -f
	}
	switch {
	case f == 0:
		return sign
	case math.IsInf(f, 0), f >= 65520:
		return sign | 0x7bff // the largest finite value, not to break the file
	case f < math.Pow(2, -14):
		return sign | uint16(math.Round(f/math.Pow(2, -24)))
	}
	exp := math.Floor(math.Log2(f))
	frac := math.Round((f/math.Pow(2, exp) - 1) * 1024)
	if frac >= 1024 {
		frac = 0
		exp++
	}
	if exp > 15 {
		return sign | 0x7bff
	}
	return sign | uint16(exp+15)<<10 | uint16(frac)
}

func decodePoints(s string, dim int) ([]PointZ, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	var out []PointZ
	le := binary.LittleEndian
	if dim == 2 {
		if len(b) < 8 {
			return nil, nil
		}
		x := float64(math.Float32frombits(le.Uint32(b)))
		y := float64(math.Float32frombits(le.Uint32(b[4:])))
		out = append(out, PointZ{x, y, 0.5})
		for o := 8; o+4 <= len(b); o += 4 {
			x += float16From(le.Uint16(b[o:]))
			y += float16From(le.Uint16(b[o+2:]))
			out = append(out, PointZ{x, y, 0.5})
		}
		return out, nil
	}
	if len(b) < 12 {
		return nil, nil
	}
	x := float64(math.Float32frombits(le.Uint32(b)))
	y := float64(math.Float32frombits(le.Uint32(b[4:])))
	z := float64(math.Float32frombits(le.Uint32(b[8:])))
	out = append(out, PointZ{x, y, z})
	for o := 12; o+6 <= len(b); o += 6 {
		x += float16From(le.Uint16(b[o:]))
		y += float16From(le.Uint16(b[o+2:]))
		z += float16From(le.Uint16(b[o+4:]))
		out = append(out, PointZ{x, y, z})
	}
	return out, nil
}

func encodePoints(pts []PointZ, dim int) string {
	if len(pts) == 0 {
		return ""
	}
	le := binary.LittleEndian
	var b []byte
	var buf [4]byte
	f32 := func(v float64) {
		le.PutUint32(buf[:], math.Float32bits(float32(v)))
		b = append(b, buf[:4]...)
	}
	f16 := func(v float64) float64 {
		bits := float16To(v)
		le.PutUint16(buf[:], bits)
		b = append(b, buf[:2]...)
		return float16From(bits)
	}
	f := pts[0]
	f32(f.X)
	f32(f.Y)
	if dim != 2 {
		f32(f.Z)
	}
	// Deltas are lossy: each goes from where the decoder will be, so that
	// the error does not add up along the stroke.
	x, y, z := float64(float32(f.X)), float64(float32(f.Y)), float64(float32(f.Z))
	for _, p := range pts[1:] {
		x += f16(p.X - x)
		y += f16(p.Y - y)
		if dim != 2 {
			z += f16(p.Z - z)
		}
	}
	return base64.StdEncoding.EncodeToString(b)
}

func shapeRecord(s *Shape) obj {
	return obj{
		{"x", s.X}, {"y", s.Y}, {"rotation", s.Rotation},
		{"isLocked", s.Locked}, {"opacity", s.Opacity},
		{"meta", metaOf(s.Meta)},
		{"id", s.ID}, {"type", s.Type},
		{"props", shapeProps(s)},
		{"parentId", s.Parent}, {"index", s.Index},
		{"typeName", "shape"},
	}
}

func shapeProps(s *Shape) any {
	switch s.Type {
	case TypeGeo:
		return obj{
			{"w", s.W}, {"h", s.H}, {"geo", s.Geo}, {"dash", s.Dash}, {"growY", s.GrowY}, {"url", s.URL},
			{"scale", s.Scale}, {"flipX", s.FlipX}, {"flipY", s.FlipY},
			{"color", s.Color}, {"labelColor", s.LabelColor}, {"fill", s.Fill}, {"size", s.Size}, {"font", s.Font},
			{"align", s.Align}, {"verticalAlign", s.VAlign}, {"richText", richOf(s)},
		}
	case TypeDraw:
		return obj{
			{"segments", segmentsOf(s)}, {"color", s.Color}, {"fill", s.Fill}, {"dash", s.Dash}, {"size", s.Size},
			{"isComplete", s.IsComplete}, {"isClosed", s.IsClosed}, {"isPen", s.IsPen},
			{"scale", s.Scale}, {"scaleX", s.ScaleX}, {"scaleY", s.ScaleY},
		}
	case TypeHighlight:
		return obj{
			{"segments", segmentsOf(s)}, {"color", s.Color}, {"size", s.Size},
			{"isComplete", s.IsComplete}, {"isPen", s.IsPen},
			{"scale", s.Scale}, {"scaleX", s.ScaleX}, {"scaleY", s.ScaleY},
		}
	case TypeArrow:
		return obj{
			{"kind", s.Kind}, {"labelColor", s.LabelColor}, {"color", s.Color}, {"fill", s.Fill}, {"dash", s.Dash},
			{"size", s.Size}, {"arrowheadStart", s.HeadStart}, {"arrowheadEnd", s.HeadEnd}, {"font", s.Font},
			{"start", vec(s.Start)}, {"end", vec(s.End)}, {"bend", s.Bend}, {"richText", richOf(s)},
			{"labelPosition", s.LabelPosition}, {"scale", s.Scale}, {"elbowMidPoint", s.ElbowMidPoint},
		}
	case TypeLine:
		pts := obj{}
		for _, p := range s.Points {
			pts = append(pts, kv{p.ID, obj{{"id", p.ID}, {"index", p.Index}, {"x", p.X}, {"y", p.Y}}})
		}
		return obj{
			{"color", s.Color}, {"dash", s.Dash}, {"size", s.Size}, {"spline", s.Spline},
			{"points", pts}, {"scale", s.Scale},
		}
	case TypeText:
		return obj{
			{"color", s.Color}, {"size", s.Size}, {"w", s.W}, {"font", s.Font}, {"textAlign", s.TextAlign},
			{"autoSize", s.AutoSize}, {"scale", s.Scale}, {"richText", richOf(s)},
		}
	case TypeNote:
		var adj any
		if s.FontAdjustment != nil {
			adj = *s.FontAdjustment
		}
		var lastEdited any
		if b, ok := s.Raw["textLastEditedBy"]; ok {
			lastEdited = json.RawMessage(b)
		}
		return obj{
			{"color", s.Color}, {"labelColor", s.LabelColor}, {"size", s.Size}, {"font", s.Font},
			{"fontSizeAdjustment", adj}, {"align", s.Align}, {"verticalAlign", s.VAlign}, {"growY", s.GrowY},
			{"url", s.URL}, {"scale", s.Scale}, {"richText", richOf(s)}, {"textLastEditedBy", lastEdited},
		}
	case TypeFrame:
		return obj{{"w", s.W}, {"h", s.H}, {"name", s.Name}, {"color", s.Color}}
	case TypeImage:
		var asset any
		if s.AssetID != "" {
			asset = s.AssetID
		}
		var crop any
		if s.Crop != nil {
			c := obj{{"topLeft", vec(s.Crop.TopLeft)}, {"bottomRight", vec(s.Crop.BottomRight)}}
			if s.Crop.IsCircle {
				c = append(c, kv{"isCircle", true})
			}
			crop = c
		}
		return obj{
			{"w", s.W}, {"h", s.H}, {"playing", s.Playing}, {"url", s.URL}, {"assetId", asset}, {"crop", crop},
			{"flipX", s.FlipX}, {"flipY", s.FlipY}, {"altText", s.AltText},
		}
	case TypeGroup:
		return obj{}
	}
	// A type this app does not know: as it came, but for its size if a
	// resize changed it.
	out := map[string]any{}
	for k, v := range s.Raw {
		out[k] = v
	}
	if _, ok := s.Raw["w"]; ok {
		out["w"] = s.W
	}
	if _, ok := s.Raw["h"]; ok {
		out["h"] = s.H
	}
	return out
}

func vec(v geo.Vec) obj { return obj{{"x", v.X}, {"y", v.Y}} }

func segmentsOf(s *Shape) []obj {
	out := make([]obj, 0, len(s.Segments))
	for _, seg := range s.Segments {
		dim := seg.Dim
		if dim == 0 {
			dim = 2
			if s.IsPen {
				dim = 3
			}
		}
		out = append(out, obj{{"type", seg.Type}, {"path", encodePoints(seg.Points, dim)}, {"dim", dim}})
	}
	return out
}

func richOf(s *Shape) any {
	if s.RichText != nil && s.richOf == s.Text {
		return s.RichText
	}
	return PlainToRich(s.Text)
}

// PlainToRich returns plain text as a rich text document, a paragraph a line.
func PlainToRich(text string) any {
	content := []any{}
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			content = append(content, obj{{"type", "paragraph"}})
			continue
		}
		content = append(content, obj{
			{"type", "paragraph"},
			{"content", []any{obj{{"type", "text"}, {"text", line}}}},
		})
	}
	return obj{{"type", "doc"}, {"content", content}}
}

type richNode struct {
	Type    string     `json:"type"`
	Text    string     `json:"text"`
	Content []richNode `json:"content"`
}

// RichToPlain returns the text of a rich text document, a line a paragraph.
func RichToPlain(raw []byte) string {
	var n richNode
	if json.Unmarshal(raw, &n) != nil {
		return ""
	}
	var lines []string
	var walk func(n richNode, prefix string)
	inline := func(n richNode) string {
		var b strings.Builder
		var rec func(n richNode)
		rec = func(n richNode) {
			if n.Type == "hardBreak" {
				b.WriteByte('\n')
			}
			b.WriteString(n.Text)
			for _, c := range n.Content {
				rec(c)
			}
		}
		rec(n)
		return b.String()
	}
	walk = func(n richNode, prefix string) {
		switch n.Type {
		case "paragraph", "heading":
			lines = append(lines, prefix+inline(n))
			return
		case "text":
			lines = append(lines, prefix+n.Text)
			return
		case "bulletList":
			for _, c := range n.Content {
				walk(c, "• ")
			}
			return
		case "orderedList":
			for i, c := range n.Content {
				walk(c, fmt.Sprintf("%d. ", i+1))
			}
			return
		}
		for _, c := range n.Content {
			walk(c, prefix)
		}
	}
	walk(n, "")
	return strings.Join(lines, "\n")
}

func readBinding(raw []byte) (*Binding, error) {
	var r struct {
		ID     ID              `json:"id"`
		Type   string          `json:"type"`
		FromID ID              `json:"fromId"`
		ToID   ID              `json:"toId"`
		Meta   json.RawMessage `json:"meta"`
		Props  map[string]json.RawMessage
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	b := &Binding{ID: r.ID, Type: r.Type, From: r.FromID, To: r.ToID, Meta: r.Meta, Raw: r.Props}
	p := props(r.Props)
	b.Terminal = p.str("terminal", "end")
	b.IsExact = p.boolean("isExact", false)
	b.IsPrecise = p.boolean("isPrecise", false)
	b.Snap = p.str("snap", "")
	var v struct{ X, Y float64 }
	if a, ok := p["normalizedAnchor"]; ok && json.Unmarshal(a, &v) == nil {
		b.Anchor = geo.V(v.X, v.Y)
	}
	return b, nil
}

func bindingRecord(b *Binding) obj {
	p := obj{
		{"terminal", b.Terminal},
		{"normalizedAnchor", vec(b.Anchor)},
		{"isExact", b.IsExact},
		{"isPrecise", b.IsPrecise},
		{"snap", orStr(b.Snap, "none")}, // tldraw's schema requires it
	}
	return obj{
		{"meta", metaOf(b.Meta)},
		{"id", b.ID},
		{"type", orStr(b.Type, "arrow")},
		{"fromId", b.From},
		{"toId", b.To},
		{"props", p},
		{"typeName", "binding"},
	}
}

func orStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func readAsset(raw []byte) (*Asset, error) {
	var r struct {
		ID    ID     `json:"id"`
		Type  string `json:"type"`
		Props map[string]json.RawMessage
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	var whole map[string]json.RawMessage
	if err := json.Unmarshal(raw, &whole); err != nil {
		return nil, err
	}
	p := props(r.Props)
	return &Asset{
		ID: r.ID, Type: r.Type,
		W: p.num("w", 0), H: p.num("h", 0),
		Name: p.str("name", ""), Mime: p.str("mimeType", ""), Src: p.str("src", ""),
		Raw: whole,
	}, nil
}

func assetRecord(a *Asset) any {
	if a.Raw != nil {
		return a.Raw
	}
	return obj{
		{"id", a.ID},
		{"typeName", "asset"},
		{"type", orStr(a.Type, "image")},
		{"props", obj{
			{"w", a.W}, {"h", a.H}, {"name", a.Name}, {"isAnimated", false},
			{"mimeType", a.Mime}, {"src", a.Src},
		}},
		{"meta", json.RawMessage("{}")},
	}
}
