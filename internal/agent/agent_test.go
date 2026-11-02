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
	return apply(e, list, applyMode{})
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
		"keycloak grew 90→121.1, now overlaps metabase by 200x11.1")
	// Further in, it reaches the text.
	wantWarnings(t, newEditor(), `[{"add":"rectangle","id":"keycloak","x":0,"y":0,"w":200,"h":90,"text":"one\ntwo\nthree\nfour"},{"add":"rectangle","id":"metabase","x":0,"y":100,"w":200,"h":90,"text":"bi"}]`,
		"keycloak grew 90→150.8, now goes under metabase, which hides its text")

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
	if n := strings.Count(warnings, "\n"); n > maxWarnings || !strings.Contains(warnings, " more; read with check") {
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
	out, err := apply(e, list, applyMode{dry: true})
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
	if out, err = apply(e, list, applyMode{dry: true}); err != nil || !strings.Contains(out, "a 0,0 100x") || !strings.Contains(out, "a grew 40→") {
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

// Text shapes don't wrap, so a card's line can run past its box unnoticed.
func TestWarnsOfTextPastItsBox(t *testing.T) {
	wantWarnings(t, newEditor(), `[{"add":"rectangle","id":"box","x":0,"y":0,"w":100,"h":100},{"add":"text","id":"line","text":"far too long","x":20,"y":10,"size":"s"},{"group":["box","line"],"id":"g"}]`,
		"line's text sticks out of box by 28")
	wantWarnings(t, newEditor(), `[{"add":"rectangle","id":"box","x":0,"y":0,"w":200,"h":100},{"add":"text","text":"fits","x":20,"y":10,"size":"s"}]`)
	// Beside it is no business of the box's.
	wantWarnings(t, newEditor(), `[{"add":"rectangle","id":"box","x":0,"y":0,"w":100,"h":100},{"add":"text","text":"caption","x":110,"y":10,"size":"s"}]`)
	// A later edit counts too, and the answer gives the new size.
	e := newEditor()
	ops(t, e, `[{"add":"rectangle","id":"box","x":0,"y":0,"w":200,"h":100},{"add":"text","id":"line","text":"fits","x":20,"y":10,"size":"s"}]`)
	list, err := decodeOps(json.RawMessage(`[{"set":"line","text":"a good deal longer than the box"}]`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := apply(e, list, applyMode{dry: true})
	if err != nil || !strings.Contains(out, "changed:\nline 20,10 279x") || !strings.Contains(out, "line's text sticks out of box by 99") {
		t.Errorf("%q, %v", out, err)
	}
}

func TestReportOfChanges(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"rectangle","id":"a","x":0,"y":0,"w":100,"h":100},{"add":"rectangle","id":"b","x":200,"y":0,"w":100,"h":100}]`)
	out := ops(t, e, `[{"add":"star","id":"s","x":500,"y":0,"w":50,"h":50},{"set":"a","x":10},{"set":"s","x":600}]`)
	want := "s 600,0 50x50\nchanged:\na 10,0 100x100\n"
	if !strings.Contains(out, want) {
		t.Errorf("%q lacks %q", out, want)
	}

	list, _ := decodeOps(json.RawMessage(`[{"add":"star","id":"q","x":700,"y":0,"w":50,"h":50},{"set":"a","x":20,"h":5,"text":"one\ntwo"}]`))
	out, err := apply(e, list, applyMode{quiet: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"added 1", "changed 1", "page bounds", "warnings:", "a grew 5→"} {
		if !strings.Contains(out, w) {
			t.Errorf("quiet lacks %q: %q", w, out)
		}
	}
	if strings.Contains(out, "q 700") || strings.Contains(out, "a 20") {
		t.Errorf("quiet lists boxes: %q", out)
	}
}

func TestCards(t *testing.T) {
	e := newEditor()
	long := strings.Repeat("words ", 40)
	wantWarnings(t, e, `[{"add":"card","id":"k","title":"Keycloak","body":"`+long+`","color":"violet","labelColor":"grey","fill":"solid","font":"sans","x":10,"y":20},{"add":"card","id":"short","title":"Hi","right":"k","gap":50},{"add":"arrow","id":"s","from":"k","to":"short","straight":"h"}]`)
	k := cardOf(e, "shape:k")
	if k == nil || k.head == nil || k.body == nil {
		t.Fatalf("parts: %+v", k)
	}
	if k.box.W != cardMaxW || k.box.Color != "violet" || k.box.Fill != "solid" || k.head.Color != "violet" || k.body.Color != "grey" || k.body.Font != "sans" || k.body.Size != "s" {
		t.Errorf("styled: box %+v head %+v body %+v", k.box, k.head, k.body)
	}
	// Fits its parts, with them in order and inside it.
	wantBox(t, e, "k", 10, 20, cardMaxW, k.box.H)
	head, body, box := e.PageBounds(k.head.ID), e.PageBounds(k.body.ID), e.PageBounds(k.box.ID)
	if !box.ContainsBox(head) || !box.ContainsBox(body) || body.Y < head.MaxY() || !near(box.MaxY(), body.MaxY()+cardPad) {
		t.Errorf("box %+v head %+v body %+v", box, head, body)
	}
	if sb := cardOf(e, "shape:short").box; sb.W != cardMinW {
		t.Errorf("short card is %v wide", sb.W)
	}
	// An arrow binds to its box, by the card's id.
	if b := e.BindingOf("shape:s", "start"); b == nil || b.To != k.box.ID {
		t.Errorf("binding %+v", b)
	}
	if got, _ := read(e, readArgs{}, "t", ""); !strings.Contains(got, `k card 10,20`) || !strings.Contains(got, `"Keycloak"`) {
		t.Errorf("read: %s", got)
	}

	// Set it, and it fits again where it was; the arrow stays level.
	was := k.box.H
	wantWarnings(t, e, `[{"set":"k","body":"short"}]`)
	k = cardOf(e, "shape:k")
	if k.box.H >= was || k.box.X != cardOf(e, "shape:k").box.X {
		t.Errorf("height %v, was %v", k.box.H, was)
	}
	wantBox(t, e, "k", 10, 20, cardMaxW, k.box.H)
	wantWarnings(t, e, `[{"set":"k","w":200,"color":"blue","x":0}]`)
	k = cardOf(e, "shape:k")
	wantBox(t, e, "k", 0, 20, 200, k.box.H)
	if k.box.Color != "blue" || k.head.Color != "blue" || k.body.Color != "grey" {
		t.Errorf("set color: %v %v %v", k.box.Color, k.head.Color, k.body.Color)
	}
	if t2 := e.PageBounds(k.body.ID); t2.W != 200-2*cardPad {
		t.Errorf("body wraps at %v", t2.W)
	}

	// A height asked for holds while the text allows, and warns when it can't.
	wantWarnings(t, e, `[{"set":"k","h":300}]`)
	wantWarnings(t, e, `[{"set":"k","title":"Changed"}]`)
	wantBox(t, e, "k", 0, 20, 200, 300)
	wantWarnings(t, e, `[{"set":"k","h":10}]`, "grew 10→")

	// A part can come and go, so long as one stays.
	wantWarnings(t, e, `[{"set":"k","body":""}]`)
	if c := cardOf(e, "shape:k"); c.body != nil {
		t.Error("body stayed")
	}
	wantWarnings(t, e, `[{"set":"k","body":"back"}]`)
	if c := cardOf(e, "shape:k"); c.body == nil || c.body.Text != "back" || c.body.Size != "s" {
		t.Errorf("body back: %+v", c.body)
	}
	if _, err := tryOps(e, `[{"set":"k","body":""},{"set":"k","title":""}]`); err == nil || !strings.Contains(err.Error(), "title or body") {
		t.Errorf("emptied: %v", err)
	}
	if _, err := tryOps(e, `[{"set":"k","text":"x"}]`); err == nil || !strings.Contains(err.Error(), "title body") {
		t.Errorf("text on a card: %v", err)
	}
	if _, err := tryOps(newEditor(), `[{"add":"card"}]`); err == nil || !strings.Contains(err.Error(), "title or body") {
		t.Errorf("empty card: %v", err)
	}

	// Copies are cards too, and take moving as one.
	out := ops(t, e, `[{"dup":"k","dx":0,"dy":500}]`)
	copyID := strings.Fields(strings.Split(out, "\n")[1])[0]
	if c := cardOf(e, shapeID(copyID)); c == nil || c.body == nil {
		t.Fatalf("copy %s isn't a card: %s", copyID, out)
	}
	ops(t, e, `[{"set":"`+copyID+`","title":"Copy","dx":100}]`)
	if got := cardOf(e, "shape:k").head.Text; got == "Copy" {
		t.Error("setting the copy changed the original")
	}

	// In a frame, in rotated company, and from a batch's style.
	e = newEditor()
	wantWarnings(t, e, `[{"style":{"color":"red","font":"mono"}},{"add":"frame","id":"f","x":100,"y":100,"w":600,"h":400},{"add":"card","id":"c","in":"f","title":"In","x":120,"y":140,"size":"l"},{"set":"f","rot":30},{"set":"c","title":"In a frame"}]`)
	c := cardOf(e, "shape:c")
	if c.head.Font != "mono" || c.box.Color != "red" || c.head.Size != "l" {
		t.Errorf("styled %+v", c.head)
	}
	if e.Doc.Shapes["shape:c"].Parent != "shape:f" {
		t.Error("left its frame")
	}
	if b := e.PageMat(c.box.ID).Invert().Mul(e.PageMat(c.head.ID)).Apply(geo.V(0, 0)); !near(b.X, cardPad) || !near(b.Y, cardPad) {
		t.Errorf("heading off the box by %v", b)
	}

	// Saved and read back, it's still a card.
	var buf bytes.Buffer
	if err := doc.Write(&buf, e.Doc); err != nil {
		t.Fatal(err)
	}
	d, err := doc.Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if cardOf(editor.New(d, fakeMeasure), "shape:c") == nil {
		t.Error("no card from the file")
	}
}

// A card growing into another is one thing gone wrong, whatever its parts do.
func TestWarningsNameTheCause(t *testing.T) {
	e := newEditor()
	wantWarnings(t, e, `[{"add":"card","id":"casbin","title":"Casbin","body":"short","w":200,"x":0,"y":0},{"add":"card","id":"routes","title":"Routes","body":"short","w":200,"x":0,"y":120}]`)
	out := wantWarnings(t, e, `[{"set":"casbin","body":"one\ntwo\nthree\nfour\nfive\nsix"}]`, "casbin grew 94.7→216.2")
	_, warnings, _ := strings.Cut(out, "warnings:\n")
	if lines := strings.Split(warnings, "\n"); len(lines) != 1 || !strings.Contains(lines[0], ", now") || !strings.Contains(lines[0], "routes") {
		t.Errorf("want one line of the cause: %q", warnings)
	}
	if strings.Contains(out, "-box") || strings.Contains(out, "-head") || strings.Contains(out, "-body") {
		t.Errorf("names a part: %q", out)
	}
	// Moving one into a thing that stayed blames what moved.
	e = newEditor()
	ops(t, e, `[{"add":"rectangle","id":"still","x":0,"y":0,"w":300,"h":100,"text":"x"},{"add":"rectangle","id":"moved","x":500,"y":0,"w":100,"h":100,"text":"y"}]`)
	wantWarnings(t, e, `[{"set":"still","color":"blue"}]`)
	wantWarnings(t, e, `[{"set":"moved","x":250}]`, "moved overlaps still by 50x100")
	e = newEditor()
	ops(t, e, `[{"add":"rectangle","id":"over","x":500,"y":0,"w":100,"h":100,"text":"y"},{"add":"rectangle","id":"under","x":0,"y":0,"w":100,"h":100,"text":"x","fill":"solid"}]`)
	wantWarnings(t, e, `[{"set":"over","x":40}]`, "over goes under under, which hides its text")

	// A card that fits new text and hits nothing is what was asked for, and
	// its arrow names the card.
	e = newEditor()
	out = wantWarnings(t, e, `[{"add":"card","id":"a","title":"A","x":0,"y":0},{"add":"card","id":"z","title":"Z","x":300,"y":0},{"add":"card","id":"c","title":"C","x":600,"y":0},{"add":"arrow","id":"s","from":"a","to":"c"}]`, "s runs through z")
	if strings.Count(out, "runs through") != 1 {
		t.Errorf("told more than once: %q", out)
	}
	wantWarnings(t, e, `[{"del":"s"}]`)
	wantWarnings(t, e, `[{"set":"a","body":"one\ntwo\nthree"}]`)
	ops(t, e, `[{"add":"arrow","id":"t","from":"a","to":"z"}]`)
	if got, _ := read(e, readArgs{IDs: names{"t"}}, "", ""); !strings.Contains(got, "from a to z") {
		t.Errorf("read: %s", got)
	}
}

func TestSetLinesUpSeveral(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"rectangle","id":"a","x":0,"y":10,"w":100,"h":100},{"add":"card","id":"c","title":"C","x":200,"y":70},{"add":"note","id":"n","x":400,"y":300}]`)
	ops(t, e, `[{"set":["a","c","n"],"y":368}]`)
	for _, id := range []string{"a", "c", "n"} {
		if b := boxOf(e, shapeID(id)); !near(b.Y, 368) {
			t.Errorf("%s at y %v", id, b.Y)
		}
	}
	if b := boxOf(e, "shape:c"); !near(b.X, 200) {
		t.Errorf("x moved to %v", b.X)
	}
	ops(t, e, `[{"set":["a","c"],"x":50}]`)
	if a, c := boxOf(e, "shape:a"), boxOf(e, "shape:c"); !near(a.X, 50) || !near(c.X, 50) {
		t.Errorf("x: %v, %v", a.X, c.X)
	}
	for op, want := range map[string]string{`{"set":["a","c"],"x":1,"y":2}`: "stack", `{"set":["a","c"],"right":"n"}`: "one shape at a time"} {
		if _, err := tryOps(e, "["+op+"]"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", op, err)
		}
	}
}

func TestCardSizeIsItsText(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"style":{"size":"xl"}},{"add":"card","id":"k","title":"T","body":"B"}]`)
	c := cardOf(e, "shape:k")
	if c.box.Size != "s" || c.head.Size != "xl" || c.body.Size != "l" {
		t.Errorf("box %s title %s body %s", c.box.Size, c.head.Size, c.body.Size)
	}
	ops(t, e, `[{"set":"k","size":"m"}]`)
	c = cardOf(e, "shape:k")
	if c.box.Size != "s" || c.head.Size != "m" || c.body.Size != "s" {
		t.Errorf("after set: box %s title %s body %s", c.box.Size, c.head.Size, c.body.Size)
	}
	// The border can still be heavier, by its own id.
	ops(t, e, `[{"set":"`+shortID(c.box.ID)+`","size":"l"}]`)
	if c = cardOf(e, "shape:k"); c.box.Size != "l" {
		t.Errorf("box %s", c.box.Size)
	}
	ops(t, e, `[{"set":"k","title":"Still"}]`)
	if c = cardOf(e, "shape:k"); c.box.Size != "l" {
		t.Errorf("refit changed the border to %s", c.box.Size)
	}
}

func TestDryRunCommits(t *testing.T) {
	e := newEditor()
	s := connect(t, &fakeHost{list: []Canvas{&fakeCanvas{id: "d1", e: e}}})
	args := map[string]any{"dry": true, "ops": []any{map[string]any{"add": "star", "id": "x"}}}
	got := textOf(call(t, s, "apply", args))
	token := regexp.MustCompile(`commit "(t\d+)"`).FindStringSubmatch(got)
	if token == nil || e.Doc.Shapes["shape:x"] != nil {
		t.Fatalf("dry: %q", got)
	}
	got = textOf(call(t, s, "apply", map[string]any{"commit": token[1], "quiet": true}))
	if !strings.HasPrefix(got, "ok ") || !strings.Contains(got, "added 1") || e.Doc.Shapes["shape:x"] == nil {
		t.Errorf("commit: %q", got)
	}
	// Only once: again would be the id taken, or a second copy.
	if r := call(t, s, "apply", map[string]any{"commit": token[1]}); !r.IsError || !strings.Contains(textOf(r), "send ops again") {
		t.Errorf("twice: %q", textOf(r))
	}
	if r := call(t, s, "apply", map[string]any{"commit": "t1", "ops": []any{map[string]any{"add": "star"}}}); !r.IsError {
		t.Errorf("commit with ops: %q", textOf(r))
	}
	// A client whose old schema requires ops sends an empty list, and that's fine.
	got = textOf(call(t, s, "apply", map[string]any{"dry": true, "ops": []any{map[string]any{"add": "star", "id": "viaEmpty"}}}))
	empty := regexp.MustCompile(`commit "(t\d+)"`).FindStringSubmatch(got)
	if r := call(t, s, "apply", map[string]any{"commit": empty[1], "ops": []any{}}); r.IsError || e.Doc.Shapes["shape:viaEmpty"] == nil {
		t.Errorf("commit with empty ops: %q", textOf(r))
	}
	// Sent as an op, it says where it goes.
	for _, name := range []string{"commit", "dry", "quiet"} {
		r := call(t, s, "apply", map[string]any{"ops": []any{map[string]any{name: "t1"}}})
		if !r.IsError || !strings.Contains(textOf(r), name+" is an argument of apply") {
			t.Errorf("%s as an op: %q", name, textOf(r))
		}
	}
	if r := call(t, s, "apply", map[string]any{}); !r.IsError || !strings.Contains(textOf(r), "commit") {
		t.Errorf("neither: %q", textOf(r))
	}

	// A commit that fails can be fixed and tried again.
	args = map[string]any{"dry": true, "ops": []any{map[string]any{"set": "y", "x": 1}}}
	if r := call(t, s, "apply", args); !r.IsError {
		t.Errorf("dry of what doesn't exist: %q", textOf(r))
	}
	ops(t, e, `[{"add":"star","id":"y"}]`)
	got = textOf(call(t, s, "apply", args))
	token = regexp.MustCompile(`commit "(t\d+)"`).FindStringSubmatch(got)
	ops(t, e, `[{"del":"y"}]`)
	if r := call(t, s, "apply", map[string]any{"commit": token[1]}); !r.IsError {
		t.Errorf("commit after its target went: %q", textOf(r))
	}
	ops(t, e, `[{"add":"star","id":"y"}]`)
	if r := call(t, s, "apply", map[string]any{"commit": token[1]}); r.IsError {
		t.Errorf("commit again: %q", textOf(r))
	}

	// Only the latest stay.
	var first string
	for i := range keptDries + 1 {
		got := textOf(call(t, s, "apply", map[string]any{"dry": true, "ops": []any{map[string]any{"add": "star"}}}))
		if i == 0 {
			first = regexp.MustCompile(`commit "(t\d+)"`).FindStringSubmatch(got)[1]
		}
	}
	if r := call(t, s, "apply", map[string]any{"commit": first}); !r.IsError {
		t.Errorf("oldest still commits: %q", textOf(r))
	}
}

// Tokens and document ids are both counters, so they must not look alike: an
// agent in d1 holding the token of the first dry run mustn't be able to swap them.
func TestDryRunTokenIsNoDocID(t *testing.T) {
	e := newEditor()
	s := connect(t, &fakeHost{list: []Canvas{&fakeCanvas{id: "d1", e: e}}})
	args := map[string]any{"dry": true, "ops": []any{map[string]any{"add": "star", "id": "x"}}}
	got := textOf(call(t, s, "apply", args))
	token := regexp.MustCompile(`commit "(t1)"`).FindStringSubmatch(got)
	if token == nil {
		t.Fatalf("the first token: %q", got)
	}
	if r := call(t, s, "apply", map[string]any{"commit": "d1"}); !r.IsError || !strings.Contains(textOf(r), "no dry run") || e.Doc.Shapes["shape:x"] != nil {
		t.Errorf("a document id as the token: %q", textOf(r))
	}
	if r := call(t, s, "apply", map[string]any{"doc": token[1], "ops": []any{map[string]any{"add": "star"}}}); !r.IsError || !strings.Contains(textOf(r), "no drawing") {
		t.Errorf("a token as the document: %q", textOf(r))
	}
	// Getting them mixed up costs nothing: the dry run is still there.
	if r := call(t, s, "apply", map[string]any{"commit": token[1]}); r.IsError || e.Doc.Shapes["shape:x"] == nil {
		t.Errorf("commit after the mix-ups: %q", textOf(r))
	}
}

// A dry run's answer is about the page it ran on, so it doesn't commit on another.
func TestCommitStaysOnItsPage(t *testing.T) {
	e := newEditor()
	s := connect(t, &fakeHost{list: []Canvas{&fakeCanvas{id: "d1", e: e}}})
	ops(t, e, `[{"page":"Other"},{"page":"Page 1"}]`)
	first := e.Page
	got := textOf(call(t, s, "apply", map[string]any{"dry": true, "ops": []any{map[string]any{"add": "star", "id": "x"}}}))
	token := regexp.MustCompile(`commit "(t\d+)"`).FindStringSubmatch(got)
	if token == nil {
		t.Fatalf("dry: %q", got)
	}
	ops(t, e, `[{"page":"Other"}]`)
	r := call(t, s, "apply", map[string]any{"commit": token[1]})
	if !r.IsError || !strings.Contains(textOf(r), `"Page 1"`) || e.Doc.Shapes["shape:x"] != nil {
		t.Fatalf("commit on another page: %q, x on the page: %v", textOf(r), e.Doc.Shapes["shape:x"] != nil)
	}
	// Going back is enough: the dry run stays.
	ops(t, e, `[{"page":"Page 1"}]`)
	if r := call(t, s, "apply", map[string]any{"commit": token[1]}); r.IsError || e.PageOf("shape:x") != first {
		t.Errorf("commit back on its page: %q", textOf(r))
	}

	// A page gone is a dry run gone.
	got = textOf(call(t, s, "apply", map[string]any{"dry": true, "ops": []any{map[string]any{"add": "star", "id": "y"}}}))
	token = regexp.MustCompile(`commit "(t\d+)"`).FindStringSubmatch(got)
	ops(t, e, `[{"page":"Other"},{"delPage":"Page 1"}]`)
	if r := call(t, s, "apply", map[string]any{"commit": token[1]}); !r.IsError || !strings.Contains(textOf(r), "send ops again") || e.Doc.Shapes["shape:y"] != nil {
		t.Errorf("commit after its page went: %q", textOf(r))
	}

	// A page op in the dry run counts from where the dry run began.
	got = textOf(call(t, s, "apply", map[string]any{"dry": true, "ops": []any{map[string]any{"page": "Third"}, map[string]any{"add": "star", "id": "z"}}}))
	token = regexp.MustCompile(`commit "(t\d+)"`).FindStringSubmatch(got)
	if token == nil {
		t.Fatalf("dry with a page op: %q", got)
	}
	if r := call(t, s, "apply", map[string]any{"commit": token[1]}); r.IsError || e.Doc.Shapes["shape:z"] == nil {
		t.Errorf("commit with a page op: %q", textOf(r))
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
	// What the handlers take, a client must be told: a strict one refuses the rest.
	declared := map[string][]string{"apply": {"ops", "dry", "commit", "quiet"}, "read": {"check"}, "view": {"theme"}}
	for _, tool := range res.Tools {
		schema, _ := json.Marshal(tool.InputSchema)
		for _, want := range declared[tool.Name] {
			if !strings.Contains(string(schema), `"`+want+`":`) {
				t.Errorf("%s doesn't declare %s: %s", tool.Name, want, schema)
			}
		}
		if tool.Name == "guide" {
			for topic := range guideTopics {
				if topic != "" && !strings.Contains(string(schema), `"`+topic+`"`) {
					t.Errorf("guide doesn't declare topic %q: %s", topic, schema)
				}
			}
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

// arrowLine returns the corners of an arrow, in the page.
func arrowLine(e *editor.Editor, id string) []geo.Vec {
	s := e.Doc.Shapes[shapeID(id)]
	m := e.PageMat(s.ID)
	var out []geo.Vec
	for _, p := range e.Arrow(s).Line {
		out = append(out, m.Apply(p))
	}
	return out
}

const threeInARow = `{"add":"rectangle","id":"a","x":0,"y":0,"w":100,"h":100,"text":"a"},{"add":"rectangle","id":"m","x":250,"y":0,"w":100,"h":100,"text":"m"},{"add":"rectangle","id":"b","x":500,"y":0,"w":100,"h":100,"text":"b"},`

// An anchor on the top of a shape leaves by the top, not along it to a corner,
// and that's enough to get over what is in the way.
func TestElbowLeavesByItsAnchorsSide(t *testing.T) {
	e := newEditor()
	wantWarnings(t, e, `[`+threeInARow+`{"add":"arrow","id":"s","from":"a","to":"b","kind":"elbow","fromAt":[0.5,0],"toAt":[0.5,0]}]`)
	line := arrowLine(e, "s")
	if len(line) != 4 {
		t.Fatalf("not a U: %v", line)
	}
	if line[0].X != 50 || line[0].Y != 0 || line[1].X != 50 || line[1].Y >= 0 {
		t.Errorf("doesn't leave by the top: %v", line)
	}
	if last := line[3]; last.X != 550 || last.Y > 0 || line[2].X != 550 {
		t.Errorf("doesn't arrive at the top: %v", line)
	}
}

// Without an anchor on an edge an elbow lies as it always did.
func TestElbowWithoutSideIsUnchanged(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"rectangle","id":"a","x":0,"y":0,"w":100,"h":100},{"add":"rectangle","id":"b","x":300,"y":200,"w":100,"h":100},{"add":"arrow","id":"s","from":"a","to":"b","kind":"elbow"}]`)
	line := arrowLine(e, "s")
	if len(line) != 4 || line[1].X != 200 || line[2].X != 200 || line[0].X != 100 {
		t.Errorf("got %v", line)
	}
}

func TestVia(t *testing.T) {
	e := newEditor()
	wantWarnings(t, e, `[`+threeInARow+`{"add":"arrow","id":"s","from":"a","to":"b","kind":"elbow"}]`, "s runs through m; another side or via routes it around")
	wantWarnings(t, e, `[{"set":"s","via":[[300,-40]]}]`)
	line := arrowLine(e, "s")
	found := false
	for i := 1; i < len(line); i++ {
		if line[i-1].Y == -40 && line[i].Y == -40 && min(line[i-1].X, line[i].X) <= 300 && max(line[i-1].X, line[i].X) >= 300 {
			found = true
		}
	}
	if !found {
		t.Errorf("doesn't pass through it: %v", line)
	}
	if got, _ := read(e, readArgs{IDs: names{"s"}}, "", ""); !strings.Contains(got, "via [[300,-40]]") {
		t.Errorf("read: %s", got)
	}
	// Going back to an arc forgets it, and an empty list clears it.
	ops(t, e, `[{"set":"s","via":[]}]`)
	if e.Doc.Shapes[shapeID("s")].Via() != nil {
		t.Error("via:[] didn't clear")
	}
	ops(t, e, `[{"set":"s","via":[[300,-40]]},{"set":"s","kind":"arc"}]`)
	if e.Doc.Shapes[shapeID("s")].Via() != nil {
		t.Error("an arc kept via")
	}
}

// It's kept in the file, and moves and turns over with the arrow.
func TestViaLasts(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"arrow","id":"s","x1":0,"y1":0,"x2":200,"y2":100,"kind":"elbow","via":[[100,-40]]}]`)
	var buf bytes.Buffer
	if err := doc.Write(&buf, e.Doc); err != nil {
		t.Fatal(err)
	}
	back, err := doc.Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if v := back.Shapes[shapeID("s")].Via(); len(v) != 1 || v[0] != geo.V(100, -40) {
		t.Errorf("saved as %v", v)
	}
	ops(t, e, `[{"dup":"s","dx":0,"dy":300},{"set":"s","dx":10,"dy":20}]`)
	if got, _ := read(e, readArgs{IDs: names{"s"}}, "", ""); !strings.Contains(got, "via [[110,-20]]") {
		t.Errorf("moved: %s", got)
	}
	var copied bool
	for id, s := range e.Doc.Shapes {
		if id != shapeID("s") && len(s.Via()) == 1 {
			m := e.PageMat(id)
			copied = m.Apply(s.Via()[0]) == geo.V(100, 260)
		}
	}
	if !copied {
		t.Error("the copy's via didn't come along")
	}
	before := arrowLine(e, "s")
	ops(t, e, `[{"flip":"s","how":"v"}]`)
	after := arrowLine(e, "s")
	if len(before) != len(after) {
		t.Fatalf("flipped: %v to %v", before, after)
	}
	box := geo.BoxOf(before)
	for i := range before {
		if want := box.Y + box.MaxY() - before[i].Y; math.Abs(after[i].Y-want) > 0.01 {
			t.Errorf("flipped: %v to %v", before, after)
			break
		}
	}
}

func TestRoutesTurnDownWhatTheyCantDo(t *testing.T) {
	const two = `{"add":"rectangle","id":"a","x":0,"y":0},{"add":"rectangle","id":"b","x":300,"y":0},`
	for list, want := range map[string]string{
		`[` + two + `{"add":"arrow","from":"a","to":"b","kind":"elbow","bend":40}]`:                                         "elbow arrow can't bend",
		`[` + two + `{"add":"arrow","id":"s","from":"a","to":"b","kind":"elbow"},{"set":"s","bend":40}]`:                    "elbow arrow can't bend",
		`[` + two + `{"add":"arrow","from":"a","to":"b","via":[[0,0]]}]`:                                                    `add "kind":"elbow"`,
		`[` + two + `{"add":"arrow","from":"a","to":"b","kind":"elbow","straight":"h","via":[[0,0]]}]`:                      "can't take via",
		`[` + two + `{"add":"arrow","id":"s","from":"a","to":"b","kind":"elbow","straight":"h"},{"set":"s","via":[[0,0]]}]`: "can't take via",
		`[` + two + `{"add":"arrow","id":"s","from":"a","to":"b","kind":"elbow","via":[[0,0]]},{"set":"s","straight":"h"}]`: "can't take via",
		`[` + two + `{"add":"arrow","from":"a","to":"b","kind":"elbow","via":[[0]]}]`:                                       "[x,y] points",
	} {
		if _, err := tryOps(newEditor(), list); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", list, err, want)
		}
	}
	// A bent arc that becomes an elbow isn't told off for what it had.
	ops(t, newEditor(), `[`+two+`{"add":"arrow","id":"s","from":"a","to":"b","bend":40},{"set":"s","kind":"elbow"}]`)
}

func TestArrowAlongAnEdge(t *testing.T) {
	const block = `{"add":"card","id":"block","title":"Block","x":200,"y":0,"w":200,"h":100},`
	wantWarnings(t, newEditor(), `[`+block+`{"add":"arrow","id":"s","x1":100,"y1":0,"x2":500,"y2":0}]`, "s runs along block's top edge for 200")
	wantWarnings(t, newEditor(), `[`+block+`{"add":"arrow","id":"s","x1":100,"y1":102,"x2":500,"y2":102}]`, "s runs along block's bottom edge")
	wantWarnings(t, newEditor(), `[`+block+`{"add":"arrow","id":"s","x1":200,"y1":-50,"x2":200,"y2":150}]`, "s runs along block's left edge")
	// Clear of it, or only touching at a corner of the route, it's fine.
	wantWarnings(t, newEditor(), `[`+block+`{"add":"arrow","id":"s","x1":100,"y1":-20,"x2":500,"y2":-20}]`)
	wantWarnings(t, newEditor(), `[`+block+`{"add":"arrow","id":"s","x1":190,"y1":0,"x2":210,"y2":0}]`)
	// The reported case: over the card, keeping the elbow, exactly on its top.
	wantWarnings(t, newEditor(), `[{"add":"rectangle","id":"a","x":0,"y":100,"w":100,"h":100},{"add":"card","id":"block","title":"Block","x":200,"y":0,"w":200,"h":100},{"add":"rectangle","id":"c","x":500,"y":100,"w":100,"h":100},{"add":"arrow","id":"u","from":"a","to":"c","kind":"elbow","fromAt":[0.5,0],"toAt":[0.5,0],"via":[[300,0]]}]`, "u runs along block's top edge for 200")
}

func TestArrowsSharingALine(t *testing.T) {
	const three = `{"add":"rectangle","id":"a","x":0,"y":100,"w":100,"h":100,"text":"a"},{"add":"rectangle","id":"b","x":500,"y":0,"w":100,"h":100,"text":"b"},{"add":"rectangle","id":"c","x":500,"y":200,"w":100,"h":100,"text":"c"},`
	// The reported case: two from one card, one line until they part.
	out := wantWarnings(t, newEditor(), `[`+three+`{"add":"arrow","id":"p","from":"a","to":"b","kind":"elbow"},{"add":"arrow","id":"q","from":"a","to":"c","kind":"elbow"}]`, "p and q share a trunk from a")
	if strings.Count(out, "share a") != 1 {
		t.Errorf("told twice: %s", out)
	}
	// Into one anchor it's as much a trunk, and with both ends alike it's no tree.
	wantWarnings(t, newEditor(), `[`+three+`{"add":"arrow","id":"p","from":"b","to":"a","kind":"elbow"},{"add":"arrow","id":"q","from":"c","to":"a","kind":"elbow"}]`, "p and q share a trunk into a")
	wantWarnings(t, newEditor(), `[`+three+`{"add":"arrow","id":"p","from":"a","to":"b"},{"add":"arrow","id":"q","from":"a","to":"b"}]`, "p and q share a line")
	// Both ways along the same straight line.
	wantWarnings(t, newEditor(), `[`+three+`{"add":"arrow","id":"p","from":"a","to":"b"},{"add":"arrow","id":"q","from":"b","to":"a"}]`, "p and q share a line")
	// Apart, or just starting from one point, they don't.
	wantWarnings(t, newEditor(), `[`+three+`{"add":"arrow","from":"a","to":"b"},{"add":"arrow","from":"a","to":"c"}]`)
	wantWarnings(t, newEditor(), `[`+three+`{"add":"arrow","from":"a","to":"b","kind":"elbow","fromAt":[1,0.25],"toAt":[0,0.5]},{"add":"arrow","from":"a","to":"c","kind":"elbow","fromAt":[1,0.75],"toAt":[0,0.5]}]`)
	// Not again, in a batch that touches neither.
	e := newEditor()
	ops(t, e, `[`+three+`{"add":"arrow","id":"p","from":"a","to":"b"},{"add":"arrow","id":"q","from":"b","to":"a"}]`)
	wantWarnings(t, e, `[{"add":"rectangle","x":0,"y":800,"text":"far"}]`)
}

// A path of several strokes is one thing to whoever draws it, so an arrow takes
// it as that. Where no shape stands for the group, the error names those to use.
func TestArrowToAGroup(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"draw","id":"cyl","d":"M0 20 C0 -6 100 -6 100 20 C100 46 0 46 0 20 M0 20 L0 120 C0 146 100 146 100 120 L100 20","fill":"solid"},{"add":"rectangle","id":"a","x":400,"y":0},{"add":"arrow","id":"s","from":"a","to":"cyl"}]`)
	if b := e.BindingOf(shapeID("s"), "end"); b == nil || b.To != shapeID("cyl-2") {
		t.Errorf("bound to %v", b)
	}
	if got, _ := read(e, readArgs{IDs: names{"s"}}, "", ""); !strings.Contains(got, "to cyl-2") {
		t.Errorf("read: %s", got)
	}
	ops(t, e, `[{"set":"cyl","dx":0,"dy":300}]`)
	if line := arrowLine(e, "s"); line[len(line)-1].Y < 250 {
		t.Errorf("didn't follow: %v", line)
	}
	_, err := tryOps(newEditor(), `[{"add":"rectangle","id":"x","x":0,"y":0},{"add":"rectangle","id":"y","x":400,"y":0},{"group":["x","y"],"id":"g"},{"add":"arrow","x1":0,"y1":500,"to":"g"}]`)
	if err == nil || !strings.Contains(err.Error(), "bind to one in it: x, y") {
		t.Errorf("got %v", err)
	}
}

func TestFrameSize(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"frame","id":"f","name":"API","x":0,"y":0,"w":300,"h":200,"size":"l","font":"sans","color":"blue"}]`)
	f := e.Doc.Shapes[shapeID("f")]
	if size, font := editor.FrameHeading(f); size != editor.TextSize("l") || font != "sans" {
		t.Errorf("got %v %q", size, font)
	}
	got, _ := read(e, readArgs{IDs: names{"f"}}, "", "")
	if !strings.Contains(got, "size l") || !strings.Contains(got, "font sans") {
		t.Errorf("read: %s", got)
	}
	// Kept in the file, for tldraw's strict props not to see it.
	var buf bytes.Buffer
	if err := doc.Write(&buf, e.Doc); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), `"props":{"w":300,"h":200,"name":"API","color":"blue","size"`) {
		t.Error("size went into the frame's props")
	}
	back, err := doc.Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if size, _ := editor.FrameHeading(back.Shapes[shapeID("f")]); size != editor.TextSize("l") {
		t.Error("the size wasn't saved")
	}
	// A frame left alone has the heading it always had.
	ops(t, e, `[{"add":"frame","id":"g","name":"Plain","x":500,"y":0,"w":300,"h":200}]`)
	if size, font := editor.FrameHeading(e.Doc.Shapes[shapeID("g")]); size != 0 || font != "" {
		t.Errorf("plain: %v %q", size, font)
	}
	// One that now reaches what is above it says so; a small one doesn't.
	wantWarnings(t, e, `[{"add":"rectangle","id":"r","x":0,"y":-85,"w":200,"h":80,"text":"r"}]`, "frame f's name runs into r")
	wantWarnings(t, e, `[{"add":"rectangle","x":500,"y":-85,"w":200,"h":80,"text":"r2"}]`)
	// Group styles and the batch's reach it, as they do any shape's.
	ops(t, e, `[{"style":{"size":"xl"}},{"add":"frame","id":"h","name":"Styled","x":1000,"y":0,"w":300,"h":200}]`)
	if size, _ := editor.FrameHeading(e.Doc.Shapes[shapeID("h")]); size != editor.TextSize("xl") {
		t.Errorf("styled: %v", size)
	}
}

// A view of the whole leaves room for a frame's heading, however small the
// picture makes the page.
func TestViewKeepsFrameHeadings(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"frame","id":"f","name":"Top","x":0,"y":0,"w":4000,"h":500}]`)
	box, _, scale, err := viewPlan(e, viewArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if need := editor.FrameHeadingFloor * 1.6 / scale; box.Y > -need {
		t.Errorf("box starts at %v, with %v needed above the frame", box.Y, need)
	}
}

// fanOut is a tree whose six arrows share a trunk, on a page with two real
// problems: a card over a frame's name, and an arrow its frame clips.
const fanOut = `{"add":"frame","id":"f","name":"Tree","size":"m","x":0,"y":0,"w":900,"h":800},` +
	`{"add":"card","id":"root","title":"Root","in":"f","x":20,"y":350,"w":100},` +
	`{"add":"card","id":"t1","title":"1","in":"f","x":600,"y":20,"w":100},` +
	`{"add":"card","id":"t2","title":"2","in":"f","x":600,"y":140,"w":100},` +
	`{"add":"card","id":"t3","title":"3","in":"f","x":600,"y":260,"w":100},` +
	`{"add":"card","id":"t4","title":"4","in":"f","x":600,"y":440,"w":100},` +
	`{"add":"card","id":"t5","title":"5","in":"f","x":600,"y":560,"w":100},` +
	`{"add":"card","id":"t6","title":"6","in":"f","x":600,"y":680,"w":100},` +
	`{"add":"arrow","id":"a1","from":"root","to":"t1","kind":"elbow"},` +
	`{"add":"arrow","id":"a2","from":"root","to":"t2","kind":"elbow"},` +
	`{"add":"arrow","id":"a3","from":"root","to":"t3","kind":"elbow"},` +
	`{"add":"arrow","id":"a4","from":"root","to":"t4","kind":"elbow"},` +
	`{"add":"arrow","id":"a5","from":"root","to":"t5","kind":"elbow"},` +
	`{"add":"arrow","id":"a6","from":"root","to":"t6","kind":"elbow"},` +
	`{"add":"card","id":"lid","title":"Over","x":0,"y":-40,"w":200},` +
	`{"add":"arrow","id":"out","from":"t1","to":"t2","kind":"elbow","via":[[1000,110]]}`

// The reported case: fifteen pairs in one tree hid what was really wrong.
func TestFanOutDoesntHideWarnings(t *testing.T) {
	out := wantWarnings(t, newEditor(), `[`+fanOut+`]`,
		"frame f's name runs into lid",
		`out sticks out of frame f, which clips it; in:"" takes it out of the frame`,
		"a1, a2, a3, a4, a5, a6 share a trunk from root")
	if n := strings.Count(out, "share a"); n != 1 {
		t.Errorf("told of %d shared lines:\n%s", n, out)
	}
	if strings.Index(out, "share a trunk") < strings.Index(out, "sticks out") {
		t.Errorf("the trunk comes before what's wrong:\n%s", out)
	}
	// With so much else to tell, the trunk is what doesn't fit.
	out = wantWarnings(t, newEditor(), `[`+fanOut+`,{"add":"arrow","id":"p","from":"t1","to":"t6"},{"add":"arrow","id":"q","from":"t6","to":"t1"}]`, "p and q share a line")
	if strings.Contains(out, "share a trunk") || !strings.Contains(out, "and 1 more") {
		t.Errorf("the trunk isn't what's cut:\n%s", out)
	}
	// More than enough of them still fit in a line.
	var list strings.Builder
	list.WriteString(`{"add":"rectangle","id":"r","x":0,"y":0,"w":100,"h":100}`)
	for i := range 20 {
		fmt.Fprintf(&list, `,{"add":"rectangle","id":"t%d","x":3000,"y":%d,"w":100,"h":60},{"add":"arrow","id":"a%d","from":"r","to":"t%d","kind":"elbow"}`, i, i*100-600, i, i)
	}
	wantWarnings(t, newEditor(), `[`+list.String()+`]`, "a0, a1, a2, a3, a4, a5, a6, a7 and 12 more share a trunk from r")
}

func TestOffShapeAnchorsAreRefused(t *testing.T) {
	const two = `{"add":"rectangle","id":"a","x":0,"y":0},{"add":"rectangle","id":"b","x":400,"y":0},`
	for _, at := range []string{`"fromAt":[2,0]`, `"fromAt":[0,-0.1]`, `"toAt":[0.5,1.01]`, `"toAt":[0.5]`} {
		if _, err := tryOps(newEditor(), `[`+two+`{"add":"arrow","from":"a","to":"b",`+at+`}]`); err == nil || !strings.Contains(err.Error(), "each 0 to 1 across the shape") {
			t.Errorf("%s: got %v", at, err)
		}
	}
	ops(t, newEditor(), `[`+two+`{"add":"arrow","from":"a","to":"b","fromAt":[0,1],"toAt":[1,0]}]`)
	e := newEditor()
	ops(t, e, `[`+two+`{"add":"arrow","id":"s","from":"a","to":"b"}]`)
	if _, err := tryOps(e, `[{"set":"s","fromAt":[2,0]}]`); err == nil {
		t.Error("set took an anchor off the shape")
	}
}

func viaOf(t *testing.T, e *editor.Editor, id string) string {
	t.Helper()
	got, err := read(e, readArgs{IDs: names{id}}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// A route keeps to what it joins when that all moves together, by one op or by
// several, and is left alone by a move that says where it goes.
func TestViaFollowsWhatItJoins(t *testing.T) {
	const row = `[` + threeInARow + `{"add":"arrow","id":"s","from":"a","to":"b","kind":"elbow","fromAt":[0.5,0],"toAt":[0.5,0],"via":[[300,-40]]}]`
	e := newEditor()
	ops(t, e, row)
	before := arrowLine(e, "s")
	out := wantWarnings(t, e, `[{"set":["a","m","b"],"dy":200}]`)
	if got := viaOf(t, e, "s"); !strings.Contains(got, "via [[300,160]]") {
		t.Errorf("via stayed behind: %s", got)
	}
	if !strings.Contains(out, "\ns ") {
		t.Errorf("the arrow isn't told of as changed: %s", out)
	}
	after := arrowLine(e, "s")
	if len(before) != len(after) {
		t.Fatalf("route changed shape: %v to %v", before, after)
	}
	for i := range before {
		if after[i].Sub(before[i]).Dist(geo.V(0, 200)) > 0.01 {
			t.Errorf("route changed shape: %v to %v", before, after)
			break
		}
	}

	// In one apply, however many ops it takes.
	e = newEditor()
	ops(t, e, row)
	ops(t, e, `[{"set":"a","dx":50},{"set":"b","dx":50}]`)
	if got := viaOf(t, e, "s"); !strings.Contains(got, "via [[350,-40]]") {
		t.Errorf("two ops: %s", got)
	}

	// Said in the same batch, it's where it was said to be.
	e = newEditor()
	ops(t, e, row)
	ops(t, e, `[{"set":["a","b"],"dx":50},{"set":"s","via":[[0,-90]]}]`)
	if got := viaOf(t, e, "s"); !strings.Contains(got, "via [[0,-90]]") {
		t.Errorf("said: %s", got)
	}

	// One end moving says nothing of where it should go.
	e = newEditor()
	ops(t, e, row)
	ops(t, e, `[{"set":"b","dx":50}]`)
	if got := viaOf(t, e, "s"); !strings.Contains(got, "via [[300,-40]]") {
		t.Errorf("one end: %s", got)
	}

	// Bound to something else, it's no longer the same arrow.
	e = newEditor()
	ops(t, e, row+``)
	ops(t, e, `[{"set":"a","dx":0,"dy":100},{"set":"b","dy":100},{"set":"s","to":"m"}]`)
	if got := viaOf(t, e, "s"); !strings.Contains(got, "via [[300,-40]]") {
		t.Errorf("rebound: %s", got)
	}

	// A dry run leaves it where it is.
	e = newEditor()
	ops(t, e, row)
	if _, err := apply(e, mustOps(t, `[{"set":["a","b"],"dy":200}]`), applyMode{dry: true}); err != nil {
		t.Fatal(err)
	}
	if got := viaOf(t, e, "s"); !strings.Contains(got, "via [[300,-40]]") {
		t.Errorf("dry: %s", got)
	}

	// An arrow in a frame moves with it: not twice over.
	e = newEditor()
	ops(t, e, `[{"add":"frame","id":"f","x":0,"y":0,"w":800,"h":400},{"add":"rectangle","id":"a","in":"f","x":20,"y":100,"w":100,"h":100},{"add":"rectangle","id":"b","in":"f","x":500,"y":100,"w":100,"h":100},{"add":"arrow","id":"s","from":"a","to":"b","kind":"elbow","via":[[300,20]]}]`)
	ops(t, e, `[{"set":"f","dx":100}]`)
	if got := viaOf(t, e, "s"); !strings.Contains(got, "via [[400,20]]") {
		t.Errorf("in a frame: %s", got)
	}
	// And one that left the frame is taken along all the same.
	ops(t, e, `[{"set":"s","in":""},{"set":"f","dx":100}]`)
	if got := viaOf(t, e, "s"); !strings.Contains(got, "via [[500,20]]") {
		t.Errorf("out of the frame: %s", got)
	}
}

func mustOps(t *testing.T, list string) []*op {
	t.Helper()
	o, err := decodeOps(json.RawMessage(list))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// The reported case: only the target moved, past a waypoint that was in its way.
func TestWarnsOfARouteCrossingItself(t *testing.T) {
	e := newEditor()
	wantWarnings(t, e, `[{"add":"rectangle","id":"a","x":0,"y":0,"w":100,"h":100},{"add":"rectangle","id":"b","x":500,"y":0,"w":100,"h":100},{"add":"arrow","id":"s","from":"a","to":"b","kind":"elbow","fromAt":[1,0.5],"toAt":[0,0.5],"via":[[300,-80]]}]`)
	out := wantWarnings(t, e, `[{"set":"b","x":200,"y":300}]`, "s's route crosses itself; its via may be out of place")
	if !crossesItself(arrowLine(e, "s")) {
		t.Errorf("no crossing in %v:\n%s", arrowLine(e, "s"), out)
	}
	wantWarnings(t, e, `[{"set":"s","via":[]}]`)

	for _, c := range []struct {
		pts  []geo.Vec
		want bool
	}{
		{[]geo.Vec{geo.V(0, 0), geo.V(100, 0), geo.V(100, 100), geo.V(200, 100)}, false},
		{[]geo.Vec{geo.V(0, 0), geo.V(100, 0), geo.V(100, 100), geo.V(50, 100), geo.V(50, -50)}, true},
		{[]geo.Vec{geo.V(0, 0), geo.V(100, 0), geo.V(20, 0)}, true},
		{[]geo.Vec{geo.V(0, 0), geo.V(100, 0), geo.V(100, 100), geo.V(0, 100)}, false},
	} {
		if got := crossesItself(c.pts); got != c.want {
			t.Errorf("%v: got %v", c.pts, got)
		}
	}
}

func TestCheckCoversTheWholePage(t *testing.T) {
	e := newEditor()
	ops(t, e, `[{"add":"frame","id":"f","name":"Top","size":"l","x":0,"y":100,"w":300,"h":200},{"add":"rectangle","id":"r","x":0,"y":20,"w":200,"h":80,"text":"r"},{"add":"rectangle","id":"far","x":800,"y":0,"text":"far"},{"add":"rectangle","id":"gone","x":800,"y":200,"text":"gone"}]`)
	// What a batch didn't touch it doesn't speak of, even dry.
	dry, err := apply(e, mustOps(t, `[{"del":"gone"}]`), applyMode{dry: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dry, "warnings") {
		t.Errorf("dry: %s", dry)
	}
	got, err := read(e, readArgs{Check: true}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `warnings 1-1 of 1 on page "Page 1"`) || !strings.Contains(got, "frame f's name runs into r") {
		t.Errorf("check: %s", got)
	}
	// It changes nothing, and can be undone to nothing.
	if _, err := read(e, readArgs{Check: true}, "", ""); err != nil || e.Doc.Shapes["shape:gone"] == nil {
		t.Errorf("check changed something: %v", err)
	}
	ops(t, e, `[{"del":"r"}]`)
	if got, _ := read(e, readArgs{Check: true}, "", ""); got != `no warnings on page "Page 1"` {
		t.Errorf("clean: %s", got)
	}

	// Of another page, by name, and what straight arrows can't do.
	ops(t, e, `[{"page":"Two"},{"add":"rectangle","id":"a","x":0,"y":0,"w":100,"h":100},{"add":"rectangle","id":"b","x":300,"y":0,"w":100,"h":100},{"add":"arrow","id":"s","from":"a","to":"b","straight":"h"}]`)
	ops(t, e, `[{"set":"b","y":400},{"page":"Page 1"}]`)
	got, err = read(e, readArgs{Check: true, Page: "Two"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "s can't be level: a and b share no height") {
		t.Errorf("other page: %s", got)
	}

	// A long list comes in pages, which apply's answer points to.
	e = newEditor()
	var list []string
	for range 30 {
		list = append(list, `{"add":"rectangle","x":0,"y":0,"w":100,"h":100,"text":"same"}`)
	}
	ops(t, e, "["+strings.Join(list, ",")+"]")
	got, _ = read(e, readArgs{Check: true, Limit: 5, Offset: 2}, "", "")
	if !strings.Contains(got, "warnings 3-7 of ") || !strings.HasSuffix(got, "more: offset 7") {
		t.Errorf("paged: %s", got)
	}
	if got, _ := read(e, readArgs{Check: true, Offset: 9999}, "", ""); !strings.HasPrefix(got, "warnings: ") {
		t.Errorf("past the end: %s", got)
	}
	if _, err := read(e, readArgs{Check: true, Find: "x"}, "", ""); err == nil {
		t.Error("check took find")
	}
}

func TestCheckOverHTTP(t *testing.T) {
	h := &fakeHost{}
	s := connect(t, h)
	call(t, s, "apply", map[string]any{"ops": []any{map[string]any{"add": "rectangle", "x": 0, "y": 0}}})
	if got := textOf(call(t, s, "read", map[string]any{"check": true})); !strings.HasPrefix(got, "no warnings") {
		t.Errorf("got %s", got)
	}
}

func TestArrowOverAFramesName(t *testing.T) {
	const frame = `{"add":"frame","id":"f","name":"A long name for it","size":"l","x":0,"y":200,"w":800,"h":300},{"add":"rectangle","id":"in","in":"f","x":40,"y":300,"w":100,"h":100},`
	wantWarnings(t, newEditor(), `[`+frame+`{"add":"rectangle","id":"up","x":0,"y":-100,"w":100,"h":60},{"add":"arrow","id":"s","from":"up","to":"in"}]`, "s crosses frame f's name")
	// A plain name is just as crossed.
	wantWarnings(t, newEditor(), `[{"add":"frame","id":"f","name":"A long name for it","x":0,"y":200,"w":800,"h":300},{"add":"rectangle","id":"in","in":"f","x":0,"y":300,"w":100,"h":100},{"add":"rectangle","id":"up","x":0,"y":-100,"w":100,"h":60},{"add":"arrow","id":"s","from":"up","to":"in"}]`, "s crosses frame f's name")
	// Past the name, or inside what clips it, it's fine.
	wantWarnings(t, newEditor(), `[`+frame+`{"add":"rectangle","id":"up","x":700,"y":-100,"w":60,"h":60},{"add":"rectangle","id":"low","in":"f","x":700,"y":300,"w":60,"h":60},{"add":"arrow","id":"s","from":"up","to":"low"}]`)
	wantWarnings(t, newEditor(), `[`+frame+`{"add":"rectangle","id":"low","in":"f","x":400,"y":300,"w":100,"h":100},{"add":"arrow","id":"s","in":"f","from":"in","to":"low"}]`)
	// Moving the frame under one counts.
	e := newEditor()
	wantWarnings(t, e, `[`+frame+`{"add":"arrow","id":"s","x1":10,"y1":-100,"x2":10,"y2":100}]`)
	wantWarnings(t, e, `[{"set":"f","y":50}]`, "s crosses frame f's name")
	// A move of what the arrow joins counts too.
	e = newEditor()
	wantWarnings(t, e, `[`+frame+`{"add":"rectangle","id":"p","x":1000,"y":-100,"w":60,"h":60},{"add":"rectangle","id":"q","x":1000,"y":600,"w":60,"h":60},{"add":"arrow","id":"s","from":"p","to":"q"}]`)
	wantWarnings(t, e, `[{"set":["p","q"],"x":50}]`, "s crosses frame f's name")
}

// What clips an arrow with both ends in a frame says how to stop it, and doing
// that does.
func TestWarningOfAClippedRouteSaysHowToFixIt(t *testing.T) {
	e := newEditor()
	wantWarnings(t, e, `[{"add":"frame","id":"f","x":0,"y":0,"w":500,"h":200},{"add":"rectangle","id":"a","in":"f","x":20,"y":50,"w":100,"h":100},{"add":"rectangle","id":"b","in":"f","x":300,"y":50,"w":100,"h":100},{"add":"arrow","id":"s","from":"a","to":"b","kind":"elbow","via":[[200,-60]]}]`,
		`s sticks out of frame f, which clips it; in:"" takes it out of the frame`)
	wantWarnings(t, e, `[{"set":"s","in":""}]`)
	if e.Doc.Shapes["shape:s"].Parent != e.Page {
		t.Error("in:\"\" left it in the frame")
	}
	// Moving what it joins so that its route leaves the frame counts too.
	e = newEditor()
	wantWarnings(t, e, `[{"add":"frame","id":"f","x":0,"y":0,"w":500,"h":200},{"add":"rectangle","id":"a","in":"f","x":20,"y":50,"w":100,"h":100},{"add":"rectangle","id":"b","in":"f","x":300,"y":50,"w":100,"h":100},{"add":"arrow","id":"s","from":"a","to":"b"}]`)
	wantWarnings(t, e, `[{"set":"b","y":400}]`, "s sticks out of frame f")
}

func TestGroupErrorNamesCards(t *testing.T) {
	_, err := tryOps(newEditor(), `[{"add":"card","id":"eq1","title":"One","body":"Body","x":0,"y":0},{"add":"card","id":"eq2","title":"Two","x":400,"y":0},{"group":["eq1","eq2"],"id":"g"},{"add":"arrow","x1":0,"y1":500,"to":"g"}]`)
	if err == nil || !strings.Contains(err.Error(), "bind to one in it: eq1, eq2") {
		t.Errorf("got %v", err)
	}
	// And what it names binds.
	ops(t, newEditor(), `[{"add":"card","id":"eq1","title":"One","x":0,"y":0},{"add":"card","id":"eq2","title":"Two","x":400,"y":0},{"group":["eq1","eq2"],"id":"g"},{"add":"arrow","x1":0,"y1":500,"to":"eq1"}]`)
}

func TestWrongTypesTeach(t *testing.T) {
	for list, want := range map[string]string{
		`[{"add":"arrow","x1":0,"y1":0,"x2":9,"y2":9,"kind":"elbow","via":[["bad"]]}]`: "via is a list of [x,y] points",
		`[{"add":"arrow","x1":0,"y1":0,"x2":9,"y2":9,"kind":"elbow","via":"bad"}]`:     "via is a list of [x,y] points",
		`[{"add":"arrow","x1":0,"y1":0,"x2":9,"y2":9,"fromAt":["a","b"]}]`:             "fromAt and toAt are [x,y]",
		`[{"add":"rectangle","x":"5"}]`:                                                "x must be a number",
		`[{"add":"rectangle","text":5}]`:                                               "text must be a string",
		`[{"add":"rectangle","locked":"yes"}]`:                                         "locked must be true or false",
		`[{"add":5}]`:                                                                  "add must be a string",
		`[{"dup":"a","n":"two"}]`:                                                      "n must be a whole number",
		`[{"style":{"color":5}}]`:                                                      "color must be a string",
		`[5]`:                                                                          "an op is an object",
	} {
		_, err := decodeOps(json.RawMessage(list))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", list, err, want)
		}
		if err != nil && strings.Contains(err.Error(), "unmarshal") {
			t.Errorf("%s: says %v", list, err)
		}
	}
}

func TestDupToldOfLostEnds(t *testing.T) {
	const two = `{"add":"rectangle","id":"a","x":0,"y":0},{"add":"rectangle","id":"b","x":400,"y":0},{"add":"arrow","id":"s","from":"a","to":"b"},{"add":"arrow","id":"half","from":"a","x2":50,"y2":500},`
	wantWarnings(t, newEditor(), `[`+two+`{"dup":"s","dy":300}]`, "the copy of s lost its from and to")
	wantWarnings(t, newEditor(), `[`+two+`{"dup":["s","a"],"dy":300}]`, "the copy of s lost its to")
	wantWarnings(t, newEditor(), `[`+two+`{"dup":["half"],"dy":300}]`, "the copy of half lost its from")
	// Copied with what it joins, it joins the copies.
	e := newEditor()
	wantWarnings(t, e, `[`+two+`{"del":"half"},{"dup":["a","b","s"],"dy":300}]`)
	bound := 0
	for id, s := range e.Doc.Shapes {
		if s.Type == doc.TypeArrow && id != shapeID("s") && e.BindingOf(id, "start") != nil && e.BindingOf(id, "end") != nil {
			bound++
		}
	}
	if bound != 1 {
		t.Errorf("%d copies stayed bound", bound)
	}
}

// At the smallest size there's no smaller style for the body, so it scales down,
// and goes back to whole sizes when the title does.
func TestCardBodySmallerAtSizeS(t *testing.T) {
	body := func(e *editor.Editor) float64 {
		s := e.Doc.Shapes["shape:c-body"]
		return editor.TextSize(s.Size) * s.Scale
	}
	head := func(e *editor.Editor) float64 {
		s := e.Doc.Shapes["shape:c-head"]
		return editor.TextSize(s.Size) * s.Scale
	}
	e := newEditor()
	ops(t, e, `[{"add":"card","id":"c","title":"Title","body":"Some body text","size":"s","x":0,"y":0}]`)
	if body(e) >= head(e) {
		t.Errorf("body %v, title %v", body(e), head(e))
	}
	// The box fits the text at that size.
	box := e.Doc.Shapes["shape:c-box"]
	want := e.Geometry(e.Doc.Shapes["shape:c-head"]).Bounds.H + cardGap + e.Geometry(e.Doc.Shapes["shape:c-body"]).Bounds.H + 2*cardPad
	if !near(box.H, want) {
		t.Errorf("box %v, text %v", box.H, want)
	}
	ops(t, e, `[{"set":"c","size":"m"}]`)
	if got := e.Doc.Shapes["shape:c-body"]; got.Size != "s" || got.Scale != 1 || head(e) != editor.TextSize("m") {
		t.Errorf("at m: body %s %v", got.Size, got.Scale)
	}
	ops(t, e, `[{"set":"c","size":"s"}]`)
	if body(e) >= head(e) {
		t.Errorf("back at s: body %v, title %v", body(e), head(e))
	}
	// Through the batch's style, and on a body added later.
	e = newEditor()
	ops(t, e, `[{"style":{"size":"s"}},{"add":"card","id":"c","title":"Title","body":"Body","x":0,"y":0}]`)
	if body(e) >= head(e) {
		t.Errorf("style: body %v, title %v", body(e), head(e))
	}
	// Saved and read back.
	var buf bytes.Buffer
	if err := doc.Write(&buf, e.Doc); err != nil {
		t.Fatal(err)
	}
	back, err := doc.Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := back.Shapes["shape:c-body"].Scale; got != cardSmallBody {
		t.Errorf("saved as %v", got)
	}
}
