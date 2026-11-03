package doc

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"strings"
)

// drawingFile is a .godraw file: plain JSON, with a line for each record so that
// it diffs well.
type drawingFile struct {
	Version  int        `json:"godrawFileFormatVersion"`
	Name     string     `json:"name"`
	GridSize float64    `json:"gridSize"`
	Pages    []*Page    `json:"pages"`
	Shapes   []*Shape   `json:"shapes"`
	Bindings []*Binding `json:"bindings"`
	Assets   []*Asset   `json:"assets"`
}

// formatVersion is the version of the files this app writes.
const formatVersion = 1

// ErrNotDrawing tells that a file is not a drawing this app reads.
var ErrNotDrawing = errors.New("this is not a GoDraw file")

// Read reads a .godraw file.
func Read(r io.Reader) (*Drawing, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return New(), nil // a file made empty, as with touch
	}
	var f drawingFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotDrawing, err)
	}
	if f.Version == 0 || len(f.Pages) == 0 {
		return nil, ErrNotDrawing
	}
	if f.Version > formatVersion {
		return nil, fmt.Errorf("this file is from a newer version of GoDraw (format %d)", f.Version)
	}
	d := &Drawing{
		Name:     f.Name,
		GridSize: f.GridSize,
		Pages:    f.Pages,
		Shapes:   make(map[ID]*Shape, len(f.Shapes)),
		Bindings: make(map[ID]*Binding, len(f.Bindings)),
		Assets:   make(map[ID]*Asset, len(f.Assets)),
	}
	d.GridSize = gridOf(d)
	if slices.Contains(d.Pages, nil) || slices.Contains(f.Shapes, nil) || slices.Contains(f.Bindings, nil) || slices.Contains(f.Assets, nil) {
		return nil, ErrNotDrawing
	}
	for _, s := range f.Shapes {
		if !knownType(s.Type) {
			return nil, fmt.Errorf("shape %s: no shape is of type %q", s.ID, s.Type)
		}
		slices.SortStableFunc(s.Points, func(a, b LinePoint) int { return strings.Compare(a.Index, b.Index) })
		d.Shapes[s.ID] = s
	}
	for _, b := range f.Bindings {
		d.Bindings[b.ID] = b
	}
	for _, a := range f.Assets {
		d.Assets[a.ID] = a
	}
	if err := check(d); err != nil {
		return nil, err
	}
	slices.SortStableFunc(d.Pages, func(a, b *Page) int { return strings.Compare(a.Index, b.Index) })
	return d, nil
}

// check refuses what no save of the app writes and the editor counts on not meeting:
// a hand's edits that would hang it or panic it later, far from the cause.
func check(d *Drawing) error {
	for _, p := range d.Pages {
		if !validIndex(p.Index) {
			return fmt.Errorf("page %s: bad index %q", p.ID, p.Index)
		}
	}
	// Shapes known not to be in a loop, however far up they go.
	free := make(map[ID]bool, len(d.Shapes))
	for _, s := range sortedByID(d.Shapes) {
		if !validIndex(s.Index) {
			return fmt.Errorf("shape %s: bad index %q", s.ID, s.Index)
		}
		for _, pt := range s.Points {
			if !validIndex(pt.Index) {
				return fmt.Errorf("shape %s: point %s has bad index %q", s.ID, pt.ID, pt.Index)
			}
		}
		if s.Gradient != nil && len(s.Gradient.Colors) < 2 {
			return fmt.Errorf("shape %s: a gradient needs 2 colors or more, and has %d", s.ID, len(s.Gradient.Colors))
		}
		path := map[ID]bool{}
		for cur := s; cur != nil && !free[cur.ID]; cur = d.Shapes[cur.Parent] {
			if path[cur.ID] {
				return fmt.Errorf("shape %s is inside itself", cur.ID)
			}
			path[cur.ID] = true
		}
		for id := range path {
			free[id] = true
		}
	}
	return nil
}

func knownType(t string) bool {
	switch t {
	case TypeGeo, TypeDraw, TypeHighlight, TypeArrow, TypeLine, TypeText, TypeNote, TypeFrame, TypeImage, TypeGroup:
		return true
	}
	return false
}

// Write writes a .godraw file. Records come in the order of their ids, so that a
// file is the same each time it is written. A deleted image's asset stays in the
// drawing for undo to bring it back, but has no place in the file, where it would
// be kept for good at the size of the image.
func Write(w io.Writer, d *Drawing) error {
	used := map[ID]bool{}
	for _, s := range d.Shapes {
		if s.AssetID != "" {
			used[s.AssetID] = true
		}
	}
	assets := make([]*Asset, 0, len(used))
	for _, id := range slices.Sorted(maps.Keys(d.Assets)) {
		if used[id] {
			assets = append(assets, d.Assets[id])
		}
	}
	b := bufio.NewWriter(w)
	name, err := json.Marshal(d.Name)
	if err != nil {
		return err
	}
	grid, err := json.Marshal(gridOf(d))
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(b, `{"godrawFileFormatVersion":%d,"name":%s,"gridSize":%s`, formatVersion, name, grid); err != nil {
		return err
	}
	if err := writeList(b, "pages", d.Pages); err != nil {
		return err
	}
	if err := writeList(b, "shapes", sortedByID(d.Shapes)); err != nil {
		return err
	}
	if err := writeList(b, "bindings", sortedByID(d.Bindings)); err != nil {
		return err
	}
	if err := writeList(b, "assets", assets); err != nil {
		return err
	}
	if _, err := b.WriteString("\n}\n"); err != nil {
		return err
	}
	return b.Flush()
}

func gridOf(d *Drawing) float64 {
	if d.GridSize <= 0 {
		return 10
	}
	return d.GridSize
}

func sortedByID[T any](m map[ID]*T) []*T {
	out := make([]*T, 0, len(m))
	for _, id := range slices.Sorted(maps.Keys(m)) {
		out = append(out, m[id])
	}
	return out
}

// writeList writes a key of a file and its records, a line each.
func writeList[T any](w io.Writer, key string, list []*T) error {
	var line bytes.Buffer
	enc := json.NewEncoder(&line)
	enc.SetEscapeHTML(false)
	fmt.Fprintf(&line, ",\n%q:[", key)
	for i, v := range list {
		if i > 0 {
			line.WriteByte(',')
		}
		line.WriteByte('\n')
		if err := enc.Encode(v); err != nil {
			return err
		}
		line.Truncate(line.Len() - 1) // the encoder's newline
		if _, err := w.Write(line.Bytes()); err != nil {
			return err
		}
		line.Reset()
	}
	line.WriteByte(']')
	_, err := w.Write(line.Bytes())
	return err
}

// float16 converts between float64 and IEEE half precision, the deltas of
// the points of strokes in a file.
func float16From(b uint16) float64 {
	sign := 1.0
	if b&0x8000 != 0 {
		sign = -1
	}
	exp := int(b>>10) & 0x1f
	frac := float64(b & 0x3ff)
	switch exp {
	case 0:
		return sign * frac * math.Pow(2, -24)
	case 31:
		if frac == 0 {
			return sign * math.Inf(1)
		}
		return math.NaN()
	}
	return sign * (1 + frac/1024) * math.Pow(2, float64(exp-15))
}

func float16To(f float64) uint16 {
	var sign uint16
	if f < 0 || (f == 0 && math.Signbit(f)) {
		sign, f = 0x8000, -f
	}
	switch {
	case f == 0:
		return sign
	case math.IsInf(f, 0), f >= 65520:
		return sign | 0x7bff // the largest finite value, not to break the file
	case f < math.Pow(2, -14):
		return sign | uint16(math.Round(f/math.Pow(2, -24)))
	}
	exp := math.Floor(math.Log2(f))
	frac := math.Round((f/math.Pow(2, exp) - 1) * 1024)
	if frac >= 1024 {
		frac = 0
		exp++
	}
	if exp > 15 {
		return sign | 0x7bff
	}
	return sign | uint16(exp+15)<<10 | uint16(frac)
}

// segmentFile is a segment as a file holds it.
type segmentFile struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Dim  int    `json:"dim"`
}

// MarshalJSON writes the points encoded. Dim, when not said, leaves pressure out
// where every point has the same that a stroke without any reads back as.
func (s Segment) MarshalJSON() ([]byte, error) {
	dim := s.Dim
	if dim == 0 {
		dim = 2
		if slices.ContainsFunc(s.Points, func(p PointZ) bool { return p.Z != 0.5 }) {
			dim = 3
		}
	}
	return json.Marshal(segmentFile{s.Type, encodePoints(s.Points, dim), dim})
}

// UnmarshalJSON reads the points back.
func (s *Segment) UnmarshalJSON(b []byte) error {
	var f segmentFile
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	pts, err := decodePoints(f.Path, f.Dim)
	if err != nil {
		return err
	}
	*s = Segment{Type: f.Type, Points: pts, Dim: f.Dim}
	return nil
}

func decodePoints(s string, dim int) ([]PointZ, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	var out []PointZ
	le := binary.LittleEndian
	if dim == 2 {
		if len(b) < 8 {
			return nil, nil
		}
		x := float64(math.Float32frombits(le.Uint32(b)))
		y := float64(math.Float32frombits(le.Uint32(b[4:])))
		out = append(out, PointZ{x, y, 0.5})
		for o := 8; o+4 <= len(b); o += 4 {
			x += float16From(le.Uint16(b[o:]))
			y += float16From(le.Uint16(b[o+2:]))
			out = append(out, PointZ{x, y, 0.5})
		}
		return out, nil
	}
	if len(b) < 12 {
		return nil, nil
	}
	x := float64(math.Float32frombits(le.Uint32(b)))
	y := float64(math.Float32frombits(le.Uint32(b[4:])))
	z := float64(math.Float32frombits(le.Uint32(b[8:])))
	out = append(out, PointZ{x, y, z})
	for o := 12; o+6 <= len(b); o += 6 {
		x += float16From(le.Uint16(b[o:]))
		y += float16From(le.Uint16(b[o+2:]))
		z += float16From(le.Uint16(b[o+4:]))
		out = append(out, PointZ{x, y, z})
	}
	return out, nil
}

func encodePoints(pts []PointZ, dim int) string {
	if len(pts) == 0 {
		return ""
	}
	le := binary.LittleEndian
	var b []byte
	var buf [4]byte
	f32 := func(v float64) {
		le.PutUint32(buf[:], math.Float32bits(float32(v)))
		b = append(b, buf[:4]...)
	}
	f16 := func(v float64) float64 {
		bits := float16To(v)
		le.PutUint16(buf[:], bits)
		b = append(b, buf[:2]...)
		return float16From(bits)
	}
	f := pts[0]
	f32(f.X)
	f32(f.Y)
	if dim != 2 {
		f32(f.Z)
	}
	// Deltas are lossy: each goes from where the decoder will be, so that
	// the error does not add up along the stroke.
	x, y, z := float64(float32(f.X)), float64(float32(f.Y)), float64(float32(f.Z))
	for _, p := range pts[1:] {
		x += f16(p.X - x)
		y += f16(p.Y - y)
		if dim != 2 {
			z += f16(p.Z - z)
		}
	}
	return base64.StdEncoding.EncodeToString(b)
}
