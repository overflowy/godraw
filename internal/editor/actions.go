package editor

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"

	"godraw/internal/doc"
	"godraw/internal/geo"
)

// --- zoom ---

// ZoomAt zooms by a factor around a point of the screen.
func (e *Editor) ZoomAt(screen geo.Vec, factor float64) {
	e.zoomTo(screen, e.Cam().Z*factor)
}

func (e *Editor) zoomTo(screen geo.Vec, z float64) {
	z = math.Max(minZoom, math.Min(maxZoom, z))
	c := e.Cam()
	// The page point under the pointer stays there.
	p := geo.V(screen.X/c.Z-c.X, screen.Y/c.Z-c.Y)
	e.SetCam(Camera{screen.X/z - p.X, screen.Y/z - p.Y, z})
}

func (e *Editor) center() geo.Vec { return geo.V(e.VW/2, e.VH/2) }

// ZoomIn goes to the next zoom stop.
func (e *Editor) ZoomIn() {
	z := e.Cam().Z
	for _, s := range zoomStops {
		if s > z*1.01 {
			e.zoomTo(e.center(), s)
			return
		}
	}
}

// ZoomOut goes to the previous zoom stop.
func (e *Editor) ZoomOut() {
	z := e.Cam().Z
	for i := len(zoomStops) - 1; i >= 0; i-- {
		if zoomStops[i] < z*0.99 {
			e.zoomTo(e.center(), zoomStops[i])
			return
		}
	}
}

// ResetZoom goes to 100%.
func (e *Editor) ResetZoom() { e.zoomTo(e.center(), 1) }

// Pan moves the camera by DIPs of the screen.
func (e *Editor) Pan(dx, dy float64) {
	c := e.Cam()
	e.SetCam(Camera{c.X - dx/c.Z, c.Y - dy/c.Z, c.Z})
}

// ContentBounds returns the box around what is on the page.
func (e *Editor) ContentBounds() (geo.Box, bool) {
	var box geo.Box
	ok := false
	for _, id := range e.Children(e.Page) {
		b := e.PageBounds(id)
		if !ok {
			box, ok = b, true
		} else {
			box = box.Union(b)
		}
	}
	return box, ok
}

// ZoomToBox fits a box of the page in the view.
func (e *Editor) ZoomToBox(box geo.Box, maxZ float64) {
	const inset = 80
	vw, vh := math.Max(e.VW-2*inset, 40), math.Max(e.VH-2*inset, 40)
	z := math.Min(vw/math.Max(box.W, 1), vh/math.Max(box.H, 1))
	z = math.Max(minZoom, math.Min(maxZ, z))
	c := box.Center()
	e.SetCam(Camera{e.VW/2/z - c.X, e.VH/2/z - c.Y, z})
}

// ZoomToFit fits what is on the page in the view.
func (e *Editor) ZoomToFit() {
	if box, ok := e.ContentBounds(); ok {
		e.ZoomToBox(box, 1)
		return
	}
	e.ResetZoom()
}

// FitNewPage gives the current page its first view, once the viewport's size
// is known: what it holds fits, so that a file opens showing its drawing, not
// wherever the origin happens to be.
func (e *Editor) FitNewPage() {
	if _, seen := e.cams[e.Page]; seen {
		return
	}
	e.ZoomToFit()
}

// ZoomToSelection fits the selection in the view.
func (e *Editor) ZoomToSelection() {
	if len(e.sel) == 0 {
		e.ZoomToFit()
		return
	}
	box, r := e.SelectionBox()
	e.ZoomToBox(geo.Rotate(r).BoundsOf(box), 2)
}

// --- styles ---

// StyleProp names a style.
type StyleProp string

// Styles.
const (
	StyleColor  StyleProp = "color"
	StyleFill   StyleProp = "fill"
	StyleDash   StyleProp = "dash"
	StyleSize   StyleProp = "size"
	StyleFont   StyleProp = "font"
	StyleAlign  StyleProp = "align"
	StyleVAlign StyleProp = "valign"
	StyleGeo    StyleProp = "geo"
	StyleKind   StyleProp = "kind"
	StyleHeadS  StyleProp = "headStart"
	StyleHeadE  StyleProp = "headEnd"
	StyleSpline StyleProp = "spline"
)

// Mixed is what SharedStyle returns for a style the selection differs on.
const Mixed = "\x00mixed"

func hasStyle(s *doc.Shape, p StyleProp) bool {
	switch s.Type {
	case doc.TypeGeo:
		return p == StyleColor || p == StyleFill || p == StyleDash || p == StyleSize || p == StyleFont || p == StyleAlign || p == StyleVAlign || p == StyleGeo
	case doc.TypeDraw:
		return p == StyleColor || p == StyleFill || p == StyleDash || p == StyleSize
	case doc.TypeHighlight:
		return p == StyleColor || p == StyleSize
	case doc.TypeArrow:
		return p == StyleColor || p == StyleFill || p == StyleDash || p == StyleSize || p == StyleFont || p == StyleKind || p == StyleHeadS || p == StyleHeadE
	case doc.TypeLine:
		return p == StyleColor || p == StyleDash || p == StyleSize || p == StyleSpline
	case doc.TypeText:
		return p == StyleColor || p == StyleSize || p == StyleFont || p == StyleAlign
	case doc.TypeNote:
		return p == StyleColor || p == StyleSize || p == StyleFont || p == StyleAlign || p == StyleVAlign
	}
	return false
}

func styleOf(s *doc.Shape, p StyleProp) string {
	switch p {
	case StyleColor:
		return s.Color
	case StyleFill:
		return s.Fill
	case StyleDash:
		return s.Dash
	case StyleSize:
		return s.Size
	case StyleFont:
		return s.Font
	case StyleAlign:
		if s.Type == doc.TypeText {
			return s.TextAlign
		}
		return s.Align
	case StyleVAlign:
		return s.VAlign
	case StyleGeo:
		return s.Geo
	case StyleKind:
		return s.Kind
	case StyleHeadS:
		return s.HeadStart
	case StyleHeadE:
		return s.HeadEnd
	case StyleSpline:
		return s.Spline
	}
	return ""
}

func setStyleOf(s *doc.Shape, p StyleProp, v string) {
	switch p {
	case StyleColor:
		s.Color = v
	case StyleFill:
		s.Fill = v
	case StyleDash:
		s.Dash = v
	case StyleSize:
		s.Size = v
	case StyleFont:
		s.Font = v
	case StyleAlign:
		if s.Type == doc.TypeText {
			s.TextAlign = v
		} else {
			s.Align = v
		}
	case StyleVAlign:
		s.VAlign = v
	case StyleGeo:
		s.Geo = v
	case StyleKind:
		s.Kind = v
	case StyleHeadS:
		s.HeadStart = v
	case StyleHeadE:
		s.HeadEnd = v
	case StyleSpline:
		s.Spline = v
	}
}

// nextStyleFor returns where a style of what comes next lives: the text tool's
// alignment is its own, as text aligns with other values than labels do.
func (e *Editor) nextStyleFor(p StyleProp) *string {
	if p == StyleAlign && e.Tool == ToolText {
		return &e.Next.TextAlign
	}
	return nextStyle(&e.Next, p)
}

func nextStyle(n *Style, p StyleProp) *string {
	switch p {
	case StyleColor:
		return &n.Color
	case StyleFill:
		return &n.Fill
	case StyleDash:
		return &n.Dash
	case StyleSize:
		return &n.Size
	case StyleFont:
		return &n.Font
	case StyleAlign:
		return &n.Align
	case StyleVAlign:
		return &n.VAlign
	case StyleGeo:
		return &n.Geo
	case StyleKind:
		return &n.Kind
	case StyleHeadS:
		return &n.HeadStart
	case StyleHeadE:
		return &n.HeadEnd
	case StyleSpline:
		return &n.Spline
	}
	return nil
}

// styleTargets returns the leaves of the selection, with what groups hold.
func (e *Editor) styleTargets() []*doc.Shape {
	var out []*doc.Shape
	seen := map[doc.ID]bool{}
	var add func(id doc.ID)
	add = func(id doc.ID) {
		if seen[id] {
			return
		}
		seen[id] = true
		s := e.Doc.Shapes[id]
		if s == nil {
			return
		}
		if s.Type == doc.TypeGroup {
			for _, k := range e.Children(id) {
				add(k)
			}
			return
		}
		out = append(out, s)
	}
	for _, id := range e.sel {
		add(id)
	}
	return out
}

// toolHasStyle says which styles a tool's new shapes take.
func toolHasStyle(t Tool, p StyleProp) bool {
	switch t {
	case ToolGeo:
		return p == StyleColor || p == StyleFill || p == StyleDash || p == StyleSize || p == StyleFont || p == StyleAlign || p == StyleVAlign
	case ToolDraw:
		return p == StyleColor || p == StyleFill || p == StyleDash || p == StyleSize
	case ToolHighlight:
		return p == StyleColor || p == StyleSize
	case ToolArrow:
		return p == StyleColor || p == StyleFill || p == StyleDash || p == StyleSize || p == StyleFont || p == StyleKind || p == StyleHeadS || p == StyleHeadE
	case ToolLine:
		return p == StyleColor || p == StyleDash || p == StyleSize || p == StyleSpline
	case ToolText:
		return p == StyleColor || p == StyleSize || p == StyleFont || p == StyleAlign
	case ToolNote:
		return p == StyleColor || p == StyleSize || p == StyleFont || p == StyleAlign || p == StyleVAlign
	}
	return false
}

// SharedStyle returns a style's value and whether the panel should show it:
// the selection's if there is one, Mixed where it differs, or else the next
// shape's.
func (e *Editor) SharedStyle(p StyleProp) (string, bool) {
	targets := e.styleTargets()
	if len(targets) > 0 {
		v, ok := "", false
		for _, s := range targets {
			if !hasStyle(s, p) {
				continue
			}
			w := styleOf(s, p)
			if !ok {
				v, ok = w, true
			} else if w != v {
				v = Mixed
			}
		}
		return v, ok
	}
	if e.Tool == ToolSelect {
		// With nothing selected the panel sets what comes next.
		return *e.nextStyleFor(p), p != StyleGeo && p != StyleKind && p != StyleHeadS && p != StyleHeadE && p != StyleSpline && p != StyleVAlign && p != StyleFont && p != StyleAlign
	}
	return *e.nextStyleFor(p), toolHasStyle(e.Tool, p)
}

// SetStyle sets a style on the selection, and on what comes next.
func (e *Editor) SetStyle(p StyleProp, v string) {
	if n := e.nextStyleFor(p); n != nil {
		*n = v
	}
	targets := e.styleTargets()
	if len(targets) == 0 {
		return
	}
	if p == StyleAlign {
		// What is selected says which alignment comes next.
		for _, s := range targets {
			if s.Type == doc.TypeText {
				e.Next.TextAlign = v
			}
		}
	}
	e.transact(func() {
		for _, s := range targets {
			if !hasStyle(s, p) || s.Locked {
				continue
			}
			e.update(s.ID, func(c *doc.Shape) {
				setStyleOf(c, p, v)
				if p == StyleSize || p == StyleFont {
					e.fitLabel(c)
				}
			})
		}
	})
}

// SharedOpacity returns the opacity of the selection, or of what comes next.
func (e *Editor) SharedOpacity() float64 {
	targets := e.styleTargets()
	if len(targets) == 0 {
		return e.Next.Opacity
	}
	return targets[0].Opacity
}

// SetOpacity sets the opacity of the selection, and of what comes next. It
// coalesces while live, for a slider being dragged.
func (e *Editor) SetOpacity(v float64, live bool) {
	e.Next.Opacity = v
	targets := e.styleTargets()
	if len(targets) == 0 {
		return
	}
	e.begin()
	for _, s := range targets {
		if !s.Locked {
			e.update(s.ID, func(c *doc.Shape) { c.Opacity = v })
		}
	}
	if !live {
		e.commit()
	}
}

// CommitOpen ends a transaction left open by a live change.
func (e *Editor) CommitOpen() {
	if e.gesture == nil && e.Editing == "" {
		e.commit()
	}
}

// --- deleting ---

// Delete deletes the selection.
func (e *Editor) Delete() {
	if len(e.sel) == 0 || e.ReadOnly {
		return
	}
	e.transact(func() {
		var ids []doc.ID
		for _, id := range e.sel {
			if s := e.Doc.Shapes[id]; s != nil && !s.Locked {
				ids = append(ids, id)
			}
		}
		e.deleteShapes(ids)
	})
}

func (e *Editor) deleteShapes(ids []doc.ID) {
	parents := map[doc.ID]bool{}
	for _, id := range ids {
		if s := e.Doc.Shapes[id]; s != nil {
			parents[s.Parent] = true
		}
		e.deleteTree(id)
	}
	e.sel = slices.DeleteFunc(slices.Clone(e.sel), func(id doc.ID) bool { return e.Doc.Shapes[id] == nil })
	e.cleanGroups(parents)
}

// deleteTree deletes a shape and what it holds, and lets arrows go of it.
func (e *Editor) deleteTree(id doc.ID) {
	for _, k := range slices.Clone(e.Children(id)) {
		e.deleteTree(k)
	}
	for _, b := range e.bindingsTo(id) {
		e.unbind(b.From, b.Terminal)
	}
	for _, term := range []string{"start", "end"} {
		if b := e.bindingOf(id, term); b != nil {
			e.removeBinding(b.ID)
		}
	}
	e.removeShape(id)
}

// cleanGroups removes groups left with too little to be groups.
func (e *Editor) cleanGroups(ids map[doc.ID]bool) {
	for id := range ids {
		s := e.Doc.Shapes[id]
		if s == nil || s.Type != doc.TypeGroup {
			continue
		}
		kids := e.Children(id)
		switch len(kids) {
		case 0:
			parent := s.Parent
			e.removeShape(id)
			e.cleanGroups(map[doc.ID]bool{parent: true})
		case 1:
			parent := s.Parent
			e.ungroupOne(id)
			e.cleanGroups(map[doc.ID]bool{parent: true})
		}
	}
}

// --- duplicating and pasting ---

// cloneShapes copies shapes with what they hold and the bindings among them,
// above the originals, offset by d, and returns the new top shapes.
func (e *Editor) cloneShapes(ids []doc.ID, d geo.Vec) []doc.ID {
	tops := map[doc.ID]string{} // the next index in each parent
	idmap := map[doc.ID]doc.ID{}
	var all []doc.ID
	for _, id := range ids {
		idmap[id] = doc.NewID("shape")
		all = append(all, id)
		for _, k := range e.Descendants(id) {
			idmap[k] = doc.NewID("shape")
			all = append(all, k)
		}
	}
	out := make([]doc.ID, 0, len(ids))
	for _, id := range all {
		s := e.Doc.Shapes[id]
		c := s.Clone()
		c.ID = idmap[id]
		if np, ok := idmap[s.Parent]; ok {
			c.Parent = np
		} else {
			c.Parent = s.Parent
			// Above everything else there.
			idx, ok := tops[s.Parent]
			if !ok {
				idx = e.topIndex(s.Parent)
			}
			c.Index = idx
			tops[s.Parent] = doc.IndexBetween(idx, "")
			local := e.parentMat(s.Parent).Invert().ApplyDir(d)
			c.X, c.Y = s.X+local.X, s.Y+local.Y
			out = append(out, c.ID)
		}
		e.put(c)
	}
	// Bindings to shapes that were copied too follow; others go, leaving
	// the copied arrow's end free where it shows.
	for _, b := range e.Doc.Bindings {
		nf, ok := idmap[b.From]
		if !ok {
			continue
		}
		if nt, ok := idmap[b.To]; ok {
			nb := *b
			nb.ID, nb.From, nb.To = doc.NewID("binding"), nf, nt
			e.putBinding(&nb)
			continue
		}
		s := e.Doc.Shapes[b.From]
		info := e.Arrow(s)
		e.update(nf, func(c *doc.Shape) {
			if b.Terminal == "start" {
				c.Start = info.Start
			} else {
				c.End = info.End
			}
		})
	}
	return out
}

// Duplicate copies the selection, a little off it.
func (e *Editor) Duplicate() {
	if len(e.sel) == 0 || e.ReadOnly {
		return
	}
	e.transact(func() {
		d := 16 / e.Cam().Z
		e.Select(e.cloneShapes(e.sorted(e.topSelected()), geo.V(d, d))...)
	})
}

// ClipboardFormat is the clipboard format of copied shapes.
const ClipboardFormat = "application/vnd.godraw.shapes+json"

// CopyData returns the selection as the clipboard holds it, and its text.
func (e *Editor) CopyData() ([]byte, string) {
	if len(e.sel) == 0 {
		return nil, ""
	}
	d := doc.New()
	d.Pages[0].ID = "page:clip"
	var texts []string
	var add func(id doc.ID, top bool)
	add = func(id doc.ID, top bool) {
		s := e.Doc.Shapes[id]
		c := s.Clone()
		if top {
			m := e.PageMat(id)
			o := m.Apply(geo.Vec{})
			c.X, c.Y, c.Rotation = o.X, o.Y, m.Rotation()
			c.Parent = "page:clip"
		}
		d.Shapes[c.ID] = c
		if s.Text != "" {
			texts = append(texts, s.Text)
		}
		if s.AssetID != "" {
			if a := e.Doc.Assets[s.AssetID]; a != nil {
				d.Assets[a.ID] = a
			}
		}
		for _, k := range e.Children(id) {
			add(k, false)
		}
	}
	for _, id := range e.sorted(e.topSelected()) {
		add(id, true)
	}
	for _, b := range e.Doc.Bindings {
		if d.Shapes[b.From] != nil && d.Shapes[b.To] != nil {
			d.Bindings[b.ID] = b
		}
	}
	// A freed arrow end keeps where it showed.
	for _, s := range d.Shapes {
		if s.Type != doc.TypeArrow {
			continue
		}
		orig := e.Doc.Shapes[s.ID]
		info := e.Arrow(orig)
		for _, term := range []string{"start", "end"} {
			b := e.bindingOf(s.ID, term)
			if b != nil && d.Shapes[b.To] == nil {
				if term == "start" {
					s.Start = info.Start
				} else {
					s.End = info.End
				}
			}
		}
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf, d); err != nil {
		return nil, ""
	}
	return buf.Bytes(), strings.Join(texts, "\n")
}

// Paste adds shapes from CopyData around a point of the page: the pointer,
// or the middle of the view.
func (e *Editor) Paste(data []byte, at *geo.Vec) error {
	d, err := doc.Read(bytes.NewReader(data))
	if err != nil {
		return err
	}
	e.transact(func() {
		idmap := map[doc.ID]doc.ID{}
		for id := range d.Shapes {
			idmap[id] = doc.NewID("shape")
		}
		var box geo.Box
		first := true
		src := e.scratchOf(d)
		var tops []*doc.Shape
		for _, s := range d.Shapes {
			if doc.IsPageID(s.Parent) {
				tops = append(tops, s)
				b := src.PageBounds(s.ID)
				if first {
					box, first = b, false
				} else {
					box = box.Union(b)
				}
			}
		}
		slices.SortFunc(tops, func(a, b *doc.Shape) int { return strings.Compare(a.Index, b.Index) })
		var delta geo.Vec
		if at != nil {
			delta = at.Sub(box.Center())
		} else {
			view := e.ViewBox()
			// Where it was, if that shows; else the middle of the view.
			if view.Overlaps(box) {
				delta = geo.V(16/e.Cam().Z, 16/e.Cam().Z)
			} else {
				delta = view.Center().Sub(box.Center())
			}
		}
		for _, a := range d.Assets {
			if e.Doc.Assets[a.ID] == nil {
				e.putAsset(a)
			}
		}
		var ids []doc.ID
		idx := e.topIndex(e.Page)
		for _, s := range tops {
			c := s.Clone()
			c.ID = idmap[s.ID]
			c.Parent = e.Page
			c.X, c.Y = s.X+delta.X, s.Y+delta.Y
			c.Index = idx
			idx = doc.IndexBetween(idx, "")
			e.put(c)
			ids = append(ids, c.ID)
		}
		for _, s := range d.Shapes {
			if doc.IsPageID(s.Parent) {
				continue
			}
			c := s.Clone()
			c.ID = idmap[s.ID]
			c.Parent = idmap[s.Parent]
			e.put(c)
		}
		for _, b := range d.Bindings {
			nb := *b
			nb.ID, nb.From, nb.To = doc.NewID("binding"), idmap[b.From], idmap[b.To]
			e.putBinding(&nb)
		}
		e.SelectNone()
		e.Select(ids...)
	})
	return nil
}

// scratchOf returns an editor over another drawing, to ask about its shapes.
func (e *Editor) scratchOf(d *doc.Drawing) *Editor {
	s := New(d, e.Measure)
	return s
}

// PasteText adds a text shape, at the middle of the view.
func (e *Editor) PasteText(text string) {
	text = strings.TrimRight(text, "\r\n")
	if text == "" || e.ReadOnly {
		return
	}
	e.transact(func() {
		s := doc.NewShape(doc.NewID("shape"), doc.TypeText)
		e.applyNext(s)
		s.Text = text
		g := e.Geometry(s)
		c := e.ViewBox().Center()
		s.Parent, s.Index = e.Page, e.topIndex(e.Page)
		s.X, s.Y = c.X-g.Bounds.W/2, c.Y-g.Bounds.H/2
		s.Cache = nil
		e.put(s)
		e.Select(s.ID)
	})
}

// InsertImage adds an image at a point of the page, or the middle of the
// view, from a file's bytes.
func (e *Editor) InsertImage(name, mime string, data []byte, w, h float64, at *geo.Vec) {
	if e.ReadOnly {
		return
	}
	e.transact(func() {
		asset := &doc.Asset{
			ID: doc.NewID("asset"), Type: "image", W: w, H: h, Name: name, Mime: mime,
			Src: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data),
		}
		e.putAsset(asset)
		// Large images come in at a size that fits the view.
		view := e.ViewBox()
		if k := math.Min(1, math.Min(view.W*0.7/w, view.H*0.7/h)); k < 1 {
			w, h = w*k, h*k
		}
		c := view.Center()
		if at != nil {
			c = *at
		}
		s := doc.NewShape(doc.NewID("shape"), doc.TypeImage)
		s.W, s.H, s.AssetID = w, h, asset.ID
		s.Parent, s.Index = e.Page, e.topIndex(e.Page)
		s.X, s.Y = c.X-w/2, c.Y-h/2
		e.put(s)
		e.Select(s.ID)
		e.Tool = ToolSelect
	})
}

// --- grouping ---

// Group groups the selection.
func (e *Editor) Group() {
	ids := e.topSelected()
	if len(ids) < 2 || e.ReadOnly {
		return
	}
	e.transact(func() {
		ids = e.sorted(ids)
		parent := e.Doc.Shapes[ids[0]].Parent
		for _, id := range ids {
			if e.Doc.Shapes[id].Parent != parent {
				parent = e.Page
				break
			}
		}
		var box geo.Box
		for i, id := range ids {
			b := e.PageBounds(id)
			if i == 0 {
				box = b
			} else {
				box = box.Union(b)
			}
		}
		g := doc.NewShape(doc.NewID("shape"), doc.TypeGroup)
		g.Parent = parent
		o := e.parentMat(parent).Invert().Apply(geo.V(box.X, box.Y))
		g.X, g.Y = o.X, o.Y
		// Where the top shape was, in order.
		g.Index = e.Doc.Shapes[ids[len(ids)-1]].Index
		e.put(g)
		idx := doc.FirstIndex
		for _, id := range ids {
			m := e.PageMat(g.ID).Invert().Mul(e.PageMat(id))
			c := e.Doc.Shapes[id].Clone()
			c.Parent = g.ID
			c.Index = idx
			idx = doc.IndexBetween(idx, "")
			o := m.Apply(geo.Vec{})
			c.X, c.Y, c.Rotation = o.X, o.Y, m.Rotation()
			e.put(c)
		}
		e.Select(g.ID)
	})
}

// Ungroup ungroups the groups in the selection.
func (e *Editor) Ungroup() {
	if e.ReadOnly {
		return
	}
	var kids []doc.ID
	any := false
	for _, id := range e.sel {
		if s := e.Doc.Shapes[id]; s != nil && s.Type == doc.TypeGroup {
			any = true
		}
	}
	if !any {
		return
	}
	e.transact(func() {
		for _, id := range slices.Clone(e.sel) {
			s := e.Doc.Shapes[id]
			if s == nil {
				continue
			}
			if s.Type == doc.TypeGroup {
				kids = append(kids, e.Children(id)...)
				e.ungroupOne(id)
			} else {
				kids = append(kids, id)
			}
		}
		e.FocusedGroup = ""
		e.Select(kids...)
	})
}

// ungroupOne moves a group's children to its parent, and removes it.
func (e *Editor) ungroupOne(id doc.ID) {
	s := e.Doc.Shapes[id]
	kids := slices.Clone(e.Children(id))
	siblings := e.Children(s.Parent)
	i := slices.Index(siblings, id)
	above := ""
	if i >= 0 && i+1 < len(siblings) {
		above = e.Doc.Shapes[siblings[i+1]].Index
	}
	idx := s.Index
	for _, k := range kids {
		m := e.parentMat(s.Parent).Invert().Mul(e.PageMat(k))
		c := e.Doc.Shapes[k].Clone()
		c.Parent = s.Parent
		idx = doc.IndexBetween(idx, above)
		c.Index = idx
		o := m.Apply(geo.Vec{})
		c.X, c.Y, c.Rotation = o.X, o.Y, m.Rotation()
		e.put(c)
	}
	e.removeShape(id)
}

// CanGroup reports whether the selection can be grouped.
func (e *Editor) CanGroup() bool { return len(e.topSelected()) > 1 }

// CanUngroup reports whether the selection holds a group.
func (e *Editor) CanUngroup() bool {
	for _, id := range e.sel {
		if s := e.Doc.Shapes[id]; s != nil && s.Type == doc.TypeGroup {
			return true
		}
	}
	return false
}

// --- order ---

// Reorder moves the selection in the paint order: "front", "forward",
// "backward" or "back".
func (e *Editor) Reorder(how string) {
	if len(e.sel) == 0 || e.ReadOnly {
		return
	}
	byParent := map[doc.ID][]doc.ID{}
	for _, id := range e.topSelected() {
		p := e.Doc.Shapes[id].Parent
		byParent[p] = append(byParent[p], id)
	}
	e.transact(func() {
		for parent, ids := range byParent {
			e.reorderIn(parent, ids, how)
		}
	})
}

func (e *Editor) reorderIn(parent doc.ID, ids []doc.ID, how string) {
	kids := slices.Clone(e.Children(parent))
	moving := map[doc.ID]bool{}
	for _, id := range ids {
		moving[id] = true
	}
	order := make([]doc.ID, 0, len(kids))
	switch how {
	case "front":
		for _, k := range kids {
			if !moving[k] {
				order = append(order, k)
			}
		}
		for _, k := range kids {
			if moving[k] {
				order = append(order, k)
			}
		}
	case "back":
		for _, k := range kids {
			if moving[k] {
				order = append(order, k)
			}
		}
		for _, k := range kids {
			if !moving[k] {
				order = append(order, k)
			}
		}
	case "forward":
		order = append(order, kids...)
		for i := len(order) - 2; i >= 0; i-- {
			if moving[order[i]] && !moving[order[i+1]] {
				order[i], order[i+1] = order[i+1], order[i]
			}
		}
	case "backward":
		order = append(order, kids...)
		for i := 1; i < len(order); i++ {
			if moving[order[i]] && !moving[order[i-1]] {
				order[i], order[i-1] = order[i-1], order[i]
			}
		}
	}
	// New indices from the bottom: simple, and they stay short.
	idx := ""
	for _, id := range order {
		idx = doc.IndexBetween(idx, "")
		if e.Doc.Shapes[id].Index != idx {
			c := e.Doc.Shapes[id].Moved()
			c.Index = idx
			e.put(c)
		}
	}
}

// --- align, distribute, flip ---

// Align lines up the selection: "left", "center-h", "right", "top",
// "center-v" or "bottom".
func (e *Editor) Align(how string) {
	ids := e.topSelected()
	if len(ids) < 2 || e.ReadOnly {
		return
	}
	box, r := e.SelectionBox()
	box = geo.Rotate(r).BoundsOf(box)
	e.transact(func() {
		for _, id := range ids {
			b := e.PageBounds(id)
			var d geo.Vec
			switch how {
			case "left":
				d.X = box.X - b.X
			case "center-h":
				d.X = box.Center().X - b.Center().X
			case "right":
				d.X = box.MaxX() - b.MaxX()
			case "top":
				d.Y = box.Y - b.Y
			case "center-v":
				d.Y = box.Center().Y - b.Center().Y
			case "bottom":
				d.Y = box.MaxY() - b.MaxY()
			}
			e.moveBy(id, d)
		}
	})
}

func (e *Editor) moveBy(id doc.ID, d geo.Vec) {
	s := e.Doc.Shapes[id]
	if s == nil || s.Locked {
		return
	}
	l := e.parentMat(s.Parent).Invert().ApplyDir(d)
	c := s.Moved()
	c.X, c.Y = s.X+l.X, s.Y+l.Y
	e.put(c)
}

// Distribute spaces the selection evenly, horizontally or vertically.
func (e *Editor) Distribute(horizontal bool) {
	ids := e.topSelected()
	if len(ids) < 3 || e.ReadOnly {
		return
	}
	type item struct {
		id doc.ID
		b  geo.Box
	}
	items := make([]item, len(ids))
	for i, id := range ids {
		items[i] = item{id, e.PageBounds(id)}
	}
	key := func(b geo.Box) float64 {
		if horizontal {
			return b.X
		}
		return b.Y
	}
	slices.SortFunc(items, func(a, b item) int {
		switch {
		case key(a.b) < key(b.b):
			return -1
		case key(a.b) > key(b.b):
			return 1
		}
		return 0
	})
	size := func(b geo.Box) float64 {
		if horizontal {
			return b.W
		}
		return b.H
	}
	total := 0.0
	for _, it := range items {
		total += size(it.b)
	}
	first, last := items[0].b, items[len(items)-1].b
	span := key(last) + size(last) - key(first)
	gap := (span - total) / float64(len(items)-1)
	at := key(first)
	e.transact(func() {
		for _, it := range items {
			var d geo.Vec
			if horizontal {
				d.X = at - it.b.X
			} else {
				d.Y = at - it.b.Y
			}
			e.moveBy(it.id, d)
			at += size(it.b) + gap
		}
	})
}

// Flip mirrors the selection across its middle.
func (e *Editor) Flip(horizontal bool) {
	if len(e.sel) == 0 || e.ReadOnly {
		return
	}
	e.begin()
	h := Handle{Kind: HandleResize, Which: geo.V(1, 0.5)}
	if !horizontal {
		h.Which = geo.V(0.5, 1)
	}
	g := newResize(e, h)
	box := g.box
	inv := geo.Rotate(-g.rotation)
	fwd := geo.Rotate(g.rotation)
	sx, sy := 1.0, 1.0
	if horizontal {
		sx = -1
	} else {
		sy = -1
	}
	for _, it := range g.items {
		e.resizeItem(it, sx, sy, box.Center(), fwd, inv, geo.V(0, 0))
	}
	e.commit()
}

// --- locks ---

// ToggleLock locks or unlocks the selection.
func (e *Editor) ToggleLock() {
	if len(e.sel) == 0 || e.ReadOnly {
		return
	}
	lock := false
	for _, id := range e.sel {
		if s := e.Doc.Shapes[id]; s != nil && !s.Locked {
			lock = true
		}
	}
	e.transact(func() {
		for _, id := range e.sel {
			e.update(id, func(c *doc.Shape) { c.Locked = lock })
		}
	})
}

// --- pages ---

// Pages returns the pages.
func (e *Editor) Pages() []*doc.Page { return e.Doc.Pages }

// SetPage goes to a page.
func (e *Editor) SetPage(id doc.ID) {
	if e.Doc.Page(id) == nil || id == e.Page {
		return
	}
	e.StopEditing()
	e.cancelGesture()
	e.Page = id
	e.SelectNone()
	e.Hovered = ""
}

// NewPage adds a page after the current one and goes to it.
func (e *Editor) NewPage() {
	e.StopEditing()
	e.transact(func() {
		pages := e.Doc.Pages
		i := 0
		for k, p := range pages {
			if p.ID == e.Page {
				i = k
			}
		}
		above := ""
		if i+1 < len(pages) {
			above = pages[i+1].Index
		}
		p := &doc.Page{ID: doc.NewID("page"), Name: e.nextPageName(), Index: doc.IndexBetween(pages[i].Index, above)}
		next := slices.Insert(slices.Clone(pages), i+1, p)
		e.setPages(next)
		e.Page = p.ID
		e.SelectNone()
	})
}

func (e *Editor) nextPageName() string {
	for n := len(e.Doc.Pages) + 1; ; n++ {
		name := fmt.Sprintf("Page %d", n)
		taken := false
		for _, p := range e.Doc.Pages {
			if p.Name == name {
				taken = true
			}
		}
		if !taken {
			return name
		}
	}
}

// RenamePage renames a page.
func (e *Editor) RenamePage(id doc.ID, name string) {
	name = strings.TrimSpace(name)
	p := e.Doc.Page(id)
	if p == nil || name == "" || name == p.Name {
		return
	}
	e.transact(func() {
		pages := slices.Clone(e.Doc.Pages)
		for i, q := range pages {
			if q.ID == id {
				c := *q
				c.Name = name
				pages[i] = &c
			}
		}
		e.setPages(pages)
	})
}

// DeletePage deletes a page and what is on it, unless it is the last one.
func (e *Editor) DeletePage(id doc.ID) {
	if len(e.Doc.Pages) < 2 {
		return
	}
	e.StopEditing()
	e.cancelGesture()
	e.transact(func() {
		for _, k := range slices.Clone(e.Children(id)) {
			e.deleteTree(k)
		}
		pages := slices.DeleteFunc(slices.Clone(e.Doc.Pages), func(p *doc.Page) bool { return p.ID == id })
		e.setPages(pages)
		if e.Page == id {
			e.Page = pages[0].ID
			e.SelectNone()
		}
	})
}

// MoveToPage moves the selection to another page.
func (e *Editor) MoveToPage(id doc.ID) {
	if len(e.sel) == 0 || e.Doc.Page(id) == nil || id == e.Page {
		return
	}
	e.transact(func() {
		for _, s := range e.sorted(e.topSelected()) {
			e.reparent(s, id)
		}
		e.SelectNone()
	})
}

// --- SVG ---

// ExportBounds returns the box of what export draws: the selection if there
// is one, else the page.
func (e *Editor) ExportBounds() (geo.Box, []doc.ID, bool) {
	ids := e.sorted(e.topSelected())
	if len(ids) == 0 {
		ids = e.Children(e.Page)
	}
	if len(ids) == 0 {
		return geo.Box{}, nil, false
	}
	var box geo.Box
	for i, id := range ids {
		b := e.visualBounds(id)
		if i == 0 {
			box = b
		} else {
			box = box.Union(b)
		}
	}
	return box, ids, true
}

// visualBounds is a shape's box with room for its stroke.
func (e *Editor) visualBounds(id doc.ID) geo.Box {
	s := e.Doc.Shapes[id]
	b := e.PageBounds(id)
	if s == nil {
		return b
	}
	pad := strokeOf(s.Size) * s.Scale
	if s.Type == doc.TypeHighlight {
		pad = fontSizeOf(s.Size) * s.Scale
	}
	if s.Type == doc.TypeGroup {
		for _, k := range e.Children(id) {
			b = b.Union(e.visualBounds(k))
		}
		return b
	}
	return b.Expand(pad)
}

// WalkPaint calls fn for what id paints, in order, with its transform.
// frames open and close around their contents, with enter and leave.
func (e *Editor) WalkPaint(id doc.ID, fn func(s *doc.Shape), enter func(frame *doc.Shape), leave func(frame *doc.Shape)) {
	s := e.Doc.Shapes[id]
	if s == nil {
		return
	}
	switch s.Type {
	case doc.TypeGroup:
		for _, k := range e.Children(id) {
			e.WalkPaint(k, fn, enter, leave)
		}
	case doc.TypeFrame:
		fn(s)
		enter(s)
		for _, k := range e.Children(id) {
			e.WalkPaint(k, fn, enter, leave)
		}
		leave(s)
	default:
		fn(s)
	}
}

// SVG returns what export draws as an SVG document.
func (e *Editor) SVG(dark bool) ([]byte, bool) {
	box, ids, ok := e.ExportBounds()
	if !ok {
		return nil, false
	}
	const pad = 32
	box = box.Expand(pad)
	pal := Light
	if dark {
		pal = Dark
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%.0f" height="%.0f" viewBox="%.2f %.2f %.2f %.2f">`, box.W, box.H, box.X, box.Y, box.W, box.H)
	fmt.Fprintf(&b, `<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" fill="%s"/>`, box.X, box.Y, box.W, box.H, pal.Background.CSS())
	clips := 0
	opacity := func(s *doc.Shape) string {
		if s.Opacity < 1 {
			return fmt.Sprintf(` opacity="%.3g"`, s.Opacity)
		}
		return ""
	}
	for _, id := range ids {
		e.WalkPaint(id, func(s *doc.Shape) {
			m := e.PageMat(s.ID)
			fmt.Fprintf(&b, `<g%s>`, opacity(s))
			for _, p := range e.Display(s, pal) {
				e.svgPrim(&b, p, m)
			}
			if s.Type == doc.TypeFrame && s.Name != "" {
				o := m.Apply(geo.V(0, -6))
				fmt.Fprintf(&b, `<text x="%.2f" y="%.2f" font-family="sans-serif" font-size="12" fill="%s">%s</text>`, o.X, o.Y, pal.Text.CSS(), xmlEscape(s.Name))
			}
			b.WriteString(`</g>`)
		}, func(f *doc.Shape) {
			clips++
			m := e.PageMat(f.ID)
			c := m.ApplyBox(geo.Box{W: f.W, H: f.H})
			fmt.Fprintf(&b, `<clipPath id="c%d"><path d="%s"/></clipPath><g clip-path="url(#c%d)">`, clips, (&geo.Path{}).Poly(c[:], true).SVG(geo.Identity), clips)
		}, func(f *doc.Shape) {
			b.WriteString(`</g>`)
		})
	}
	b.WriteString(`</svg>`)
	return b.Bytes(), true
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	if err := xmlEscapeText(&b, s); err != nil {
		return ""
	}
	return b.String()
}

func xmlEscapeText(b *bytes.Buffer, s string) error {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	_, err := r.WriteString(b, s)
	return err
}

func svgFontFamily(font string) string {
	switch font {
	case "draw":
		return "'Shantell Sans', 'Comic Sans MS', cursive"
	case "serif":
		return "Georgia, serif"
	case "mono":
		return "Menlo, monospace"
	}
	return "-apple-system, 'Helvetica Neue', Arial, sans-serif"
}

func (e *Editor) svgPrim(b *bytes.Buffer, p Prim, m geo.Mat) {
	alpha := ""
	if p.Color.A < 1 {
		alpha = fmt.Sprintf(` fill-opacity="%.3g" stroke-opacity="%.3g"`, p.Color.A, p.Color.A)
	}
	switch p.Kind {
	case PrimFill:
		fmt.Fprintf(b, `<path d="%s" fill="%s"%s/>`, p.Path.SVG(m), p.Color.CSS(), alpha)
	case PrimStroke:
		dash := ""
		if len(p.Dash) > 0 {
			parts := make([]string, len(p.Dash))
			for i, d := range p.Dash {
				parts[i] = fmt.Sprintf("%.2f", d)
			}
			dash = ` stroke-dasharray="` + strings.Join(parts, " ") + `"`
		}
		capJoin := ` stroke-linecap="round" stroke-linejoin="round"`
		if p.Butt {
			capJoin = ` stroke-linecap="butt" stroke-linejoin="miter"`
		}
		fmt.Fprintf(b, `<path d="%s" fill="none" stroke="%s" stroke-width="%.2f"%s%s%s/>`, p.Path.SVG(m), p.Color.CSS(), p.Width, capJoin, dash, alpha)
	case PrimText:
		if p.Block == nil {
			return
		}
		deg := m.Rotation() * 180 / math.Pi
		for _, l := range p.Block.Lines {
			o := m.Apply(geo.V(p.X+l.X, p.Y+l.Y+p.Size*1.05))
			fmt.Fprintf(b, `<text x="%.2f" y="%.2f" font-family="%s" font-size="%.2f" fill="%s" transform="rotate(%.2f %.2f %.2f)" xml:space="preserve">%s</text>`,
				o.X, o.Y, svgFontFamily(p.Font), p.Size, p.Color.CSS(), deg, o.X, o.Y, xmlEscape(l.Text))
		}
	case PrimImage:
		a := e.Doc.Assets[p.Asset]
		if a == nil {
			return
		}
		deg := m.Rotation() * 180 / math.Pi
		o := m.Apply(geo.V(p.Box.X, p.Box.Y))
		sx, sy := 1.0, 1.0
		if p.FlipX {
			sx = -1
		}
		if p.FlipY {
			sy = -1
		}
		fmt.Fprintf(b, `<image x="0" y="0" width="%.2f" height="%.2f" href="%s" preserveAspectRatio="none" transform="translate(%.2f %.2f) rotate(%.2f) translate(%.2f %.2f) scale(%g %g) translate(%.2f %.2f)"/>`,
			p.Box.W, p.Box.H, xmlEscape(a.Src), o.X, o.Y, deg, p.Box.W/2, p.Box.H/2, sx, sy, -p.Box.W/2, -p.Box.H/2)
	}
}

// DebugJSON is a compact dump of the shapes on the page, for tests.
func (e *Editor) DebugJSON() string {
	type item struct {
		ID, Type string
		X, Y     float64
	}
	var out []item
	for _, id := range e.Children(e.Page) {
		s := e.Doc.Shapes[id]
		out = append(out, item{string(id), s.Type, s.X, s.Y})
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// Nudge moves the selection by DIPs of the screen.
func (e *Editor) Nudge(d geo.Vec) {
	if len(e.sel) == 0 || e.ReadOnly {
		return
	}
	d = d.Mul(1 / e.Cam().Z)
	e.transact(func() {
		for _, id := range e.topSelected() {
			e.moveBy(id, d)
		}
	})
}

// GeoKinds lists the geo shapes, in the order the toolbar shows them.
var GeoKinds = []string{
	"rectangle", "ellipse", "triangle", "diamond", "pentagon",
	"hexagon", "octagon", "star", "rhombus", "rhombus-2",
	"oval", "trapezoid", "arrow-right", "arrow-left", "arrow-up",
	"arrow-down", "x-box", "check-box", "cloud", "heart",
}

// GeoOutlineOf returns the outline of a geo shape in a w×h box, to show it
// in buttons.
func GeoOutlineOf(kind string, w, h float64) []geo.Vec { return geoOutline(kind, w, h, kind) }
