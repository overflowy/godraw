// Package doc is the document: its records, and the .godraw file they are read
// from and written to.
//
// Committed records are immutable: an edit clones a record and replaces it.
// A snapshot of a drawing is then a copy of its maps, cheap enough to take
// on each edit, and history keeps the records themselves.
package doc

import (
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
// "straight", from drawing with Shift. A file holds its points encoded.
type Segment struct {
	Type   string
	Points []PointZ
	Dim    int // 2 when the points carry no pressure
}

// LinePoint is a vertex of a line shape.
type LinePoint struct {
	ID    string  `json:"id"`
	Index string  `json:"index"`
	X     float64 `json:"x,omitempty"`
	Y     float64 `json:"y,omitempty"`
}

// Crop is the part of an image a shape shows, as fractions of it.
type Crop struct {
	TopLeft     geo.Vec `json:"topLeft,omitzero"`
	BottomRight geo.Vec `json:"bottomRight,omitzero"`
	IsCircle    bool    `json:"isCircle,omitempty"`
}

// Shape is a shape on the canvas: a flat union of the props of every type, of
// which each type reads its own. In a file, what is zero is left out.
type Shape struct {
	ID       ID      `json:"id"`
	Type     string  `json:"type"`
	Parent   ID      `json:"parent"`
	Index    string  `json:"index"`
	X        float64 `json:"x,omitempty"`
	Y        float64 `json:"y,omitempty"`
	Rotation float64 `json:"rotation,omitempty"`
	Opacity  float64 `json:"opacity,omitempty"`
	Locked   bool    `json:"locked,omitempty"`

	W     float64 `json:"w,omitempty"`
	H     float64 `json:"h,omitempty"`
	GrowY float64 `json:"growY,omitempty"`
	Scale float64 `json:"scale,omitempty"`

	Geo        string    `json:"geo,omitempty"`
	Color      string    `json:"color,omitempty"`      // a palette color or #rrggbb
	LabelColor string    `json:"labelColor,omitempty"` // a palette color or #rrggbb
	Fill       string    `json:"fill,omitempty"`       // a kind of fill or #rrggbb
	Gradient   *Gradient `json:"gradient,omitempty"`   // what it fills with, in place of fill
	Dash       string    `json:"dash,omitempty"`       // none draws no outline
	Size       string    `json:"size,omitempty"`
	Font       string    `json:"font,omitempty"`
	Align      string    `json:"align,omitempty"` // horizontal alignment of labels
	VAlign     string    `json:"valign,omitempty"`
	TextAlign  string    `json:"textAlign,omitempty"` // of text shapes
	Padding    *float64  `json:"padding,omitempty"`   // how far a label sits in from its box
	Radius     float64   `json:"radius,omitempty"`    // how round a rectangle's corners are
	Shadow     *Shadow   `json:"shadow,omitempty"`    // what it casts or glows with
	AutoSize   bool      `json:"autoSize,omitempty"`
	URL        string    `json:"url,omitempty"`
	Name       string    `json:"name,omitempty"`
	FlipX      bool      `json:"flipX,omitempty"`
	FlipY      bool      `json:"flipY,omitempty"`

	Text string `json:"text,omitempty"`
	Runs []Run  `json:"runs,omitempty"` // the parts of Text with styles of their own

	Segments []Segment `json:"segments,omitempty"`
	IsClosed bool      `json:"isClosed,omitempty"`
	IsPen    bool      `json:"isPen,omitempty"`
	ScaleX   float64   `json:"scaleX,omitempty"`
	ScaleY   float64   `json:"scaleY,omitempty"`

	Kind          string    `json:"kind,omitempty"` // arc or elbow
	HeadStart     string    `json:"headStart,omitempty"`
	HeadEnd       string    `json:"headEnd,omitempty"`
	Start         geo.Vec   `json:"start,omitzero"`
	End           geo.Vec   `json:"end,omitzero"`
	Bend          float64   `json:"bend,omitempty"`
	LabelPosition float64   `json:"labelPosition,omitempty"`
	LabelSide     string    `json:"labelSide,omitempty"` // above, or on the line
	ElbowMidPoint float64   `json:"elbowMidPoint,omitempty"`
	Via           []geo.Vec `json:"via,omitempty"`      // what an elbow passes through, in its own space
	ViaAuto       bool      `json:"viaAuto,omitempty"`  // that the app chose Via, to go around something
	Straight      string    `json:"straight,omitempty"` // h or v: that it runs level
	StraightAt    *float64  `json:"straightAt,omitempty"`

	Spline string      `json:"spline,omitempty"`
	Points []LinePoint `json:"points,omitempty"`

	AssetID ID     `json:"assetId,omitempty"`
	Crop    *Crop  `json:"crop,omitempty"`
	AltText string `json:"altText,omitempty"`

	Card  string  `json:"card,omitempty"`  // box, head or body: a card's part
	CardH float64 `json:"cardH,omitempty"` // the height asked of a card's box

	FrameSize string `json:"frameSize,omitempty"` // a frame's size style, for its heading and border
	FrameFont string `json:"frameFont,omitempty"` // the font of a frame's heading

	// Cache holds what the editor derives from the shape, as its geometry;
	// a clone starts without it. It belongs to the main thread, unlike the rest
	// of a record, which snapshots read elsewhere; the editor empties it once the
	// record has left the document.
	Cache any `json:"-"`
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

// SetText sets the text. Other text has no use for the runs of the last.
func (s *Shape) SetText(t string) {
	if t != s.Text {
		s.Runs = nil
	}
	s.Text = t
}

// Gradient is a fill that blends colors, evenly spaced along a line. Angle is that
// line's direction, in degrees as CSS has them: 180 runs top to bottom, 90 left to
// right.
type Gradient struct {
	Colors []string `json:"colors"`
	Angle  float64  `json:"angle"`
}

// NoOutline says that a shape draws no outline.
func (s *Shape) NoOutline() bool { return s.Dash == "none" }

// Shadow is what a shape casts, or with no offset and a bright color, glows with.
type Shadow struct {
	X       float64 `json:"x,omitempty"`
	Y       float64 `json:"y,omitempty"`
	Blur    float64 `json:"blur,omitempty"`
	Color   string  `json:"color,omitempty"`
	Opacity float64 `json:"opacity,omitempty"`
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

// SetRuns gives a shape a text in parts.
func (s *Shape) SetRuns(runs []Run) {
	var b strings.Builder
	for _, r := range runs {
		b.WriteString(r.Text)
	}
	s.Text = b.String()
	s.Runs = runs
}

// SetVia sets the points an elbow arrow passes through, in its own space, which
// are then its own and not the app's choice.
func (s *Shape) SetVia(via []geo.Vec) {
	s.ViaAuto = false
	s.Via = nil
	if len(via) > 0 {
		s.Via = via
	}
}

// Binding ties an end of an arrow to a shape.
type Binding struct {
	ID        ID      `json:"id"`
	From      ID      `json:"from"`
	To        ID      `json:"to"`
	Terminal  string  `json:"terminal"` // start or end
	Anchor    geo.Vec `json:"anchor,omitzero"`
	IsExact   bool    `json:"isExact,omitempty"`
	IsPrecise bool    `json:"isPrecise,omitempty"`
}

// Page is a page of the drawing.
type Page struct {
	ID    ID     `json:"id"`
	Name  string `json:"name"`
	Index string `json:"index"`
}

// Asset is an image a shape shows, kept in the drawing as a data URL.
type Asset struct {
	ID   ID      `json:"id"`
	W    float64 `json:"w,omitempty"`
	H    float64 `json:"h,omitempty"`
	Name string  `json:"name,omitempty"`
	Mime string  `json:"mime,omitempty"`
	Src  string  `json:"src,omitempty"`
}

// Drawing is a whole document. The records in its maps are immutable, and
// only the editor changes the maps.
type Drawing struct {
	Name     string
	GridSize float64
	Pages    []*Page
	Shapes   map[ID]*Shape
	Bindings map[ID]*Binding
	Assets   map[ID]*Asset
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

// NewShape returns a shape of a type with its default props.
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
