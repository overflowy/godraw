package app

import (
	"bytes"
	"image"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"

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
	var buf bytes.Buffer
	if err := png.Encode(&buf, a.RenderImage(box.Expand(32), ids, scale)); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}

// RenderImage draws shapes in a box of the page, at a scale, and images that
// are not decoded yet come out as they will show.
func (a *App) RenderImage(box geo.Box, ids []doc.ID, scale float64) *image.RGBA {
	e := a.ed
	inUse := a.assetsInUse()
	for id, asset := range e.Doc.Assets {
		if inUse[id] && a.decoded[id] != asset.Src && strings.HasPrefix(asset.Src, "data:") {
			if b := decodeDataURL(asset.Src); b != nil {
				a.bitmaps[id] = b
			}
			a.decoded[id] = asset.Src
		}
	}
	// Very large pages come out at a size a file can hold.
	const maxSide = 8192.0
	if k := maxSide / (math.Max(box.W, box.H) * scale); k < 1 {
		scale *= k
	}
	pal := e.Palette()
	// In tiles, each a frame with a mask atlas of its own: a whole page of
	// strokes wouldn't fit one, and a tile that wouldn't fit it splits. Tiles are
	// whole device pixels apart, so that nothing shows where they meet.
	const tile, smallest = 1024, 128
	w, h := int(math.Ceil(box.W*scale)), int(math.Ceil(box.H*scale))
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	sceneAt := func(p *ui.Painter, tx, ty, size int) *scene {
		at := geo.V(box.X+float64(tx)/scale, box.Y+float64(ty)/scale)
		side := math.Ceil(float64(size) / scale)
		s := &scene{a: a, e: e, p: p, pal: pal, export: true, shrink: math.Min(scale, 1)}
		s.cam = editor.Camera{X: -at.X, Y: -at.Y, Z: 1}
		s.view = geo.Box{X: at.X, Y: at.Y, W: side, H: side}
		return s
	}
	var render func(tx, ty, size int)
	render = func(tx, ty, size int) {
		if tx >= w || ty >= h {
			return
		}
		if size > smallest && sceneAt(nil, tx, ty, size).maskArea(ids, scale, false) > atlasBudget {
			half := size / 2
			render(tx, ty, half)
			render(tx+half, ty, half)
			render(tx, ty+half, half)
			render(tx+half, ty+half, half)
			return
		}
		img := renderFrame(int(math.Ceil(float64(size)/scale)), scale, func(p *ui.Painter, r ui.Rect) {
			s := sceneAt(p, tx, ty, size)
			p.Fill(r, toUI(pal.Background), 0)
			s.paint(ids)
		})
		draw.Draw(out, image.Rect(tx, ty, tx+size, ty+size), img, image.Point{}, draw.Src)
	}
	for ty := 0; ty < h; ty += tile {
		for tx := 0; tx < w; tx += tile {
			render(tx, ty, tile)
		}
	}
	return out
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
	data, ok := a.RenderPNG(2)
	if !ok {
		return
	}
	a.saveAs(data, a.exportName(".png"), "PNG image", "png")
}

// ExportSVG asks where, and writes an SVG there.
func (a *App) ExportSVG() {
	data, ok := a.ed.SVG(a.ed.DarkMode)
	if !ok {
		return
	}
	a.saveAs(data, a.exportName(".svg"), "SVG image", "svg")
}

// CopyPNG copies what export draws as an image.
func (a *App) CopyPNG() {
	data, ok := a.RenderPNG(2)
	if !ok || !a.hasSvc {
		return
	}
	a.services.WriteClipboardData(transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, data))))
}

func (a *App) saveAs(data []byte, name, kind, ext string) {
	win := a.host.Window()
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Parent: win, Title: "Export", DefaultPath: name,
			Filters: []mygo.FileFilter{{Name: kind, Extensions: []string{ext}}},
		})
		if err != nil || path == "" {
			return
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			mygo.Dialog.Error("Couldn't export", err.Error())
		}
	}()
}
