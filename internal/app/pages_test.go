package app

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"godraw/internal/doc"
	"godraw/internal/editor"
)

// renameFirstPage opens the list of pages, and double clicks the page's name.
func renameFirstPage(t *testing.T, a *App, tt *ui.Tester) {
	t.Helper()
	if err := tt.Click("Pages"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	add, ok := tt.Find("New page")
	if !ok {
		t.Fatal("no list of pages")
	}
	// The row above the divider above "New page".
	x, y := add.X+add.W/2, add.Y-24
	tt.ClickAt(x, y)
	tt.ClickAt(x, y)
	tt.Frame()
	if a.renamingPage == "" {
		t.Fatal("a double click didn't start a rename")
	}
}

func pageName(a *App) string { return a.Editor().Doc.Pages[0].Name }

func TestClickingAwayEndsAPageRename(t *testing.T) {
	a, tt, _ := newTest(t)
	renameFirstPage(t, a, tt)
	tt.Type("Plans")
	tt.Frame()
	tt.ClickAt(700, 500)
	tt.Frame()
	if a.pagesOpen {
		t.Error("the list stayed open")
	}
	if a.renamingPage != "" {
		t.Fatalf("a rename stays open with nothing to end it")
	}
	if got := pageName(a); got != "Page 1Plans" {
		t.Errorf("the page is %q", got)
	}
	tt.Key(0, ui.KeyR)
	if e := a.Editor(); e.Tool != editor.ToolGeo {
		t.Errorf("tool %q: the keyboard is dead", e.Tool)
	}
}

// The press that ends a rename starts something else, which is a step of its own.
func TestARenameEndedByAStrokeStaysApart(t *testing.T) {
	a, tt, _ := newTest(t)
	e := a.Editor()
	renameFirstPage(t, a, tt)
	tt.Type("Plans")
	tt.Frame()
	// The tool is picked with the list open, as the toolbar can.
	e.SetTool(editor.ToolDraw)

	tt.Press(700, 500)
	tt.Move(760, 540)
	if !e.InGesture() {
		t.Fatal("no stroke under way")
	}
	if got := pageName(a); got != "Page 1Plans" {
		t.Errorf("the page is %q mid stroke", got)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Release(760, 540)
	tt.Frame()
	if got := pageName(a); got != "Page 1Plans" {
		t.Errorf("cancelling the stroke made the page %q", got)
	}
	if len(e.Doc.Shapes) != 0 {
		t.Errorf("%d shapes after cancelling", len(e.Doc.Shapes))
	}

	e.SetTool(editor.ToolDraw)
	drag(tt, 700, 500, 760, 540)
	if len(e.Doc.Shapes) != 1 {
		t.Fatalf("%d shapes after a stroke", len(e.Doc.Shapes))
	}
	e.Undo()
	if len(e.Doc.Shapes) != 0 {
		t.Errorf("%d shapes after undoing the stroke", len(e.Doc.Shapes))
	}
	if got := pageName(a); got != "Page 1Plans" {
		t.Errorf("undoing the stroke made the page %q", got)
	}
}

func TestEscapeCancelsAPageRename(t *testing.T) {
	a, tt, _ := newTest(t)
	before := pageName(a)
	renameFirstPage(t, a, tt)
	tt.Type("Plans")
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.renamingPage != "" {
		t.Errorf("still renaming %s", a.renamingPage)
	}
	if got := pageName(a); got != before {
		t.Errorf("Escape renamed the page to %q", got)
	}
	// The list may be open or not: the keyboard works either way, once it's shut.
	a.pagesOpen = false
	tt.Frame()
	tt.Key(0, ui.KeyR)
	if e := a.Editor(); e.Tool != editor.ToolGeo {
		t.Errorf("tool %q: the keyboard is dead", e.Tool)
	}
}

func TestEnterEndsAPageRename(t *testing.T) {
	a, tt, _ := newTest(t)
	renameFirstPage(t, a, tt)
	tt.Type("Plans")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if got := pageName(a); got != "Page 1Plans" || a.renamingPage != "" {
		t.Errorf("page %q, renaming %q", got, a.renamingPage)
	}
}

// A page can go while its rename is open, as a file reloads.
func TestAPageRenameDoesntOutliveItsPage(t *testing.T) {
	a, tt, _ := newTest(t)
	renameFirstPage(t, a, tt)
	d := doc.New()
	d.Pages[0].ID = "page:other"
	a.Editor().Replace(d)
	tt.Frame()
	if a.renamingPage != "" {
		t.Errorf("renaming %s, which is gone", a.renamingPage)
	}
}

func TestEnterLeavesALockedShape(t *testing.T) {
	a, tt, _ := newTest(t)
	e := a.Editor()
	tt.Key(0, ui.KeyR)
	drag(tt, 200, 200, 400, 320)
	e.ToggleLock()
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if e.Editing != "" {
		t.Error("a locked shape took writing")
	}
}

func TestExportTakesTheDocumentsName(t *testing.T) {
	a, _, h := newTest(t)
	h.name = "plans.godraw"
	if got := a.exportName(".svg"); got != "plans.svg" {
		t.Errorf("exports as %q", got)
	}
	h.name = "Untitled 2"
	if got := a.exportName(".png"); got != "Untitled 2.png" {
		t.Errorf("exports as %q", got)
	}
	h.name = ""
	if got := a.exportName(".png"); got != "drawing.png" {
		t.Errorf("exports as %q", got)
	}
	// What's inside a file says less than its name, which may have changed since.
	a.Editor().Doc.Name = "named"
	if got := a.exportName(".png"); got != "named.png" {
		t.Errorf("exports as %q", got)
	}
	h.name = "poster.godraw"
	if got := a.exportName(".png"); got != "poster.png" {
		t.Errorf("exports as %q", got)
	}
}
