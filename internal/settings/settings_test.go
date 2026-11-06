package settings

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"godraw/internal/doc"
	"godraw/internal/editor"
)

func open(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSettingsLastToTheNextLaunch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "GoDraw", "settings.json")
	s := open(t, path)
	if s.Prefs() != DefaultPrefs() {
		t.Fatalf("a first launch has %+v", s.Prefs())
	}
	was := s.Prefs()
	now := was
	now.Theme, now.Grid, now.Minimap = "dark", true, true
	now.Style.Color = "blue"
	s.ChangePrefs(was, now)
	view := View{Page: "p2", Cams: map[doc.ID]editor.Camera{"p1": {X: 1, Y: 2, Z: 0.5}, "p2": {X: -3, Y: 4, Z: 2}}}
	s.SetView("/drawings/plan.godraw", view)
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}

	again := open(t, path)
	if got := again.Prefs(); got != now {
		t.Errorf("prefs came back as %+v, want %+v", got, now)
	}
	got, ok := again.View("/drawings/plan.godraw")
	if !ok || !got.Equal(view) {
		t.Errorf("view came back as %+v, %v", got, ok)
	}
	if _, ok := again.View("/drawings/other.godraw"); ok {
		t.Error("a drawing never viewed has a view")
	}
}

// Two windows: what one changes stands until the other changes the same thing.
func TestAWindowKeepsOnlyWhatItChanged(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "settings.json"))
	base := s.Prefs()

	a := base
	a.Theme = "dark"
	s.ChangePrefs(base, a)

	b := base
	b.Grid = true
	b.Style.Size = "xl"
	s.ChangePrefs(base, b) // b still thinks the theme is the system's

	got := s.Prefs()
	if got.Theme != "dark" || !got.Grid || got.Style.Size != "xl" || got.Style.Color != base.Style.Color {
		t.Errorf("merged prefs are %+v", got)
	}
}

func TestAnOlderFileKeepsTheDefaultsItLacks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	data := `{"prefs":{"theme":"dark","style":{"Color":"red"}},"views":{"/a.godraw":{"page":"p1","cams":{"p1":{"X":0,"Y":0,"Z":0}}}}}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	s := open(t, path)
	p := s.Prefs()
	want := editor.DefaultStyle()
	want.Color = "red"
	if p.Theme != "dark" || p.Style != want {
		t.Errorf("prefs are %+v", p)
	}
	if v, _ := s.View("/a.godraw"); len(v.Cams) != 0 {
		t.Errorf("a camera of zoom 0 came back: %+v", v.Cams)
	}
}

func TestABrokenFileGivesTheDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err == nil {
		t.Error("no error to log")
	}
	if s.Prefs() != DefaultPrefs() {
		t.Errorf("prefs are %+v", s.Prefs())
	}
}

func TestACameraJSONCantHoldIsLeftOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s := open(t, path)
	s.SetView("/a.godraw", View{Page: "p1", Cams: map[doc.ID]editor.Camera{"p1": {Z: 1}, "p2": {X: math.NaN(), Z: 1}}})
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	v, _ := open(t, path).View("/a.godraw")
	if len(v.Cams) != 1 {
		t.Errorf("cams are %+v", v.Cams)
	}
}

func TestOnlyTheLastViewedDrawingsAreKept(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "settings.json"))
	for i := range maxViews + 5 {
		s.SetView(filepath.Join("/d", string(rune('a'+i%26))+string(rune('a'+i/26))+".godraw"), View{Page: "p"})
	}
	if n := len(s.data.Views); n != maxViews {
		t.Errorf("%d views kept", n)
	}
	if _, ok := s.View("/d/aa.godraw"); ok {
		t.Error("the first drawing viewed is still kept")
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
}

// Changes that keep coming, as a pan's, write the file once, after they stop.
func TestTheFileWaitsForChangesToStop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s := open(t, path)
	s.delay = 300 * time.Millisecond
	start := time.Now()
	for i := 0; time.Since(start) < 3*s.delay; i++ {
		s.SetView("/a.godraw", View{Page: "p1", Cams: map[doc.ID]editor.Camera{"p1": {X: float64(i), Z: 1}}})
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("written while the changes went on")
	}
	last, _ := s.View("/a.godraw")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never written after the changes stopped")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if v, _ := open(t, path).View("/a.godraw"); !v.Equal(last) {
		t.Errorf("wrote %+v, not the last view %+v", v, last)
	}
}
