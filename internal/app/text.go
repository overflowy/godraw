package app

import (
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/egoist/mygo/ui"

	"godraw/internal/assets"
	"godraw/internal/editor"
)

const (
	drawFamily  = "Shantell Sans"
	sansFamily  = "IBM Plex Sans"
	serifFamily = "IBM Plex Serif"
	monoFamily  = "IBM Plex Mono"
)

var registerFonts = sync.OnceFunc(func() {
	for family, data := range map[string][]byte{
		drawFamily:  assets.DrawFont,
		sansFamily:  assets.SansFont,
		serifFamily: assets.SerifFont,
		monoFamily:  assets.MonoFont,
	} {
		if err := ui.RegisterFont(data, family); err != nil {
			panic(err)
		}
	}
})

// family returns the font family of a font style.
func family(font string) string {
	switch font {
	case "draw":
		return drawFamily
	case "serif":
		return serifFamily
	case "mono":
		return monoFamily
	}
	return sansFamily
}

type measureKey struct {
	text, font  string
	size, width float32
}

// measurer lays out the text of shapes, keeping what it laid out: a shape's
// text is measured for its geometry and its drawing alike. A window and the
// exports it makes on the side share one, from any goroutine.
type measurer struct {
	mu    sync.Mutex
	cache map[measureKey]editor.TextBlock
}

func newMeasurer() *measurer {
	registerFonts()
	return &measurer{cache: map[measureKey]editor.TextBlock{}}
}

func (m *measurer) measure(text, font string, size, width float64) editor.TextBlock {
	k := measureKey{text, font, float32(size), float32(width)}
	m.mu.Lock()
	b, ok := m.cache[k]
	m.mu.Unlock()
	if ok {
		return b
	}
	// Laid out unlocked: the main thread doesn't wait for an export's text.
	b = layoutBlock(text, font, size, width)
	m.mu.Lock()
	if len(m.cache) > 4096 {
		clear(m.cache)
	}
	m.cache[k] = b
	m.mu.Unlock()
	return b
}

func layoutBlock(text, font string, size, width float64) editor.TextBlock {
	f := ui.Font{Family: family(font), Size: float32(size), LineHeight: editor.LineHeight}
	l := ui.ShapeText(text, f, float32(width))
	line := size * editor.LineHeight
	units := utf16.Encode([]rune(text))
	lines := l.Lines()
	out := editor.TextBlock{Line: line}
	for i, ln := range lines {
		end := min(ln.End, len(units))
		start := min(ln.Start, end)
		s := string(utf16.Decode(units[start:end]))
		trimmed := strings.TrimRight(s, " \t\r\n")
		w := float64(ln.Rect.W)
		if trimmed != s {
			// Whitespace that ends a line takes no room when it aligns.
			c := l.Caret(start + len(utf16.Encode([]rune(trimmed))))
			w = float64(c.X - ln.Rect.X)
		}
		out.Lines = append(out.Lines, editor.TextLine{Text: trimmed, Y: float64(i) * line, W: w})
		if float64(ln.Rect.W) > out.W {
			out.W = float64(ln.Rect.W)
		}
	}
	if len(lines) == 0 {
		out.Lines = []editor.TextLine{{}}
	}
	out.H = float64(len(out.Lines)) * line
	return out
}

// paintTheme returns the theme that the canvas paints with: t, with a font smaller
// than any text it paints. A Painter.RichText's lines are at least as tall as the
// theme's font, with their glyphs as far down them, so that small text would fall
// away from where it's meant to be. RichText it is all the same, rather than layouts
// kept from frame to frame: the text system lets go of its fonts when it has made
// too many, as a zoom does, and painting a layout made before that crashes.
func paintTheme(t *ui.Theme) *ui.Theme {
	painted := *t
	painted.FontSize = 1
	return &painted
}

// naturalHeight returns the height a font's line takes, in its size, without
// a set line height: text draws in it, centered in its line.
var naturalHeights, ascents sync.Map

func naturalHeight(font string) float32 {
	if v, ok := naturalHeights.Load(font); ok {
		return v.(float32)
	}
	registerFonts()
	const probe = 100
	l := ui.ShapeText("Hg", ui.Font{Family: family(font), Size: probe}, 0)
	_, h := l.Size()
	v := h / probe
	naturalHeights.Store(font, v)
	return v
}

// ascentOf returns how far below the top of its natural line a font's baseline
// lies, in its size: where text of different sizes has to meet.
func ascentOf(font string) float32 {
	if v, ok := ascents.Load(font); ok {
		return v.(float32)
	}
	registerFonts()
	const probe = 100
	l := ui.ShapeText("Hg", ui.Font{Family: family(font), Size: probe}, 0)
	var v float32
	if lines := l.Lines(); len(lines) > 0 {
		v = lines[0].Baseline / probe
	}
	ascents.Store(font, v)
	return v
}
