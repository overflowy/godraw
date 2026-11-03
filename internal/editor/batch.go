package editor

import (
	"errors"
	"fmt"
	"slices"

	"godraw/internal/doc"
	"godraw/internal/geo"
)

// LabelPadding is the room around the labels of shapes.
const LabelPadding = labelPadding

// NoteSize is the side of a note, before it grows.
const NoteSize = noteSize

// LabelSize returns the font size of labels at a size style.
func LabelSize(size string) float64 { return labelSizeOf(size) }

// TextSize returns the font size of text shapes at a size style.
func TextSize(size string) float64 { return fontSizeOf(size) }

// ArrowLabelSize returns the font size of arrows' labels at a size style.
func ArrowLabelSize(size string) float64 { return arrowLabelSizeOf(size) }

// ErrBusy says that someone at the canvas is in the middle of a gesture or of
// typing, whose edits a batch must not mix with.
var ErrBusy = errors.New("canvas busy")

// Busy reports whether a gesture or text edit is under way.
func (e *Editor) Busy() bool { return e.gesture != nil || e.Editing != "" }

// Batch runs edits a program makes as one step of undo: if fn fails, or
// panics, nothing it did stays. The selection and focused group stay as they
// were, less what fn deleted, unless fn left the page.
func (e *Editor) Batch(fn func() error) (err error) {
	if e.Busy() {
		return ErrBusy
	}
	e.commit() // what a slider left open
	sel, focus, page := slices.Clone(e.sel), e.FocusedGroup, e.Page
	e.begin()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("internal error: %v", r)
		}
		if err != nil {
			e.rollback()
			return
		}
		e.sel, e.FocusedGroup = nil, ""
		if e.Page == page {
			e.sel = slices.DeleteFunc(sel, func(id doc.ID) bool { return e.Doc.Shapes[id] == nil })
			if e.Doc.Shapes[focus] != nil {
				e.FocusedGroup = focus
			}
		}
		e.commit()
	}()
	return fn()
}

// Put adds a shape or replaces one, within a Batch. The shape must be new or
// a fresh Clone or Moved.
func (e *Editor) Put(s *doc.Shape) { e.put(s) }

// PutAsset adds an asset, within a Batch.
func (e *Editor) PutAsset(a *doc.Asset) { e.putAsset(a) }

// TopIndex returns an index above a parent's children.
func (e *Editor) TopIndex(parent doc.ID) string { return e.topIndex(parent) }

// FitLabel grows a shape to hold its label.
func (e *Editor) FitLabel(s *doc.Shape) { e.fitLabel(s) }

// MoveBy moves a shape by a distance in the page, within a Batch.
func (e *Editor) MoveBy(id doc.ID, d geo.Vec) { e.moveBy(id, d) }

// Reparent moves a shape to another parent, keeping where it is in the page.
func (e *Editor) Reparent(id, parent doc.ID) { e.reparent(id, parent) }

// VisualBounds returns a shape's box in the page, with room for its stroke.
func (e *Editor) VisualBounds(id doc.ID) geo.Box { return e.visualBounds(id) }

// BindingOf returns the binding of an end of an arrow, if any.
func (e *Editor) BindingOf(arrow doc.ID, terminal string) *doc.Binding {
	return e.bindingOf(arrow, terminal)
}

// BindingsTo returns the arrows' bindings to a shape.
func (e *Editor) BindingsTo(id doc.ID) []*doc.Binding { return e.bindingsTo(id) }

// ArrowLabelBox returns the box of an arrow's label, in the arrow's space.
func (e *Editor) ArrowLabelBox(s *doc.Shape) *geo.Box { return e.arrowLabelBox(s) }

// At returns the point a part of the way along the arrow, from 0 to 1.
func (i *ArrowInfo) At(t float64) geo.Vec { return i.at(t) }

// Bind ties an end of an arrow to a shape, at an anchor given as fractions of
// the shape's box.
func (e *Editor) Bind(arrow doc.ID, terminal string, target doc.ID, anchor geo.Vec) {
	nb := &doc.Binding{
		ID: doc.NewID("binding"), Type: "arrow", From: arrow, To: target, Terminal: terminal,
		Anchor: anchor, IsPrecise: anchor != geo.V(0.5, 0.5),
	}
	if b := e.bindingOf(arrow, terminal); b != nil {
		nb.ID, nb.Meta, nb.Raw = b.ID, b.Meta, b.Raw
	}
	e.putBinding(nb)
}

// Unbind lets an end of an arrow go, leaving it where it shows.
func (e *Editor) Unbind(arrow doc.ID, terminal string) { e.unbind(arrow, terminal) }

// ScaleSelection scales the selection around the top left of its box.
func (e *Editor) ScaleSelection(sx, sy float64) {
	if len(e.sel) == 0 {
		return
	}
	e.transact(func() {
		h := Handle{Kind: HandleResize, Which: geo.V(1, 1)}
		g := newResize(e, h)
		anchor := geo.V(g.box.X, g.box.Y)
		fwd, inv := geo.Rotate(g.rotation), geo.Rotate(-g.rotation)
		for _, it := range g.items {
			e.resizeItem(it, sx, sy, anchor, fwd, inv, h.Which)
		}
	})
}

// DuplicateAs copies the selection, off it by d, naming each new shape with
// fresh, and returns the copies of what was selected.
func (e *Editor) DuplicateAs(d geo.Vec, fresh func() doc.ID) []doc.ID {
	var out []doc.ID
	e.transact(func() {
		out = e.cloneShapes(e.sorted(e.topSelected()), d, fresh)
	})
	return out
}
