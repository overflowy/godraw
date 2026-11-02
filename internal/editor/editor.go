// Package editor is the canvas without a window: the document being edited,
// the camera, the selection, tools and their gestures, undo, and what each
// shape draws. Package app shows it and feeds it input.
package editor

import (
	"maps"
	"math"
	"slices"
	"strings"

	"godraw/internal/doc"
	"godraw/internal/geo"
)

// Tool is what the pointer does on the canvas.
type Tool string

// Tools.
const (
	ToolSelect    Tool = "select"
	ToolHand      Tool = "hand"
	ToolDraw      Tool = "draw"
	ToolEraser    Tool = "eraser"
	ToolArrow     Tool = "arrow"
	ToolText      Tool = "text"
	ToolNote      Tool = "note"
	ToolGeo       Tool = "geo"
	ToolLine      Tool = "line"
	ToolHighlight Tool = "highlight"
	ToolLaser     Tool = "laser"
	ToolFrame     Tool = "frame"
)

// Camera is what the viewport shows: screen = (page + (X, Y)) * Z.
type Camera struct{ X, Y, Z float64 }

// Zoom stops of the zoom in and out commands, and its limits.
var zoomStops = []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 4, 8}

const (
	minZoom = 0.05
	maxZoom = 8
)

// TextLine is a line of laid out text.
type TextLine struct {
	Text    string
	X, Y, W float64
}

// TextBlock is laid out text: its lines start at X 0 before alignment.
type TextBlock struct {
	Lines []TextLine
	W, H  float64
	Line  float64 // line height
}

// Measure lays text out, wrapping at width (0 only breaks at newlines).
type Measure func(text, font string, size, width float64) TextBlock

// Style is the styles that new shapes take, and that the style panel sets.
type Style struct {
	Color, Fill, Dash, Size, Font, Align, VAlign string
	TextAlign                                    string // of text shapes, as Align is of labels
	Geo, Kind, HeadStart, HeadEnd, Spline        string
	Opacity                                      float64
}

// DefaultStyle is the style of a new canvas, tldraw's.
func DefaultStyle() Style {
	return Style{
		Color: "black", Fill: "none", Dash: "draw", Size: "m", Font: "draw", Align: "middle", VAlign: "middle", TextAlign: "start",
		Geo: "rectangle", Kind: "arc", HeadStart: "none", HeadEnd: "arrow", Spline: "line", Opacity: 1,
	}
}

// Mods are the modifier keys held.
type Mods struct{ Shift, Alt, Cmd, Ctrl bool }

// Trail is a line that fades, of the laser and the eraser.
type Trail struct {
	Points []TrailPoint
	Laser  bool
	Live   bool
}

// TrailPoint is a point of a Trail, at a time in seconds.
type TrailPoint struct {
	P geo.Vec
	T float64
}

// Editor is one canvas.
type Editor struct {
	Doc     *doc.Drawing
	Page    doc.ID
	Measure Measure

	// OnChange tells that the document changed, with a snapshot of it. It
	// follows each commit, undo and redo, not each move of a drag.
	OnChange func(*doc.Drawing)
	// OnRequest asks the window for what the editor cannot do itself,
	// as "insert-image".
	OnRequest func(what string)

	cams         map[doc.ID]Camera
	VW, VH       float64
	sel          []doc.ID
	Hovered      doc.ID
	FocusedGroup doc.ID
	Editing      doc.ID
	EditText     string
	Tool         Tool
	ToolLocked   bool
	Next         Style

	Grid, Snap, DarkMode, FocusMode, ReadOnly, Debug bool

	// What the canvas shows as it goes on.
	Brush   *geo.Box
	Snaps   []SnapLine
	Hint    map[doc.ID]bool // shapes outlined, as an arrow's target
	Erasing map[doc.ID]bool
	DropOn  doc.ID // the frame shapes would drop into
	Trails  []*Trail
	Now     float64 // the clock of trails, in seconds

	pointer   Pointer
	gesture   gesture
	spaceHeld bool

	undo, redo []*entry
	tx         *entry

	kids     map[doc.ID][]doc.ID
	arrows   map[doc.ID]*arrowMemo
	bindFrom map[doc.ID][]*doc.Binding // by arrow; nil when stale
	bindTo   map[doc.ID][]*doc.Binding // by target
}

// Pointer is the state of the pointer.
type Pointer struct {
	Screen, Page         geo.Vec
	DownScreen, DownPage geo.Vec
	Down                 bool
	Button               int
	Mods                 Mods
	Time                 float64
}

// New returns an editor of a drawing.
func New(d *doc.Drawing, measure Measure) *Editor {
	e := &Editor{
		Doc: d, Measure: measure, Tool: ToolSelect, Next: DefaultStyle(),
		cams: map[doc.ID]Camera{}, arrows: map[doc.ID]*arrowMemo{},
		VW: 1200, VH: 800,
	}
	e.Page = d.Pages[0].ID
	return e
}

// Replace shows another drawing in the editor, as when a file reloads,
// forgetting history.
func (e *Editor) Replace(d *doc.Drawing) {
	e.cancelGesture()
	e.Doc = d
	e.undo, e.redo, e.tx = nil, nil, nil
	e.kids, e.sel, e.Editing, e.Hovered, e.FocusedGroup = nil, nil, "", "", ""
	e.bindFrom, e.bindTo = nil, nil
	e.arrows = map[doc.ID]*arrowMemo{}
	if d.Page(e.Page) == nil {
		e.Page = d.Pages[0].ID
	}
}

// Cam returns the camera of the current page.
func (e *Editor) Cam() Camera {
	if c, ok := e.cams[e.Page]; ok {
		return c
	}
	return Camera{Z: 1}
}

// SetCam moves the camera of the current page.
func (e *Editor) SetCam(c Camera) {
	c.Z = math.Max(minZoom, math.Min(maxZoom, c.Z))
	e.cams[e.Page] = c
}

// ToPage returns a point on the screen in the page.
func (e *Editor) ToPage(p geo.Vec) geo.Vec {
	c := e.Cam()
	return geo.V(p.X/c.Z-c.X, p.Y/c.Z-c.Y)
}

// ToScreen returns a point of the page on the screen.
func (e *Editor) ToScreen(p geo.Vec) geo.Vec {
	c := e.Cam()
	return geo.V((p.X+c.X)*c.Z, (p.Y+c.Y)*c.Z)
}

// ViewBox returns what the viewport shows, in the page.
func (e *Editor) ViewBox() geo.Box {
	c := e.Cam()
	return geo.Box{X: -c.X, Y: -c.Y, W: e.VW / c.Z, H: e.VH / c.Z}
}

// Pointer returns the state of the pointer.
func (e *Editor) Pointer() Pointer { return e.pointer }

// Palette returns the current theme.
func (e *Editor) Palette() *Palette {
	if e.DarkMode {
		return Dark
	}
	return Light
}

// --- history ---

type change[T any] struct{ before, after *T }

type entry struct {
	shapes                map[doc.ID]change[doc.Shape]
	bindings              map[doc.ID]change[doc.Binding]
	assets                map[doc.ID]change[doc.Asset]
	pages                 *change[[]*doc.Page]
	name                  *change[string]
	selBefore             []doc.ID
	selAfter              []doc.ID
	pageBefore, pageAfter doc.ID
}

func (x *entry) empty() bool {
	return len(x.shapes) == 0 && len(x.bindings) == 0 && len(x.assets) == 0 && x.pages == nil && x.name == nil
}

// begin opens a transaction, into which edits go until commit or rollback.
func (e *Editor) begin() {
	if e.tx != nil {
		return
	}
	e.tx = &entry{
		shapes:     map[doc.ID]change[doc.Shape]{},
		bindings:   map[doc.ID]change[doc.Binding]{},
		assets:     map[doc.ID]change[doc.Asset]{},
		selBefore:  slices.Clone(e.sel),
		pageBefore: e.Page,
	}
}

// commit ends the transaction, which becomes a step of undo.
func (e *Editor) commit() {
	x := e.tx
	if x == nil {
		return
	}
	e.tx = nil
	// What changed and changed back is no change.
	for id, c := range x.shapes {
		if c.before == c.after {
			delete(x.shapes, id)
		}
	}
	for id, c := range x.bindings {
		if c.before == c.after {
			delete(x.bindings, id)
		}
	}
	for id, c := range x.assets {
		if c.before == c.after {
			delete(x.assets, id)
		}
	}
	if x.empty() {
		return
	}
	x.selAfter, x.pageAfter = slices.Clone(e.sel), e.Page
	e.undo = append(e.undo, x)
	e.redo = nil
	e.changed()
}

func (e *Editor) changed() {
	if e.OnChange != nil {
		e.OnChange(e.Doc.Snapshot())
	}
}

// rollback ends the transaction, restoring what it changed.
func (e *Editor) rollback() {
	x := e.tx
	if x == nil {
		return
	}
	e.tx = nil
	e.apply(x, true)
}

// transact runs fn as a step of undo.
func (e *Editor) transact(fn func()) {
	if e.tx != nil {
		fn()
		return
	}
	e.begin()
	fn()
	e.commit()
}

func (e *Editor) apply(x *entry, back bool) {
	for id, c := range x.shapes {
		v := c.after
		if back {
			v = c.before
		}
		if v == nil {
			delete(e.Doc.Shapes, id)
		} else {
			e.Doc.Shapes[id] = v
		}
	}
	for id, c := range x.bindings {
		v := c.after
		if back {
			v = c.before
		}
		if v == nil {
			delete(e.Doc.Bindings, id)
		} else {
			e.Doc.Bindings[id] = v
		}
	}
	for id, c := range x.assets {
		v := c.after
		if back {
			v = c.before
		}
		if v == nil {
			delete(e.Doc.Assets, id)
		} else {
			e.Doc.Assets[id] = v
		}
	}
	if x.pages != nil {
		if back {
			e.Doc.Pages = slices.Clone(*x.pages.before)
		} else {
			e.Doc.Pages = slices.Clone(*x.pages.after)
		}
	}
	if x.name != nil {
		if back {
			e.Doc.Name = *x.name.before
		} else {
			e.Doc.Name = *x.name.after
		}
	}
	e.kids = nil
	e.bindFrom, e.bindTo = nil, nil
	if back {
		e.sel, e.Page = slices.Clone(x.selBefore), x.pageBefore
	} else {
		e.sel, e.Page = slices.Clone(x.selAfter), x.pageAfter
	}
	if e.Doc.Page(e.Page) == nil {
		e.Page = e.Doc.Pages[0].ID
	}
	e.sel = slices.DeleteFunc(e.sel, func(id doc.ID) bool { return e.Doc.Shapes[id] == nil })
}

// CanUndo reports whether there is something to undo.
func (e *Editor) CanUndo() bool { return len(e.undo) > 0 }

// CanRedo reports whether there is something to redo.
func (e *Editor) CanRedo() bool { return len(e.redo) > 0 }

// Undo undoes the last step.
func (e *Editor) Undo() {
	e.StopEditing()
	e.cancelGesture()
	e.commit() // what a slider left open
	if len(e.undo) == 0 {
		return
	}
	x := e.undo[len(e.undo)-1]
	e.undo = e.undo[:len(e.undo)-1]
	e.redo = append(e.redo, x)
	e.apply(x, true)
	e.changed()
}

// Redo redoes the last step undone.
func (e *Editor) Redo() {
	e.StopEditing()
	e.cancelGesture()
	e.commit()
	if len(e.redo) == 0 {
		return
	}
	x := e.redo[len(e.redo)-1]
	e.redo = e.redo[:len(e.redo)-1]
	e.undo = append(e.undo, x)
	e.apply(x, false)
	e.changed()
}

// --- records ---

// put replaces a shape or adds it. The shape must be new or a fresh clone.
func (e *Editor) put(s *doc.Shape) {
	e.begin()
	before := e.Doc.Shapes[s.ID]
	if c, ok := e.tx.shapes[s.ID]; ok {
		e.tx.shapes[s.ID] = change[doc.Shape]{c.before, s}
	} else {
		e.tx.shapes[s.ID] = change[doc.Shape]{before, s}
	}
	e.Doc.Shapes[s.ID] = s
	if before == nil || before.Parent != s.Parent || before.Index != s.Index {
		e.kids = nil
	}
}

// update replaces a shape with a clone that fn changes.
func (e *Editor) update(id doc.ID, fn func(*doc.Shape)) {
	s := e.Doc.Shapes[id]
	if s == nil {
		return
	}
	c := s.Clone()
	fn(c)
	e.put(c)
}

// removeShape takes a shape out, by itself, leaving its children.
func (e *Editor) removeShape(id doc.ID) {
	e.begin()
	before := e.Doc.Shapes[id]
	if before == nil {
		return
	}
	if c, ok := e.tx.shapes[id]; ok {
		e.tx.shapes[id] = change[doc.Shape]{c.before, nil}
	} else {
		e.tx.shapes[id] = change[doc.Shape]{before, nil}
	}
	delete(e.Doc.Shapes, id)
	e.kids = nil
}

func (e *Editor) putBinding(b *doc.Binding) {
	e.begin()
	before := e.Doc.Bindings[b.ID]
	if c, ok := e.tx.bindings[b.ID]; ok {
		e.tx.bindings[b.ID] = change[doc.Binding]{c.before, b}
	} else {
		e.tx.bindings[b.ID] = change[doc.Binding]{before, b}
	}
	e.Doc.Bindings[b.ID] = b
	e.bindFrom, e.bindTo = nil, nil
}

func (e *Editor) removeBinding(id doc.ID) {
	e.begin()
	before := e.Doc.Bindings[id]
	if before == nil {
		return
	}
	if c, ok := e.tx.bindings[id]; ok {
		e.tx.bindings[id] = change[doc.Binding]{c.before, nil}
	} else {
		e.tx.bindings[id] = change[doc.Binding]{before, nil}
	}
	delete(e.Doc.Bindings, id)
	e.bindFrom, e.bindTo = nil, nil
}

func (e *Editor) putAsset(a *doc.Asset) {
	e.begin()
	before := e.Doc.Assets[a.ID]
	if c, ok := e.tx.assets[a.ID]; ok {
		e.tx.assets[a.ID] = change[doc.Asset]{c.before, a}
	} else {
		e.tx.assets[a.ID] = change[doc.Asset]{before, a}
	}
	e.Doc.Assets[a.ID] = a
}

func (e *Editor) setPages(pages []*doc.Page) {
	e.begin()
	before := slices.Clone(e.Doc.Pages)
	after := slices.Clone(pages)
	if e.tx.pages != nil {
		before = *e.tx.pages.before
	}
	e.tx.pages = &change[[]*doc.Page]{&before, &after}
	e.Doc.Pages = after
}

// SetDocName renames the drawing.
func (e *Editor) SetDocName(name string) {
	e.transact(func() {
		before := e.Doc.Name
		e.tx.name = &change[string]{&before, &name}
		e.Doc.Name = name
	})
}

// --- structure ---

func (e *Editor) children() map[doc.ID][]doc.ID {
	if e.kids != nil {
		return e.kids
	}
	k := make(map[doc.ID][]doc.ID, 8)
	for id, s := range e.Doc.Shapes {
		k[s.Parent] = append(k[s.Parent], id)
	}
	for _, list := range k {
		slices.SortFunc(list, func(a, b doc.ID) int {
			if c := strings.Compare(e.Doc.Shapes[a].Index, e.Doc.Shapes[b].Index); c != 0 {
				return c
			}
			return strings.Compare(string(a), string(b))
		})
	}
	e.kids = k
	return k
}

// Children returns the children of a page or shape, bottom first.
func (e *Editor) Children(id doc.ID) []doc.ID { return e.children()[id] }

// PageOf returns the page a shape is on.
func (e *Editor) PageOf(id doc.ID) doc.ID {
	for !doc.IsPageID(id) {
		s := e.Doc.Shapes[id]
		if s == nil {
			return ""
		}
		id = s.Parent
	}
	return id
}

// Descendants returns a shape's descendants, depth first, not itself.
func (e *Editor) Descendants(id doc.ID) []doc.ID {
	var out []doc.ID
	var walk func(doc.ID)
	walk = func(id doc.ID) {
		for _, k := range e.children()[id] {
			out = append(out, k)
			walk(k)
		}
	}
	walk(id)
	return out
}

// IsAncestor reports whether a is an ancestor of id.
func (e *Editor) IsAncestor(a, id doc.ID) bool {
	for {
		s := e.Doc.Shapes[id]
		if s == nil {
			return false
		}
		if s.Parent == a {
			return true
		}
		id = s.Parent
	}
}

// outermostSelectable returns the group holding id that a click selects,
// within the focused group, or id if it is in none.
func (e *Editor) outermostSelectable(id doc.ID) doc.ID {
	best := id
	cur := id
	for {
		s := e.Doc.Shapes[cur]
		if s == nil || doc.IsPageID(s.Parent) || s.Parent == e.FocusedGroup {
			return best
		}
		p := e.Doc.Shapes[s.Parent]
		if p == nil {
			return best
		}
		if p.Type == doc.TypeGroup {
			best = p.ID
		}
		cur = p.ID
	}
}

// PageMat returns the transform from a shape's space to the page.
func (e *Editor) PageMat(id doc.ID) geo.Mat {
	s := e.Doc.Shapes[id]
	if s == nil {
		return geo.Identity
	}
	m := geo.Compose(s.X, s.Y, s.Rotation)
	if doc.IsPageID(s.Parent) {
		return m
	}
	return e.PageMat(s.Parent).Mul(m)
}

// parentMat returns the transform from a parent's space to the page.
func (e *Editor) parentMat(parent doc.ID) geo.Mat {
	if doc.IsPageID(parent) {
		return geo.Identity
	}
	return e.PageMat(parent)
}

// --- selection ---

// Selection returns the selected shapes.
func (e *Editor) Selection() []doc.ID { return e.sel }

// IsSelected reports whether a shape is selected.
func (e *Editor) IsSelected(id doc.ID) bool { return slices.Contains(e.sel, id) }

// Select selects shapes.
func (e *Editor) Select(ids ...doc.ID) {
	e.sel = slices.Clone(ids)
	// The focused group holds the selection: leave it as it moves away.
	if e.FocusedGroup != "" {
		keep := false
		for _, id := range ids {
			if e.IsAncestor(e.FocusedGroup, id) {
				keep = true
			}
		}
		if !keep {
			e.FocusedGroup = ""
		}
	}
}

// SelectAll selects what is on the page, or in the focused group.
func (e *Editor) SelectAll() {
	parent := e.Page
	if e.FocusedGroup != "" {
		parent = e.FocusedGroup
	}
	var ids []doc.ID
	for _, id := range e.Children(parent) {
		if !e.Doc.Shapes[id].Locked {
			ids = append(ids, id)
		}
	}
	e.Select(ids...)
}

// SelectNone clears the selection.
func (e *Editor) SelectNone() {
	e.sel = nil
	e.FocusedGroup = ""
}

// topSelected returns the selected shapes without those whose ancestor is
// selected too.
func (e *Editor) topSelected() []doc.ID {
	out := make([]doc.ID, 0, len(e.sel))
	chosen := make(map[doc.ID]bool, len(e.sel))
	for _, id := range e.sel {
		chosen[id] = true
	}
	for _, id := range e.sel {
		inside := false
		for s := e.Doc.Shapes[id]; s != nil; s = e.Doc.Shapes[s.Parent] {
			if chosen[s.Parent] {
				inside = true
				break
			}
		}
		if !inside {
			out = append(out, id)
		}
	}
	return out
}

// sorted returns ids in the order they paint.
func (e *Editor) sorted(ids []doc.ID) []doc.ID {
	order := map[doc.ID]int{}
	n := 0
	var walk func(doc.ID)
	walk = func(id doc.ID) {
		for _, k := range e.Children(id) {
			order[k] = n
			n++
			walk(k)
		}
	}
	walk(e.PageOf(firstOr(ids, e.Page)))
	out := slices.Clone(ids)
	slices.SortStableFunc(out, func(a, b doc.ID) int { return order[a] - order[b] })
	return out
}

func firstOr(ids []doc.ID, def doc.ID) doc.ID {
	if len(ids) > 0 {
		return ids[0]
	}
	return def
}

func keysOf(m map[doc.ID]bool) []doc.ID { return slices.Collect(maps.Keys(m)) }
