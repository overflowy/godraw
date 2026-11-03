// Package doc is the document: its records, as tldraw has them, and the
// .tldr file they are read from and written to.
//
// Committed records are immutable: an edit clones a record and replaces it.
// A snapshot of a drawing is then a copy of its maps, cheap enough to take
// on each edit, and history keeps the records themselves.
package doc

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"godraw/internal/geo"
)

// ID names a record, with its kind first: "shape:abc", "page:page".
type ID string

// Types of shapes.
const (
	TypeGeo       = "geo"
	TypeDraw      = "draw"
	TypeHighlight = "highlight"
	TypeArrow     = "arrow"
	TypeLine      = "line"
	TypeText      = "text"
	TypeNote      = "note"
	TypeFrame     = "frame"
	TypeImage     = "image"
	TypeGroup     = "group"
)

// PointZ is a point of a freehand stroke, with its pressure.
type PointZ struct{ X, Y, Z float64 }

// Segment is a run of points of a freehand stroke: type "free" or
// "straight", from drawing with Shift.
type Segment struct {
	Type   string
	Points []PointZ
	Dim    int // 2 when the points carry no pressure
}

// LinePoint is a vertex of a line shape.
type LinePoint struct {
	ID, Index string
	X, Y      float64
}

// Crop is the part of an image a shape shows, as fractions of it.
type Crop struct {
	TopLeft, BottomRight geo.Vec
	IsCircle             bool
}

// Shape is a shape on the canvas: a flat union of the props of every type,
// of which each type reads its own. Props this app does not model stay in
// Raw and go back to the file, and shapes of types it does not know stay
// whole, as tldraw's embeds and bookmarks.
type Shape struct {
	ID       ID
	Type     string
	Parent   ID
	Index    string
	X, Y     float64
	Rotation float64
	Opacity  float64
	Locked   bool
	Meta     json.RawMessage

	W, H, GrowY float64
	Scale       float64

	Geo        string
	Color      string
	LabelColor string
	Fill       string
	Dash       string
	Size       string
	Font       string
	Align      string // horizontal alignment of labels
	VAlign     string
	TextAlign  string // of text shapes
	AutoSize   bool
	URL        string
	Name       string
	FlipX      bool
	FlipY      bool

	// Text is the shape's text. RichText, when not nil, is the rich text
	// document it came from, and goes back to the file while Text is
	// still what it held (richOf).
	Text     string
	RichText json.RawMessage
	richOf   string

	Segments       []Segment
	IsComplete     bool
	IsClosed       bool
	IsPen          bool
	ScaleX, ScaleY float64

	Kind           string // arc or elbow
	HeadStart      string
	HeadEnd        string
	Start, End     geo.Vec
	Bend           float64
	LabelPosition  float64
	ElbowMidPoint  float64
	FontAdjustment *float64

	Spline string
	Points []LinePoint

	AssetID ID
	Crop    *Crop
	AltText string
	Playing bool

	Raw map[string]json.RawMessage

	// Cache holds what the editor derives from the shape, as its geometry;
	// a clone starts without it. It belongs to the main thread, unlike the rest
	// of a record, which snapshots read elsewhere; the editor empties it once the
	// record has left the document.
	Cache any
}

// Clone returns a copy to edit. Slices are shared: replace them, rather
// than writing into them.
func (s *Shape) Clone() *Shape {
	c := *s
	c.Cache = nil
	return &c
}

// Moved returns a copy to place somewhere else: what is derived from the
// shape's own form stays valid, whatever its position, turn, parent or order.
func (s *Shape) Moved() *Shape {
	c := *s
	return &c
}

// SetText sets the text, leaving RichText to be rebuilt from it unless it
// is the same text. Other text has no use for the runs of the last.
func (s *Shape) SetText(t string) {
	if t != s.Text {
		s.SetMeta(MetaRuns, nil)
	}
	s.Text = t
}

// Keys of a shape's meta, for what this app adds to an arrow that tldraw has no
// prop for. tldraw keeps them, and shows the arrow without them.
const (
	MetaStraight   = "godrawStraight"   // h or v
	MetaStraightAt = "godrawStraightAt" // 0 to 1 across what both ends share
	MetaLabel      = "godrawLabel"      // above
	MetaCard       = "godrawCard"       // box, head or body: a card's part
	MetaCardH      = "godrawCardH"      // the height asked of a card's box
	MetaVia        = "godrawVia"        // what an elbow arrow passes through
	MetaViaAuto    = "godrawViaAuto"    // that the app chose it, to go around something
	MetaFrameSize  = "godrawFrameSize"  // a frame's size style, for its heading and border
	MetaFrameFont  = "godrawFrameFont"  // the font of a frame's heading

	// What a file keeps for this app alone, as tldraw's validators take only its own
	// colors and dashes. There, a shape shows as its props say, without these.
	MetaColor      = "godrawColor"      // #rrggbb, in place of color
	MetaLabelColor = "godrawLabelColor" // #rrggbb, in place of labelColor
	MetaNoOutline  = "godrawNoOutline"  // that a shape draws no outline
	MetaGradient   = "godrawGradient"   // what a shape fills with, in place of fill
	MetaPadding    = "godrawPadding"    // how far a label sits in from its box
	MetaFill       = "godrawFill"       // #rrggbb, in place of fill
	MetaRadius     = "godrawRadius"     // how round a rectangle's corners are
	MetaShadow     = "godrawShadow"     // what a shape casts or glows with
	MetaRuns       = "godrawRuns"       // the parts of a text with styles of their own
)

// MetaValue reads a key of the shape's meta into v, and says whether it could.
func (s *Shape) MetaValue(key string, v any) bool {
	if len(s.Meta) <= 2 {
		return false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(s.Meta, &m) != nil {
		return false
	}
	raw, ok := m[key]
	return ok && json.Unmarshal(raw, v) == nil
}

// SetMeta sets a key of the shape's meta, or removes it if v is nil. The rest
// of the meta stays.
func (s *Shape) SetMeta(key string, v any) {
	m := map[string]json.RawMessage{}
	if len(s.Meta) > 0 && json.Unmarshal(s.Meta, &m) != nil {
		return
	}
	if v == nil {
		if _, ok := m[key]; !ok {
			return
		}
		delete(m, key)
	} else {
		raw, err := json.Marshal(v)
		if err != nil {
			return
		}
		m[key] = raw
	}
	if b, err := json.Marshal(m); err == nil {
		s.Meta = b
	}
}

// Gradient is a fill that blends colors, evenly spaced along a line. Angle is that
// line's direction, in degrees as CSS has them: 180 runs top to bottom, 90 left to
// right.
type Gradient struct {
	Colors []string `json:"colors"`
	Angle  float64  `json:"angle"`
}

// Gradient returns what a shape fills with instead of its fill, if anything.
func (s *Shape) Gradient() *Gradient {
	var g Gradient
	if !s.MetaValue(MetaGradient, &g) || len(g.Colors) < 2 {
		return nil
	}
	return &g
}

// NoOutline says that a shape draws no outline, whatever its dash.
func (s *Shape) NoOutline() bool {
	var none bool
	return s.MetaValue(MetaNoOutline, &none) && none
}

// Radius returns how round a rectangle's corners were asked to be, or 0.
func (s *Shape) Radius() float64 {
	var r float64
	s.MetaValue(MetaRadius, &r)
	return r
}

// Shadow is what a shape casts, or with no offset and a bright color, glows with.
type Shadow struct {
	X       float64 `json:"x,omitempty"`
	Y       float64 `json:"y,omitempty"`
	Blur    float64 `json:"blur,omitempty"`
	Color   string  `json:"color,omitempty"`
	Opacity float64 `json:"opacity,omitempty"`
}

// Shadow returns what a shape casts, if anything.
func (s *Shape) Shadow() *Shadow {
	var sh Shadow
	if !s.MetaValue(MetaShadow, &sh) {
		return nil
	}
	return &sh
}

// Run is a part of a text with a style of its own. What it leaves empty is the
// shape's. Scale multiplies the shape's font size.
type Run struct {
	Text  string  `json:"text"`
	Font  string  `json:"font,omitempty"`
	Color string  `json:"color,omitempty"`
	Scale float64 `json:"scale,omitempty"`
	Sup   bool    `json:"sup,omitempty"`
	Sub   bool    `json:"sub,omitempty"`
}

// Runs returns the parts of a shape's text, if it has any and they still add up
// to it: tldraw keeps the meta of a text it edits.
func (s *Shape) Runs() []Run {
	var runs []Run
	if !s.MetaValue(MetaRuns, &runs) {
		return nil
	}
	n := 0
	for _, r := range runs {
		if !strings.HasPrefix(s.Text[n:], r.Text) {
			return nil
		}
		n += len(r.Text)
	}
	if n != len(s.Text) {
		return nil
	}
	return runs
}

// SetRuns gives a shape a text in parts.
func (s *Shape) SetRuns(runs []Run) {
	var b strings.Builder
	for _, r := range runs {
		b.WriteString(r.Text)
	}
	s.Text = b.String()
	s.SetMeta(MetaRuns, runs)
}

// Via returns the points an elbow arrow passes through, in its own space.
func (s *Shape) Via() []geo.Vec {
	var pts [][2]float64
	if !s.MetaValue(MetaVia, &pts) {
		return nil
	}
	out := make([]geo.Vec, len(pts))
	for i, p := range pts {
		out[i] = geo.V(p[0], p[1])
	}
	return out
}

// SetVia sets the points an elbow arrow passes through, in its own space.
func (s *Shape) SetVia(via []geo.Vec) {
	s.SetMeta(MetaViaAuto, nil)
	if len(via) == 0 {
		s.SetMeta(MetaVia, nil)
		return
	}
	pts := make([][2]float64, len(via))
	for i, p := range via {
		pts[i] = [2]float64{p.X, p.Y}
	}
	s.SetMeta(MetaVia, pts)
}

// Binding ties an end of an arrow to a shape.
type Binding struct {
	ID        ID
	Type      string
	From, To  ID
	Terminal  string // start or end
	Anchor    geo.Vec
	IsExact   bool
	IsPrecise bool
	Snap      string
	Meta      json.RawMessage
	Raw       map[string]json.RawMessage
}

// Page is a page of the drawing.
type Page struct {
	ID    ID
	Name  string
	Index string
	Meta  json.RawMessage
}

// Asset is a file a shape shows, kept in the drawing as a data URL.
type Asset struct {
	ID   ID
	Type string
	W, H float64
	Name string
	Mime string
	Src  string
	// Raw is the record from the file, kept for what this app does not
	// model.
	Raw map[string]json.RawMessage
}

// Drawing is a whole document. The records in its maps are immutable, and
// only the editor changes the maps.
type Drawing struct {
	Name     string
	GridSize float64
	Meta     json.RawMessage
	Pages    []*Page
	Shapes   map[ID]*Shape
	Bindings map[ID]*Binding
	Assets   map[ID]*Asset
	// Others are records of types this app leaves alone.
	Others []json.RawMessage
}

// New returns a drawing of one empty page.
func New() *Drawing {
	return &Drawing{
		GridSize: 10,
		Pages:    []*Page{{ID: "page:page", Name: "Page 1", Index: FirstIndex}},
		Shapes:   map[ID]*Shape{},
		Bindings: map[ID]*Binding{},
		Assets:   map[ID]*Asset{},
	}
}

// Snapshot returns a copy that later edits of d do not change.
func (d *Drawing) Snapshot() *Drawing {
	c := *d
	c.Pages = slices.Clone(d.Pages)
	c.Shapes = maps.Clone(d.Shapes)
	c.Bindings = maps.Clone(d.Bindings)
	c.Assets = maps.Clone(d.Assets)
	return &c
}

// Equal reports whether two snapshots hold the same records, so that undoing
// back to what the file has leaves the document unedited.
func Equal(a, b *Drawing) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil || a.Name != b.Name || !slices.Equal(a.Pages, b.Pages) {
		return false
	}
	return maps.Equal(a.Shapes, b.Shapes) && maps.Equal(a.Bindings, b.Bindings) && maps.Equal(a.Assets, b.Assets)
}

// Page returns the page with an id, or nil.
func (d *Drawing) Page(id ID) *Page {
	for _, p := range d.Pages {
		if p.ID == id {
			return p
		}
	}
	return nil
}

// IsPageID reports whether id names a page.
func IsPageID(id ID) bool { return len(id) > 5 && id[:5] == "page:" }

// NewShape returns a shape of a type with tldraw's default props.
func NewShape(id ID, typ string) *Shape {
	s := &Shape{
		ID: id, Type: typ, Opacity: 1, Scale: 1, Index: FirstIndex,
		Color: "black", LabelColor: "black", Fill: "none", Dash: "draw", Size: "m", Font: "draw",
		Align: "middle", VAlign: "middle",
	}
	switch typ {
	case TypeGeo:
		s.Geo, s.W, s.H = "rectangle", 100, 100
	case TypeDraw, TypeHighlight:
		s.ScaleX, s.ScaleY = 1, 1
	case TypeArrow:
		s.Kind, s.HeadEnd, s.HeadStart = "arc", "arrow", "none"
		s.End = geo.V(2, 0)
		s.LabelPosition, s.ElbowMidPoint = 0.5, 0.5
	case TypeLine:
		s.Spline = "line"
		s.Points = []LinePoint{{ID: "a1", Index: "a1"}, {ID: "a2", Index: "a2", X: 0.1, Y: 0.1}}
	case TypeText:
		s.TextAlign, s.W, s.AutoSize = "start", 8, true
	case TypeNote:
		s.Align, s.VAlign = "middle", "middle"
		s.Color = "black"
	case TypeFrame:
		s.W, s.H = 160, 90
	case TypeImage:
		s.W, s.H = 100, 100
	}
	return s
}
