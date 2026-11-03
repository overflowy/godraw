package app

import (
	"math"
	"testing"

	"godraw/internal/editor"
)

// Text must take the room in this app that it takes in tldraw, or what fits here
// wraps there. The widths are what tldraw measures, with its own fonts.
func TestTextWidthsMatchTldraw(t *testing.T) {
	registerFonts()
	const fox = "The quick brown fox jumps over the lazy dog"
	for _, c := range []struct {
		font, text string
		size, want float64
	}{
		{"draw", fox, 18, 416.39},
		{"draw", fox, 24, 555.17},
		{"draw", fox, 36, 832.77},
		{"draw", "A body under a medium title", 18, 267.31},
		{"draw", "Medium card", 24, 161.64},
		{"draw", "0123456789", 18, 115.42},
		{"sans", fox, 18, 364.66},
		{"serif", fox, 18, 383.77},
		{"mono", fox, 18, 464.41},
	} {
		got := layoutBlock(c.text, c.font, c.size, 100000).W
		if math.Abs(got-c.want)/c.want > 0.005 {
			t.Errorf("%s %q at %v: %.2f wide, tldraw has %.2f", c.font, c.text, c.size, got, c.want)
		}
	}
}

// What a font lacks still takes room, from another font.
func TestTextFallsBack(t *testing.T) {
	registerFonts()
	for _, font := range []string{"draw", "sans", "serif", "mono"} {
		if got := layoutBlock("日本語", font, 18, 100000).W; got < 18 {
			t.Errorf("%s: %.2f wide", font, got)
		}
	}
}

// A label on the line wraps as tldraw's does on an arrow of that size. The heights
// are what tldraw measures, with its padding: 35.5 is a line, and each more is 27.
func TestArrowLabelsWrapLikeTldraw(t *testing.T) {
	const long = "A label long enough that it has to wrap somewhere on the way across"
	for _, c := range []struct {
		w, h  float64
		text  string
		elbow bool
		want  float64
	}{
		{150, 0, "JWKS, Admin API", false, 89.5},
		{130, 0, "COPY, upsert", false, 89.5},
		{300, 0, "JWKS, Admin API", false, 35.5},
		{80, 0, "reads", false, 35.5},
		{400, 0, long, false, 89.5},
		{0, 300, "JWKS, Admin API", false, 35.5},
		{0, 300, long + " and more", false, 89.5},
		{200, 100, "JWKS, Admin API", true, 62.5},
		{120, 200, "COPY, upsert and more", true, 35.5},
	} {
		a, _, _ := newTest(t)
		e := a.ed
		e.SetTool(editor.ToolArrow)
		cmd := editor.Mods{Cmd: true}
		e.PointerMove(100, 100, cmd, 0)
		e.PointerDown(100, 100, 0, 1, cmd, 0)
		e.PointerMove(100+c.w, 100+c.h, cmd, 0)
		e.PointerUp(100+c.w, 100+c.h, cmd, 0)
		id := e.Selection()[0]
		if err := e.Batch(func() error {
			s := e.Doc.Shapes[id].Clone()
			s.SetText(c.text)
			if c.elbow {
				s.Kind = "elbow"
			}
			e.Put(s)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if got := e.ArrowLabelBox(e.Doc.Shapes[id]).H; math.Abs(got-c.want) > 0.5 {
			t.Errorf("%q on %vx%v: %.1f tall, tldraw has %.1f", c.text, c.w, c.h, got, c.want)
		}
		// And that's how it's drawn, not only the room kept for it.
		lines := int(math.Round((c.want - 8.5) / 27))
		for _, p := range e.Display(e.Doc.Shapes[id], e.Palette()) {
			if p.Kind == editor.PrimText && len(p.Block.Lines) != lines {
				t.Errorf("%q on %vx%v: draws %d lines, tldraw has %d", c.text, c.w, c.h, len(p.Block.Lines), lines)
			}
		}
	}
}
