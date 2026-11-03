// Package app is the window of a drawing: the canvas, its panels and its
// keyboard, in MyGo's native UI, around a package editor Editor.
package app

import (
	"bytes"
	"encoding/base64"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"godraw/internal/doc"
	"godraw/internal/editor"
	"godraw/internal/geo"
)

// Host is what a window asks of what holds it.
type Host interface {
	// Changed tells that the document has a new snapshot.
	Changed(d *doc.Drawing)
	// Update runs fn on the main thread, and draws a frame after it.
	Update(fn func())
	// Window returns the window, nil in tests.
	Window() *mygo.Window
}

// App is one window's state.
type App struct {
	// MCP tells how the app's MCP server is doing; nil shows nothing of it.
	MCP func() MCPStatus

	ed   *editor.Editor
	host Host
	meas *measurer
	t0   time.Time

	theme string // "", "light" or "dark"

	canvasHandle ui.Handle
	opacity      float64 // bound to the opacity slider
	opacityLive  bool

	geoOpen, pagesOpen, helpOpen bool
	minimap                      bool
	renamingPage                 doc.ID
	renameText                   string
	editKey                      int
	lastEditing                  doc.ID

	services ui.Services
	hasSvc   bool

	bitmaps map[doc.ID]*ui.Bitmap
	decoded map[doc.ID]string
	pending map[doc.ID]bool
	inUse   map[doc.ID]bool // the assets shapes show; nil once the document changed
	inUseOf *doc.Drawing    // the drawing inUse is of
	inUseN  int             // how many shapes it had, as a change not yet committed shows

	sizeBadge bool

	zoomedAt time.Time // when the zoom going on last moved; zero for none
	zoom     zoomState
	rest     restView

	cells      map[cellKey]*cellPlan
	details    map[detailKey]*detail
	cellsFrame int

	mcpOpen   bool
	mcpCopied string // the field that says it copied
	mcpCopies int    // how many times it has, for a timer to know its own
}

// New returns the state of a window showing a drawing.
func New(d *doc.Drawing, host Host) *App {
	m := newMeasurer()
	a := &App{
		meas: m, host: host, t0: time.Now(),
		bitmaps: map[doc.ID]*ui.Bitmap{}, decoded: map[doc.ID]string{}, pending: map[doc.ID]bool{},
		minimap: false, sizeBadge: true,
	}
	a.ed = editor.New(d, m.measure)
	a.ed.OnChange = func(d *doc.Drawing) {
		a.inUse = nil
		host.Changed(d)
	}
	return a
}

// Editor returns the window's editor.
func (a *App) Editor() *editor.Editor { return a.ed }

func (a *App) showSizeBadge() bool { return a.sizeBadge }

func (a *App) seconds(t time.Time) float64 { return t.Sub(a.t0).Seconds() }

// bitmapOf returns the decoded bitmap of an asset, nil until it is ready.
func (a *App) bitmapOf(id doc.ID) *ui.Bitmap {
	return a.bitmaps[id]
}

// assetsInUse returns the assets that shapes show, and forgets the images of the
// others, which take far more memory than anything else a drawing has. Deleting a
// shape leaves its asset in the document, for undo. Bringing a shape back decodes its
// image again. It looks through the shapes only after a change to the document.
func (a *App) assetsInUse() map[doc.ID]bool {
	d := a.ed.Doc
	if a.inUse != nil && a.inUseOf == d && a.inUseN == len(d.Shapes) {
		return a.inUse
	}
	a.inUse, a.inUseOf, a.inUseN = map[doc.ID]bool{}, d, len(d.Shapes)
	if len(d.Assets) > 0 {
		for _, s := range d.Shapes {
			if s.AssetID != "" && d.Assets[s.AssetID] != nil {
				a.inUse[s.AssetID] = true
			}
		}
	}
	for id := range a.decoded {
		if !a.inUse[id] {
			delete(a.decoded, id)
			delete(a.bitmaps, id)
		}
	}
	return a.inUse
}

// decodeAssets decodes on the side what shapes are about to show.
func (a *App) decodeAssets() {
	inUse := a.assetsInUse()
	for id, asset := range a.ed.Doc.Assets {
		if !inUse[id] || a.decoded[id] == asset.Src || a.pending[id] {
			continue
		}
		if !strings.HasPrefix(asset.Src, "data:") {
			a.decoded[id] = asset.Src
			continue
		}
		a.pending[id] = true
		src := asset.Src
		go func() {
			b := decodeDataURL(src)
			a.host.Update(func() {
				delete(a.pending, id)
				// It may have left the drawing or changed meanwhile.
				if asset := a.ed.Doc.Assets[id]; asset == nil || asset.Src != src || !a.assetsInUse()[id] {
					return
				}
				a.decoded[id] = src
				if b != nil {
					a.bitmaps[id] = b
				}
			})
		}()
	}
}

func decodeDataURL(src string) *ui.Bitmap {
	_, after, ok := strings.Cut(src, ",")
	if !ok {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(after)
	if err != nil {
		return nil
	}
	b, err := ui.DecodeBitmap(data)
	if err != nil {
		return nil
	}
	return b
}

// View builds the window.
func (a *App) View(c *ui.Context) {
	e := a.ed
	a.services = c.Services()
	a.hasSvc = true
	a.applyTheme(c)
	// The camera works in the size the canvas had last.
	if b := a.canvasHandle.Bounds(c); b.W > 0 {
		e.VW, e.VH = float64(b.W), float64(b.H)
		e.FitNewPage()
	}
	a.decodeAssets()
	e.ModsChanged(modsOf(c.Modifiers()))
	if len(e.Trails) > 0 {
		if e.AgeTrails(a.seconds(c.Now())); len(e.Trails) > 0 {
			c.AnimationFrame()
		}
	}
	a.shortcuts(c)
	ui.Box(c).Fill().Children(func() {
		a.canvas(c)
		a.textEditor(c)
		if !e.FocusMode {
			a.topLeft(c)
			a.stylePanel(c)
			a.toolbar(c)
			a.zoomPanel(c)
		}
	})
	a.helpDialog(c)
	a.mcpDialog(c)
}

func (a *App) applyTheme(c *ui.Context) {
	var t *ui.Theme
	switch a.theme {
	case "light":
		t = ui.LightTheme()
	case "dark":
		t = ui.DarkTheme()
	default:
		// As the system has it.
		a.ed.DarkMode = c.Theme().Dark
		return
	}
	c.SetTheme(t)
	a.ed.DarkMode = t.Dark
}

func modsOf(m ui.Modifiers) editor.Mods {
	return editor.Mods{Shift: m&ui.Shift != 0, Alt: m&ui.Alt != 0, Cmd: m&ui.Cmd != 0, Ctrl: m&ui.Ctrl != 0}
}

// canvas builds the element the drawing is painted on and takes input on.
func (a *App) canvas(c *ui.Context) {
	e := a.ed
	ui.Themed(c.Key("canvas-paint"), paintTheme(c.Theme())).Background(ui.Transparent).
		Absolute().Top(0).Left(0).Right(0).Bottom(0).Children(func() {
		ui.Box(c.Key("canvas-ink")).Fill().Draw(a.paintCanvas)
	})
	cv := ui.Box(c.Key("canvas")).Absolute().Top(0).Left(0).Right(0).Bottom(0).
		Focusable().FocusRing(false).Bind(&a.canvasHandle).
		Cursor(cursorOf(e.CursorNow()))
	cv.Label("Canvas")
	cv.HandleInput(func(ev ui.InputEvent) bool { return a.input(c, ev) })
	cv.ContextMenu(a.contextMenu)
}

func cursorOf(k editor.Cursor) ui.Cursor {
	switch k {
	case editor.CursorCross:
		return ui.CursorCrosshair
	case editor.CursorText:
		return ui.CursorText
	case editor.CursorGrab:
		return ui.CursorGrab
	case editor.CursorGrabbing:
		return ui.CursorGrabbing
	case editor.CursorEW:
		return ui.CursorResizeEW
	case editor.CursorNS:
		return ui.CursorResizeNS
	case editor.CursorNESW:
		return ui.CursorResizeNESW
	case editor.CursorNWSE:
		return ui.CursorResizeNWSE
	case editor.CursorRotate:
		return ui.CursorMove
	case editor.CursorPointer:
		return ui.CursorPointer
	case editor.CursorMove:
		return ui.CursorMove
	}
	return ui.CursorDefault
}

// input takes the canvas's input as it comes.
func (a *App) input(c *ui.Context, ev ui.InputEvent) bool {
	e := a.ed
	t := a.seconds(ev.Time)
	x, y := float64(ev.X), float64(ev.Y)
	switch ev.Kind {
	case ui.InputPointerDown:
		a.canvasHandle.Focus(c)
		e.PointerDown(x, y, ev.Button, ev.Clicks, modsOf(ev.Mods), t)
		// The right button's menu opens as the framework's: only its press
		// is the editor's, for what it selects.
		return ev.Button != 1
	case ui.InputPointerMove:
		e.PointerMove(x, y, modsOf(ev.Mods), t)
		return true
	case ui.InputPointerUp:
		e.PointerUp(x, y, modsOf(ev.Mods), t)
		return true
	case ui.InputScroll:
		if ev.Mods&(ui.Cmd|ui.Ctrl) != 0 {
			a.ZoomGesture(geo.V(x, y), math.Exp(-float64(ev.DY)*0.01), ev.Time)
		} else {
			e.Pan(float64(ev.DX), float64(ev.DY))
		}
		e.PointerMove(x, y, modsOf(ev.Mods), t)
		return true
	case ui.InputZoom:
		a.ZoomGesture(geo.V(x, y), float64(ev.Scale), ev.Time)
		return true
	case ui.InputKeyDown:
		if ev.Key == ui.KeySpace && e.Editing == "" {
			e.SetSpace(true)
			return true
		}
	case ui.InputKeyUp:
		if ev.Key == ui.KeySpace {
			e.SetSpace(false)
			return true
		}
	}
	return false
}

// textEditor builds the input over the shape whose text is being edited.
func (a *App) textEditor(c *ui.Context) {
	e := a.ed
	if e.Editing == "" {
		a.lastEditing = ""
		return
	}
	if a.lastEditing != e.Editing {
		a.lastEditing = e.Editing
		a.editKey++
	}
	box, size, font, align, col := e.EditBox()
	if size == 0 {
		return
	}
	cam := e.Cam()
	m := e.PageMat(e.Editing)
	// Text that sizes itself gets a little room, on the side it grows to, to
	// keep what is typed from wrapping before the shape has grown to hold it.
	// Text that wraps wraps as it will show.
	if s := e.Doc.Shapes[e.Editing]; s != nil && s.Type == doc.TypeText && s.AutoSize {
		switch align {
		case "middle":
			box.X -= size / 2
		case "end":
			box.X -= size
		}
		box.W += size
	}
	// Room below for rounding, without moving the lines.
	box.H += 2 / cam.Z
	center := e.ToScreen(m.Apply(box.Center()))
	sw, sh := box.W*cam.Z, box.H*cam.Z
	input := ui.TextAreaBase(c.Key(a.editKey), &e.EditText).
		Absolute().Left(float32(center.X-sw/2)).Top(float32(center.Y-sh/2)).Size(float32(sw), float32(sh)).
		Font(family(font)).FontSize(float32(size * cam.Z)).LineHeight(editor.LineHeight).
		TextColor(toUI(col)).Label("Text").AutoFocus().FocusRing(false)
	switch align {
	case "middle":
		input.TextAlign(ui.Center)
	case "end":
		input.TextAlign(ui.End)
	default:
		input.TextAlign(ui.Start)
	}
	if deg := m.Rotation() * 180 / math.Pi; math.Abs(deg) > 0.01 {
		input.Rotate(float32(deg))
	}
	input.OnChange(e.EditChanged)
}

// --- clipboard ---

func (a *App) copySelection() bool {
	data, text := a.ed.CopyData()
	if data == nil {
		return false
	}
	item := []transfer.Representation{transfer.Bytes(transfer.Format(editor.ClipboardFormat), data)}
	if text != "" {
		item = append(item, transfer.Bytes(transfer.Text, []byte(text)))
	}
	a.services.WriteClipboardData(transfer.New(transfer.NewItem(item...)))
	return true
}

func (a *App) paste() {
	e := a.ed
	formats := a.services.ClipboardFormats()
	has := func(f transfer.Format) bool {
		return slices.Contains(formats, f)
	}
	// What is pasted comes where it was if that shows, not at the pointer.
	var at *geo.Vec
	switch {
	case has(transfer.Format(editor.ClipboardFormat)):
		d := a.services.ReadClipboardData(transfer.Format(editor.ClipboardFormat))
		if b, err := d.Read(transfer.Format(editor.ClipboardFormat)); err == nil {
			if err := e.Paste(b, at); err != nil {
				log.Printf("godraw: paste: %v", err)
			}
		}
	case has(transfer.PNG):
		d := a.services.ReadClipboardData(transfer.PNG)
		if b, err := d.Read(transfer.PNG); err == nil {
			a.insertImageData("image.png", "image/png", b, at)
		}
	default:
		e.PasteText(a.services.ReadClipboard())
	}
}

// insertImageData adds an image from its bytes.
func (a *App) insertImageData(name, mime string, data []byte, at *geo.Vec) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return
	}
	a.ed.InsertImage(name, mime, data, float64(cfg.Width), float64(cfg.Height), at)
}

// --- files ---

func mimeOf(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	return ""
}

// DropFiles adds the images among files dropped at a point of the window, in
// DIPs.
func (a *App) DropFiles(files []string, x, y float64) {
	a.dropFiles(files, a.ed.ToPage(geo.V(x, y)))
}

// dropFiles adds the images among files, around a point of the page. Reading
// them happens on the side.
func (a *App) dropFiles(files []string, at geo.Vec) {
	go func() {
		offset := 0.0
		for _, path := range files {
			mime := mimeOf(path)
			if mime == "" {
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			p := at.Add(geo.V(offset, offset))
			offset += 24
			name := filepath.Base(path)
			a.host.Update(func() { a.insertImageData(name, mime, data, &p) })
		}
	}()
}

// InsertImageFromDialog asks for images, and adds them.
func (a *App) InsertImageFromDialog() {
	win := a.host.Window()
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: win, Title: "Insert Image", Filters: []mygo.FileFilter{{Name: "Images", Extensions: []string{"png", "jpg", "jpeg", "gif"}}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		a.host.Update(func() { a.dropFiles(paths, a.ed.ViewBox().Center()) })
	}()
}
