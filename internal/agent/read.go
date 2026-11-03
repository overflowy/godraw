package agent

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"godraw/internal/doc"
	"godraw/internal/editor"
	"godraw/internal/geo"
)

// readArgs is what read asks for.
type readArgs struct {
	Doc    string `json:"doc"`
	Page   string `json:"page"`
	IDs    names  `json:"ids"`
	Find   string `json:"find"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
	Check  bool   `json:"check"`
}

const (
	defaultLimit = 50
	maxLimit     = 500
	maxTextShown = 60
)

// onPage runs fn with a page current, and goes back, all before a frame could
// show it.
func onPage(e *editor.Editor, name string, fn func() (string, error)) (string, error) {
	if name == "" {
		return fn()
	}
	p := findPage(e, name)
	if p == nil {
		return "", fmt.Errorf("no page %q", name)
	}
	was := e.Page
	e.Page = p.ID
	defer func() { e.Page = was }()
	return fn()
}

// read describes the canvas: the whole of it, or in full what ids name.
func read(e *editor.Editor, a readArgs, title, otherDocs string) (string, error) {
	return onPage(e, a.Page, func() (string, error) {
		if a.Check {
			if len(a.IDs) > 0 || a.Find != "" {
				return "", errors.New("check looks at the whole page, so it takes neither ids nor find")
			}
			return checkReport(e, a), nil
		}
		if len(a.IDs) > 0 {
			return details(e, a.IDs)
		}
		return overview(e, a, title, otherDocs), nil
	})
}

// checkReport lists the warnings of the whole page.
func checkReport(e *editor.Editor, a readArgs) string {
	list, capped := checkWholePage(e)
	name := e.Doc.Page(e.Page).Name
	if len(list) == 0 {
		return fmt.Sprintf("no warnings on page %q", name)
	}
	first, end, more := window(a, len(list))
	if first >= len(list) {
		return fmt.Sprintf("warnings: %d", len(list))
	}
	var b strings.Builder
	total := strconv.Itoa(len(list))
	if capped {
		total = "at least " + total
	}
	fmt.Fprintf(&b, "warnings %d-%d of %s on page %q:\n", first+1, end, total, name)
	for _, w := range list[first:end] {
		b.WriteString(w)
		b.WriteByte('\n')
	}
	b.WriteString(more)
	return strings.TrimRight(b.String(), "\n")
}

// window works out which of n lines a read shows, and how to ask for the rest.
func window(a readArgs, n int) (first, end int, more string) {
	limit := a.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	limit = min(limit, maxLimit)
	first = min(max(a.Offset, 0), n)
	end = min(first+limit, n)
	if end < n {
		more = fmt.Sprintf("more: offset %d\n", end)
	}
	return first, end, more
}

func overview(e *editor.Editor, a readArgs, title, otherDocs string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "doc %q%s\n", title, otherDocs)
	b.WriteString("pages:")
	for _, p := range e.Pages() {
		mark := ""
		if p.ID == e.Page {
			mark = "*"
		}
		fmt.Fprintf(&b, " %q%s", p.Name, mark)
	}
	b.WriteByte('\n')
	view := e.ViewBox()
	fmt.Fprintf(&b, "view %s zoom %s theme %s\n", boxText(view), num(e.Cam().Z), themeName(e))
	if box, ok := e.ContentBounds(); ok {
		fmt.Fprintf(&b, "bounds %s\n", boxText(box))
	}
	if sel := e.Selection(); len(sel) > 0 {
		short := make([]string, len(sel))
		for i, id := range sel {
			short[i] = shortID(id)
		}
		fmt.Fprintf(&b, "selected %s\n", strings.Join(short, " "))
	}

	// What it lists: the page's top shapes, or whatever matches at any depth.
	var list []doc.ID
	if a.Find != "" {
		q := strings.ToLower(a.Find)
		for _, id := range e.Descendants(e.Page) {
			if matches(e.Doc.Shapes[id], q) || (q == cardKind && cardOf(e, id) != nil) {
				list = append(list, id)
			}
		}
	} else {
		list = e.Children(e.Page)
	}
	offset, end, more := window(a, len(list))
	if offset >= len(list) {
		fmt.Fprintf(&b, "shapes: %d", len(list))
		return b.String()
	}
	if len(list) > 0 {
		fmt.Fprintf(&b, "shapes %d-%d of %d, bottom first:\n", offset+1, end, len(list))
	}
	for _, id := range list[offset:end] {
		b.WriteString(line(e, id))
		b.WriteByte('\n')
	}
	b.WriteString(more)
	return strings.TrimRight(b.String(), "\n")
}

func matches(s *doc.Shape, q string) bool {
	return strings.Contains(strings.ToLower(s.Text), q) || strings.Contains(strings.ToLower(s.Name), q) ||
		s.Type == q || s.Geo == q
}

// line is a shape in a line: its id, what it is, where, and only what differs
// from the defaults.
func line(e *editor.Editor, id doc.ID) string {
	s := e.Doc.Shapes[id]
	var b strings.Builder
	b.WriteString(shortID(id))
	b.WriteByte(' ')
	card := cardOf(e, id)
	if card != nil {
		b.WriteString(cardKind)
	} else {
		b.WriteString(kindName(s))
	}
	b.WriteByte(' ')
	b.WriteString(boxText(boxOf(e, id)))
	if s.Type != doc.TypeArrow {
		if deg := e.PageMat(id).Rotation() * 180 / math.Pi; math.Abs(deg) > 0.01 {
			fmt.Fprintf(&b, " rot %s", num(deg))
		}
	}
	if s.Type == doc.TypeArrow {
		if t := e.BindingOf(id, "start"); t != nil {
			fmt.Fprintf(&b, " from %s", shortID(ownerOf(e, t.To)))
		}
		if t := e.BindingOf(id, "end"); t != nil {
			fmt.Fprintf(&b, " to %s", shortID(ownerOf(e, t.To)))
		}
	}
	if s.Type != doc.TypeFrame && s.Type != doc.TypeGroup && s.Type != doc.TypeImage {
		for _, v := range []struct{ val, def string }{{s.Color, "black"}, {fillName(s), "none"}, {dashName(s), "draw"}, {s.Size, "m"}} {
			if v.val != "" && v.val != v.def {
				b.WriteByte(' ')
				b.WriteString(v.val)
			}
		}
	}
	if s.Type == doc.TypeFrame && s.Name != "" {
		fmt.Fprintf(&b, " %q", clip(s.Name))
	}
	if s.Text != "" {
		fmt.Fprintf(&b, " %q", clip(s.Text))
	}
	if card != nil && card.head != nil {
		fmt.Fprintf(&b, " %q", clip(card.head.Text))
	}
	if n := len(e.Children(id)); n > 0 {
		fmt.Fprintf(&b, " holds %d", n)
	}
	if s.Locked {
		b.WriteString(" locked")
	}
	return b.String()
}

func fillName(s *doc.Shape) string {
	if g := s.Gradient; g != nil {
		return "gradient " + strings.Join(g.Colors, ",") + " angle " + num(g.Angle)
	}
	if editor.IsHex(s.Fill) {
		return "fill " + s.Fill
	}
	return s.Fill
}

func dashName(s *doc.Shape) string {
	if s.NoOutline() {
		return "no-outline"
	}
	return s.Dash
}

func kindName(s *doc.Shape) string {
	if s.Type == doc.TypeGeo {
		return s.Geo
	}
	return s.Type
}

func clip(s string) string {
	r := []rune(s)
	if len(r) > maxTextShown {
		return string(r[:maxTextShown]) + "…"
	}
	return s
}

// details says all there is of the shapes named, with what they hold.
func details(e *editor.Editor, list names) (string, error) {
	list, err := spread(e, list)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, name := range list {
		id := shapeID(name)
		s := e.Doc.Shapes[id]
		if s == nil {
			return "", fmt.Errorf("no shape %q", name)
		}
		b.WriteString(line(e, id))
		b.WriteByte('\n')
		kv := func(k, v string) {
			if v != "" {
				fmt.Fprintf(&b, "  %s %s\n", k, v)
			}
		}
		if !doc.IsPageID(s.Parent) {
			kv("in", shortID(s.Parent))
		}
		if s.Text != "" {
			kv("text", strconv.Quote(s.Text))
		}
		switch s.Type {
		case doc.TypeGeo, doc.TypeNote:
			kv("font", s.Font)
			kv("align", s.Align)
			kv("valign", s.VAlign)
			kv("labelColor", s.LabelColor)
			if s.Padding != nil {
				kv("padding", num(*s.Padding))
			}
		case doc.TypeText:
			kv("font", s.Font)
			kv("align", s.TextAlign)
			if !s.AutoSize {
				kv("wrap", num(s.W*s.Scale))
			}
		case doc.TypeArrow:
			kv("kind", s.Kind)
			kv("head", s.HeadEnd)
			kv("tail", s.HeadStart)
			if s.Kind == "elbow" {
				m := e.PageMat(id)
				var via []string
				for _, p := range s.Via {
					p = m.Apply(p)
					via = append(via, "["+num(p.X)+","+num(p.Y)+"]")
				}
				if via != nil {
					kv("via", "["+strings.Join(via, ",")+"]")
				}
			} else if s.Bend != 0 {
				kv("bend", num(s.Bend))
			}
			kv("font", s.Font)
			kv("label", s.LabelSide)
			if s.Straight != "" {
				kv("straight", s.Straight)
				if s.StraightAt != nil {
					kv("at", ratio(*s.StraightAt))
				}
			}
		case doc.TypeFrame:
			kv("size", s.FrameSize)
			kv("font", s.FrameFont)
		case doc.TypeLine:
			kv("spline", s.Spline)
			kv("vertices", strconv.Itoa(len(s.Points)))
		case doc.TypeDraw, doc.TypeHighlight:
			n := 0
			for _, seg := range s.Segments {
				n += len(seg.Points)
			}
			kv("points", strconv.Itoa(n))
			if s.IsClosed {
				kv("closed", "true")
			}
		case doc.TypeImage:
			if a := e.Doc.Assets[s.AssetID]; a != nil {
				kv("asset", fmt.Sprintf("%s %sx%s", a.Name, num(a.W), num(a.H)))
			}
			kv("alt", s.AltText)
		}
		kv("url", s.URL)
		if s.Scale != 1 {
			kv("scale", ratio(s.Scale))
		}
		if s.Opacity != 1 {
			kv("opacity", ratio(s.Opacity))
		}
		if v := s.Radius; v > 0 {
			kv("radius", num(v))
		}
		if sh := s.Shadow; sh != nil {
			kv("shadow", shadowText(sh))
		}
		if n := len(s.Runs); n > 0 {
			kv("runs", strconv.Itoa(n))
		}
		if kids := e.Children(id); len(kids) > 0 {
			b.WriteString("  holds:\n")
			for _, k := range kids {
				b.WriteString("  ")
				b.WriteString(line(e, k))
				b.WriteByte('\n')
			}
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// boxOf returns the box a shape's x, y, w and h describe: before it turns, the
// same size, around the same center. Set them back and it stays where it is.
func boxOf(e *editor.Editor, id doc.ID) geo.Box {
	s := e.Doc.Shapes[id]
	if s.Type == doc.TypeArrow {
		return e.PageBounds(id)
	}
	b := e.Geometry(s).Bounds
	c := e.PageMat(id).Apply(b.Center())
	return geo.Box{X: c.X - b.W/2, Y: c.Y - b.H/2, W: b.W, H: b.H}
}

// boxText is a box as "x,y wxh".
func boxText(b geo.Box) string {
	return num(b.X) + "," + num(b.Y) + " " + num(b.W) + "x" + num(b.H)
}

// num is a number at a precision worth its tokens.
func num(v float64) string {
	return strconv.FormatFloat(math.Round(v*10)/10+0, 'f', -1, 64)
}

// ratio is a number that's a multiple or a share of something, where a tenth is far
// from enough: a scale of 0.85 isn't one of 0.9.
func ratio(v float64) string {
	return strconv.FormatFloat(math.Round(v*1000)/1000+0, 'f', -1, 64)
}

// shadowText is a shadow as an op says it.
func shadowText(sh *doc.Shadow) string {
	out := "blur " + num(sh.Blur)
	if sh.X != 0 || sh.Y != 0 {
		out += " x " + num(sh.X) + " y " + num(sh.Y)
	}
	if sh.Color != "" {
		out += " " + sh.Color
	}
	if sh.Opacity != 0 {
		out += " opacity " + ratio(sh.Opacity)
	}
	return out
}

// themeName says how the canvas looks now.
func themeName(e *editor.Editor) string {
	if e.DarkMode {
		return "dark"
	}
	return "light"
}

// viewArgs is what view asks for.
type viewArgs struct {
	Doc   string    `json:"doc"`
	Page  string    `json:"page"`
	IDs   names     `json:"ids"`
	Box   []float64 `json:"box"`
	Px    int       `json:"px"`
	Theme string    `json:"theme"`
}

const (
	defaultPx = 768
	minPx     = 64
	maxPx     = 2048
)

// viewPlan works out what view renders: shapes, the box around them, and the
// scale that fits it in the pixels asked for. Small drawings aren't blown up
// beyond what stays sharp.
func viewPlan(e *editor.Editor, a viewArgs) (geo.Box, []doc.ID, float64, error) {
	var ids []doc.ID
	if len(a.IDs) > 0 {
		list, err := spread(e, a.IDs)
		if err != nil {
			return geo.Box{}, nil, 0, err
		}
		for _, name := range list {
			id := shapeID(name)
			if e.Doc.Shapes[id] == nil {
				return geo.Box{}, nil, 0, fmt.Errorf("no shape %q", name)
			}
			ids = append(ids, id)
		}
		slices.SortStableFunc(ids, func(x, y doc.ID) int {
			return strings.Compare(e.Doc.Shapes[x].Index, e.Doc.Shapes[y].Index)
		})
	} else {
		ids = e.Children(e.Page)
	}
	var box geo.Box
	switch {
	case len(a.Box) == 4:
		box = geo.Box{X: a.Box[0], Y: a.Box[1], W: a.Box[2], H: a.Box[3]}
		if box.W <= 0 || box.H <= 0 {
			return geo.Box{}, nil, 0, errors.New("box needs a positive w and h")
		}
	case len(a.Box) != 0:
		return geo.Box{}, nil, 0, errors.New("box is [x,y,w,h]")
	case len(ids) == 0:
		return geo.Box{}, nil, 0, errors.New("the page is empty; pass box to see it anyway")
	default:
		for i, id := range ids {
			if i == 0 {
				box = e.VisualBounds(id)
			} else {
				box = box.Union(e.VisualBounds(id))
			}
		}
		box = box.Expand(16)
	}
	px := a.Px
	if px == 0 {
		px = defaultPx
	}
	px = max(min(px, maxPx), minPx)
	scale := math.Min(float64(px)/math.Max(box.W, box.H), 2)
	// Headings keep their size in a picture of the whole, so need more of it.
	if len(a.Box) == 0 && scale < 1 {
		top := box.Y
		for _, id := range ids {
			for _, k := range append(e.Descendants(id), id) {
				if s := e.Doc.Shapes[k]; s.Type == doc.TypeFrame {
					top = math.Min(top, e.FrameHeadingBox(s, 1/scale).Y-8/scale)
				}
			}
		}
		box.H += box.Y - top
		box.Y = top
		scale = math.Min(float64(px)/math.Max(box.W, box.H), 2)
	}
	return box, ids, scale, nil
}
