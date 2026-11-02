package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"godraw/internal/doc"
	"godraw/internal/editor"
	"godraw/internal/geo"
)

// fakeMeasure lays text out in a fixed-width font, so that tests do not depend
// on the fonts of the machine.
func fakeMeasure(text, font string, size, width float64) editor.TextBlock {
	cw := size * 0.5
	line := size * editor.LineHeight
	b := editor.TextBlock{Line: line}
	for i, l := range strings.Split(text, "\n") {
		w := float64(len(l)) * cw
		b.Lines = append(b.Lines, editor.TextLine{Text: l, Y: float64(i) * line, W: w})
		b.W = math.Max(b.W, w)
	}
	b.H = float64(len(b.Lines)) * line
	return b
}

func newEditor() *editor.Editor { return editor.New(doc.New(), fakeMeasure) }

func tryOps(e *editor.Editor, ops string) (string, error) {
	list, err := decodeOps(json.RawMessage(ops))
	if err != nil {
		return "", err
	}
	return apply(e, list, false)
}

func ops(t *testing.T, e *editor.Editor, list string) string {
	t.Helper()
	out, err := tryOps(e, list)
	if err != nil {
		t.Fatalf("%s: %v", list, err)
	}
	return out
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func wantBox(t *testing.T, e *editor.Editor, id string, x, y, w, h float64) {
	t.Helper()
	b := boxOf(e, shapeID(id))
	if !near(b.X, x) || !near(b.Y, y) || !near(b.W, w) || !near(b.H, h) {
		t.Errorf("%s at %s, want %v,%v %vx%v", id, boxText(b), x, y, w, h)
	}
}

// Everything the guide shows must work as it says.
func TestGuideExamplesRun(t *testing.T) {
	example := regexp.MustCompile(`\[\{.*\}\]`)
	found := 0
	for topic, text := range guideTopics {
		for _, l := range strings.Split(text, "\n") {
			m := example.FindString(l)
			if m == "" {
				continue
			}
			found++
			if _, err := tryOps(newEditor(), m); err != nil {
				t.Errorf("guide %q: %s: %v", topic, m, err)
			}
		}
	}
	if found < 5 {
		t.Errorf("only %d examples found", found)
	}
}

// The guide must name every field and every shape there is, so that nothing
// can only be found by accident.
func TestGuideCoversEverything(t *testing.T) {
	var all strings.Builder
	for _, text := range guideTopics {
		all.WriteString(text)
		all.WriteByte('\n')
	}
	guideText := all.String()
	for kind, list := range addFields {
		if kind != doc.TypeGeo && !strings.Contains(guideText, kind+":") {
			t.Errorf("guide lacks %s", kind)
		}
		for _, name := range strings.Fields(list) {
			if !strings.Contains(guideText, name) {
				t.Errorf("guide lacks field %s of %s", name, kind)
			}
		}
	}
	for _, name := range strings.Fields(placeFields) {
		if !strings.Contains(guideText, name) {
			t.Errorf("guide lacks %s", name)
		}
	}
	for name, enum := range styleEnums {
		for _, v := range enum {
			if !strings.Contains(guideText, v) {
				t.Errorf("guide lacks %s of %s", v, name)
			}
		}
	}
}

func TestErrorChangesNothing(t *testing.T) {
	e := newEditor()
	changes := 0
	e.OnChange = func(*doc.Drawing) { changes++ }
	ops(t, e, `[{"add":"rectangle","id":"a","x":0,"y":0}]`)
	changes = 0
	_, err := tryOps(e, `[{"add":"ellipse","id":"b"},{"set":"a","color":"red"},{"page":"Other"},{"add":"star","id":"a"}]`)
	if err == nil || !strings.Contains(err.Error(), `ops[3]`) || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("got %v", err)
	}
	if e.Doc.Shapes["shape:b"] != nil || len(e.Pages()) != 1 || e.Doc.Shapes["shape:a"].Color != "black" {
		t.Error("a failed batch left something behind")
	}
	if changes != 0 {
		t.Errorf("%d changes told", changes)
	}
	e.Undo()
	if len(e.Doc.Shapes) != 0 {
		t.Error("a failed batch became a step of undo")
	}
}

func TestBatchIsOneUndoStep(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"rectangle","id":"a"},{"add":"star","id":"b","right":"a"},{"add":"arrow","from":"a","to":"b"},{"group":["a","b"],"id":"g"},{"flip":"g","how":"h"},{"dup":"g"}]`)
	if len(e.Doc.Shapes) < 5 {
		t.Fatalf("%d shapes", len(e.Doc.Shapes))
	}
	e.Undo()
	if len(e.Doc.Shapes) != 0 || len(e.Doc.Bindings) != 0 {
		t.Errorf("undo left %d shapes, %d bindings", len(e.Doc.Shapes), len(e.Doc.Bindings))
	}
	if e.CanUndo() {
		t.Error("more than one step")
	}
	e.Redo()
	if e.Doc.Shapes["shape:g"] == nil {
		t.Error("redo didn't bring it back")
	}
}

func TestWaitsForGestures(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"rectangle","id":"a","x":0,"y":0,"w":100,"h":100,"fill":"solid"}]`)
	e.SetCam(editor.Camera{Z: 1})
	e.PointerDown(50, 50, 0, 1, editor.Mods{}, 0)
	e.PointerMove(80, 80, editor.Mods{}, 0.1)
	if _, err := tryOps(e, `[{"del":"a"}]`); err != editor.ErrBusy {
		t.Fatalf("mid-gesture: %v", err)
	}
	e.PointerUp(80, 80, editor.Mods{}, 0.2)
	ops(t, e, `[{"del":"a"}]`)

	ops(t, e, `[{"add":"note","id":"n"}]`)
	e.StartEditing("shape:n")
	if _, err := tryOps(e, `[{"add":"star"}]`); err != editor.ErrBusy {
		t.Fatalf("mid-typing: %v", err)
	}
	if e.Editing != "shape:n" {
		t.Error("the batch ended the user's typing")
	}
}

func TestSelectionStaysTheUsers(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"rectangle","id":"a"},{"add":"rectangle","id":"b"}]`)
	e.Select("shape:a", "shape:b")
	ops(t, e, `[{"add":"star","id":"c"},{"group":["c","a"],"id":"g"},{"ungroup":"g"},{"del":"b"}]`)
	sel := e.Selection()
	if len(sel) != 1 || sel[0] != "shape:a" {
		t.Errorf("selection is %v", sel)
	}
}

func TestPlacing(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"rectangle","id":"a","x":100,"y":100,"w":200,"h":100},
		{"add":"rectangle","id":"r","right":"a","w":100,"h":50},
		{"add":"rectangle","id":"l","left":"a","gap":10,"w":100,"h":50},
		{"add":"rectangle","id":"b","below":"a","w":100,"h":50},
		{"add":"rectangle","id":"t","above":"a","gap":0,"x":0,"w":100,"h":50}]`)
	wantBox(t, e, "r", 420, 125, 100, 50)
	wantBox(t, e, "l", -10, 125, 100, 50)
	wantBox(t, e, "b", 150, 320, 100, 50)
	wantBox(t, e, "t", 0, 50, 100, 50)

	f := newEditor()
	ops(t, f, `[{"add":"rectangle","id":"a","w":100,"h":100},{"add":"rectangle","id":"b","w":100,"h":100}]`)
	wantBox(t, f, "a", 0, 0, 100, 100)
	wantBox(t, f, "b", 0, 140, 100, 100)
}

func TestCoordinatesAreThePages(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"frame","id":"f","x":300,"y":200,"w":400,"h":300,"rot":30},
		{"add":"rectangle","id":"a","in":"f","x":350,"y":250,"w":60,"h":40},
		{"add":"draw","id":"d","in":"f","d":"M350 400 L450 400"},
		{"add":"line","id":"l","in":"f","d":"M350 450 450 450"}]`)
	// In a frame that is turned, what goes in takes its turn, around its own
	// center, and x and y still say where in the page.
	wantBox(t, e, "a", 350, 250, 60, 40)
	if !near(e.PageMat("shape:a").Rotation(), 30*math.Pi/180) {
		t.Error("the shape didn't take the frame's turn")
	}
	if b := e.PageBounds("shape:d"); !near(b.X, 350) || !near(b.W, 100) || !near(b.Y, 400) {
		t.Errorf("path in a frame at %s", boxText(b))
	}
	if b := e.PageBounds("shape:l"); !near(b.X, 350) || !near(b.Y, 450) || !near(b.W, 100) {
		t.Errorf("line in a frame at %s", boxText(b))
	}
	if !near(e.PageMat("shape:d").Rotation(), 0) {
		t.Error("a path takes the page's axes, not the frame's turn")
	}
}

// What read says of a shape can be set back without moving it.
func TestBoxRoundTrips(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"rectangle","id":"a","x":100,"y":50,"w":200,"h":80,"rot":45},
		{"add":"draw","id":"d","d":"M10 10 C40 80 80 -80 120 10","x":300,"y":300,"rot":20},
		{"add":"text","id":"t","text":"hello","x":0,"y":500,"rot":-10}]`)
	wantBox(t, e, "a", 100, 50, 200, 80)
	for _, id := range []string{"a", "d", "t"} {
		before := boxOf(e, shapeID(id))
		ops(t, e, `[{"set":"`+id+`","x":`+num(before.X)+`,"y":`+num(before.Y)+`}]`)
		after := boxOf(e, shapeID(id))
		if math.Abs(after.X-before.X) > 0.1 || math.Abs(after.Y-before.Y) > 0.1 {
			t.Errorf("%s moved: %s to %s", id, boxText(before), boxText(after))
		}
	}
	// Turning keeps the center.
	c := boxOf(e, "shape:a").Center()
	ops(t, e, `[{"set":"a","rot":90}]`)
	if got := boxOf(e, "shape:a").Center(); got.Dist(c) > 0.01 {
		t.Errorf("center went from %v to %v", c, got)
	}
}

func TestLabelsFit(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"rectangle","id":"a","text":"short"},{"add":"rectangle","id":"b","text":"a label far longer than a box would be by default, if it had one"},{"add":"rectangle","id":"c","w":100,"h":20,"text":"grows"}]`)
	if s := e.Doc.Shapes["shape:b"]; s.W <= e.Doc.Shapes["shape:a"].W || s.W > 360 {
		t.Errorf("label width gave w=%v", s.W)
	}
	if s := e.Doc.Shapes["shape:c"]; s.W != 100 || s.GrowY <= 0 {
		t.Errorf("a label too big for its box gave %v+%v", s.H, s.GrowY)
	}
}

// wantWarnings runs ops and checks that the warnings they get include each of
// want, and no others if none is wanted.
func wantWarnings(t *testing.T, e *editor.Editor, list string, want ...string) string {
	t.Helper()
	out := ops(t, e, list)
	_, warnings, found := strings.Cut(out, "warnings:\n")
	if len(want) == 0 && found {
		t.Errorf("%s: unwanted warnings:\n%s", list, warnings)
	}
	for _, w := range want {
		if !strings.Contains(warnings, w) {
			t.Errorf("%s: no warning %q in:\n%s", list, w, out)
		}
	}
	return out
}

// What the feedback that led to warnings ran into: a box that grows into the
// next one, covering it.
func TestWarnsOfGrowth(t *testing.T) {
	e := newEditor()
	wantWarnings(t, e, `[{"add":"rectangle","id":"keycloak","x":0,"y":0,"w":200,"h":90,"text":"one\ntwo\nthree"},{"add":"rectangle","id":"metabase","x":0,"y":110,"w":200,"h":90,"text":"bi"}]`,
		"keycloak grew 90→121", "metabase overlaps keycloak by 200x11")
	// Further in, it reaches the text.
	wantWarnings(t, newEditor(), `[{"add":"rectangle","id":"keycloak","x":0,"y":0,"w":200,"h":90,"text":"one\ntwo\nthree\nfour"},{"add":"rectangle","id":"metabase","x":0,"y":100,"w":200,"h":90,"text":"bi"}]`,
		"metabase covers keycloak's text")

	// Later changes count, and only growth does.
	e = newEditor()
	wantWarnings(t, e, `[{"add":"rectangle","id":"a","x":0,"y":0,"w":200,"h":90,"text":"one"}]`)
	wantWarnings(t, e, `[{"set":"a","text":"one\ntwo\nthree\nfour"}]`, "a grew 90→")
	wantWarnings(t, e, `[{"set":"a","color":"blue"}]`)

	// Without h, fitting is what was asked for.
	wantWarnings(t, newEditor(), `[{"add":"rectangle","w":200,"text":"one\ntwo\nthree\nfour"}]`)
}

func TestWarnsOfCoveredText(t *testing.T) {
	e := newEditor()
	wantWarnings(t, e, `[{"add":"rectangle","id":"a","x":0,"y":0,"w":300,"h":100,"text":"label"},{"add":"rectangle","id":"lid","x":100,"y":20,"w":100,"h":60,"fill":"solid"}]`,
		"lid covers a's text")
	// Painted under it, the text still shows.
	wantWarnings(t, newEditor(), `[{"add":"rectangle","id":"under","x":100,"y":20,"w":100,"h":60,"fill":"solid"},{"add":"rectangle","id":"a","x":0,"y":0,"w":300,"h":100,"text":"label"}]`)
	// An outline around a label hides nothing, but a fill does.
	wantWarnings(t, newEditor(), `[{"add":"rectangle","x":50,"y":50,"w":100,"h":80,"text":"a"},{"add":"rectangle","x":0,"y":0,"w":300,"h":300}]`)
	wantWarnings(t, newEditor(), `[{"add":"rectangle","id":"a","x":50,"y":50,"w":100,"h":80,"text":"a"},{"add":"rectangle","id":"sheet","x":0,"y":0,"w":300,"h":300,"fill":"solid"}]`,
		"sheet covers a's text")
}

// Warnings that cried wolf on every drawing would soon go unread.
func TestWarnsOfNothingMeant(t *testing.T) {
	// An illustration: shapes without text overlap as they like.
	wantWarnings(t, newEditor(), `[{"add":"rectangle","x":0,"y":100,"w":200,"h":120,"fill":"solid"},{"add":"triangle","x":-20,"y":40,"w":240,"h":80,"fill":"solid"},{"add":"ellipse","x":150,"y":-30,"w":90,"h":90,"fill":"fill"}]`)
	// A box wholly inside a container, whose title is clear of it.
	wantWarnings(t, newEditor(), `[{"add":"rectangle","id":"api","x":0,"y":0,"w":400,"h":300,"text":"API","valign":"start"},{"add":"rectangle","id":"h","x":40,"y":100,"w":160,"h":80,"text":"handler"}]`)
	// A card: text on an empty box.
	wantWarnings(t, newEditor(), `[{"add":"rectangle","id":"b","x":0,"y":0,"w":240,"h":100},{"add":"text","id":"h","text":"Head","x":16,"y":8},{"add":"text","id":"c","text":"Body","size":"s","x":16,"y":56},{"group":["b","h","c"],"id":"card"}]`)
	// Boxes that only touch.
	wantWarnings(t, newEditor(), `[{"add":"rectangle","x":0,"y":0,"w":100,"h":100,"text":"a"},{"add":"rectangle","x":100,"y":0,"w":100,"h":100,"text":"b"}]`)
	// What was there before stays the user's business.
	e := newEditor()
	wantWarnings(t, e, `[{"add":"rectangle","id":"a","x":0,"y":0,"w":100,"h":100,"text":"a"},{"add":"rectangle","id":"b","x":80,"y":0,"w":100,"h":100,"text":"b"}]`, "b overlaps a")
	wantWarnings(t, e, `[{"add":"rectangle","id":"c","x":500,"y":0,"w":100,"h":100,"text":"c"}]`)
	// Turned shapes are not in the boxes they report.
	wantWarnings(t, newEditor(), `[{"add":"rectangle","id":"a","x":0,"y":0,"w":200,"h":80,"text":"a","rot":45},{"add":"rectangle","x":0,"y":-60,"w":60,"h":80,"text":"b"}]`)
}

func TestWarnsOfArrows(t *testing.T) {
	boxes := `{"add":"rectangle","id":"a","x":0,"y":0,"w":100,"h":100,"text":"a"},{"add":"rectangle","id":"b","x":120,"y":0,"w":100,"h":100,"text":"b"}`
	wantWarnings(t, newEditor(), `[`+boxes+`,{"add":"arrow","id":"s","from":"a","to":"b"}]`, "s is only")
	wantWarnings(t, newEditor(), `[{"add":"rectangle","id":"a","x":0,"y":0,"w":100,"h":100},{"add":"rectangle","id":"b","x":60,"y":0,"w":100,"h":100},{"add":"arrow","id":"s","from":"a","to":"b"}]`, "s doesn't show")

	far := `{"add":"rectangle","id":"a","x":0,"y":0,"w":100,"h":100,"text":"a"},{"add":"rectangle","id":"b","x":200,"y":0,"w":100,"h":100,"text":"b"}`
	wantWarnings(t, newEditor(), `[`+far+`,{"add":"arrow","id":"s","from":"a","to":"b","text":"a label longer"}]`, "s's label leaves")
	// Off its line, it doesn't hide it.
	e := newEditor()
	wantWarnings(t, e, `[`+far+`,{"add":"arrow","id":"s","from":"a","to":"b","text":"a label longer","label":"above"}]`)
	lb := e.ArrowLabelBox(e.Doc.Shapes["shape:s"])
	if lb.MaxY() >= 50-1 {
		t.Errorf("label above the line at y 50 ends at %v", lb.MaxY())
	}

	wantWarnings(t, newEditor(), `[{"add":"rectangle","id":"a","x":0,"y":0,"w":300,"h":100,"text":"a label that reaches the edge"},{"add":"rectangle","id":"b","x":340,"y":0,"w":100,"h":100,"text":"b"},{"add":"arrow","id":"s","from":"a","to":"b","text":"a label too wide for the gap"}]`, "s's label covers a's text")

	wantWarnings(t, newEditor(), `[`+far+`,{"add":"arrow","id":"s","from":"a","to":"b","fromAt":[0.5,0.5],"toAt":[0.5,0.53]}]`, "s is 1.3 off level")

	// One in its way, unless it starts inside: that's leaving a container.
	wantWarnings(t, newEditor(), `[{"add":"rectangle","id":"a","x":0,"y":0,"w":100,"h":100},{"add":"rectangle","id":"c","x":200,"y":0,"w":100,"h":100,"text":"c"},{"add":"rectangle","id":"b","x":400,"y":0,"w":100,"h":100},{"add":"arrow","id":"s","from":"a","to":"b"}]`, "s runs through c")
	wantWarnings(t, newEditor(), `[{"add":"rectangle","id":"api","x":0,"y":0,"w":400,"h":300,"text":"API","valign":"start"},{"add":"rectangle","id":"in","x":40,"y":100,"w":100,"h":100},{"add":"rectangle","id":"out","x":600,"y":100,"w":100,"h":100},{"add":"arrow","from":"in","to":"out"}]`)

	// Moving what an arrow joins counts as touching the arrow.
	e = newEditor()
	wantWarnings(t, e, `[`+far+`,{"add":"arrow","id":"s","from":"a","to":"b"}]`)
	wantWarnings(t, e, `[{"set":"b","x":120}]`, "s is only")
}

func TestWarnsOfWhatFramesClip(t *testing.T) {
	const add = `[{"add":"frame","id":"f","x":0,"y":0,"w":200,"h":100},{"add":"rectangle","id":"a","in":"f","x":150,"y":10,"w":100,"h":50}]`
	wantWarnings(t, newEditor(), add, "a sticks out of frame f")
	wantWarnings(t, newEditor(), `[{"add":"frame","id":"f","x":0,"y":0,"w":200,"h":100},{"add":"rectangle","id":"a","in":"f","x":50,"y":10,"w":100,"h":50}]`)
	// An arrow in and out of a frame goes on the page, where nothing clips it.
	e := newEditor()
	wantWarnings(t, e, `[{"add":"frame","id":"f","x":200,"y":0,"w":200,"h":100},{"add":"rectangle","id":"a","in":"f","x":250,"y":10,"w":100,"h":50},{"add":"rectangle","id":"b","x":0,"y":10,"w":100,"h":50},{"add":"arrow","id":"s","from":"b","to":"a"}]`)
	if got := e.Doc.Shapes["shape:s"].Parent; got != e.Page {
		t.Errorf("arrow across a frame's edge is in %v", got)
	}
}

func TestWarningsStayShort(t *testing.T) {
	var list []string
	for range 100 {
		list = append(list, `{"add":"rectangle","x":0,"y":0,"w":100,"h":100,"text":"same"}`)
	}
	out := ops(t, newEditor(), "["+strings.Join(list, ",")+"]")
	_, warnings, _ := strings.Cut(out, "warnings:\n")
	if n := strings.Count(warnings, "\n"); n > maxWarnings+1 || !strings.HasSuffix(warnings, "and more") {
		t.Errorf("%d warnings:\n%s", n, warnings)
	}
}

func TestDryRun(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"rectangle","id":"a","x":0,"y":0,"w":100,"h":90,"text":"a"}]`)
	cam := e.Cam()
	shapes, undo := len(e.Doc.Shapes), e.CanUndo()
	before := e.Doc.Shapes["shape:a"]
	list, err := decodeOps(json.RawMessage(`[{"page":"Other"},{"add":"star","id":"new"},{"page":"Page 1"},{"set":"a","text":"one\ntwo\nthree\nfour","x":500},{"camera":"fit"},{"del":"a"}]`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := apply(e, list, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "dry run, nothing changed") || !strings.Contains(out, "new 0,0") {
		t.Errorf("answer %q", out)
	}
	if len(e.Doc.Shapes) != shapes || e.Doc.Shapes["shape:a"] != before || len(e.Pages()) != 1 || e.Cam() != cam || e.CanUndo() != undo {
		t.Error("a dry run changed the drawing")
	}
	e.Undo()
	if e.Doc.Shapes["shape:a"] != nil {
		t.Error("a dry run took a step of undo")
	}

	// It measures what it would make, warnings and all.
	e = newEditor()
	list, _ = decodeOps(json.RawMessage(`[{"add":"rectangle","id":"a","x":0,"y":0,"w":100,"h":40,"text":"one\ntwo"}]`))
	if out, err = apply(e, list, true); err != nil || !strings.Contains(out, "a 0,0 100x") || !strings.Contains(out, "a grew 40→") {
		t.Errorf("%q, %v", out, err)
	}
	if len(e.Doc.Shapes) != 0 {
		t.Error("left shapes behind")
	}
}

func TestStraightArrows(t *testing.T) {
	line := func(e *editor.Editor, id string) (geo.Vec, geo.Vec) {
		s := e.Doc.Shapes[shapeID(id)]
		info, m := e.Arrow(s), e.PageMat(s.ID)
		return m.Apply(info.Start), m.Apply(info.End)
	}
	level := func(t *testing.T, e *editor.Editor, id string, y float64) {
		t.Helper()
		a, b := line(e, id)
		if !near(a.Y, b.Y) || !near(a.Y, y) {
			t.Errorf("%s runs from %v to %v, want level at %v", id, a, b, y)
		}
	}
	e := newEditor()
	wantWarnings(t, e, `[{"add":"rectangle","id":"a","x":0,"y":0,"w":100,"h":200},{"add":"rectangle","id":"b","x":300,"y":100,"w":100,"h":200},{"add":"arrow","id":"s","from":"a","to":"b","straight":"h"},{"add":"arrow","id":"low","from":"b","to":"a","straight":"h","at":0.75}]`)
	level(t, e, "s", 150) // the middle of the 100 to 200 they share
	level(t, e, "low", 175)

	// A later apply moves or grows what it joins, and it stays level.
	wantWarnings(t, e, `[{"set":"a","h":240}]`)
	level(t, e, "s", 170)
	wantWarnings(t, e, `[{"set":"b","text":"one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine","dy":-50}]`, "b grew 200→")
	if a, b := line(e, "s"); !near(a.Y, b.Y) {
		t.Errorf("after growing: %v to %v", a, b)
	}

	// Its grouped target moves, and it follows.
	wantWarnings(t, e, `[{"group":["a","low"],"id":"g"}]`)
	wantWarnings(t, e, `[{"set":"g","dy":10}]`)
	if a, b := line(e, "s"); !near(a.Y, b.Y) {
		t.Errorf("after moving the group: %v to %v", a, b)
	}

	// Vertical, the same.
	e = newEditor()
	ops(t, e, `[{"add":"rectangle","id":"a","x":0,"y":0,"w":200,"h":100},{"add":"rectangle","id":"b","x":100,"y":300,"w":200,"h":100},{"add":"arrow","id":"s","from":"a","to":"b","straight":"v"}]`)
	if a, b := line(e, "s"); !near(a.X, b.X) || !near(a.X, 150) {
		t.Errorf("v runs from %v to %v", a, b)
	}

	// Nothing shared: it says so, and the batch still goes through.
	e = newEditor()
	wantWarnings(t, e, `[{"add":"rectangle","id":"a","x":0,"y":0,"w":100,"h":100},{"add":"rectangle","id":"b","x":300,"y":400,"w":100,"h":100},{"add":"arrow","id":"s","from":"a","to":"b","straight":"h"}]`, "s can't be level: a and b share no height")

	// Saved and read back, it's still straight.
	var buf bytes.Buffer
	if err := doc.Write(&buf, e.Doc); err != nil {
		t.Fatal(err)
	}
	d, err := doc.Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	var axis string
	if !d.Shapes["shape:s"].MetaValue(doc.MetaStraight, &axis) || axis != "h" {
		t.Errorf("straight back from the file is %q", axis)
	}

	// Asking for anchors, or off, ends it; so does letting an end go.
	for _, change := range []string{`{"set":"s","straight":"off"}`, `{"set":"s","fromAt":[1,0.2]}`, `{"set":"s","to":""}`} {
		e := newEditor()
		ops(t, e, `[{"add":"rectangle","id":"a","x":0,"y":0,"w":100,"h":100},{"add":"rectangle","id":"b","x":300,"y":0,"w":100,"h":100},{"add":"arrow","id":"s","from":"a","to":"b","straight":"h"}]`)
		ops(t, e, "["+change+"]")
		if e.Doc.Shapes["shape:s"].MetaValue(doc.MetaStraight, &axis) {
			t.Errorf("%s left it straight", change)
		}
	}
}

func TestArrowLabelSide(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"rectangle","id":"a","x":0,"y":0,"w":100,"h":100},{"add":"rectangle","id":"b","x":0,"y":300,"w":100,"h":100},{"add":"rectangle","id":"c","x":400,"y":0,"w":100,"h":100},{"add":"arrow","id":"down","from":"a","to":"b","text":"x","label":"above"},{"add":"arrow","id":"across","from":"a","to":"c","text":"x"}]`)
	if lb := e.ArrowLabelBox(e.Doc.Shapes["shape:down"]); lb.X <= 50 {
		t.Errorf("label of an arrow running down starts at %v, beside the line at 50", lb.X)
	}
	centered := *e.ArrowLabelBox(e.Doc.Shapes["shape:across"])
	ops(t, e, `[{"set":"across","label":"above"}]`)
	if lb := e.ArrowLabelBox(e.Doc.Shapes["shape:across"]); lb.MaxY() >= 50 || lb.X != centered.X {
		t.Errorf("above is %v, centered was %v", lb, centered)
	}
	if out := ops(t, e, `[{"set":"across","label":"center"}]`); out == "" {
		t.Error("no answer")
	}
	if lb := *e.ArrowLabelBox(e.Doc.Shapes["shape:across"]); lb != centered {
		t.Errorf("back to center is %v, was %v", lb, centered)
	}
	if meta := string(e.Doc.Shapes["shape:across"].Meta); strings.Contains(meta, doc.MetaLabel) {
		t.Errorf("meta keeps %s", meta)
	}
}

func TestReportListsAll(t *testing.T) {
	var list []string
	for i := range 60 {
		list = append(list, fmt.Sprintf(`{"add":"rectangle","id":"r%d","x":%d,"y":0,"w":100,"h":100}`, i, i*200))
	}
	out := ops(t, newEditor(), "["+strings.Join(list, ",")+"]")
	if !strings.Contains(out, "r59 ") || strings.Contains(out, "more") {
		t.Errorf("%s", out)
	}
}

func TestArrows(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"rectangle","id":"a","x":0,"y":0,"w":100,"h":100},
		{"add":"rectangle","id":"b","x":300,"y":0,"w":100,"h":100},
		{"add":"arrow","id":"x","from":"a","to":"b","text":"to","head":"triangle","tail":"dot","bend":20,"kind":"arc"},
		{"add":"arrow","id":"y","x1":0,"y1":200,"x2":100,"y2":250},
		{"add":"arrow","id":"z","from":"a","toAt":[1,0.5],"to":"b"}]`)
	if len(e.Doc.Bindings) != 4 {
		t.Fatalf("%d bindings", len(e.Doc.Bindings))
	}
	s := e.Doc.Shapes["shape:x"]
	if s.Text != "to" || s.HeadEnd != "triangle" || s.HeadStart != "dot" || s.Bend != 20 {
		t.Errorf("arrow props %+v", s)
	}
	if b := e.BindingOf("shape:z", "end"); !b.IsPrecise || b.Anchor != geo.V(1, 0.5) {
		t.Errorf("anchor %+v", b)
	}
	if b := e.PageBounds("shape:y"); !near(b.X, 0) || !near(b.Y, 200) || !near(b.W, 100) {
		t.Errorf("free arrow at %s", boxText(b))
	}
	// It follows what it joins.
	before := e.PageBounds("shape:x")
	ops(t, e, `[{"set":"b","dy":400}]`)
	if after := e.PageBounds("shape:x"); after.H <= before.H+100 {
		t.Errorf("arrow didn't follow: %s to %s", boxText(before), boxText(after))
	}
	// Freed, it stays where it shows, and takes new ends.
	ops(t, e, `[{"set":"x","to":""},{"set":"x","x2":900,"y2":900}]`)
	if e.BindingOf("shape:x", "end") != nil {
		t.Error("still bound")
	}
	if b := e.PageBounds("shape:x"); b.MaxX() < 890 {
		t.Errorf("end didn't move: %s", boxText(b))
	}

	f := newEditor()
	ops(t, f, `[{"add":"frame","id":"f","x":0,"y":0},{"add":"star","id":"a","in":"f","x":10,"y":10},{"add":"star","id":"b","in":"f","x":200,"y":10},{"add":"arrow","id":"x","from":"a","to":"b"}]`)
	if f.Doc.Shapes["shape:x"].Parent != "shape:f" {
		t.Error("an arrow within a frame should be in the frame")
	}
}

func TestPaths(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"draw","id":"o","d":"M0 40C0 0 80 0 80 40S0 80 0 40Z","fill":"solid","color":"violet"},
		{"add":"draw","id":"two","d":"M0 0H10Z M20 20H30Z"},
		{"add":"draw","id":"big","d":"M0 0 L100 0","x":500,"y":500,"w":300},
		{"add":"highlight","id":"h","d":"M0 0 L100 0","color":"yellow"},
		{"add":"line","id":"l","d":"M0 0 100 50 200 0","spline":"cubic","dash":"dashed"}]`)
	o := e.Doc.Shapes["shape:o"]
	if o.Type != doc.TypeDraw || !o.IsClosed || o.Fill != "solid" {
		t.Errorf("closed path %+v", o)
	}
	if n := len(o.Segments[0].Points); n < 10 {
		t.Errorf("a curve sampled to %d points", n)
	}
	if g := e.Doc.Shapes["shape:two"]; g.Type != doc.TypeGroup || len(e.Children("shape:two")) != 2 {
		t.Errorf("subpaths gave %v", g)
	}
	if b := e.PageBounds("shape:big"); !near(b.X, 500) || !near(b.Y, 500) || !near(b.W, 300) {
		t.Errorf("scaled path at %s", boxText(b))
	}
	if l := e.Doc.Shapes["shape:l"]; len(l.Points) != 3 || l.Spline != "cubic" || l.Points[1].X != 100 {
		t.Errorf("line %+v", l.Points)
	}
	if _, err := tryOps(e, `[{"add":"line","d":"M0 0 C1 1 2 2 3 3"}]`); err == nil {
		t.Error("a line took a curve")
	}
	// A new path replaces the old, where it says.
	ops(t, e, `[{"set":"h","d":"M0 0 L10 10","x":40,"y":40}]`)
	if b := e.PageBounds("shape:h"); !near(b.X, 40) || !near(b.W, 10) {
		t.Errorf("new path at %s", boxText(b))
	}
}

func TestCornersStaySharp(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"draw","id":"box","d":"M0 0 H100 V100 H0 Z"}]`)
	pts := e.Doc.Shapes["shape:box"].Segments[0].Points
	// Points close on both sides of each corner keep a stroke from rounding it.
	for _, corner := range [][2]float64{{100, 0}, {100, 100}, {0, 100}} {
		found := false
		for i, p := range pts {
			if p.X == corner[0] && p.Y == corner[1] && i > 0 && i < len(pts)-1 {
				d := math.Hypot(pts[i-1].X-p.X, pts[i-1].Y-p.Y)
				found = d < 2
			}
		}
		if !found {
			t.Errorf("no close points around corner %v", corner)
		}
	}
}

func TestStyles(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"style":{"color":"blue","dash":"solid","opacity":0.5}},
		{"add":"rectangle","id":"a"},
		{"add":"rectangle","id":"b","color":"red"},
		{"add":"text","id":"t","text":"x"},
		{"add":"frame","id":"f"}]`)
	a, b := e.Doc.Shapes["shape:a"], e.Doc.Shapes["shape:b"]
	if a.Color != "blue" || a.Dash != "solid" || a.Opacity != 0.5 || b.Color != "red" {
		t.Errorf("style: %v %v %v %v", a.Color, a.Dash, a.Opacity, b.Color)
	}
	if s := e.Doc.Shapes["shape:t"]; s.Dash == "solid" {
		t.Error("text took a dash")
	}
	// The next batch starts fresh.
	ops(t, e, `[{"add":"star","id":"c"}]`)
	if e.Doc.Shapes["shape:c"].Color != "black" {
		t.Error("a style outlived its batch")
	}
	// Set reaches through groups.
	ops(t, e, `[{"group":["a","b"],"id":"g"},{"set":"g","color":"green","locked":true}]`)
	if e.Doc.Shapes["shape:a"].Color != "green" || !e.Doc.Shapes["shape:g"].Locked {
		t.Error("a group's set didn't reach")
	}
	if _, err := tryOps(e, `[{"del":"g"}]`); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Errorf("deleted a locked group: %v", err)
	}
	ops(t, e, `[{"set":"g","locked":false},{"del":"g"}]`)
}

func TestVerbs(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"rectangle","id":"a","x":0,"y":0,"w":50,"h":50},
		{"add":"rectangle","id":"b","x":200,"y":30,"w":50,"h":50},
		{"add":"rectangle","id":"c","x":700,"y":90,"w":50,"h":50}]`)
	ops(t, e, `[{"arrange":["a","b","c"],"how":"top"}]`)
	if !near(e.PageBounds("shape:c").Y, 0) {
		t.Error("align top")
	}
	ops(t, e, `[{"arrange":["a","b","c"],"how":"distribute-h"}]`)
	if !near(e.PageBounds("shape:b").X, 350) {
		t.Errorf("distribute: %v", e.PageBounds("shape:b").X)
	}
	ops(t, e, `[{"order":"a","how":"front"}]`)
	if kids := e.Children(e.Page); kids[len(kids)-1] != "shape:a" {
		t.Error("to the front")
	}
	out := ops(t, e, `[{"dup":"a","dx":0,"dy":100,"n":3}]`)
	if n := len(e.Children(e.Page)); n != 6 {
		t.Errorf("dup gave %d shapes", n)
	}
	if !strings.Contains(out, "s3 ") {
		t.Errorf("copies not reported with short ids: %s", out)
	}
	if b := e.PageBounds("shape:s3"); !near(b.Y, 300) {
		t.Errorf("third copy at %s", boxText(b))
	}
	ops(t, e, `[{"set":"a","w":100,"h":100}]`)
	wantBox(t, e, "a", 0, 0, 100, 100)
	ops(t, e, `[{"del":"*"}]`)
	if len(e.Doc.Shapes) != 0 {
		t.Error("del * left shapes")
	}
}

func TestPages(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"star","id":"one"},{"page":"Second"},{"add":"star","id":"two"}]`)
	if len(e.Pages()) != 2 || e.Doc.Page(e.Page).Name != "Second" || e.PageOf("shape:two") != e.Page {
		t.Errorf("pages: %v", e.Pages())
	}
	if _, err := tryOps(e, `[{"set":"one","x":1}]`); err == nil || !strings.Contains(err.Error(), "another page") {
		t.Errorf("reached another page: %v", err)
	}
	ops(t, e, `[{"page":"Page 1","name":"First"}]`)
	if e.Doc.Page(e.Page).Name != "First" {
		t.Error("rename")
	}
	ops(t, e, `[{"delPage":"Second"}]`)
	if len(e.Pages()) != 1 || e.Doc.Shapes["shape:two"] != nil {
		t.Error("delPage")
	}
	if _, err := tryOps(e, `[{"delPage":"First"}]`); err == nil {
		t.Error("deleted the only page")
	}
}

// Errors tell how to get it right, so that an agent needn't look it up.
func TestErrorsTeach(t *testing.T) {
	cases := []struct{ ops, want string }{
		{`{}`, "list of objects"},
		{`[]`, "empty"},
		{`[{}]`, "exactly one verb"},
		{`[{"add":"star","del":"a"}]`, "exactly one verb"},
		{`[{"add":"blob"}]`, "cloud"},
		{`[{"add":"star","colour":"red"}]`, "unknown field"},
		{`[{"add":"star","color":"purple"}]`, "light-violet"},
		{`[{"add":"star","fill":"gradient"}]`, "pattern"},
		{`[{"add":"note","w":10}]`, "it takes"},
		{`[{"add":"arrow","x":10,"from":"a","to":"b"}]`, "placed by its ends"},
		{`[{"add":"arrow","from":"a"}]`, "needs to"},
		{`[{"add":"draw"}]`, "needs d"},
		{`[{"add":"draw","d":"nonsense"}]`, "path"},
		{`[{"add":"image"}]`, "needs src"},
		{`[{"add":"image","src":"/nonexistent/x.png"}]`, "can't read"},
		{`[{"add":"star","id":"a b"}]`, "bad id"},
		{`[{"add":"star","w":-1}]`, "above 0"},
		{`[{"add":"star","opacity":2}]`, "between 0 and 1"},
		{`[{"add":"star","right":"ghost"}]`, `no shape "ghost"`},
		{`[{"add":"star","right":"a","left":"a"}]`, "no shape"},
		{`[{"set":"ghost","x":1}]`, `no shape "ghost"`},
		{`[{"set":"ghost"}]`, "nothing to change"},
		{`[{"order":"a"}]`, "front"},
		{`[{"arrange":"a","how":"up"}]`, "center-h"},
		{`[{"flip":"a","how":"x"}]`, "needs how"},
		{`[{"style":{"x":1}}]`, "style can't set"},
		{`[{"style":{"color":"red"},"color":"blue"}]`, "inside it"},
		{`[{"del":"a","n":1}]`, "has no"},
	}
	for _, c := range cases {
		_, err := tryOps(newEditor(), c.ops)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.ops, err, c.want)
		}
	}
}

func TestImages(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pic.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	src, _ := json.Marshal(path)
	e := newEditor()
	ops(t, e, `[{"add":"image","id":"i","src":`+string(src)+`,"w":100,"x":0,"y":0,"alt":"red"}]`)
	s := e.Doc.Shapes["shape:i"]
	if s.W != 100 || s.H != 50 || s.AltText != "red" {
		t.Errorf("image %vx%v", s.W, s.H)
	}
	a := e.Doc.Assets[s.AssetID]
	if a == nil || a.Mime != "image/png" || !strings.HasPrefix(a.Src, "data:image/png;base64,") || a.Name != "pic.png" {
		t.Errorf("asset %+v", a)
	}
	// Not an image: a file an agent can't get at by pretending it is one.
	txt := filepath.Join(t.TempDir(), "secret.txt")
	os.WriteFile(txt, []byte("secret"), 0o644)
	bad, _ := json.Marshal(txt)
	if _, err := tryOps(e, `[{"add":"image","src":`+string(bad)+`}]`); err == nil || strings.Contains(err.Error(), "secret\"") {
		t.Errorf("non-image: %v", err)
	}
}

// What an agent draws is a file tldraw and the app can open again.
func TestFileRoundTrip(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"frame","id":"f","x":0,"y":0,"name":"F"},
		{"add":"rectangle","id":"a","in":"f","x":20,"y":20,"text":"A","color":"blue","fill":"solid"},
		{"add":"cloud","id":"b","x":600,"y":20,"w":200,"h":100,"dash":"dotted"},
		{"add":"arrow","id":"x","from":"a","to":"b","text":"go","bend":30},
		{"add":"draw","id":"d","d":"M0 400C50 480 100 320 150 400Z","fill":"pattern"},
		{"add":"highlight","id":"h","d":"M200 400 L300 400"},
		{"add":"line","id":"l","d":"M400 400 500 450 600 400","spline":"cubic"},
		{"add":"text","id":"t","text":"two\nlines","x":700,"y":400,"scale":2,"w":300},
		{"add":"note","id":"n","text":"N","x":900,"y":0,"color":"yellow"},
		{"add":"draw","id":"m","d":"M0 600H10 M20 600H30"},
		{"page":"Next"},
		{"add":"star","id":"s"}]`)
	var buf bytes.Buffer
	if err := doc.Write(&buf, e.Doc); err != nil {
		t.Fatal(err)
	}
	d, err := doc.Read(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Shapes) != len(e.Doc.Shapes) || len(d.Bindings) != 2 || len(d.Pages) != 2 {
		t.Fatalf("read back %d shapes, %d bindings, %d pages", len(d.Shapes), len(d.Bindings), len(d.Pages))
	}
	f := editor.New(d, fakeMeasure)
	for _, id := range []doc.ID{"shape:a", "shape:b", "shape:d", "shape:l", "shape:t", "shape:n", "shape:h"} {
		f.Page = f.PageOf(id)
		e.Page = e.PageOf(id)
		x, y := e.PageBounds(id), f.PageBounds(id)
		if math.Abs(x.X-y.X) > 1 || math.Abs(x.Y-y.Y) > 1 || math.Abs(x.W-y.W) > 1 || math.Abs(x.H-y.H) > 1 {
			t.Errorf("%s: wrote %s, read %s", id, boxText(x), boxText(y))
		}
	}
	if s := d.Shapes["shape:t"]; s.Text != "two\nlines" || s.Scale != 2 || s.AutoSize {
		t.Errorf("text %+v", s)
	}
}

func TestRead(t *testing.T) {
	e := newEditor()
	var list []string
	for range 120 {
		list = append(list, `{"add":"rectangle","w":10,"h":10}`)
	}
	list = append(list, `{"add":"rectangle","id":"needle","text":"The Needle","color":"red","fill":"solid","x":1000,"y":1000,"w":200,"h":100,"rot":15}`)
	ops(t, e, "["+strings.Join(list, ",")+"]")

	out, err := read(e, readArgs{}, "T", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "shapes 1-50 of 121") || !strings.Contains(out, "more: offset 50") {
		t.Errorf("no paging:\n%s", out)
	}
	if strings.Contains(out, "needle") {
		t.Error("the page's top is past the limit")
	}
	// Cheap enough to call without thinking about it: about 10 tokens a shape.
	if len(out) > 2200 {
		t.Errorf("overview of 50 shapes takes %d bytes", len(out))
	}
	out, _ = read(e, readArgs{Offset: 120}, "T", "")
	if !strings.Contains(out, `needle rectangle 1000,1000 200x100 rot 15 red solid "The Needle"`) {
		t.Errorf("line:\n%s", out)
	}
	out, _ = read(e, readArgs{Find: "NEEDLE"}, "T", "")
	if !strings.Contains(out, "of 1") || !strings.Contains(out, "needle") {
		t.Errorf("find:\n%s", out)
	}
	out, err = read(e, readArgs{IDs: names{"needle"}}, "T", "")
	if err != nil || !strings.Contains(out, "font draw") || !strings.Contains(out, `text "The Needle"`) {
		t.Errorf("detail:\n%s\n%v", out, err)
	}
	if _, err = read(e, readArgs{IDs: names{"ghost"}}, "T", ""); err == nil {
		t.Error("read a ghost")
	}
	// Reading another page doesn't go there.
	ops(t, e, `[{"page":"Other"},{"add":"star","id":"o"}]`)
	page := e.Page
	ops(t, e, `[{"page":"Page 1"}]`)
	out, err = read(e, readArgs{Page: "Other"}, "T", "")
	if err != nil || !strings.Contains(out, "o star") || e.Page == page {
		t.Errorf("other page:\n%s\n%v", out, err)
	}
}

// --- over the wire ---

// fakeCanvas runs what is asked of the main thread at once.
type fakeCanvas struct {
	id string
	e  *editor.Editor
}

func (c *fakeCanvas) ID() string             { return c.id }
func (c *fakeCanvas) Title() string          { return "Untitled " + c.id }
func (c *fakeCanvas) Editor() *editor.Editor { return c.e }
func (c *fakeCanvas) Run(fn func())          { fn() }
func (c *fakeCanvas) Render(box geo.Box, ids []doc.ID, scale float64) *image.RGBA {
	return image.NewRGBA(image.Rect(0, 0, int(box.W*scale), int(box.H*scale)))
}

type fakeHost struct{ list []Canvas }

func (h *fakeHost) Canvases() []Canvas { return h.list }
func (h *fakeHost) New() (Canvas, error) {
	c := &fakeCanvas{id: "new", e: newEditor()}
	h.list = append(h.list, c)
	return c, nil
}

func connect(t *testing.T, h Host) *mcp.ClientSession {
	t.Helper()
	srv := httptest.NewServer(Handler(h, "test"))
	t.Cleanup(srv.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: srv.URL + endpoint}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func call(t *testing.T, s *mcp.ClientSession, tool string, args any) *mcp.CallToolResult {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	return res
}

func textOf(r *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

// What agents carry on every turn. It's the point of the design: if this
// grows, something moved to where it costs more.
func TestToolsStayCheap(t *testing.T) {
	s := connect(t, &fakeHost{})
	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"guide", "read", "apply", "view"} {
		if !names[want] {
			t.Errorf("no tool %s", want)
		}
	}
	data, _ := json.Marshal(res.Tools)
	visible := len(data)
	// The app-side ones don't exist: there are exactly the four.
	if len(res.Tools) != 4 {
		t.Errorf("%d tools", len(res.Tools))
	}
	const budget = 2200
	if visible > budget {
		t.Errorf("tool definitions take %d bytes, over %d", visible, budget)
	}
	t.Logf("tool definitions: %d bytes", visible)
	for topic, text := range guideTopics {
		if len(text) > 1600 {
			t.Errorf("guide %q takes %d bytes", topic, len(text))
		}
	}
}

func TestOverHTTP(t *testing.T) {
	e := newEditor()
	s := connect(t, &fakeHost{list: []Canvas{&fakeCanvas{id: "d1", e: e}, &fakeCanvas{id: "d2", e: newEditor()}}})

	if got := textOf(call(t, s, "guide", map[string]any{})); !strings.Contains(got, "topics you need") {
		t.Errorf("guide: %s", got)
	}
	if got := textOf(call(t, s, "guide", map[string]any{"topic": "paths"})); !strings.Contains(got, "SVG") {
		t.Errorf("guide paths: %s", got)
	}
	r := call(t, s, "apply", map[string]any{"ops": []any{
		map[string]any{"add": "rectangle", "id": "a", "x": 0, "y": 0, "w": 100, "h": 80, "text": "Hi"},
	}})
	if r.IsError || !strings.Contains(textOf(r), "a 0,0 100x80") {
		t.Errorf("apply: %s", textOf(r))
	}
	if e.Doc.Shapes["shape:a"] == nil {
		t.Error("nothing drawn")
	}
	r = call(t, s, "apply", map[string]any{"ops": []any{map[string]any{"add": "rectangle", "color": "mauve"}}})
	if !r.IsError || !strings.Contains(textOf(r), "ops[0]: color") {
		t.Errorf("a bad op should be a tool error the agent reads: %s", textOf(r))
	}
	r = call(t, s, "apply", map[string]any{"doc": "d9", "ops": []any{map[string]any{"add": "star"}}})
	if !r.IsError || !strings.Contains(textOf(r), "d1, d2") {
		t.Errorf("bad doc: %s", textOf(r))
	}
	r = call(t, s, "read", map[string]any{})
	if !strings.Contains(textOf(r), `a rectangle 0,0 100x80 "Hi"`) || !strings.Contains(textOf(r), "d2=") {
		t.Errorf("read: %s", textOf(r))
	}
	r = call(t, s, "view", map[string]any{"px": 400})
	if r.IsError || len(r.Content) != 2 {
		t.Fatalf("view: %s", textOf(r))
	}
	pic, ok := r.Content[0].(*mcp.ImageContent)
	if !ok || pic.MIMEType != "image/png" {
		t.Fatalf("view gave %T", r.Content[0])
	}
	img, err := png.Decode(bytes.NewReader(pic.Data))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); max(b.Dx(), b.Dy()) > 400 {
		t.Errorf("view is %dx%d, asked for 400", b.Dx(), b.Dy())
	}
	if !strings.Contains(textOf(r), "page box") {
		t.Errorf("view caption: %s", textOf(r))
	}
	r = call(t, s, "read", map[string]any{"nonsense": 1})
	if !r.IsError {
		t.Error("read took an unknown argument")
	}
}

func TestNoWindowOpen(t *testing.T) {
	h := &fakeHost{}
	s := connect(t, h)
	if r := call(t, s, "read", map[string]any{}); !r.IsError || !strings.Contains(textOf(r), "no drawing") {
		t.Errorf("read: %s", textOf(r))
	}
	if r := call(t, s, "apply", map[string]any{"ops": []any{map[string]any{"add": "star"}}}); r.IsError {
		t.Errorf("apply should open a drawing: %s", textOf(r))
	}
	if len(h.list) != 1 {
		t.Errorf("%d canvases", len(h.list))
	}
}

type themedCanvas struct {
	fakeCanvas
	dark bool
}

func (c *themedCanvas) Render(box geo.Box, ids []doc.ID, scale float64) *image.RGBA {
	c.dark = c.e.DarkMode
	return c.fakeCanvas.Render(box, ids, scale)
}

func TestViewTheme(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"star"}]`)
	c := &themedCanvas{fakeCanvas: fakeCanvas{id: "d1", e: e}}
	s := connect(t, &fakeHost{list: []Canvas{c}})
	if got := textOf(call(t, s, "view", map[string]any{})); !strings.Contains(got, "light theme") || c.dark {
		t.Errorf("window's theme: %q, rendered dark %v", got, c.dark)
	}
	if got := textOf(call(t, s, "view", map[string]any{"theme": "dark"})); !strings.Contains(got, "dark theme") || !c.dark {
		t.Errorf("asked for dark: %q, rendered dark %v", got, c.dark)
	}
	if e.DarkMode {
		t.Error("view left the window dark")
	}
	if got := call(t, s, "view", map[string]any{"theme": "blue"}); !got.IsError || !strings.Contains(textOf(got), "light, dark") {
		t.Errorf("bad theme: %q", textOf(got))
	}
	if got := textOf(call(t, s, "read", map[string]any{})); !strings.Contains(got, "theme light") {
		t.Errorf("read: %q", got)
	}
	if got := textOf(call(t, s, "apply", map[string]any{"dry": true, "ops": []any{map[string]any{"add": "star", "id": "x"}}})); !strings.Contains(got, "dry run") || e.Doc.Shapes["shape:x"] != nil {
		t.Errorf("dry over the wire: %q", got)
	}
}

func TestViewEmptyPage(t *testing.T) {
	s := connect(t, &fakeHost{list: []Canvas{&fakeCanvas{id: "d1", e: newEditor()}}})
	if r := call(t, s, "view", map[string]any{}); !r.IsError || !strings.Contains(textOf(r), "empty") {
		t.Errorf("view: %s", textOf(r))
	}
	if r := call(t, s, "view", map[string]any{"box": []any{0, 0, 100, 100}}); r.IsError {
		t.Errorf("view with box: %s", textOf(r))
	}
	if r := call(t, s, "view", map[string]any{"box": []any{0, 0}}); !r.IsError {
		t.Error("view took half a box")
	}
}

// A panic in what an agent asked for must not take the app down with it.
func TestPanicFailsTheCall(t *testing.T) {
	c := &fakeCanvas{id: "d1", e: newEditor()}
	err := onMain(context.Background(), c, func() error { panic("boom") })
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("got %v", err)
	}
	e := c.e
	err = e.Batch(func() error {
		s := doc.NewShape("shape:x", doc.TypeGeo)
		s.Parent, s.Index = e.Page, e.TopIndex(e.Page)
		e.Put(s)
		panic("boom")
	})
	if err == nil || e.Doc.Shapes["shape:x"] != nil {
		t.Errorf("a panicking batch left its work: %v", err)
	}
}

func TestStartReportsThePortTaken(t *testing.T) {
	url, err := Start("127.0.0.1:0", &fakeHost{}, "test")
	if err != nil || !strings.HasPrefix(url, "http://127.0.0.1:") || !strings.HasSuffix(url, endpoint) {
		t.Fatalf("got %q, %v", url, err)
	}
	addr := strings.TrimSuffix(strings.TrimPrefix(url, "http://"), endpoint)
	if _, err := Start(addr, &fakeHost{}, "test"); err == nil {
		t.Error("a second server took the same port")
	}
}

// Web pages must not reach it: not by posting to it, nor by rebinding a domain
// to this machine.
func TestWebPagesCantReachIt(t *testing.T) {
	e := newEditor()
	srv := httptest.NewServer(Handler(&fakeHost{list: []Canvas{&fakeCanvas{id: "d1", e: e}}}, "test"))
	defer srv.Close()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"apply","arguments":{"ops":[{"add":"star"}]}}}`
	post := func(mut func(*http.Request)) int {
		req, _ := http.NewRequest("POST", srv.URL+endpoint, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		mut(req)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := post(func(r *http.Request) {
		r.Header.Set("Origin", "https://evil.example")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
	}); code != http.StatusForbidden {
		t.Errorf("cross-site post: %d", code)
	}
	if code := post(func(r *http.Request) { r.Host = "evil.example" }); code != http.StatusForbidden {
		t.Errorf("rebound host: %d", code)
	}
	if len(e.Doc.Shapes) != 0 {
		t.Error("a web page drew on the canvas")
	}
}
