package editor

import (
	"bytes"
	"math/rand/v2"
	"reflect"
	"runtime"
	"slices"
	"testing"

	"godraw/internal/doc"
	"godraw/internal/geo"
	"godraw/internal/stress"
)

// paintAll derives what a frame would of each shape.
func paintAll(e *Editor) {
	for _, s := range e.Doc.Shapes {
		e.Geometry(s)
		e.Display(s, Light)
	}
}

// holdsNothing says that a record keeps nothing it derived.
func holdsNothing(s *doc.Shape) bool {
	c, ok := s.Cache.(*shapeCache)
	return !ok || reflect.DeepEqual(*c, shapeCache{})
}

// History keeps old records without what they'd draw: a recolor of everything would
// otherwise hold on to the whole page's drawing, once for each step of undo.
func TestHistoryLetsGoOfCaches(t *testing.T) {
	h := newHarness(t)
	e := h.Editor
	id := h.rect(100, 100, 80, 60).ID
	was := e.Doc.Shapes[id]
	paintAll(e)
	drawn := e.Display(was, Light)
	if holdsNothing(was) {
		t.Fatal("painting cached nothing")
	}

	// An edit: the record it replaces goes to history without its cache.
	e.Select(id)
	e.SetStyle(StyleColor, "blue")
	blue := e.Doc.Shapes[id]
	if blue == was {
		t.Fatal("the recolor changed nothing")
	}
	if !holdsNothing(was) {
		t.Error("a replaced record kept its cache")
	}
	paintAll(e)
	if !holdsNothing(was) {
		t.Error("painting brought the cache of a record in history back")
	}

	// Undo: the record leaving goes without its cache, and the one back draws as it did.
	e.Undo()
	if e.Doc.Shapes[id] != was {
		t.Fatal("undo didn't bring the record back")
	}
	if !holdsNothing(blue) {
		t.Error("a record undone kept its cache")
	}
	if got := e.Display(was, Light); !reflect.DeepEqual(got, drawn) {
		t.Errorf("after undo it draws %v, was %v", got, drawn)
	}

	// Redo the same way.
	e.Redo()
	if e.Doc.Shapes[id] != blue {
		t.Fatal("redo didn't bring the record back")
	}
	if !holdsNothing(was) {
		t.Error("a record redone over kept its cache")
	}
	paintAll(e)

	// Gone from the page, it goes without its cache too.
	e.Select(id)
	e.Delete()
	if !holdsNothing(blue) {
		t.Error("a deleted record kept its cache")
	}
	e.Undo()
	paintAll(e)
	if holdsNothing(blue) {
		t.Error("a record brought back derived nothing")
	}
}

// A record that only moved shares its cache with what follows it: its drawing stays
// valid, undoing a move costs nothing, and arrows know it for the same form.
func TestMovedRecordsKeepTheirCache(t *testing.T) {
	h := newHarness(t)
	e := h.Editor
	id := h.rect(100, 100, 80, 60).ID
	paintAll(e)
	was := e.Doc.Shapes[id]
	cache := was.Cache
	e.Select(id)
	e.Nudge(geo.V(10, 0))
	moved := e.Doc.Shapes[id]
	if moved == was {
		t.Fatal("the nudge changed nothing")
	}
	if was.Cache != cache || moved.Cache != cache || holdsNothing(moved) {
		t.Errorf("a move changed the cache: %p and %p, was %p", was.Cache, moved.Cache, cache)
	}
	e.Undo()
	if was.Cache != cache || moved.Cache != cache {
		t.Errorf("undoing a move changed the cache: %p and %p, was %p", was.Cache, moved.Cache, cache)
	}
	e.Redo()
	if was.Cache != cache || moved.Cache != cache {
		t.Errorf("redoing a move changed the cache: %p and %p, was %p", was.Cache, moved.Cache, cache)
	}
}

// Older records that only moved share the cache of the one that's replaced: they
// let go of it together, or a move before each edit would keep all of them.
func TestMovedRecordsLetGoTogether(t *testing.T) {
	h := newHarness(t)
	e := h.Editor
	id := h.rect(100, 100, 80, 60).ID
	paintAll(e)
	was := e.Doc.Shapes[id]
	e.Select(id)
	e.Nudge(geo.V(10, 0))
	moved := e.Doc.Shapes[id]
	if holdsNothing(moved) {
		t.Fatal("a move lost the cache")
	}
	e.SetStyle(StyleColor, "blue")
	if !holdsNothing(moved) || !holdsNothing(was) {
		t.Errorf("records in history kept a cache: moved %v, older %v", !holdsNothing(moved), !holdsNothing(was))
	}
	if was.Cache != moved.Cache {
		t.Error("the records that only moved no longer share a cache")
	}
	e.Undo()
	e.Undo()
	paintAll(e)
	if e.Doc.Shapes[id] != was || holdsNothing(was) {
		t.Error("undoing back didn't derive it again")
	}
}

// liveHeap returns the bytes in use after a full collection.
func liveHeap() uint64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// Steps of undo cost little, as recolors of everything.
func TestHistoryHeapGrowsSlowly(t *testing.T) {
	const steps = 20
	for _, moves := range []bool{false, true} {
		edits := float64(steps)
		if moves {
			edits *= 2
		}
		e := New(stress.Diagram(1000), fakeMeasure)
		paintAll(e)
		before := liveHeap()
		for i := range steps {
			e.SelectAll()
			e.SetStyle(StyleColor, []string{"blue", "red"}[i%2])
			paintAll(e)
			if moves {
				e.Nudge(geo.V(1, 0))
				paintAll(e)
			}
		}
		after := liveHeap()
		grew := (float64(after) - float64(before)) / (1 << 20) / edits
		t.Logf("moves %v: %.2f MB a step", moves, grew)
		if grew > 2 {
			t.Errorf("moves %v: %.2f MB a step of undo", moves, grew)
		}
		runtime.KeepAlive(e)
	}
}

// Dropped caches change nothing that shows: whatever undo and redo bring back,
// arrows and shapes are what a new editor would make of the same records.
func TestUndoShowsWhatANewEditorWould(t *testing.T) {
	h := newHarness(t)
	e := h.Editor
	var boxes []doc.ID
	for i := range 4 {
		boxes = append(boxes, h.rect(100+float64(i)*250, 100, 80, 80).ID)
	}
	var arrows []doc.ID
	for i := range 3 {
		e.SetTool(ToolArrow)
		x := 140 + float64(i)*250
		h.drag(x, 140, x+250, 140)
		arrows = append(arrows, e.Selection()[0])
	}
	if len(e.Doc.Bindings) == 0 {
		t.Fatal("the arrows aren't bound")
	}
	check := func(step string) {
		t.Helper()
		paintAll(e)
		d := e.Doc.Snapshot()
		for id, s := range d.Shapes {
			d.Shapes[id] = s.Clone()
		}
		fresh := New(d, e.Measure)
		for id, s := range e.Doc.Shapes {
			if got, want := e.Geometry(s), fresh.Geometry(d.Shapes[id]); !reflect.DeepEqual(got, want) {
				t.Errorf("%s: %s has geometry %v, want %v", step, id, got, want)
			}
			if got, want := e.Display(s, Light), fresh.Display(d.Shapes[id], Light); !reflect.DeepEqual(got, want) {
				t.Errorf("%s: %s draws %v, want %v", step, id, got, want)
			}
		}
		for _, id := range arrows {
			if s := e.Doc.Shapes[id]; s != nil {
				if got, want := e.Arrow(s), fresh.Arrow(d.Shapes[id]); !reflect.DeepEqual(got, want) {
					t.Errorf("%s: arrow %s lies as %+v, want %+v", step, id, got, want)
				}
			}
		}
	}
	// Undo goes no further back than this, for the shapes to stay.
	first := len(e.undo)
	r := rand.New(rand.NewPCG(1, 2))
	some := func() {
		list := []doc.ID{boxes[r.IntN(len(boxes))]}
		if r.IntN(2) == 0 {
			list = append(list, boxes[r.IntN(len(boxes))], arrows[r.IntN(len(arrows))])
		}
		// Redo may delete some again.
		e.Select(slices.DeleteFunc(list, func(id doc.ID) bool { return e.Doc.Shapes[id] == nil })...)
	}
	check("at first")
	for range 300 {
		switch r.IntN(7) {
		case 0:
			some()
			e.SetStyle(StyleColor, []string{"blue", "red", "green"}[r.IntN(3)])
			check("a recolor")
		case 1:
			// A new form, which arrows bound to it end elsewhere for.
			if s := e.Doc.Shapes[boxes[r.IntN(len(boxes))]]; s != nil {
				e.transact(func() {
					c := s.Clone()
					c.W, c.H = 40+float64(r.IntN(120)), 40+float64(r.IntN(120))
					e.put(c)
				})
			}
			check("a resize")
		case 2:
			some()
			e.Nudge(geo.V(float64(r.IntN(40)-20), float64(r.IntN(40)-20)))
			check("a nudge")
		case 3:
			if id := arrows[r.IntN(len(arrows))]; e.Doc.Shapes[id] != nil {
				e.Select(id)
				e.SetStyle(StyleColor, []string{"blue", "red", "green"}[r.IntN(3)])
			}
			check("an arrow's recolor")
		case 4:
			some()
			e.Delete()
			check("a delete")
			e.Undo()
			check("a delete undone")
		case 5:
			for range r.IntN(4) + 1 {
				if len(e.undo) > first {
					e.Undo()
				}
			}
			check("an undo")
		case 6:
			for range r.IntN(4) + 1 {
				e.Redo()
			}
			check("a redo")
		}
	}
}

// Records go on being written elsewhere, as snapshots save, while history lets go of
// their caches here: under the race detector, the two never meet. And they compare
// as they did, by which records they are.
func TestDroppingCachesLeavesSnapshotsAlone(t *testing.T) {
	e := New(stress.Diagram(200), fakeMeasure)
	paintAll(e)
	first := e.Doc.Snapshot()
	saves := make(chan *doc.Drawing, 8)
	done := make(chan error, 1)
	go func() {
		for d := range saves {
			if err := doc.Write(&bytes.Buffer{}, d); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	e.OnChange = func(d *doc.Drawing) { saves <- d }
	steps := 0
	for i := range 20 {
		e.SelectAll()
		e.SetStyle(StyleColor, []string{"blue", "red"}[i%2])
		e.SelectNone()
		paintAll(e)
		steps++
		if doc.Equal(first, e.Doc.Snapshot()) {
			t.Errorf("step %d compares as the first", i)
		}
	}
	for range steps {
		e.Undo()
		paintAll(e)
	}
	close(saves)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !doc.Equal(first, e.Doc.Snapshot()) {
		t.Error("undoing everything doesn't compare as the first")
	}
}
