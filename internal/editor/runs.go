package editor

import (
	"math"
	"strings"
	"unicode"

	"godraw/internal/doc"
)

// TextPiece is a part of a line of text in a style of its own, as one run of a
// shape's text comes to on that line.
type TextPiece struct {
	Text string
	X    float64 // from where the line starts
	Size float64
	Rise float64 // how far above the line's baseline it stands
	Font string
	Ink  Color // Alpha 0 for the color of the shape's text
	// The color asked for, until a palette says what it paints with.
	color string
}

const (
	// How large a superscript or subscript is, in the text around it.
	scriptSize = 0.65
	// How far they stand off the baseline, in the size of the text around them.
	supRise = 0.38
	subRise = -0.16
)

// TextBlockOf lays out a shape's text: text, which may have something added to the
// shape's own, wrapping at width. Runs lay it out only when it's the shape's own.
func (e *Editor) TextBlockOf(s *doc.Shape, text string, size, width float64) TextBlock {
	if text == s.Text || text == s.Text+" " {
		if runs := s.Runs(); runs != nil {
			return e.layoutRuns(runs, s.Font, size, width)
		}
	}
	return e.Measure(text, s.Font, size, width)
}

// fragment is a piece of a word in the style of one run.
type fragment struct {
	text string
	run  int
}

// layoutRuns lays out text in parts of different styles. Lines break at spaces
// and newlines; the measure breaks a single style's text with all a system knows of
// languages, which what's broken across styles has no use for. Each line's pieces are
// measured whole, for what's painted as one to land where it's laid out.
func (e *Editor) layoutRuns(runs []doc.Run, font string, size, width float64) TextBlock {
	styleOf := func(i int) (string, float64) {
		r := runs[i]
		f := font
		if r.Font != "" {
			f = r.Font
		}
		k := size
		if r.Scale > 0 {
			k *= r.Scale
		}
		if r.Sup || r.Sub {
			k *= scriptSize
		}
		return f, k
	}
	wide := func(fs []fragment) float64 {
		w := 0.0
		for _, m := range mergeFragments(fs) {
			f, k := styleOf(m.run)
			w += e.Measure(m.text, f, k, 0).W
		}
		return w
	}
	var lines [][]fragment
	var line []fragment
	used := 0.0
	flush := func() {
		lines = append(lines, line)
		line, used = nil, 0
	}
	for _, para := range splitParagraphs(runs) {
		for _, word := range splitWords(para) {
			w := wide(word)
			if width > 0 && len(line) > 0 && used+wide(trimmedRight(word)) > width {
				flush()
			}
			line = append(line, word...)
			used += w
		}
		flush()
	}
	out := TextBlock{Line: size * LineHeight}
	y := 0.0
	for _, fs := range lines {
		l := TextLine{Y: y, Tall: out.Line, BigSize: size, BigFont: font}
		var text strings.Builder
		for _, m := range mergeFragments(trimmedRight(fs)) {
			f, k := styleOf(m.run)
			r := runs[m.run]
			rise := 0.0
			base := k
			switch {
			case r.Sup:
				base = k / scriptSize
				rise = base * supRise
			case r.Sub:
				base = k / scriptSize
				rise = base * subRise
			}
			if base > l.BigSize {
				l.BigSize, l.BigFont = base, f
				l.Tall = base * LineHeight
			}
			w := e.Measure(m.text, f, k, 0).W
			l.Pieces = append(l.Pieces, TextPiece{Text: m.text, X: l.W, Size: k, Rise: rise, Font: f, color: r.Color})
			l.W += w
			text.WriteString(m.text)
		}
		l.Text = text.String()
		out.Lines = append(out.Lines, l)
		out.W = math.Max(out.W, l.W)
		y += l.Tall
	}
	if len(out.Lines) == 0 {
		out.Lines = []TextLine{{}}
	}
	out.H = math.Max(y, out.Line)
	return out
}

// splitParagraphs returns runs' text as fragments, cut at its newlines.
func splitParagraphs(runs []doc.Run) [][]fragment {
	out := [][]fragment{nil}
	for i, r := range runs {
		parts := strings.Split(r.Text, "\n")
		for k, part := range parts {
			if k > 0 {
				out = append(out, nil)
			}
			if part != "" {
				out[len(out)-1] = append(out[len(out)-1], fragment{part, i})
			}
		}
	}
	return out
}

// splitWords cuts a paragraph after each run of spaces. A word can span runs, as a
// number and its exponent do, and doesn't break where they meet.
func splitWords(para []fragment) [][]fragment {
	var out [][]fragment
	var cur []fragment
	for _, f := range para {
		rest := f.text
		for rest != "" {
			end := strings.IndexFunc(rest, unicode.IsSpace)
			if end < 0 {
				cur = append(cur, fragment{rest, f.run})
				break
			}
			next := strings.TrimLeftFunc(rest[end:], unicode.IsSpace)
			cur = append(cur, fragment{rest[:len(rest)-len(next)], f.run})
			out = append(out, cur)
			cur = nil
			rest = next
		}
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// trimmedRight returns fragments without the spaces that end them, which take no
// room at the end of a line.
func trimmedRight(fs []fragment) []fragment {
	out := append([]fragment(nil), fs...)
	for len(out) > 0 {
		last := &out[len(out)-1]
		last.text = strings.TrimRightFunc(last.text, unicode.IsSpace)
		if last.text != "" {
			break
		}
		out = out[:len(out)-1]
	}
	return out
}

// mergeFragments joins what comes next to each other in one run.
func mergeFragments(fs []fragment) []fragment {
	var out []fragment
	for _, f := range fs {
		if n := len(out); n > 0 && out[n-1].run == f.run {
			out[n-1].text += f.text
			continue
		}
		out = append(out, f)
	}
	return out
}

// inked returns a block with the colors its pieces asked for, in a palette.
func inked(b TextBlock, pal *Palette) TextBlock {
	lines := make([]TextLine, len(b.Lines))
	for i, l := range b.Lines {
		if len(l.Pieces) > 0 {
			pieces := make([]TextPiece, len(l.Pieces))
			for k, pc := range l.Pieces {
				if pc.color != "" {
					pc.Ink = pal.colorOf(pc.color)
				}
				pieces[k] = pc
			}
			l.Pieces = pieces
		}
		lines[i] = l
	}
	b.Lines = lines
	return b
}
