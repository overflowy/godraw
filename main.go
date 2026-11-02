// godraw is a local whiteboard in the manner of tldraw, drawn with MyGo's
// native UI. It reads and writes tldraw's .tldr files.
package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"godraw/internal/app"
	"godraw/internal/doc"
)

// window is what a document's window holds.
type window struct {
	doc *mygo.Document[*doc.Drawing]
	win *mygo.Window
	app *app.App
}

func (w *window) Changed(d *doc.Drawing) { w.doc.Set(d) }

func (w *window) Update(fn func()) {
	if w.win != nil {
		w.win.Update(fn)
	}
}

func (w *window) Window() *mygo.Window { return w.win }

// openDrawing opens a drawing in a window of its own, off the main thread.
var openDrawing func(path string)

// windows finds the state of a window, for the menus.
var windows = map[*mygo.Window]*window{}

// focused returns the state of the window a menu acts on.
func focused(win *mygo.Window) *window {
	if w := windows[win]; w != nil {
		return w
	}
	for _, w := range windows {
		if w.win.IsFocused() {
			return w
		}
	}
	return nil
}

// act makes a menu item's click run on the window it applies to, and draw.
func act(fn func(a *app.App)) func(*mygo.MenuItem, *mygo.Window) {
	return func(_ *mygo.MenuItem, win *mygo.Window) {
		if w := focused(win); w != nil {
			w.win.Update(func() { fn(w.app) })
		}
	}
}

func menu() *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Label: "File", Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleNew},
			{Role: mygo.RoleOpen},
			{Role: mygo.RoleOpenRecent},
			mygo.Separator(),
			{Role: mygo.RoleClose},
			{Role: mygo.RoleSave},
			{Role: mygo.RoleSaveAs},
			{Role: mygo.RoleDuplicate},
			{Role: mygo.RoleRename},
			{Role: mygo.RoleRevert},
			mygo.Separator(),
			{Label: "Insert Image…", Accelerator: "CmdOrCtrl+U", Click: act(func(a *app.App) { a.InsertImageFromDialog() })},
			{Label: "Export as PNG…", Accelerator: "CmdOrCtrl+Shift+E", Click: act(func(a *app.App) { a.ExportPNG() })},
			{Label: "Export as SVG…", Click: act(func(a *app.App) { a.ExportSVG() })},
			{Label: "Copy as PNG", Accelerator: "CmdOrCtrl+Shift+C", Click: act(func(a *app.App) { a.CopyPNG() })},
		}},
		{Role: mygo.RoleEditMenu},
		{Label: "View", Submenu: []*mygo.MenuItem{
			{Label: "Zoom In", Click: act(func(a *app.App) { a.Editor().ZoomIn() })},
			{Label: "Zoom Out", Click: act(func(a *app.App) { a.Editor().ZoomOut() })},
			{Label: "Zoom to 100%", Click: act(func(a *app.App) { a.Editor().ResetZoom() })},
			{Label: "Zoom to Fit", Click: act(func(a *app.App) { a.Editor().ZoomToFit() })},
			{Label: "Zoom to Selection", Click: act(func(a *app.App) { a.Editor().ZoomToSelection() })},
			mygo.Separator(),
			{Label: "Toggle Grid", Click: act(func(a *app.App) { e := a.Editor(); e.Grid = !e.Grid })},
			{Label: "Toggle Focus Mode", Click: act(func(a *app.App) { e := a.Editor(); e.FocusMode = !e.FocusMode })},
			{Label: "Toggle Dark Mode", Click: act(func(a *app.App) { a.ToggleDark() })},
			mygo.Separator(),
			{Role: mygo.RoleToggleFullScreen},
		}},
		{Role: mygo.RoleWindowMenu},
	})
}

// fileArgs returns the drawings named on the command line. MyGo opens them
// itself in apps the mygo CLI built; this covers go run and go build too, and
// a file asked for twice just focuses its window.
func fileArgs() []string {
	var files []string
	for _, a := range os.Args[1:] {
		if !isDrawing(a) {
			continue
		}
		if info, err := os.Stat(a); err != nil || !info.Mode().IsRegular() {
			log.Printf("godraw: can't open %s", a)
			continue
		}
		files = append(files, a)
	}
	return files
}

// isDrawing reports whether a path names a drawing.
func isDrawing(path string) bool { return strings.EqualFold(filepath.Ext(path), ".tldr") }

func main() {
	files := fileArgs()
	var docs *mygo.Documents[*doc.Drawing]
	openDrawing = func(path string) {
		if _, err := docs.Open(path); err != nil {
			log.Printf("godraw: %s: %v", path, err)
		}
	}
	docs = mygo.NewDocuments(mygo.DocumentsOptions[*doc.Drawing]{
		Types: []mygo.DocumentType{{Name: "tldraw Drawing", Extensions: []string{"tldr"}}},
		New:   doc.New,
		// With files to open, no untitled document comes up beside them.
		NoUntitled: len(files) > 0,
		Read:       func(r io.Reader) (*doc.Drawing, error) { return doc.Read(r) },
		Write:      func(w io.Writer, d *doc.Drawing) error { return doc.Write(w, d) },
		// Records don't change once made, so a drawing compares by them:
		// undoing back to what the file has leaves it unedited.
		Equal: doc.Equal,
		Window: func(d *mygo.Document[*doc.Drawing]) *mygo.Window {
			w := &window{doc: d}
			w.app = app.New(d.Content().Snapshot(), w)
			w.win = mygo.NewWindow(mygo.WindowOptions{
				Width: 1280, Height: 820, MinWidth: 640, MinHeight: 420,
				StateKey: "drawing",
				Content:  ui.View(w.app.View),
			})
			windows[w.win] = w
			w.win.OnClosed(func() { delete(windows, w.win) })
			// Dropped drawings open; dropped images land where they fall.
			w.win.OnFileDrop(func(e *mygo.FileDropEvent) {
				var images []string
				for _, p := range e.Paths {
					if isDrawing(p) {
						go openDrawing(p)
					} else {
						images = append(images, p)
					}
				}
				w.win.Update(func() { w.app.DropFiles(images, float64(e.X), float64(e.Y)) })
			})
			// Reverted, or changed by another app: show what the file has.
			d.OnReload(func() {
				w.win.Update(func() { w.app.Editor().Replace(d.Content().Snapshot()) })
			})
			return w.win
		},
	})
	mygo.App.WhenReady(func() {
		mygo.App.SetMenu(menu())
		if len(files) == 0 {
			return
		}
		go func() {
			opened := 0
			for _, f := range files {
				if _, err := docs.Open(f); err != nil {
					log.Printf("godraw: %s: %v", f, err)
					continue
				}
				opened++
			}
			if opened == 0 {
				docs.New() // nothing else would show a window
			}
		}()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
