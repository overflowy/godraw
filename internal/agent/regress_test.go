package agent

import (
	"bytes"
	"encoding/base64"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"godraw/internal/doc"
	"godraw/internal/editor"
	"godraw/internal/geo"
)

// boxDist is how far two boxes differ, at the worst of their sides.
func boxDist(a, b geo.Box) float64 {
	return max(math.Abs(a.X-b.X), math.Abs(a.Y-b.Y), math.Abs(a.W-b.W), math.Abs(a.H-b.H))
}

func TestSetMovesAndResizesBeforeItLocks(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"rectangle","id":"a","x":0,"y":0,"w":100,"h":100}]`)
	ops(t, e, `[{"set":"a","locked":true,"x":500,"w":300}]`)
	wantBox(t, e, "a", 500, 0, 300, 100)
	if !e.Doc.Shapes[shapeID("a")].Locked {
		t.Error("it isn't locked")
	}
	if _, err := tryOps(e, `[{"set":"a","x":0}]`); err == nil {
		t.Error("a locked shape took a move")
	}
	// The other way round too: what's unlocked by an op moves in it.
	ops(t, e, `[{"set":"a","locked":false,"x":40,"h":50}]`)
	wantBox(t, e, "a", 40, 0, 300, 50)
	if e.Doc.Shapes[shapeID("a")].Locked {
		t.Error("it's still locked")
	}
}

func TestSetOfEachThingALockCouldStop(t *testing.T) {
	for _, c := range []struct{ name, set string }{
		{"dx", `{"set":"a","locked":true,"dx":50}`},
		{"beside", `{"set":"a","locked":true,"right":"b","gap":0}`},
		{"turn", `{"set":"a","locked":true,"rot":90}`},
		{"size", `{"set":"a","locked":true,"h":300}`},
	} {
		e := newEditor()
		ops(t, e, `[{"add":"rectangle","id":"a","x":0,"y":0,"w":100,"h":100},{"add":"rectangle","id":"b","x":200,"y":0,"w":100,"h":100}]`)
		before := *e.Doc.Shapes[shapeID("a")]
		ops(t, e, `[`+c.set+`]`)
		s := e.Doc.Shapes[shapeID("a")]
		if !s.Locked {
			t.Errorf("%s: not locked", c.name)
		}
		if s.X == before.X && s.Rotation == before.Rotation && s.H == before.H {
			t.Errorf("%s: it answered ok, and did nothing", c.name)
		}
	}
}

func TestLockingAGroupAsItMoves(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"rectangle","id":"a","x":0,"y":0,"w":100,"h":100},{"add":"rectangle","id":"b","x":200,"y":0,"w":100,"h":100},{"group":["a","b"],"id":"g"}]`)
	ops(t, e, `[{"set":"g","locked":true,"dx":50}]`)
	if !e.Doc.Shapes[shapeID("g")].Locked {
		t.Error("the group isn't locked")
	}
	wantBox(t, e, "a", 50, 0, 100, 100)
}

func TestLockingACardAsItMoves(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"card","id":"c","title":"Title","body":"Body","x":0,"y":0,"w":200}]`)
	ops(t, e, `[{"set":"c","locked":true,"x":300}]`)
	if !e.Doc.Shapes[shapeID("c")].Locked {
		t.Error("the card isn't locked")
	}
	if b := boxOf(e, shapeID("c")); !near(b.X, 300) {
		t.Errorf("card at %s", boxText(b))
	}
}

func TestSetInLeavesNoEmptyGroup(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"frame","id":"f","x":500,"y":0,"w":400,"h":400},{"add":"rectangle","id":"a","x":0,"y":0,"w":100,"h":100},{"add":"rectangle","id":"b","x":200,"y":0,"w":100,"h":100},{"group":["a","b"],"id":"g"}]`)
	out := ops(t, e, `[{"set":"a","in":"f"}]`)
	if e.Doc.Shapes[shapeID("g")] != nil {
		t.Errorf("a group of one stayed:\n%s", out)
	}
	if s := e.Doc.Shapes[shapeID("b")]; s == nil || !doc.IsPageID(s.Parent) {
		t.Errorf("what was left is %+v", s)
	}
	wantBox(t, e, "b", 200, 0, 100, 100)
	wantBox(t, e, "a", 0, 0, 100, 100)
	// Both at once: the second finds the group gone.
	e = newEditor()
	ops(t, e, `[{"add":"frame","id":"f","x":500,"y":0,"w":400,"h":400},{"add":"rectangle","id":"a","x":0,"y":0,"w":100,"h":100},{"add":"rectangle","id":"b","x":200,"y":0,"w":100,"h":100},{"group":["a","b"],"id":"g"}]`)
	ops(t, e, `[{"set":["a","b"],"in":"f"}]`)
	if len(e.Children(shapeID("f"))) != 2 || e.Doc.Shapes[shapeID("g")] != nil {
		t.Errorf("frame holds %v", e.Children(shapeID("f")))
	}
	// And the group itself, named beside what empties it.
	e = newEditor()
	ops(t, e, `[{"add":"frame","id":"f","x":500,"y":0,"w":400,"h":400},{"add":"rectangle","id":"a","x":0,"y":0,"w":100,"h":100},{"add":"rectangle","id":"b","x":200,"y":0,"w":100,"h":100},{"group":["a","b"],"id":"g"}]`)
	ops(t, e, `[{"set":["a","g"],"in":"f"}]`)
	// Ordering what's left, of which the group is no longer any of it.
	e = newEditor()
	ops(t, e, `[{"add":"frame","id":"f","x":500,"y":0,"w":400,"h":400},{"add":"rectangle","id":"c","in":"f","x":510,"y":10},{"add":"rectangle","id":"a","x":0,"y":0,"w":100,"h":100},{"add":"rectangle","id":"b","x":200,"y":0,"w":100,"h":100},{"group":["a","b"],"id":"g"}]`)
	ops(t, e, `[{"set":["g","a"],"in":"f","over":"c"}]`)
}

func TestFlippingACardKeepsItsTitleInside(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"card","id":"c","title":"Title","body":"Body text","x":100,"y":100,"w":240}]`)
	card := boxOf(e, shapeID("c"))
	ops(t, e, `[{"flip":"c","how":"v"}]`)
	if after := boxOf(e, shapeID("c")); boxDist(after, card) > 0.01 {
		t.Errorf("flipping a card made %s of %s", boxText(after), boxText(card))
	}
	for _, part := range e.Descendants(shapeID("c")) {
		if b := boxOf(e, part); !card.Expand(0.01).ContainsBox(b) {
			t.Errorf("%s at %s is out of the card at %s", part, boxText(b), boxText(card))
		}
	}
}

func TestFailedBatchLeavesTheCamera(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"rectangle","id":"a","x":5000,"y":5000,"w":100,"h":100}]`)
	e.SetCam(editor.Camera{X: 1, Y: 2, Z: 0.5})
	before := e.Cam()
	if _, err := tryOps(e, `[{"camera":"fit"},{"set":"nope","x":0}]`); err == nil {
		t.Fatal("no error")
	}
	if e.Cam() != before {
		t.Errorf("a failed batch moved the camera to %+v", e.Cam())
	}
	if _, err := apply(e, mustOps(t, `[{"camera":"a"}]`), applyMode{dry: true}); err != nil {
		t.Fatal(err)
	}
	if e.Cam() != before {
		t.Errorf("a dry run moved the camera to %+v", e.Cam())
	}
	ops(t, e, `[{"camera":"fit"}]`)
	if e.Cam() == before {
		t.Error("the camera didn't move for one that worked")
	}
}

func TestFailedBatchLeavesTheFocusedGroup(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"rectangle","id":"a","x":0,"y":0},{"add":"rectangle","id":"b","x":200,"y":0},{"add":"rectangle","id":"c","x":400,"y":0},{"group":["a","b"],"id":"g"}]`)
	e.FocusedGroup = shapeID("g")
	if _, err := tryOps(e, `[{"set":"c","x":1},{"order":"c","how":"front"},{"set":"nope","x":0}]`); err == nil {
		t.Fatal("no error")
	}
	if e.FocusedGroup != shapeID("g") {
		t.Errorf("a failed batch left focus on %q", e.FocusedGroup)
	}
}

func TestFlipMirrorsAGeoShape(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"triangle","id":"t","x":0,"y":0,"w":200,"h":100}]`)
	s := e.Doc.Shapes[shapeID("t")]
	if apex := e.Geometry(s).Outline[0]; !near(apex.Y, 0) {
		t.Fatalf("apex at %v", apex)
	}
	ops(t, e, `[{"flip":"t","how":"v"}]`)
	wantBox(t, e, "t", 0, 0, 200, 100)
	s = e.Doc.Shapes[shapeID("t")]
	if apex := e.Geometry(s).Outline[0]; !near(apex.Y, 100) {
		t.Errorf("apex at %v after flipping", apex)
	}
}

func TestCrossesItselfIsTheEditorsAnswer(t *testing.T) {
	e := newEditor()
	// What a route's own check would reroute is what the warning tells of.
	wantWarnings(t, e, `[{"add":"rectangle","id":"a","x":0,"y":0,"w":100,"h":100},{"add":"rectangle","id":"m2","x":110,"y":150,"w":80,"h":100},{"add":"rectangle","id":"b","x":200,"y":300,"w":100,"h":100},{"add":"arrow","id":"s","from":"a","to":"b","kind":"elbow","fromAt":[1,0.5],"toAt":[0,0.5],"via":[[300,-80]]}]`, "s's route crosses itself; its via may be out of place")
}

// The body of a large call stays out of what answerUnknown keeps, which only ever
// needs a small one.
func TestAnswerUnknownKeepsLittleOfALargeBody(t *testing.T) {
	var kept int
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := new(bytes.Buffer)
		if _, err := buf.ReadFrom(r.Body); err != nil {
			t.Error(err)
		}
		kept = buf.Len()
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(notHandled + ": made/up"))
	})
	big := `{"jsonrpc":"2.0","id":7,"method":"made/up","params":{"pad":"` + base64.StdEncoding.EncodeToString(make([]byte, 4<<20)) + `"}}`
	rec := httptest.NewRecorder()
	answerUnknown(next).ServeHTTP(rec, httptest.NewRequest("POST", "/mcp", strings.NewReader(big)))
	if kept != len(big) {
		t.Errorf("the handler saw %d bytes of %d", kept, len(big))
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a call too long to answer got %d: %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	answerUnknown(next).ServeHTTP(rec, httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":7,"method":"made/up"}`)))
	if !strings.Contains(rec.Body.String(), "method not found") {
		t.Errorf("a short call got %d: %s", rec.Code, rec.Body)
	}

	var c capped
	if _, err := c.Write(make([]byte, maxUnknownBody+1)); err != nil {
		t.Fatal(err)
	}
	if !c.over || c.buf.Len() > maxUnknownBody {
		t.Errorf("kept %d bytes, over %v", c.buf.Len(), c.over)
	}
}
