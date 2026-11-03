package doc

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"godraw/internal/geo"
)

func readFixture(t *testing.T) *Drawing {
	t.Helper()
	f, err := os.Open("testdata/fixture.godraw")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	d, err := Read(f)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// The fixture has a shape of each form a file holds.
func TestReadsFixture(t *testing.T) {
	d := readFixture(t)
	if d.Name != "fixture" || len(d.Pages) != 2 || d.Pages[0].Name != "First" {
		t.Errorf("document %q pages %+v", d.Name, d.Pages)
	}
	g := d.Shapes["shape:geo"]
	if g == nil || g.Geo != "cloud" || g.Dash != "dashed" || g.Fill != "pattern" || g.Size != "l" || g.Font != "mono" || g.Align != "start" || g.VAlign != "end" || g.W != 300 {
		t.Errorf("geo %+v", g)
	}
	if g.Text != "A label\nsecond line" {
		t.Errorf("text read as %q", g.Text)
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

// A file holds all a shape has, whatever it is, and what is zero stays zero.
func TestRoundTripKeepsEverything(t *testing.T) {
	d := readFixture(t)
	padding, at := 0.0, 0.25
	all := NewShape("shape:all", TypeGeo)
	all.Parent, all.Opacity, all.Color, all.LabelColor, all.Fill = "page:page", 0, "#ff8800", "#123456", "#10163a"
	all.Dash, all.Padding, all.Radius = "none", &padding, 12
	all.Gradient = &Gradient{Colors: []string{"blue", "#ffffff"}, Angle: 90}
	all.Shadow = &Shadow{Y: 4, Blur: 12, Color: "#000000", Opacity: 0.35}
	all.SetRuns([]Run{{Text: "E = mc"}, {Text: "2", Sup: true, Scale: 0.7}})
	all.Card, all.CardH = "box", 80
	d.Shapes[all.ID] = all
	elbow := NewShape("shape:elbow", TypeArrow)
	elbow.Parent, elbow.Kind, elbow.LabelSide, elbow.Straight, elbow.StraightAt = "page:page", "elbow", "above", "h", &at
	elbow.Via, elbow.ViaAuto = []geo.Vec{geo.V(1, 2), geo.V(0, -3)}, true
	d.Shapes[elbow.ID] = elbow
	frame := NewShape("shape:frame", TypeFrame)
	frame.Parent, frame.FrameSize, frame.FrameFont = "page:page", "l", "serif"
	d.Shapes[frame.ID] = frame

	var b bytes.Buffer
	if err := Write(&b, d); err != nil {
		t.Fatal(err)
	}
	back, err := Read(bytes.NewReader(b.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if back.Name != d.Name || back.GridSize != d.GridSize || !reflect.DeepEqual(back.Pages, d.Pages) || !reflect.DeepEqual(back.Bindings, d.Bindings) {
		t.Errorf("came back %+v", back)
	}
	if len(back.Shapes) != len(d.Shapes) {
		t.Errorf("%d shapes, had %d", len(back.Shapes), len(d.Shapes))
	}
	for id, s := range d.Shapes {
		got := back.Shapes[id]
		if got == nil {
			t.Errorf("%s lost", id)
			continue
		}
		// What a draw shape's points hold is checked on its own.
		c, g := *s, *got
		c.Segments, g.Segments = nil, nil
		if !reflect.DeepEqual(&c, &g) {
			t.Errorf("%s came back\n%+v\nnot\n%+v", id, g, c)
		}
	}
	var again bytes.Buffer
	if err := Write(&again, back); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b.Bytes(), again.Bytes()) {
		t.Error("written again, the file differs")
	}
	if !json.Valid(b.Bytes()) {
		t.Error("invalid JSON")
	}
	// A line a record, for a diff to show what changed.
	if n := strings.Count(b.String(), "\n"); n < len(d.Shapes)+len(d.Pages)+len(d.Bindings) {
		t.Errorf("%d lines", n)
	}
}

// A stroke keeps its pressure if it has any, and doesn't spend room on it if not.
func TestSegmentsSayWhetherTheyHavePressure(t *testing.T) {
	for _, c := range []struct {
		z   float64
		dim int
	}{{0.5, 2}, {0.8, 3}} {
		b, err := json.Marshal(Segment{Type: "free", Points: []PointZ{{1, 2, c.z}, {3, 4, c.z}}})
		if err != nil {
			t.Fatal(err)
		}
		var s Segment
		if err := json.Unmarshal(b, &s); err != nil {
			t.Fatal(err)
		}
		if s.Dim != c.dim || len(s.Points) != 2 || math.Abs(s.Points[1].Z-c.z) > 0.01 || s.Points[1].X != 3 {
			t.Errorf("pressure %v came back %+v", c.z, s)
		}
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
	for _, s := range []string{
		"not json",
		`{"foo":1}`,
		`{"godrawFileFormatVersion":1,"records":[]}`,
		`{"godrawFileFormatVersion":99,"pages":[{"id":"page:a"}]}`,
		`{"godrawFileFormatVersion":1,"pages":[{"id":"page:a"}],"shapes":[{"id":"shape:a","type":"embed"}]}`,
		`{"godrawFileFormatVersion":1,"pages":[{"id":"page:a"}],"shapes":[null]}`,
	} {
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

// A file holds only the images something shows.
func TestWriteDropsUnusedImages(t *testing.T) {
	d := New()
	for _, a := range []*Asset{
		{ID: "asset:used", Src: "data:,"},
		{ID: "asset:gone", Src: "data:,"},
	} {
		d.Assets[a.ID] = a
	}
	s := NewShape("shape:i", TypeImage)
	s.Parent, s.Index, s.AssetID = "page:page", FirstIndex, "asset:used"
	d.Shapes[s.ID] = s
	var buf bytes.Buffer
	if err := Write(&buf, d); err != nil {
		t.Fatal(err)
	}
	if d.Assets["asset:gone"] == nil {
		t.Error("writing changed the drawing")
	}
	back, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[ID]bool{"asset:used": true, "asset:gone": false} {
		if (back.Assets[id] != nil) != want {
			t.Errorf("%s in the file: %v", id, back.Assets[id] != nil)
		}
	}
}
