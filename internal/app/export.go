package app

import (
	"bytes"
	"image"
	"image/draw"
	"image/png"
	"math"
	"os"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"godraw/internal/editor"
	"godraw/internal/geo"
)

// RenderPNG draws what export draws (the selection, else the page) at a
// scale, and encodes it.
func (a *App) RenderPNG(scale float64) ([]byte, bool) {
	e := a.ed
	box, ids, ok := e.ExportBounds()
	if !ok {
		return nil, false
	}
	box = box.Expand(32)
	// Very large pages come out at a size a file can hold.
	const maxSide = 8192.0
	if k := maxSide / (math.Max(box.W, box.H) * scale); k < 1 {
		scale *= k
	}
	pal := e.Palette()
	// In tiles, each a frame with a mask atlas of its own: a whole page of
	// strokes wouldn't fit one. Tiles are whole device pixels apart, so that
	// nothing shows where they meet.
	const tile = 1024
	w, h := int(math.Ceil(box.W*scale)), int(math.Ceil(box.H*scale))
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	side := int(math.Ceil(tile / scale))
	for ty := 0; ty < h; ty += tile {
		for tx := 0; tx < w; tx += tile {
			at := geo.V(box.X+float64(tx)/scale, box.Y+float64(ty)/scale)
			img := ui.Render(func(c *ui.Context) {
				ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
					s := &scene{a: a, e: e, p: p, pal: pal, export: true}
					s.cam = editor.Camera{X: -at.X, Y: -at.Y, Z: 1}
					s.view = geo.Box{X: at.X, Y: at.Y, W: float64(side), H: float64(side)}
					p.Fill(r, toUI(pal.Background), 0)
					for _, id := range ids {
						sh := e.Doc.Shapes[id]
						switch sh.Type {
						case "group":
							s.layer(id, true)
							s.layer(id, false)
						case "frame":
							s.frame(sh)
						default:
							s.shape(sh)
						}
					}
				})
			}, side, side, float32(scale))
			draw.Draw(out, image.Rect(tx, ty, tx+tile, ty+tile), img, image.Point{}, draw.Src)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}

func (a *App) exportName(ext string) string {
	name := a.ed.Doc.Name
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
		os.WriteFile(path, data, 0o644)
	}()
}
