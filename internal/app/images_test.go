package app

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"reflect"
	"runtime"
	"testing"
	"time"
	"weak"

	"github.com/egoist/mygo/ui"

	"godraw/internal/doc"
	"godraw/internal/editor"
	"godraw/internal/geo"
)

// solidPNG returns a PNG of one color.
func solidPNG(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// insertedImage adds an image in the middle of the view, not yet decoded.
func insertedImage(t *testing.T, e *editor.Editor, data []byte, w, h float64) (doc.ID, doc.ID) {
	t.Helper()
	at := e.ViewBox().Center()
	e.InsertImage("x.png", "image/png", data, w, h, &at)
	shape := e.Selection()[0]
	return shape, e.Doc.Shapes[shape].AssetID
}

// settled pumps and draws until no image is decoding on the side.
func settled(t *testing.T, a *App, tt *ui.Tester, h *testHost) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		tt.Frame()
		h.pump()
		tt.Frame()
		if len(a.pending) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("an image never finished decoding")
		}
		time.Sleep(time.Millisecond)
	}
}

// posted waits for what the decoder posts for the main thread, without running it.
func posted(t *testing.T, h *testHost, n int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		h.mu.Lock()
		got := len(h.queued)
		h.mu.Unlock()
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d posts, waiting for %d", got, n)
		}
		time.Sleep(time.Millisecond)
	}
}

func forgotten(a *App, id doc.ID) bool {
	_, decoded := a.decoded[id]
	return a.bitmaps[id] == nil && !decoded
}

// An image leaves memory along with the last shape to show it, whichever way that
// goes, and undo brings it back.
func TestImagesLeaveWithTheirShapes(t *testing.T) {
	red := color.RGBA{220, 30, 30, 255}
	data := solidPNG(t, 400, 300, red)
	isRed := func(c color.RGBA) bool { return c.R > 180 && c.G < 90 && c.B < 90 }
	for _, c := range []struct {
		name string
		away func(e *editor.Editor, shape doc.ID)
		back func(e *editor.Editor)
	}{
		{"undone", func(e *editor.Editor, _ doc.ID) { e.Undo() }, func(e *editor.Editor) { e.Redo() }},
		{"deleted", func(e *editor.Editor, shape doc.ID) { e.Select(shape); e.Delete() }, func(e *editor.Editor) { e.Undo() }},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, tt, h := newTest(t)
			e := a.Editor()
			shape, asset := insertedImage(t, e, data, 400, 300)
			at := e.ToScreen(e.PageBounds(shape).Center())
			shows := func() color.RGBA {
				return tt.Image().RGBAAt(int(at.X), int(at.Y))
			}
			e.SelectNone()
			settled(t, a, tt, h)
			if a.bitmapOf(asset) == nil {
				t.Fatal("the image never decoded")
			}
			if got := shows(); !isRed(got) {
				t.Fatalf("the image shows as %v", got)
			}
			gone := weak.Make(a.bitmapOf(asset))

			c.away(e, shape)
			tt.Frame()
			if !forgotten(a, asset) {
				t.Errorf("still decoded: %v, %q", a.bitmaps[asset], a.decoded[asset])
			}
			if len(a.pending) != 0 {
				t.Error("what left is decoding again")
			}
			runtime.GC()
			runtime.GC()
			if gone.Value() != nil {
				t.Error("something still holds the bitmap")
			}

			// Back, it shows as a placeholder until it's decoded, on the side.
			c.back(e)
			tt.Frame()
			if got := shows(); isRed(got) {
				t.Errorf("the image shows as %v before it's decoded", got)
			}
			settled(t, a, tt, h)
			if a.bitmapOf(asset) == nil {
				t.Fatal("the image never decoded again")
			}
			if got := shows(); !isRed(got) {
				t.Errorf("the image shows as %v after coming back", got)
			}
		})
	}
}

// An image stays for as long as any shape shows it.
func TestImageStaysWhileAShapeShowsIt(t *testing.T) {
	a, tt, h := newTest(t)
	e := a.Editor()
	first, asset := insertedImage(t, e, solidPNG(t, 40, 30, color.RGBA{0, 0, 200, 255}), 40, 30)
	settled(t, a, tt, h)
	e.Select(first)
	e.Duplicate()
	second := e.Selection()[0]
	if second == first || e.Doc.Shapes[second].AssetID != asset {
		t.Fatalf("the copy is %s of %s", second, e.Doc.Shapes[second].AssetID)
	}
	tt.Frame()
	e.Select(first)
	e.Delete()
	settled(t, a, tt, h)
	if a.bitmapOf(asset) == nil {
		t.Error("the image left with a shape still showing it")
	}
	e.Select(second)
	e.Delete()
	settled(t, a, tt, h)
	if !forgotten(a, asset) {
		t.Error("the image stayed after the last shape showing it")
	}
}

// What a decoder finishes after its image left is thrown away, and what comes back
// while it's decoding takes that result, rather than decoding twice.
func TestStaleDecodes(t *testing.T) {
	data := solidPNG(t, 40, 30, color.RGBA{0, 200, 0, 255})
	other := "data:image/png;base64,iVBORw0KGgo="

	t.Run("left", func(t *testing.T) {
		a, tt, h := newTest(t)
		e := a.Editor()
		_, asset := insertedImage(t, e, data, 40, 30)
		tt.Frame()
		if !a.pending[asset] {
			t.Fatal("nothing is decoding")
		}
		e.Undo()
		tt.Frame()
		posted(t, h, 1)
		h.pump()
		tt.Frame()
		if !forgotten(a, asset) || len(a.pending) != 0 {
			t.Errorf("a stale decode came back: %v, %q, %v", a.bitmaps[asset], a.decoded[asset], a.pending)
		}
	})

	t.Run("came back", func(t *testing.T) {
		a, tt, h := newTest(t)
		e := a.Editor()
		_, asset := insertedImage(t, e, data, 40, 30)
		tt.Frame()
		e.Undo()
		tt.Frame()
		e.Redo()
		tt.Frame()
		posted(t, h, 1)
		time.Sleep(20 * time.Millisecond) // for a second one to show
		h.mu.Lock()
		got := len(h.queued)
		h.mu.Unlock()
		if got != 1 {
			t.Errorf("%d decodes of one image", got)
		}
		h.pump()
		if a.bitmapOf(asset) == nil {
			t.Error("the decode that came back was thrown away")
		}
		tt.Frame()
		if len(a.pending) != 0 {
			t.Error("it decodes again")
		}
	})

	t.Run("changed", func(t *testing.T) {
		a, tt, h := newTest(t)
		e := a.Editor()
		_, asset := insertedImage(t, e, data, 40, 30)
		src := e.Doc.Assets[asset].Src
		tt.Frame()
		changed := *e.Doc.Assets[asset]
		changed.Src = other
		e.Doc.Assets[asset] = &changed
		posted(t, h, 1)
		h.pump()
		if a.decoded[asset] == src || a.bitmaps[asset] != nil {
			t.Error("the decode of what the asset was is what it shows")
		}
		settled(t, a, tt, h)
		if a.decoded[asset] != other {
			t.Errorf("the changed asset isn't decoded: %q", a.decoded[asset])
		}
	})
}

// An image added to an edit still open, as a slider leaves, shows before it's committed.
func TestImageInAnOpenEdit(t *testing.T) {
	a, tt, h := newTest(t)
	e := a.Editor()
	box := e.ViewBox()
	e.SetGeoTool("rectangle")
	e.PointerDown(box.X+10, box.Y+10, 0, 1, editor.Mods{}, 0)
	e.PointerMove(box.X+90, box.Y+90, editor.Mods{}, 0.1)
	e.PointerUp(box.X+90, box.Y+90, editor.Mods{}, 0.2)
	tt.Frame()
	e.SetOpacity(0.5, true)
	_, asset := insertedImage(t, e, solidPNG(t, 40, 30, color.RGBA{0, 0, 200, 255}), 40, 30)
	settled(t, a, tt, h)
	if a.bitmapOf(asset) == nil {
		t.Error("an image in an open edit never decoded")
	}
	e.CommitOpen()
	settled(t, a, tt, h)
	if a.bitmapOf(asset) == nil {
		t.Error("the image left on commit")
	}
}

// Showing another drawing lets go of the images of the last.
func TestReplaceForgetsImages(t *testing.T) {
	a, tt, h := newTest(t)
	e := a.Editor()
	_, asset := insertedImage(t, e, solidPNG(t, 40, 30, color.RGBA{0, 0, 200, 255}), 40, 30)
	settled(t, a, tt, h)
	e.Replace(doc.New())
	tt.Frame()
	if !forgotten(a, asset) {
		t.Error("an image of the last drawing stayed")
	}
}

// An export decodes what shapes show, and nothing that no shape does.
func TestExportDecodesOnlyWhatShows(t *testing.T) {
	a, tt, h := newTest(t)
	e := a.Editor()
	shape, asset := insertedImage(t, e, solidPNG(t, 40, 30, color.RGBA{0, 0, 200, 255}), 40, 30)
	keep := e.PageBounds(shape)
	img := a.RenderImage(keep, e.Children(e.Page), 1)
	if a.bitmapOf(asset) == nil {
		t.Fatal("the export didn't decode the image")
	}
	if got := img.RGBAAt(img.Rect.Dx()/2, img.Rect.Dy()/2); got.B < 150 || got.R > 60 {
		t.Errorf("the image exports as %v", got)
	}
	e.Select(shape)
	e.Delete()
	a.RenderImage(geo.Box{W: 50, H: 50}, e.Children(e.Page), 1)
	if !forgotten(a, asset) {
		t.Error("an export kept or decoded an image no shape shows")
	}
	settled(t, a, tt, h)
}

// Without a change to the document, no frame looks through its shapes.
func TestFramesDontLookForImages(t *testing.T) {
	a, tt, h := newTest(t)
	e := a.Editor()
	insertedImage(t, e, solidPNG(t, 40, 30, color.RGBA{0, 0, 200, 255}), 40, 30)
	settled(t, a, tt, h)
	looked := func() uintptr { return reflect.ValueOf(a.inUse).Pointer() }
	seen := looked()
	tt.Frame()
	if looked() != seen {
		t.Error("a frame looked through the shapes again")
	}
	e.SelectAll()
	e.Delete()
	tt.Frame()
	if looked() == seen {
		t.Error("a change didn't have a frame look through the shapes")
	}
}

// The memory of an image goes with it.
func TestForgottenImageFreesTheHeap(t *testing.T) {
	const w, h = 3000, 2000 // 24 MB of pixels
	a, tt, host := newTest(t)
	e := a.Editor()
	_, asset := insertedImage(t, e, solidPNG(t, w, h, color.RGBA{0, 0, 200, 255}), 600, 400)
	settled(t, a, tt, host)
	if a.bitmapOf(asset) == nil {
		t.Fatal("the image never decoded")
	}
	heap := func() uint64 {
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}
	before := heap()
	e.Undo()
	tt.Frame()
	after := heap()
	freed := (float64(before) - float64(after)) / (1 << 20)
	t.Logf("freed %.1f MB of %.1f", freed, float64(before)/(1<<20))
	if freed < 20 {
		t.Errorf("forgetting a 24 MB image freed %.1f MB", freed)
	}
	runtime.KeepAlive(a)
	runtime.KeepAlive(tt)
}
