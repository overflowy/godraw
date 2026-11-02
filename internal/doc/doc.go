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
	// a clone starts without it. It belongs to the main thread.
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
// is the same text.
func (s *Shape) SetText(t string) {
	s.Text = t
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
