// Package settings keeps what the user set between launches: the preferences
// every window takes, and how each drawing was last viewed. They live in a
// JSON file of their own, never in the drawings.
package settings

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"maps"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"time"

	"godraw/internal/doc"
	"godraw/internal/editor"
)

// Prefs are the preferences a new window starts with.
type Prefs struct {
	Theme     string       `json:"theme"` // "", "light" or "dark"
	Grid      bool         `json:"grid"`
	Snap      bool         `json:"snap"`
	Minimap   bool         `json:"minimap"`
	FocusMode bool         `json:"focusMode"`
	Style     editor.Style `json:"style"` // of the shapes drawn next
}

// DefaultPrefs are the preferences of a first launch.
func DefaultPrefs() Prefs { return Prefs{Style: editor.DefaultStyle()} }

// View is how a drawing was last viewed: the page it showed, and the camera of
// each page that was seen.
type View struct {
	Page doc.ID                   `json:"page"`
	Cams map[doc.ID]editor.Camera `json:"cams"`
}

// Equal reports whether two views are the same.
func (v View) Equal(o View) bool { return v.Page == o.Page && maps.Equal(v.Cams, o.Cams) }

// maxViews is how many drawings' views the file keeps: those of the drawings
// viewed last.
const maxViews = 200

// saveDelay is how long the file waits after the last change before it is
// written: each change starts the wait again, so that a pan writes the file
// once, after it ends, not on every frame.
const saveDelay = 500 * time.Millisecond

type file struct {
	Prefs Prefs                `json:"prefs"`
	Views map[string]*viewItem `json:"views"`
}

type viewItem struct {
	View
	Used time.Time `json:"used"`
}

// Store is the settings file. Its methods may be called from any goroutine.
type Store struct {
	path  string
	delay time.Duration // saveDelay, but in tests

	mu    sync.Mutex
	data  file
	dirty bool
	timer *time.Timer

	write sync.Mutex // serializes writes of the file
}

// Open reads the settings at path. A file missing or unreadable gives the
// defaults, and an error to log for the latter.
func Open(path string) (*Store, error) {
	s := &Store{path: path, delay: saveDelay, data: file{Prefs: DefaultPrefs(), Views: map[string]*viewItem{}}}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	// Decoding onto the defaults keeps them for what an older file lacks.
	f := file{Prefs: DefaultPrefs()}
	if err := json.Unmarshal(b, &f); err != nil {
		return s, err
	}
	s.data.Prefs = sane(f.Prefs)
	for k, v := range f.Views {
		if v != nil {
			v.Cams = usable(v.Cams)
			s.data.Views[k] = v
		}
	}
	return s, nil
}

// sane fixes what would make a broken canvas out of a file edited by hand.
func sane(p Prefs) Prefs {
	switch p.Theme {
	case "", "light", "dark":
	default:
		p.Theme = ""
	}
	if math.IsNaN(p.Style.Opacity) || p.Style.Opacity <= 0 || p.Style.Opacity > 1 {
		p.Style.Opacity = 1
	}
	return p
}

// usable returns the cameras that can show a page; JSON has no NaN either.
func usable(cams map[doc.ID]editor.Camera) map[doc.ID]editor.Camera {
	out := make(map[doc.ID]editor.Camera, len(cams))
	for id, c := range cams {
		if finite(c.X) && finite(c.Y) && finite(c.Z) && c.Z > 0 {
			out[id] = c
		}
	}
	return out
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// Prefs returns the preferences.
func (s *Store) Prefs() Prefs {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Prefs
}

// View returns how the drawing at path was last viewed.
func (s *Store) View(path string) (View, bool) {
	if path == "" {
		return View{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.data.Views[key(path)]
	if v == nil {
		return View{}, false
	}
	return View{Page: v.Page, Cams: maps.Clone(v.Cams)}, true
}

// ChangePrefs keeps what a window changed of the preferences, from was to
// now. Only the fields that changed are kept, so that another window's change
// stands until this one makes a change of its own to the same field.
func (s *Store) ChangePrefs(was, now Prefs) {
	s.mu.Lock()
	defer s.mu.Unlock()
	merged := s.data.Prefs
	merge(reflect.ValueOf(&merged).Elem(), reflect.ValueOf(was), reflect.ValueOf(now))
	if merged == s.data.Prefs {
		return
	}
	s.data.Prefs = merged
	s.changed()
}

// merge sets the fields of dst that changed from was to now, field by field
// through nested structs.
func merge(dst, was, now reflect.Value) {
	if dst.Kind() != reflect.Struct {
		if !was.Equal(now) {
			dst.Set(now)
		}
		return
	}
	for i := range dst.NumField() {
		merge(dst.Field(i), was.Field(i), now.Field(i))
	}
}

// SetView keeps how the drawing at path is viewed; "", untitled, is not kept.
func (s *Store) SetView(path string, view View) {
	if path == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(path)
	cams := usable(view.Cams)
	if old := s.data.Views[k]; old != nil && old.Equal(View{Page: view.Page, Cams: cams}) {
		return
	}
	s.data.Views[k] = &viewItem{Page: view.Page, Cams: cams, Used: time.Now()}
	s.prune()
	s.changed()
}

// changed writes the file once changes stop for s.delay. s.mu is held.
func (s *Store) changed() {
	s.dirty = true
	if s.timer != nil {
		// A timer that fired already only flushes again, which writes what
		// changed since or nothing.
		s.timer.Reset(s.delay)
		return
	}
	s.timer = time.AfterFunc(s.delay, func() {
		if err := s.Flush(); err != nil {
			log.Printf("godraw: saving settings: %v", err)
		}
	})
}

// prune forgets the views of all but the drawings viewed last. s.mu is held.
func (s *Store) prune() {
	if len(s.data.Views) <= maxViews {
		return
	}
	keys := slices.Collect(maps.Keys(s.data.Views))
	slices.SortFunc(keys, func(a, b string) int { return s.data.Views[b].Used.Compare(s.data.Views[a].Used) })
	for _, k := range keys[maxViews:] {
		delete(s.data.Views, k)
	}
}

// Flush writes what changed now.
func (s *Store) Flush() error {
	s.write.Lock()
	defer s.write.Unlock()
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	if !s.dirty {
		s.mu.Unlock()
		return nil
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	s.dirty = false
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if err := writeFile(s.path, append(b, '\n')); err != nil {
		s.mu.Lock()
		s.dirty = true
		s.mu.Unlock()
		return err
	}
	return nil
}

// writeFile replaces the file at path whole, so that a crash leaves the old one.
func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // gone already if the rename worked
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close() // the write's error says more
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// key is what a drawing's view is kept under: its file's absolute path.
func key(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}
