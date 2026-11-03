package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"godraw/internal/doc"
	"godraw/internal/editor"
)

// everything is what a verb names to mean all that's on the page.
const everything = "*"

// targets is what a verb acts on: ids as an agent lists them, a run of ids as an
// answer prints it, everything, or a selector.
type targets struct {
	list names
	sel  *selector
}

// selector picks shapes by what they are and where, in place of naming them.
type selector struct {
	Kind names   `json:"kind"`
	Page *string `json:"page"`
}

const selectorHelp = `a selector is {"kind":"rectangle"}, a list of kinds, {"page":"Name"} or both`

func (t *targets) UnmarshalJSON(b []byte) error {
	if bytes.HasPrefix(bytes.TrimSpace(b), []byte("{")) {
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		sel := &selector{}
		if err := d.Decode(sel); err != nil || (len(sel.Kind) == 0 && sel.Page == nil) {
			return errors.New(selectorHelp)
		}
		for _, kind := range sel.Kind {
			if !selectable(kind) {
				return fmt.Errorf("no kind %q; use group, card text note frame image draw highlight line arrow, geo for every geo shape, or one: %s", kind, strings.Join(editor.GeoKinds, " "))
			}
		}
		t.sel = sel
		return nil
	}
	if json.Unmarshal(b, &t.list) != nil {
		return errors.New("expected an id, a list of ids, or " + selectorHelp)
	}
	if len(t.list) > 1 && slices.Contains(t.list, everything) {
		return errors.New(`"*" is all on the page, so it stands alone`)
	}
	return nil
}

// isEverything says whether it picks all that's on a page, of any kind.
func (t *targets) isEverything() bool {
	if t.sel != nil {
		return len(t.sel.Kind) == 0
	}
	return len(t.list) == 1 && t.list[0] == everything
}

// broad says whether it picks shapes without naming them. An op over those takes
// what can take it, where one over ids is refused for what can't.
func (t *targets) broad() bool { return t.sel != nil || t.isEverything() }

// selectable says whether a selector can name a kind.
func selectable(kind string) bool {
	if kind == doc.TypeGroup || slices.Contains(editor.GeoKinds, kind) {
		return true
	}
	_, ok := addFields[kind]
	return ok
}

// targets returns what an op's verb acts on, if it acts on shapes.
func (o *op) targets() *targets {
	for _, t := range []*targets{o.Set, o.Del, o.Dup, o.Group, o.Ungroup, o.Arrange, o.Order, o.Flip, o.Camera} {
		if t != nil {
			return t
		}
	}
	return nil
}

// autoNumber returns the number of an id the server gave, as 12 of s12.
func autoNumber(name string) (int, bool) {
	rest, ok := strings.CutPrefix(name, "s")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil && "s"+strconv.Itoa(n) == name
}

// spread lists ids with each run, as s4-s9, written out. A shape that has that very
// id comes first: ids are the agent's to choose.
func spread(e *editor.Editor, list names) (names, error) {
	out := make(names, 0, len(list))
	for _, name := range list {
		first, last, isRun := strings.Cut(name, "-")
		lo, okLo := autoNumber(first)
		hi, okHi := autoNumber(last)
		if !isRun || !okLo || !okHi || lo >= hi || e.Doc.Shapes[shapeID(name)] != nil {
			out = append(out, name)
			continue
		}
		// More ids than shapes can't all be there, and may be more than to write out.
		if n := hi - lo + 1; n > len(e.Doc.Shapes) {
			return nil, fmt.Errorf("%s runs over %d ids, and there are %d shapes", name, n, len(e.Doc.Shapes))
		}
		for i := lo; i <= hi; i++ {
			out = append(out, "s"+strconv.Itoa(i))
		}
	}
	return out, nil
}

// errPicksNothing says that a selector found no shape.
var errPicksNothing = errors.New("picks nothing")

// pick resolves what a verb acts on, all of it on the current page. Ids that are
// locked are refused unless lockedToo, and otherwise a selector leaves them out,
// as it does what they hold, and says so.
func (r *run) pick(t *targets, lockedToo bool) ([]doc.ID, error) {
	if t.broad() {
		return r.picked(t, lockedToo)
	}
	if len(t.list) == 0 {
		return nil, errors.New("names no shapes")
	}
	list, err := spread(r.e, t.list)
	if err != nil {
		return nil, err
	}
	ids := make([]doc.ID, 0, len(list))
	seen := make(map[doc.ID]bool, len(list))
	for _, name := range list {
		s, err := r.current(name)
		if err != nil {
			return nil, err
		}
		if s.Locked && !lockedToo {
			return nil, fmt.Errorf("%s is locked; {\"set\":%q,\"locked\":false} first", name, name)
		}
		if !seen[s.ID] {
			seen[s.ID] = true
			ids = append(ids, s.ID)
		}
	}
	return ids, nil
}

// picked returns what a selector finds on the current page: its top shapes, or
// the kinds asked for at any depth, a card counting as one.
func (r *run) picked(t *targets, lockedToo bool) ([]doc.ID, error) {
	var kinds names
	if t.sel != nil {
		kinds = t.sel.Kind
	}
	from := r.e.Children(r.e.Page)
	if len(kinds) > 0 {
		from = r.e.Descendants(r.e.Page)
	}
	var ids, locked []doc.ID
	for _, id := range from {
		s := r.e.Doc.Shapes[id]
		if len(kinds) > 0 && !r.isOfKind(s, kinds) {
			continue
		}
		if !lockedToo && r.isLocked(s) {
			locked = append(locked, id)
			continue
		}
		ids = append(ids, id)
	}
	if len(locked) > 0 {
		r.warnAt(rankLayout, "left alone what is locked: %s", nameList(locked))
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w on page %q that isn't locked", errPicksNothing, r.e.Doc.Page(r.e.Page).Name)
	}
	return ids, nil
}

// isOfKind says whether a shape is of one of these kinds. A card's parts aren't:
// a card is what they make.
func (r *run) isOfKind(s *doc.Shape, kinds names) bool {
	if s.Card != "" {
		return false
	}
	if s.Type == doc.TypeGroup {
		if r.cardOf(s.ID) != nil {
			return slices.Contains(kinds, cardKind)
		}
		return slices.Contains(kinds, doc.TypeGroup)
	}
	return slices.Contains(kinds, s.Type) || (s.Geo != "" && slices.Contains(kinds, s.Geo))
}

// isLocked says whether a shape is locked, or held by what is.
func (r *run) isLocked(s *doc.Shape) bool {
	for ; s != nil; s = r.e.Doc.Shapes[s.Parent] {
		if s.Locked {
			return true
		}
	}
	return false
}

// heldBy says whether something that holds a shape is among these.
func (r *run) heldBy(id doc.ID, among map[doc.ID]bool) bool {
	for s := r.e.Doc.Shapes[r.e.Doc.Shapes[id].Parent]; s != nil; s = r.e.Doc.Shapes[s.Parent] {
		if among[s.ID] {
			return true
		}
	}
	return false
}

// setAll does what an op says to each of these shapes, and returns those it changed.
// When they were picked rather than named, each takes what it can, and it's only an
// error for none to take a field.
func (r *run) setAll(ids []doc.ID, o *op, broad bool) ([]doc.ID, error) {
	chosen := make(map[doc.ID]bool, len(ids))
	for _, id := range ids {
		chosen[id] = true
	}
	refused := map[string][]doc.ID{}
	why := map[string]error{}
	took := map[string]bool{}
	done := make([]doc.ID, 0, len(ids))
	for _, id := range ids {
		// A group that an earlier one left with too little has gone.
		if r.e.Doc.Shapes[id] == nil {
			continue
		}
		one := o
		if broad {
			if one = r.fit(id, o, took, refused, why); one == nil {
				continue
			}
		}
		// What holds it takes it along.
		if (one.DX != nil || one.DY != nil) && r.heldBy(id, chosen) {
			c := *one
			c.DX, c.DY = nil, nil
			one = &c
		}
		if err := r.setOne(id, one); err != nil {
			return nil, fmt.Errorf("%s: %w", shortID(id), err)
		}
		done = append(done, id)
	}
	for _, name := range said(&o.fields) {
		list := refused[name]
		switch {
		case len(list) == 0:
		case !took[name]:
			return nil, fmt.Errorf("none of the %d shapes takes %q: %w", len(ids), name, why[name])
		case name != "dx" && name != "dy": // an arrow that doesn't move goes with what it joins
			r.warnAt(rankLayout, "%s left out of what doesn't take it: %s", name, nameList(list))
		}
	}
	return done, nil
}

// fit returns an op with only what a shape takes of it, or nil if that's nothing,
// keeping count of who took and refused what.
func (r *run) fit(id doc.ID, o *op, took map[string]bool, refused map[string][]doc.ID, why map[string]error) *op {
	s := r.e.Doc.Shapes[id]
	c := *o
	kept := false
	v := reflect.ValueOf(&c.fields).Elem()
	for i := range v.NumField() {
		if v.Field(i).IsNil() {
			continue
		}
		name := v.Type().Field(i).Tag.Get("json")
		if err := r.refuses(s, o, name); err != nil {
			v.Field(i).SetZero()
			refused[name] = append(refused[name], id)
			why[name] = err
			continue
		}
		took[name] = true
		kept = true
	}
	if !kept {
		return nil
	}
	if c.D == nil && c.Fn == nil {
		c.path = nil
	}
	return &c
}
