package doc

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"slices"
	"strings"
	"testing"
)

func readFixture(t *testing.T) *Drawing {
	t.Helper()
	f, err := os.Open("testdata/tldraw.tldr")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d, err := Read(f)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// The fixture was written with tldraw's own encoders and passes its
// validators, so this reads what tldraw writes.
func TestReadsWhatTldrawWrites(t *testing.T) {
	d := readFixture(t)
	if d.Name != "fixture" || len(d.Pages) != 2 || d.Pages[0].Name != "First" {
		t.Errorf("document %q pages %+v", d.Name, d.Pages)
	}
	g := d.Shapes["shape:geo"]
	if g == nil || g.Geo != "cloud" || g.Dash != "dashed" || g.Fill != "pattern" || g.Size != "l" || g.Font != "mono" || g.Align != "start" || g.VAlign != "end" || g.W != 300 {
		t.Errorf("geo %+v", g)
	}
	if g.Text != "Bold label\nsecond line" {
		t.Errorf("rich text read as %q", g.Text)
	}
	want := []PointZ{{0, 0, .5}, {3.5, 1.25, .5}, {10, -4, .5}, {20.5, 7.75, .5}}
	for _, id := range []ID{"shape:draw", "shape:draw2d"} {
		s := d.Shapes[id]
		if s == nil || len(s.Segments) != 1 {
			t.Fatalf("%s %+v", id, s)
		}
		got := s.Segments[0].Points
		if len(got) != len(want) {
			t.Fatalf("%s has %d points", id, len(got))
		}
		for i := range want {
			if math.Abs(got[i].X-want[i].X) > 0.01 || math.Abs(got[i].Y-want[i].Y) > 0.01 || math.Abs(got[i].Z-want[i].Z) > 0.01 {
				t.Errorf("%s point %d = %+v, want %+v", id, i, got[i], want[i])
			}
		}
	}
	if s := d.Shapes["shape:draw"]; !s.IsPen {
		t.Error("pen lost")
	}
	if s := d.Shapes["shape:draw2d"]; s.ScaleX != 2 {
		t.Errorf("scaleX %v", s.ScaleX)
	}
	b := d.Bindings["binding:b1"]
	if b == nil || b.From != "shape:arrow" || b.To != "shape:geo" || b.Terminal != "end" || b.Anchor.X != 0.25 || !b.IsPrecise {
		t.Errorf("binding %+v", b)
	}
	l := d.Shapes["shape:line"]
	if l == nil || len(l.Points) != 3 || l.Points[1].X != 50 || l.Spline != "cubic" {
		t.Errorf("line %+v", l)
	}
}

// Writing keeps what this app doesn't model: formatted text it didn't edit,
// shapes of types it doesn't know, page meta.
func TestRoundTripKeepsWhatItDoesNotModel(t *testing.T) {
	d := readFixture(t)
	var b bytes.Buffer
	if err := Write(&b, d); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, `"marks":[{"type":"bold"}]`) {
		t.Error("bold mark lost from unedited text")
	}
	if !strings.Contains(out, `"url":"https://example.com"`) {
		t.Error("embed lost")
	}
	if !strings.Contains(out, `"hello":"kept"`) {
		t.Error("page meta lost")
	}
	d2, err := Read(bytes.NewReader(b.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(d2.Shapes) != len(d.Shapes) || len(d2.Bindings) != len(d.Bindings) || len(d2.Pages) != len(d.Pages) {
		t.Errorf("round trip: %d shapes %d bindings %d pages", len(d2.Shapes), len(d2.Bindings), len(d2.Pages))
	}
	if d2.Shapes["shape:geo"].Text != d.Shapes["shape:geo"].Text {
		t.Error("text changed")
	}

	// Edited, its text is new, and nothing claims the old formatting.
	s := d.Shapes["shape:geo"].Clone()
	s.SetText("plain now")
	d.Shapes[s.ID] = s
	b.Reset()
	if err := Write(&b, d); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "Bold label") {
		t.Error("old text written after an edit")
	}
	if !json.Valid(b.Bytes()) {
		t.Error("invalid JSON")
	}
}

func TestPointsSurviveLossyDeltas(t *testing.T) {
	// A long stroke far from the origin, with deltas half floats cannot
	// hold exactly: the error must not add up along it.
	var pts []PointZ
	for i := range 5000 {
		f := float64(i)
		pts = append(pts, PointZ{X: 5000 + f*0.137 + math.Sin(f/7)*3, Y: -3000 + f*0.071 + math.Cos(f/5)*2, Z: .5})
	}
	for _, dim := range []int{2, 3} {
		got, err := decodePoints(encodePoints(pts, dim), dim)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(pts) {
			t.Fatalf("dim %d: %d points", dim, len(got))
		}
		for i := range pts {
			if d := math.Hypot(got[i].X-pts[i].X, got[i].Y-pts[i].Y); d > 0.01 {
				t.Fatalf("dim %d: point %d is %v off", dim, i, d)
			}
		}
	}
}

func TestFloat16(t *testing.T) {
	for _, v := range []float64{0, 1, -1, 0.5, 1.25, -4, 1024, 0.0001, 65000} {
		got := float16From(float16To(v))
		if math.Abs(got-v) > math.Max(math.Abs(v)/1000, 1e-5) {
			t.Errorf("%v came back %v", v, got)
		}
	}
}

func TestIndexOrder(t *testing.T) {
	// Each index sorts between its bounds, whatever they are.
	check := func(a, b string) string {
		t.Helper()
		k := IndexBetween(a, b)
		if a != "" && k <= a {
			t.Errorf("IndexBetween(%q, %q) = %q, not after %q", a, b, k, a)
		}
		if b != "" && k >= b {
			t.Errorf("IndexBetween(%q, %q) = %q, not before %q", a, b, k, b)
		}
		return k
	}
	if k := check("", ""); k != FirstIndex {
		t.Errorf("first index %q", k)
	}
	k := FirstIndex
	var seq []string
	for range 200 {
		k = check(k, "")
		seq = append(seq, k)
	}
	if !slices.IsSorted(seq) {
		t.Error("appending is out of order")
	}
	k = FirstIndex
	for range 200 {
		k = check("", k)
	}
	lo, hi := "a1", "a2"
	for i := range 80 {
		m := check(lo, hi)
		if i%2 == 0 {
			lo = m
		} else {
			hi = m
		}
	}
	check("a1", "a1V")
	check("Zz", "a0")
}

func TestReadRejectsOtherFiles(t *testing.T) {
	for _, s := range []string{"not json", `{"foo":1}`, `{"tldrawFileFormatVersion":99,"records":[]}`} {
		if _, err := Read(strings.NewReader(s)); err == nil {
			t.Errorf("read %q without an error", s)
		}
	}
	d, err := Read(strings.NewReader(""))
	if err != nil || len(d.Pages) != 1 {
		t.Errorf("empty file: %v %+v", err, d)
	}
}

func TestSnapshotsAreIndependent(t *testing.T) {
	d := New()
	s := NewShape("shape:a", TypeGeo)
	s.Parent = "page:page"
	d.Shapes[s.ID] = s
	snap := d.Snapshot()
	if !Equal(d, snap) {
		t.Error("a snapshot differs from its drawing")
	}
	c := s.Clone()
	c.X = 5
	d.Shapes[s.ID] = c
	if Equal(d, snap) {
		t.Error("an edit doesn't show")
	}
	if snap.Shapes[s.ID].X != 0 {
		t.Error("an edit reached a snapshot")
	}
	d.Shapes[s.ID] = s
	if !Equal(d, snap) {
		t.Error("undoing to the same record should compare equal")
	}
}
