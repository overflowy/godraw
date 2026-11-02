package app

import (
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/egoist/mygo/ui"

	"godraw/internal/assets"
	"godraw/internal/editor"
)

const drawFamily = "Shantell Sans"

var registerFonts = sync.OnceFunc(func() {
	if err := ui.RegisterFont(assets.DrawFont, drawFamily); err != nil {
		panic(err)
	}
})

// family returns the font family of a font style.
func family(font string) string {
	switch font {
	case "draw":
		return drawFamily
	case "serif":
		return "Georgia, serif"
	case "mono":
		return "monospace"
	}
	return "system-ui"
}

type measureKey struct {
	text, font  string
	size, width float32
}

// measurer lays out the text of shapes, keeping what it laid out: a shape's
// text is measured for its geometry and its drawing alike.
type measurer struct {
	cache map[measureKey]editor.TextBlock
}

func newMeasurer() *measurer {
	registerFonts()
	return &measurer{cache: map[measureKey]editor.TextBlock{}}
}

func (m *measurer) measure(text, font string, size, width float64) editor.TextBlock {
	k := measureKey{text, font, float32(size), float32(width)}
	if b, ok := m.cache[k]; ok {
		return b
	}
	if len(m.cache) > 4096 {
		clear(m.cache)
	}
	b := layoutBlock(text, font, size, width)
	m.cache[k] = b
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

// naturalHeight returns the height a font's line takes, in its size, without
// a set line height: text draws in it, centered in its line.
var naturalHeights sync.Map

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
