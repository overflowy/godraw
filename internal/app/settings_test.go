package app

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"godraw/internal/doc"
	"godraw/internal/editor"
	"godraw/internal/settings"
)

// A window opened with the last launch's settings shows the drawing as it was.
func TestAWindowTakesTheSettingsItHadLastTime(t *testing.T) {
	d := doc.New()
	first := New(d, &testHost{})
	first.Editor().NewPage()
	d = first.Editor().Doc
	second := d.Pages[1].ID

	p := settings.DefaultPrefs()
	p.Theme, p.Grid, p.Minimap = "dark", true, true
	p.Style.Color = "green"
	cam := editor.Camera{X: 120, Y: -40, Z: 2}

	a := New(d, &testHost{})
	a.SetPrefs(p)
	a.SetViewState(settings.View{Page: second, Cams: map[doc.ID]editor.Camera{second: cam, "gone": {Z: 1}}})
	var views []settings.View
	a.OnViewChange = func(v settings.View) { views = append(views, v) }
	tt := ui.NewTester(a.View, 1280, 820)
	tt.Frame()
	tt.Frame()

	e := a.Editor()
	if e.Page != second {
		t.Errorf("shows page %s, not %s", e.Page, second)
	}
	if got := e.Cam(); got != cam {
		t.Errorf("camera %+v, not %+v: the page was fitted again", got, cam)
	}
	if !e.DarkMode || !e.Grid || !a.minimap || e.Next.Color != "green" {
		t.Errorf("prefs not taken: dark %v grid %v minimap %v color %s", e.DarkMode, e.Grid, a.minimap, e.Next.Color)
	}
	if a.Prefs() != p {
		t.Errorf("prefs %+v, not %+v", a.Prefs(), p)
	}
	if len(views) != 1 {
		t.Fatalf("told of %d views, not 1", len(views))
	}
	if _, ok := views[0].Cams["gone"]; ok {
		t.Error("a page the drawing doesn't have kept a camera")
	}
}

func TestAWindowTellsWhatChanged(t *testing.T) {
	a := New(doc.New(), &testHost{})
	var prefs [][2]settings.Prefs
	var views []settings.View
	a.OnPrefsChange = func(was, now settings.Prefs) { prefs = append(prefs, [2]settings.Prefs{was, now}) }
	a.OnViewChange = func(v settings.View) { views = append(views, v) }
	tt := ui.NewTester(a.View, 1280, 820)
	tt.Frame()
	tt.Frame()
	if len(prefs) != 0 {
		t.Errorf("told of %d changes of prefs that didn't change", len(prefs))
	}
	nViews := len(views)

	tt.Key(ui.Cmd, ui.KeyQuote) // the grid
	tt.Frame()
	if len(prefs) != 1 || prefs[0][0].Grid || !prefs[0][1].Grid {
		t.Fatalf("turning the grid on told %+v", prefs)
	}

	a.Editor().ZoomIn()
	tt.Frame()
	if len(views) != nViews+1 || views[len(views)-1].Cams[a.Editor().Page] != a.Editor().Cam() {
		t.Errorf("zooming in told %d views", len(views)-nViews)
	}
	tt.Frame()
	if len(views) != nViews+1 {
		t.Error("a frame that changed nothing told of a view")
	}
}
