package editor

import (
	"fmt"
	"strconv"
)

// Color is a color with its alpha, from 0 to 1.
type Color struct {
	R, G, B uint8
	A       float64
}

// Hex returns the color of a "#rrggbb" string.
func Hex(s string) Color {
	if len(s) != 7 || s[0] != '#' {
		return Color{A: 1}
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return Color{A: 1}
	}
	return Color{uint8(v >> 16), uint8(v >> 8), uint8(v), 1}
}

// Alpha returns c with its alpha multiplied by a.
func (c Color) Alpha(a float64) Color {
	c.A *= a
	return c
}

// CSS returns the color as SVG takes it.
func (c Color) CSS() string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// Swatch is a named color in each of the forms shapes use it.
type Swatch struct {
	Solid, Semi, Pattern, Fill, Lined, NoteFill, NoteText, FrameStroke, FrameFill, FrameText, Highlight Color
}

// Palette is the colors of a theme: tldraw's, so that files look as they do
// there.
type Palette struct {
	Dark       bool
	Text       Color
	Background Color
	Solid      Color
	Cursor     Color
	Snap       Color
	Selection  Color
	SelFill    Color
	BrushFill  Color
	BrushLine  Color
	Laser      Color
	Grid       Color
	Swatches   map[string]Swatch
}

// Colors lists the names of the palette's colors, in the order the style
// panel shows them.
var Colors = []string{
	"black", "grey", "light-violet", "violet",
	"blue", "light-blue", "yellow", "orange",
	"green", "light-green", "light-red", "red",
	"white",
}

func sw(solid, semi, pattern, fill, lined, noteFill, noteText, frameStroke, frameFill, frameText, highlight string) Swatch {
	return Swatch{
		Hex(solid), Hex(semi), Hex(pattern), Hex(fill), Hex(lined), Hex(noteFill), Hex(noteText),
		Hex(frameStroke), Hex(frameFill), Hex(frameText), Hex(highlight),
	}
}

// Light is the light theme.
var Light = &Palette{
	Text:       Hex("#000000"),
	Background: Hex("#f9fafb"),
	Solid:      Hex("#fcfffe"),
	Cursor:     Hex("#000000"),
	Snap:       Hex("#e64a4a"),
	Selection:  Hex("#2f80ed"),
	SelFill:    Color{31, 143, 255, 0.24},
	BrushFill:  Color{143, 143, 143, 0.102},
	BrushLine:  Color{143, 143, 143, 0.251},
	Laser:      Hex("#ff0000"),
	Grid:       Hex("#bcc1c7"),
	Swatches: map[string]Swatch{
		"black":        sw("#1d1d1d", "#e8e8e8", "#494949", "#1d1d1d", "#363636", "#FCE19C", "#000000", "#717171", "#ffffff", "#000000", "#fddd00"),
		"blue":         sw("#4465e9", "#dce1f8", "#6681ee", "#4465e9", "#6580ec", "#8AA3FF", "#000000", "#6681ec", "#f9fafe", "#000000", "#10acff"),
		"green":        sw("#099268", "#d3e9e3", "#39a785", "#099268", "#0bad7c", "#6FC896", "#000000", "#37a684", "#f8fcfa", "#000000", "#00ffc8"),
		"grey":         sw("#9fa8b2", "#eceef0", "#bcc3c9", "#9fa8b2", "#bbc1c9", "#C0CAD3", "#000000", "#aaaaab", "#fcfcfd", "#000000", "#cbe7f1"),
		"light-blue":   sw("#4ba1f1", "#ddedfa", "#6fbbf8", "#4ba1f1", "#7abaf5", "#9BC4FD", "#000000", "#6cb2f3", "#fafcff", "#000000", "#00f4ff"),
		"light-green":  sw("#4cb05e", "#dbf0e0", "#65cb78", "#4cb05e", "#7ec88c", "#98D08A", "#000000", "#6dbe7c", "#fafdfa", "#000000", "#65f641"),
		"light-red":    sw("#f87777", "#f4dadb", "#fe9e9e", "#f87777", "#f99a9a", "#F7A5A1", "#000000", "#f89090", "#fffbfb", "#000000", "#ff7fa3"),
		"light-violet": sw("#e085f4", "#f5eafa", "#e9acf8", "#e085f4", "#e9abf7", "#DFB0F9", "#000000", "#e59bf5", "#fefbff", "#000000", "#ff88ff"),
		"orange":       sw("#e16919", "#f8e2d4", "#f78438", "#e16919", "#ea8643", "#FAA475", "#000000", "#e68544", "#fef9f6", "#000000", "#ffa500"),
		"red":          sw("#e03131", "#f4dadb", "#e55959", "#e03131", "#e75f5f", "#FC8282", "#000000", "#e55757", "#fef9f9", "#000000", "#ff636e"),
		"violet":       sw("#ae3ec9", "#ecdcf2", "#bd63d3", "#ae3ec9", "#be68d4", "#DB91FD", "#000000", "#bc62d3", "#fdf9fd", "#000000", "#c77cff"),
		"yellow":       sw("#f1ac4b", "#f9f0e6", "#fecb92", "#f1ac4b", "#f5c27a", "#FED49A", "#000000", "#f3bb6c", "#fffdfa", "#000000", "#fddd00"),
		"white":        sw("#FFFFFF", "#f5f5f5", "#f9f9f9", "#FFFFFF", "#ffffff", "#FFFFFF", "#000000", "#7d7d7d", "#ffffff", "#000000", "#ffffff"),
	},
}

// Dark is the dark theme.
var Dark = &Palette{
	Dark:       true,
	Text:       Hex("#f9fafb"),
	Background: Hex("#101012"),
	Solid:      Hex("#010403"),
	Cursor:     Hex("#ffffff"),
	Snap:       Hex("#e64a4a"),
	Selection:  Hex("#2f80ed"),
	SelFill:    Color{36, 145, 255, 0.20},
	BrushFill:  Color{143, 143, 143, 0.102},
	BrushLine:  Color{143, 143, 143, 0.251},
	Laser:      Hex("#ff0000"),
	Grid:       Hex("#3a3a3f"),
	Swatches: map[string]Swatch{
		"black":        sw("#f2f2f2", "#2c3036", "#989898", "#f2f2f2", "#ffffff", "#2c2c2c", "#f2f2f2", "#5c5c5c", "#0c0c0c", "#f2f2f2", "#d2b700"),
		"blue":         sw("#4f72fc", "#262d40", "#3a4b9e", "#4f72fc", "#3c5cdd", "#2A3F98", "#f2f2f2", "#384994", "#11141f", "#f2f2f2", "#0079d2"),
		"green":        sw("#099268", "#253231", "#366a53", "#099268", "#087856", "#014429", "#f2f2f2", "#10513C", "#0E1614", "#f2f2f2", "#009774"),
		"grey":         sw("#9398b0", "#33373c", "#7c8187", "#9398b0", "#8388a5", "#56595F", "#f2f2f2", "#42474D", "#151719", "#f2f2f2", "#9cb4cb"),
		"light-blue":   sw("#4dabf7", "#2a3642", "#4d7aa9", "#4dabf7", "#2793ec", "#1F5495", "#f2f2f2", "#075797", "#0B1823", "#f2f2f2", "#00bdc8"),
		"light-green":  sw("#40c057", "#2a3830", "#4e874e", "#40c057", "#37a44b", "#21581D", "#f2f2f2", "#1C5427", "#0F1911", "#f2f2f2", "#00a000"),
		"light-red":    sw("#ff8787", "#3c2b2b", "#a56767", "#ff8787", "#ff6666", "#7a3333", "#f2f2f2", "#6f3232", "#181212", "#f2f2f2", "#db005b"),
		"light-violet": sw("#e599f7", "#383442", "#9770a9", "#e599f7", "#dc71f4", "#762F8E", "#f2f2f2", "#6c367a", "#1C151E", "#f2f2f2", "#c400c7"),
		"orange":       sw("#f76707", "#3b2e27", "#9f552d", "#f76707", "#f54900", "#7c3905", "#f2f2f2", "#773a0e", "#1c1512", "#f2f2f2", "#d07a00"),
		"red":          sw("#e03131", "#382726", "#8f3734", "#e03131", "#c31d1d", "#7e201f", "#f2f2f2", "#701e1e", "#1b1313", "#f2f2f2", "#de002c"),
		"violet":       sw("#ae3ec9", "#342938", "#763a8b", "#ae3ec9", "#8f2fa7", "#5f1c70", "#f2f2f2", "#6d1583", "#1b0f21", "#f2f2f2", "#9e00ee"),
		"yellow":       sw("#ffc034", "#3b352b", "#fecb92", "#ffc034", "#ffae00", "#8a5e1c", "#f2f2f2", "#684e12", "#1e1911", "#f2f2f2", "#d2b700"),
		"white":        sw("#f3f3f3", "#f5f5f5", "#f9f9f9", "#f3f3f3", "#f3f3f3", "#eaeaea", "#1d1d1d", "#ffffff", "#ffffff", "#000000", "#ffffff"),
	},
}

// Swatch returns a color of the palette by name, black for names it has not.
func (p *Palette) Swatch(name string) Swatch {
	if s, ok := p.Swatches[name]; ok {
		return s
	}
	return p.Swatches["black"]
}

// Sizes of the size style, as tldraw has them.
func strokeOf(size string) float64 {
	switch size {
	case "s":
		return 2
	case "l":
		return 5
	case "xl":
		return 10
	}
	return 3.5
}

// fontSizeOf is the size of the text of text shapes.
func fontSizeOf(size string) float64 {
	switch size {
	case "s":
		return 18
	case "l":
		return 36
	case "xl":
		return 44
	}
	return 24
}

// labelSizeOf is the size of the labels of shapes and notes.
func labelSizeOf(size string) float64 {
	switch size {
	case "s":
		return 18
	case "l":
		return 26
	case "xl":
		return 32
	}
	return 22
}

// arrowLabelSizeOf is the size of the labels of arrows.
func arrowLabelSizeOf(size string) float64 {
	switch size {
	case "s":
		return 18
	case "l":
		return 24
	case "xl":
		return 28
	}
	return 20
}

// LineHeight is the height of a line of text, in its font size.
const LineHeight = 1.35

const (
	labelPadding = 16
	noteSize     = 200
)
