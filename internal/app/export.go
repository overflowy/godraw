package app

import (
	"bytes"
	"context"
	"image"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"godraw/internal/doc"
	"godraw/internal/editor"
	"godraw/internal/geo"
)

// exportGCPercent is how soon the GC runs while a PNG renders. Without it, a
// large picture lets the heap double before the GC looks, and the tiles' buffers,
// outside the heap, wait for it meanwhile.
const exportGCPercent = 10

// exportGC is what the GC goes back to once no export is rendering.
var exportGC struct {
	sync.Mutex
	rendering int
	percent   int
}

// collectHard has the GC run often until the function it returns is called, then
// gives the memory back. The setting is the whole process's, so the first export
// in keeps what it was, and the last one out restores it.
func collectHard() (done func()) {
	exportGC.Lock()
	defer exportGC.Unlock()
	if exportGC.rendering == 0 {
		exportGC.percent = debug.SetGCPercent(exportGCPercent)
	}
	exportGC.rendering++
	return func() {
		exportGC.Lock()
		exportGC.rendering--
		last := exportGC.rendering == 0
		if last {
			debug.SetGCPercent(exportGC.percent)
		}
		exportGC.Unlock()
		if last {
			debug.FreeOSMemory()
		}
	}
}

// RenderPNG draws what export draws (the selection, else the page) at a
// scale, and encodes it. An agent's view goes to RenderImage instead, which leaves
// the GC alone: many in a row must not hold up the canvas.
func (a *App) RenderPNG(scale float64) ([]byte, bool) {
	box, ids, ok := a.ed.ExportBounds()
	if !ok {
		return nil, false
	}
	defer collectHard()()
	return encodePNG(a.RenderImage(box.Expand(editor.ExportPad), ids, scale))
}

func encodePNG(img image.Image) ([]byte, bool) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}

// drawTurn is about how long drawing an export on the side holds the main
// thread at a time, before input and frames have their turn.
const drawTurn = 12 * time.Millisecond

// sideTile is the side of the tiles an export drawn on the side draws in, in
// device pixels: a tile is the least it draws in a turn. On the showcase's
// pages, a turn of 256 takes 12 to 15 ms and at most 40, where one shape fills
// a tile; 512 took up to 70. Smaller ones take longer all told.
const sideTile = 256

// aside returns a window of its own over d, a snapshot of this one's
// drawing, with its page, selection, theme and text layouts. Exports are made
// from it on the side: edits meanwhile change nothing it reads, and nothing it
// does reaches this window. One that works off the main thread needs d
// detached.
func (a *App) aside(d *doc.Drawing) *App {
	b := New(d, offscreen{name: a.host.Name()})
	b.meas, b.ed.Measure = a.meas, a.meas.measure
	b.ed.Page, b.ed.DarkMode = a.ed.Page, a.ed.DarkMode
	b.ed.Select(a.ed.Selection()...)
	return b
}

// offscreen holds a window that only makes exports: what it would tell, or
// ask to run, has no window to go to.
type offscreen struct{ name string }

func (offscreen) Changed(*doc.Drawing) {}
func (offscreen) Update(fn func())     { fn() }
func (offscreen) Do(fn func())         { fn() }
func (offscreen) Window() *mygo.Window { return nil }
func (o offscreen) Name() string       { return o.name }

// makePNG draws what export draws (the selection, else the page) at a scale,
// without making the window wait, and gives done the PNG, on a goroutine, or
// nothing and false if ctx ends first. It returns false, and done isn't called,
// when there is nothing to export.
//
// Painting is the main thread's only, as all windows share their glyphs and
// masks. So it paints there a few tiles at a time, waiting for each turn to end
// before it asks for the next, which lets input and frames go between. There, it
// can share what the window derived from shapes. It decodes images of its own,
// as a bitmap changes as it draws, and it decodes them and encodes the PNG on
// the side.
func (a *App) makePNG(ctx context.Context, scale float64, done func([]byte, bool)) bool {
	box, ids, ok := a.ed.ExportBounds()
	if !ok {
		return false
	}
	b := a.aside(a.ed.Doc.Snapshot())
	untrack := collectHard()
	go func() {
		defer untrack()
		b.decodeForExport()
		var pic *picture
		for finished := false; !finished; {
			ran := false
			if ctx.Err() == nil {
				a.host.Do(func() {
					ran = true
					if pic == nil {
						pic = b.newPicture(box.Expand(editor.ExportPad), ids, scale, sideTile)
					}
					finished = pic.draw(drawTurn)
				})
			}
			if !ran { // ended, or the app quit
				done(nil, false)
				return
			}
		}
		done(encodePNG(pic.out))
	}()
	return true
}

// makeSVG writes what export draws as an SVG, without making the window wait,
// and gives done the SVG, on a goroutine, or nothing and false if ctx ended
// before it began. It returns false, and done isn't called, when there is
// nothing to export. Unlike a PNG it paints nothing, so all of it goes on the
// side, over a detached snapshot: text it lays out shapes under the text
// system's lock.
func (a *App) makeSVG(ctx context.Context, done func([]byte, bool)) bool {
	if _, _, ok := a.ed.ExportBounds(); !ok {
		return false
	}
	b := a.aside(a.ed.Doc.Detached())
	go func() {
		if ctx.Err() != nil {
			done(nil, false)
			return
		}
		done(b.ed.SVG(b.ed.DarkMode))
	}()
	return true
}

// RenderImage draws shapes in a box of the page, at a scale, and images that
// are not decoded yet come out as they will show.
func (a *App) RenderImage(box geo.Box, ids []doc.ID, scale float64) *image.RGBA {
	a.decodeForExport()
	p := a.newPicture(box, ids, scale, 1024)
	p.draw(0)
	return p.out
}

// decodeForExport decodes the images that shapes show and aren't decoded yet.
func (a *App) decodeForExport() {
	inUse := a.assetsInUse()
	for id, asset := range a.ed.Doc.Assets {
		if inUse[id] && a.decoded[id] != asset.Src && strings.HasPrefix(asset.Src, "data:") {
			if b := decodeDataURL(asset.Src); b != nil {
				a.bitmaps[id] = b
			}
			a.decoded[id] = asset.Src
		}
	}
}

// picture is an export being drawn, in tiles, each a frame with a mask atlas
// of its own: a whole page of strokes wouldn't fit one, and a tile that
// wouldn't fit it splits. Tiles are whole device pixels apart, so that nothing
// shows where they meet.
type picture struct {
	a     *App
	box   geo.Box
	ids   []doc.ID
	scale float64
	pal   *editor.Palette
	out   *image.RGBA
	todo  []tileAt // what is left to draw, next first
}

// tileAt is a tile of a picture, its corner and side in device pixels.
type tileAt struct{ x, y, size int }

func (a *App) newPicture(box geo.Box, ids []doc.ID, scale float64, tile int) *picture {
	// Very large pages come out at a size a file can hold.
	const maxSide = 8192.0
	if k := maxSide / (math.Max(box.W, box.H) * scale); k < 1 {
		scale *= k
	}
	w, h := int(math.Ceil(box.W*scale)), int(math.Ceil(box.H*scale))
	p := &picture{
		a: a, box: box, ids: ids, scale: scale, pal: a.ed.Palette(),
		out: image.NewRGBA(image.Rect(0, 0, w, h)),
	}
	for y := 0; y < h; y += tile {
		for x := 0; x < w; x += tile {
			p.todo = append(p.todo, tileAt{x, y, tile})
		}
	}
	return p
}

// draw draws tiles until they are all drawn, which it reports, or until it has
// taken budget; 0 draws them all. A tile too full for its atlas splits in
// four, each drawn on its own, so that a turn can end between them.
func (p *picture) draw(budget time.Duration) bool {
	const smallest = 128
	start := time.Now()
	for len(p.todo) > 0 {
		t := p.todo[0]
		p.todo = p.todo[1:]
		if t.size > smallest && p.sceneAt(nil, t.x, t.y, t.size).maskArea(p.ids, p.scale, false) > atlasBudget {
			h := t.size / 2
			var quarters []tileAt
			for _, q := range []tileAt{{t.x, t.y, h}, {t.x + h, t.y, h}, {t.x, t.y + h, h}, {t.x + h, t.y + h, h}} {
				if q.x < p.out.Rect.Dx() && q.y < p.out.Rect.Dy() {
					quarters = append(quarters, q)
				}
			}
			p.todo = append(quarters, p.todo...)
			continue
		}
		p.render(t)
		if budget > 0 && time.Since(start) >= budget {
			break
		}
	}
	return len(p.todo) == 0
}

func (p *picture) sceneAt(pt *ui.Painter, tx, ty, size int) *scene {
	at := geo.V(p.box.X+float64(tx)/p.scale, p.box.Y+float64(ty)/p.scale)
	side := math.Ceil(float64(size) / p.scale)
	s := &scene{a: p.a, e: p.a.ed, p: pt, pal: p.pal, export: true, shrink: math.Min(p.scale, 1)}
	s.cam = editor.Camera{X: -at.X, Y: -at.Y, Z: 1}
	s.view = geo.Box{X: at.X, Y: at.Y, W: side, H: side}
	return s
}

func (p *picture) render(t tileAt) {
	img := renderFrame(int(math.Ceil(float64(t.size)/p.scale)), p.scale, func(pt *ui.Painter, r ui.Rect) {
		pt.Fill(r, toUI(p.pal.Background), 0)
		p.sceneAt(pt, t.x, t.y, t.size).paint(p.ids)
	})
	draw.Draw(p.out, image.Rect(t.x, t.y, t.x+t.size, t.y+t.size), img, image.Point{}, draw.Src)
}

// renderFrame paints a frame of side×side DIPs at a scale. It doesn't use
// ui.Render, whose first frame is at scale 1: below that, the frame's buffer is
// far larger than the picture, and stays so until the GC finds it, as it's outside
// the Go heap. A frame that starts at nothing and grows asks only for what it ends at.
func renderFrame(side int, scale float64, paint func(p *ui.Painter, r ui.Rect)) *image.RGBA {
	grown := false
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Themed(c, paintTheme(c.Theme())).Background(ui.Transparent).Fill().Children(func() {
			ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
				if grown {
					paint(p, r)
				}
			})
		})
	}, 1, 1)
	tt.SetScale(float32(scale))
	grown = true
	tt.SetSize(side, side)
	return tt.Image()
}

func (a *App) exportName(ext string) string {
	name := a.host.Name()
	name = strings.TrimSuffix(name, filepath.Ext(name))
	if name == "" {
		name = a.ed.Doc.Name
	}
	if name == "" {
		name = "drawing"
	}
	return name + ext
}

// ExportPNG asks where, and writes a PNG there.
func (a *App) ExportPNG() {
	a.exportAs(".png", "PNG image", func(ctx context.Context, done func([]byte, bool)) bool {
		return a.makePNG(ctx, 2, done)
	})
}

// ExportSVG asks where, and writes an SVG there.
func (a *App) ExportSVG() { a.exportAs(".svg", "SVG image", a.makeSVG) }

// CopyPNG copies what export draws as an image. It draws on the side, and a
// copy asked for meanwhile ends it, to take the clipboard instead.
func (a *App) CopyPNG() {
	if !a.hasSvc {
		return
	}
	if a.copying != nil {
		a.copying()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.copying = cancel
	svc := a.services
	a.makePNG(ctx, 2, func(data []byte, ok bool) {
		if !ok {
			return
		}
		a.host.Update(func() {
			if ctx.Err() == nil {
				svc.WriteClipboardData(transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, data))))
				cancel()
			}
		})
	})
}

// exportAs asks where to save a file of a kind, and writes there what start
// makes, as makePNG and makeSVG do: on the side while the dialog is open, and
// ending it if the dialog is cancelled.
func (a *App) exportAs(ext, kind string, start func(ctx context.Context, done func([]byte, bool)) bool) {
	type result struct {
		data []byte
		ok   bool
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan result, 1)
	if !start(ctx, func(data []byte, ok bool) { ready <- result{data, ok} }) {
		cancel()
		return
	}
	win, name := a.host.Window(), a.exportName(ext)
	go func() {
		defer cancel()
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Parent: win, Title: "Export", DefaultPath: name,
			Filters: []mygo.FileFilter{{Name: kind, Extensions: []string{strings.TrimPrefix(ext, ".")}}},
		})
		if err != nil || path == "" {
			return
		}
		r := <-ready
		if !r.ok {
			mygo.Dialog.Error("Couldn't export", "The file couldn't be made.")
			return
		}
		if err := os.WriteFile(path, r.data, 0o644); err != nil {
			mygo.Dialog.Error("Couldn't export", err.Error())
		}
	}()
}
