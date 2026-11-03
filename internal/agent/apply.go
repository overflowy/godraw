package agent

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"maps"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"godraw/internal/doc"
	"godraw/internal/editor"
	"godraw/internal/geo"
)

// Limits that keep one call from holding the window up.
const (
	maxOps      = 5000
	maxCopies   = 500
	maxMade     = 10000 // shapes one dup makes, with what they hold
	maxSubpaths = 500
	maxPoints   = 20000
	maxImage    = 20 << 20
	// How far a number can reach. It's far more than a drawing needs, and it keeps
	// what's worked out from numbers finite, which a file has to be.
	maxNumber = 1e7
	// How far numbers can take a shape, moved and copied.
	maxReached = 1e12
	sampleStep = 4.0
	defaultGap = 120.0 // room for an arrow with a short label
	copyGap    = 20.0
)

// names is one id or several, as an agent finds it shortest to say.
type names []string

func (n *names) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*n = names{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return errors.New("expected an id or a list of ids")
	}
	*n = many
	return nil
}

// fields is what an op can say of a shape. Each is a pointer, so that saying
// nothing differs from saying zero.
type fields struct {
	ID *string `json:"id"`
	In *string `json:"in"`

	X   *float64 `json:"x"`
	Y   *float64 `json:"y"`
	W   *float64 `json:"w"`
	H   *float64 `json:"h"`
	DX  *float64 `json:"dx"`
	DY  *float64 `json:"dy"`
	Rot *float64 `json:"rot"`
	Gap *float64 `json:"gap"`

	Right *string `json:"right"`
	Left  *string `json:"left"`
	Above *string `json:"above"`
	Below *string `json:"below"`

	Behind *string `json:"behind"`
	Over   *string `json:"over"`

	Text  *textValue `json:"text"`
	Title *textValue `json:"title"`
	Body  *textValue `json:"body"`
	Name  *string    `json:"name"`
	URL   *string    `json:"url"`
	Alt   *string    `json:"alt"`
	Src   *string    `json:"src"`
	D     *string    `json:"d"`
	Fn    *string    `json:"fn"`

	Color         *string     `json:"color"`
	LabelColor    *string     `json:"labelColor"`
	Fill          *string     `json:"fill"`
	Gradient      *[]string   `json:"gradient"`
	GradientAngle *float64    `json:"gradientAngle"`
	Dash          *string     `json:"dash"`
	Size          *string     `json:"size"`
	Font          *string     `json:"font"`
	BodyFont      *string     `json:"bodyFont"`
	Align         *string     `json:"align"`
	VAlign        *string     `json:"valign"`
	Padding       *float64    `json:"padding"`
	Radius        *float64    `json:"radius"`
	Shadow        *doc.Shadow `json:"shadow"`
	Use           *string     `json:"use"`
	Spline        *string     `json:"spline"`
	Opacity       *float64    `json:"opacity"`
	Scale         *float64    `json:"scale"`
	Locked        *bool       `json:"locked"`

	From   *string      `json:"from"`
	To     *string      `json:"to"`
	FromAt *[]float64   `json:"fromAt"`
	ToAt   *[]float64   `json:"toAt"`
	X1     *float64     `json:"x1"`
	Y1     *float64     `json:"y1"`
	X2     *float64     `json:"x2"`
	Y2     *float64     `json:"y2"`
	Kind   *string      `json:"kind"`
	Bend   *float64     `json:"bend"`
	Head   *string      `json:"head"`
	Tail   *string      `json:"tail"`
	Via    *[][]float64 `json:"via"`

	Straight *string  `json:"straight"`
	At       *float64 `json:"at"`
	Label    *string  `json:"label"`

	How  *string `json:"how"`
	N    *int    `json:"n"`
	Rows *int    `json:"rows"`
	Cols *int    `json:"cols"`
}

// op is one change: exactly one of its verbs, and fields.
type op struct {
	Add     *string  `json:"add"`
	Set     *targets `json:"set"`
	Del     *targets `json:"del"`
	Dup     *targets `json:"dup"`
	Group   *targets `json:"group"`
	Ungroup *targets `json:"ungroup"`
	Arrange *targets `json:"arrange"`
	Order   *targets `json:"order"`
	Flip    *targets `json:"flip"`
	Camera  *targets `json:"camera"`
	Page    *string  `json:"page"`
	DelPage *string  `json:"delPage"`
	Style   *fields  `json:"style"`
	With    with     `json:"with"`
	fields

	verb    string
	path    *geo.Path // of D
	picture *picture  // of Src
}

// picture is an image an op adds, read before the batch runs.
type picture struct {
	name, mime string
	data       []byte
	w, h       float64
}

var (
	// What each kind of shape takes, besides where it goes (placeFields).
	addFields = map[string]string{
		doc.TypeGeo:       "w h text color labelColor fill gradient gradientAngle dash size font align valign padding radius shadow url",
		doc.TypeText:      "w text color size font align scale",
		doc.TypeNote:      "text color labelColor size font align valign padding scale shadow url",
		doc.TypeFrame:     "w h name color size font",
		doc.TypeImage:     "src w h alt shadow url",
		doc.TypeDraw:      "d fn x1 x2 w h color fill gradient gradientAngle dash size shadow",
		doc.TypeHighlight: "d w h color size",
		doc.TypeLine:      "d w h color dash size spline shadow",
		cardKind:          "title body w h color labelColor fill gradient gradientAngle dash size font bodyFont align radius shadow",
		doc.TypeArrow:     "from to fromAt toAt x1 y1 x2 y2 text color labelColor dash size font kind bend via head tail straight at label",
	}
	// Of geo shapes, only a rectangle has corners to round.
	cornerlessGeo = strings.Replace(addFields[doc.TypeGeo], " radius", "", 1)
	placeFields   = "id in x y rot opacity locked right left above below gap behind over use"
	verbFields    = map[string]string{
		"del": "", "ungroup": "", "camera": "", "delPage": "",
		"group": "id", "arrange": "how rows cols gap", "order": "how behind over", "flip": "how",
		"dup": "dx dy n rows cols gap id", "page": "name",
	}

	styleEnums = map[string][]string{
		"color":      editor.Colors,
		"labelColor": editor.Colors,
		"fill":       {"none", "semi", "solid", "pattern", "fill", "lined-fill"},
		"dash":       {"draw", "dashed", "dotted", "solid", "none"},
		"size":       {"s", "m", "l", "xl"},
		"font":       {"draw", "sans", "serif", "mono"},
		"bodyFont":   {"draw", "sans", "serif", "mono"},
		"align":      {"start", "middle", "end"},
		"valign":     {"start", "middle", "end"},
		"spline":     {"line", "cubic"},
		"kind":       {"arc", "elbow"},
		"head":       editor.ArrowHeads,
		"tail":       editor.ArrowHeads,
		"straight":   {"h", "v", "off"},
		"label":      {"center", "above"},
	}
	verbHows = map[string][]string{
		"arrange": {"left", "center-h", "right", "top", "center-v", "bottom", "distribute-h", "distribute-v", "grid"},
		"order":   {"front", "forward", "backward", "back"},
		"flip":    {"h", "v"},
	}
)

// takenBy returns what a kind of shape takes, besides where it goes.
func takenBy(kind, geoKind string) string {
	if kind == doc.TypeGeo && geoKind != "rectangle" {
		return cornerlessGeo
	}
	return addFields[kind]
}

// said returns the names of the fields f sets, in its own order.
func said(f *fields) []string {
	var out []string
	v := reflect.ValueOf(f).Elem()
	for i := range v.NumField() {
		if !v.Field(i).IsNil() {
			out = append(out, strings.Split(v.Type().Field(i).Tag.Get("json"), ",")[0])
		}
	}
	return out
}

func allows(list, name string) bool { return slices.Contains(strings.Fields(list), name) }

// decodeOps reads ops strictly, and does what can be done off the main thread:
// checking them against one another, parsing paths and reading images. It tells of
// all that's wrong at once, in a *batchError, and leaves nil in place of those ops:
// what's left can still run, to find what's wrong with the rest.
func decodeOps(raw json.RawMessage) ([]*op, error) {
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, errors.New(`ops must be a list of objects, as [{"add":"rectangle"}]`)
	}
	if len(list) == 0 {
		return nil, errors.New("ops is empty")
	}
	if len(list) > maxOps {
		return nil, fmt.Errorf("%d ops is too many at once; the limit is %d", len(list), maxOps)
	}
	ops := make([]*op, len(list))
	var bad batchError
	for i, item := range list {
		o, err := decodeOp(item)
		if err != nil {
			bad.add(i, err)
			bad.names = append(bad.names, namesIn(item)...)
			bad.styles = append(bad.styles, styleNameIn(item)...)
			continue
		}
		ops[i] = o
	}
	if len(bad.list) > 0 {
		return ops, &bad
	}
	return ops, nil
}

// maxErrors is how many errors an answer lists, of a batch with many.
const maxErrors = 20

// batchError is all that's wrong with a batch: nothing of it is applied.
type batchError struct {
	list []opFailure
	// What failed ops would have made, for what uses those not to be blamed as well.
	names, styles []string
}

type opFailure struct {
	at  int
	err error
}

func (b *batchError) add(at int, err error) { b.list = append(b.list, opFailure{at, err}) }

func (b *batchError) Error() string {
	slices.SortStableFunc(b.list, func(x, y opFailure) int { return x.at - y.at })
	if len(b.list) == 1 {
		return fmt.Sprintf("ops[%d]: %v; nothing was changed", b.list[0].at, b.list[0].err)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%d ops failed; nothing was changed:", len(b.list))
	for i, f := range b.list {
		if i == maxErrors {
			fmt.Fprintf(&out, "\nand %d more", len(b.list)-maxErrors)
			break
		}
		fmt.Fprintf(&out, "\nops[%d]: %v", f.at, f.err)
	}
	return out.String()
}

// namesIn returns the ids an op that can't be read meant to make.
func namesIn(raw json.RawMessage) []string {
	var o struct {
		ID   json.RawMessage `json:"id"`
		With json.RawMessage `json:"with"`
	}
	if json.Unmarshal(raw, &o) != nil {
		return nil
	}
	var out []string
	one := func(b json.RawMessage) {
		var id string
		if json.Unmarshal(b, &id) == nil && id != "" {
			out = append(out, id)
		}
	}
	one(o.ID)
	var items []struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(o.With, &items) == nil {
		for _, item := range items {
			one(item.ID)
		}
	}
	return out
}

// styleNameIn returns the style an op that can't be read meant to name.
func styleNameIn(raw json.RawMessage) []string {
	var o struct {
		Style json.RawMessage `json:"style"`
		Name  string          `json:"name"`
	}
	if json.Unmarshal(raw, &o) != nil || o.Style == nil || o.Name == "" {
		return nil
	}
	return []string{o.Name}
}

func decodeOp(raw json.RawMessage) (*op, error) {
	var keys map[string]json.RawMessage
	if json.Unmarshal(raw, &keys) == nil {
		for _, name := range []string{"commit", "dry", "quiet", "doc"} {
			if _, ok := keys[name]; ok {
				return nil, fmt.Errorf("%s is an argument of apply, beside ops, not an op", name)
			}
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	o := &op{}
	if err := d.Decode(o); err != nil {
		if wrong, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
			return nil, wrongType(wrong)
		}
		return nil, errors.New(strings.TrimPrefix(err.Error(), "json: "))
	}
	var verbs []string
	verbNames := map[string]bool{
		"add": o.Add != nil, "set": o.Set != nil, "del": o.Del != nil, "dup": o.Dup != nil,
		"group": o.Group != nil, "ungroup": o.Ungroup != nil, "arrange": o.Arrange != nil,
		"order": o.Order != nil, "flip": o.Flip != nil, "camera": o.Camera != nil,
		"page": o.Page != nil, "delPage": o.DelPage != nil, "style": o.Style != nil,
	}
	for name, set := range verbNames {
		if set {
			verbs = append(verbs, name)
		}
	}
	if len(verbs) != 1 {
		slices.Sort(verbs)
		return nil, fmt.Errorf("needs exactly one verb (add set del dup group ungroup arrange order flip camera page delPage style), got [%s]", strings.Join(verbs, " "))
	}
	o.verb = verbs[0]
	if o.With != nil && o.verb != "dup" {
		return nil, errors.New(`with goes with dup`)
	}
	if o.Style != nil {
		if err := checkValues(o.Style); err != nil {
			return nil, err
		}
		for _, name := range said(&o.fields) {
			if name != "name" {
				return nil, fmt.Errorf("style takes its fields inside it, not %q beside it", name)
			}
		}
		if o.Name != nil && (*o.Name == "" || strings.ContainsAny(*o.Name, " \t\n")) {
			return nil, fmt.Errorf("bad style name %q", *o.Name)
		}
		for _, name := range said(o.Style) {
			if !isStyleField(name) {
				return nil, fmt.Errorf("style can't set %q; it takes color labelColor fill gradient gradientAngle dash size font bodyFont align valign padding radius shadow scale spline opacity use", name)
			}
		}
		return o, nil
	}
	if err := checkValues(&o.fields); err != nil {
		return nil, err
	}
	switch o.verb {
	case "add":
		kind, geoKind, err := shapeKind(*o.Add)
		if err != nil {
			return nil, err
		}
		list := takenBy(kind, geoKind)
		for _, name := range said(&o.fields) {
			if !allows(list, name) && !allows(placeFields, name) {
				return nil, fmt.Errorf("%s has no %q; it takes %s %s", *o.Add, name, list, placeFields)
			}
		}
		if o.Dash != nil && *o.Dash == "none" && !outlineOptional(kind) {
			return nil, fmt.Errorf(`a %s is its line, so it can't have dash "none"`, kind)
		}
		if kind == doc.TypeArrow {
			for _, name := range []string{"x", "y", "rot", "right", "left", "above", "below", "gap"} {
				if slices.Contains(said(&o.fields), name) {
					return nil, fmt.Errorf(`an arrow is placed by its ends: use from/to or x1,y1,x2,y2, not %q`, name)
				}
			}
			if o.Straight != nil && *o.Straight != "off" && (o.From == nil || o.To == nil) {
				return nil, errors.New(`straight needs from and to; for free ends, give y1 and y2 the same value`)
			}
			if o.At != nil && (o.Straight == nil || *o.Straight == "off") {
				return nil, errors.New(`at goes with straight:"h" or "v"`)
			}
		}
		if o.Gap != nil && !o.saysBeside() {
			return nil, errors.New(gapHelp)
		}
		if kind == doc.TypeDraw && (o.X1 != nil || o.X2 != nil) && o.Fn == nil {
			return nil, errors.New("x1 and x2 are where a draw's fn runs, and it has none")
		}
		if kind == doc.TypeImage {
			if o.Src == nil {
				return nil, errors.New(`image needs src: a file path or a data: URL`)
			}
			if o.picture, err = readPicture(*o.Src); err != nil {
				return nil, err
			}
		}
	case "set":
		// Each target says what it takes, as the batch runs.
		if o.Src != nil {
			return nil, errors.New("an image's src can't change; add a new image")
		}
	default:
		list, known := verbFields[o.verb]
		if !known {
			return nil, fmt.Errorf("unknown verb %q", o.verb)
		}
		for _, name := range said(&o.fields) {
			if !allows(list, name) {
				return nil, fmt.Errorf("%s has no %q", o.verb, name)
			}
		}
		beside := o.Behind != nil || o.Over != nil
		if beside && o.How != nil {
			return nil, errors.New("order takes how, or behind or over, not both")
		}
		if hows := verbHows[o.verb]; hows != nil && !beside {
			if o.How == nil || !slices.Contains(hows, *o.How) {
				if o.verb == "order" {
					return nil, fmt.Errorf(`order needs how: %s; or behind or over, naming a shape`, strings.Join(hows, ", "))
				}
				return nil, fmt.Errorf("%s needs how: %s", o.verb, strings.Join(hows, ", "))
			}
		}
		if o.verb == "arrange" && *o.How != "grid" && (o.Rows != nil || o.Cols != nil || o.Gap != nil) {
			return nil, errors.New(`rows, cols and gap go with how:"grid"`)
		}
	}
	if err := o.readPath(); err != nil {
		return nil, err
	}
	if o.verb == "add" && *o.Add == doc.TypeDraw && o.path != nil && saysFill(&o.fields) && !o.closes() {
		return nil, errors.New(openFillHelp)
	}
	return o, nil
}

// gapHelp says what gap is, of what sits beside something.
const gapHelp = "gap is how far from what right, left, above or below names, and this names none"

// saysBeside says whether fields place a shape beside another.
func (f *fields) saysBeside() bool {
	return f.Right != nil || f.Left != nil || f.Above != nil || f.Below != nil
}

// openFillHelp says why a fill doesn't take on a draw.
const openFillHelp = "a draw only fills if its path closes; end d with Z"

// saysFill says whether fields ask for something to fill with.
func saysFill(f *fields) bool {
	return (f.Fill != nil && *f.Fill != "none") || (f.Gradient != nil && len(*f.Gradient) > 0)
}

// closes says whether any of an op's path closes, and so can hold a fill.
func (o *op) closes() bool {
	_, closed := o.path.Flatten(1)
	return slices.Contains(closed, true)
}

// readPath works out the path an op's d or fn says.
func (o *op) readPath() error {
	f := &o.fields
	if f.D != nil && f.Fn != nil {
		return errors.New("use one of d, fn")
	}
	if f.Fn != nil {
		if f.X1 == nil || f.X2 == nil {
			return errors.New(`fn needs x1 and x2, where it runs, as {"fn":"300+40*sin(x/9)","x1":0,"x2":400}`)
		}
		p, err := plot(*f.Fn, *f.X1, *f.X2)
		if err != nil {
			return err
		}
		o.path = p
		return nil
	}
	if f.D == nil {
		return nil
	}
	p, err := geo.ParseSVGPath(*f.D)
	if err != nil {
		return err
	}
	for _, seg := range p.Segs {
		for _, v := range seg.P {
			if !inReach(v.X) || !inReach(v.Y) {
				return fmt.Errorf("d must be numbers from %s to %s", num(-maxNumber), num(maxNumber))
			}
		}
	}
	o.path = p
	return nil
}

func isStyleField(name string) bool {
	_, enum := styleEnums[name]
	return (enum && !slices.Contains([]string{"head", "tail", "kind", "straight", "label"}, name)) ||
		slices.Contains([]string{"opacity", "padding", "gradient", "gradientAngle", "radius", "shadow", "scale", "use"}, name)
}

// outlineOptional says whether a kind of shape shows without its line.
func outlineOptional(kind string) bool {
	return kind != doc.TypeArrow && kind != doc.TypeLine
}

// isColor says whether a string names a color: the palette's, or one of its own.
func isColor(s string) bool {
	return slices.Contains(editor.Colors, s) || editor.IsHex(s)
}

// checkValues checks each value against what its field can hold.
func checkValues(f *fields) error {
	v := reflect.ValueOf(f).Elem()
	for i := range v.NumField() {
		field := v.Field(i)
		if field.IsNil() {
			continue
		}
		name := v.Type().Field(i).Tag.Get("json")
		if s, ok := field.Interface().(*string); ok {
			enum := styleEnums[name]
			if name == "color" || name == "labelColor" {
				if !isColor(*s) {
					return fmt.Errorf("%s %q: use %s, or #rrggbb", name, *s, strings.Join(enum, ", "))
				}
			} else if name == "fill" {
				if !slices.Contains(enum, *s) && !editor.IsHex(*s) {
					return fmt.Errorf("fill %q: use %s, or #rrggbb", *s, strings.Join(enum, ", "))
				}
			} else if enum != nil && !slices.Contains(enum, *s) {
				return fmt.Errorf("%s %q: use %s", name, *s, strings.Join(enum, ", "))
			}
		}
		if n, ok := field.Interface().(*float64); ok && !inReach(*n) {
			return fmt.Errorf("%s must be a number from %s to %s", name, num(-maxNumber), num(maxNumber))
		}
	}
	if f.Padding != nil && *f.Padding < 0 {
		return errors.New("padding can't be below 0")
	}
	if f.Radius != nil && *f.Radius < 0 {
		return errors.New("radius can't be below 0")
	}
	if err := checkShadow(f.Shadow); err != nil {
		return err
	}
	if f.Gradient != nil {
		g := *f.Gradient
		if len(g) == 1 || len(g) > maxGradient {
			return fmt.Errorf("%s, from 2 to %d of them, or [] to clear it", gradientHelp, maxGradient)
		}
		for _, c := range g {
			if !isColor(c) {
				return fmt.Errorf("gradient color %q: use %s, or #rrggbb", c, strings.Join(editor.Colors, ", "))
			}
		}
		if len(g) > 0 && f.Fill != nil && *f.Fill == "none" {
			return errors.New(`fill "none" would hide its gradient`)
		}
	}
	if f.Behind != nil && f.Over != nil {
		return errors.New("use one of behind, over")
	}
	if f.Opacity != nil && (*f.Opacity < 0 || *f.Opacity > 1) {
		return errors.New("opacity must be between 0 and 1")
	}
	if f.At != nil && (*f.At < 0 || *f.At > 1) {
		return errors.New("at must be between 0 and 1, across the height or width both ends share")
	}
	if f.Straight != nil && *f.Straight != "off" {
		if f.FromAt != nil || f.ToAt != nil {
			return errors.New("straight works out the anchors itself, so it can't take fromAt or toAt; use at to place it")
		}
		if f.Bend != nil && *f.Bend != 0 {
			return errors.New("a straight arrow can't bend")
		}
		if f.Via != nil && len(*f.Via) > 0 {
			return errors.New(`straight runs level, so it can't take via`)
		}
	}
	for _, n := range []*int{f.Rows, f.Cols} {
		if n != nil && (*n < 1 || *n > maxMade) {
			return fmt.Errorf("rows and cols must be from 1 to %d", maxMade)
		}
	}
	if f.Via != nil {
		if len(*f.Via) > maxVia {
			return fmt.Errorf("via takes up to %d points", maxVia)
		}
		for _, p := range *f.Via {
			if len(p) != 2 {
				return errors.New(viaHelp)
			}
			if !inReach(p[0]) || !inReach(p[1]) {
				return fmt.Errorf("via must be numbers from %s to %s", num(-maxNumber), num(maxNumber))
			}
		}
	}
	if f.Scale != nil && *f.Scale <= 0 {
		return errors.New("scale must be above 0")
	}
	for _, size := range []*float64{f.W, f.H} {
		if size != nil && *size <= 0 {
			return errors.New("w and h must be above 0")
		}
	}
	for _, at := range []*[]float64{f.FromAt, f.ToAt} {
		if at != nil && (len(*at) != 2 || (*at)[0] < 0 || (*at)[0] > 1 || (*at)[1] < 0 || (*at)[1] > 1) {
			return errors.New(anchorHelp)
		}
	}
	return nil
}

// checkShadow checks what a shadow says.
func checkShadow(sh *doc.Shadow) error {
	if sh == nil {
		return nil
	}
	if sh.Color != "" && !isColor(sh.Color) {
		return fmt.Errorf("shadow color %q: use %s, or #rrggbb", sh.Color, strings.Join(editor.Colors, ", "))
	}
	if sh.Opacity < 0 || sh.Opacity > 1 {
		return errors.New("shadow opacity must be between 0 and 1")
	}
	if sh.Blur < 0 {
		return errors.New("shadow blur can't be below 0")
	}
	if sh.Blur > maxBlur {
		return fmt.Errorf("shadow blur goes up to %d", maxBlur)
	}
	if !inReach(sh.X) || !inReach(sh.Y) {
		return fmt.Errorf("shadow x and y must be numbers from %s to %s", num(-maxNumber), num(maxNumber))
	}
	return nil
}

// maxBlur is more than a shadow should need, and keeps its layers on a page's scale.
const maxBlur = 400

// inReach says whether a number is one to take: finite, and within maxNumber.
func inReach(n float64) bool { return math.Abs(n) <= maxNumber }

// maxVia is more points than a route should need, and keeps a mistake from
// costing every frame.
const maxVia = 32

// maxGradient is more colors than a fill should need.
const maxGradient = 8

const (
	gradientHelp = "gradient is a list of colors"
	shadowHelp   = `shadow is {"x":0,"y":4,"blur":12,"color":"#000000","opacity":0.35}, each optional`
	viaHelp      = "via is a list of [x,y] points in the page"
	anchorHelp   = "fromAt and toAt are [x,y], each 0 to 1 across the shape"
)

// wrongType says what a field should have held, in place of what the decoder
// says, which is in terms of this program. Style's fields are the same fields.
func wrongType(e *json.UnmarshalTypeError) error {
	if e.Field == "" {
		return errors.New(`an op is an object, as {"add":"rectangle"}`)
	}
	name := strings.TrimPrefix(strings.TrimPrefix(e.Field, "style."), "fields.")
	name, _, _ = strings.Cut(name, ".")
	switch name {
	case "via":
		return errors.New(viaHelp)
	case "gradient":
		return errors.New(gradientHelp)
	case "shadow":
		return errors.New(shadowHelp)
	case "fromAt", "toAt":
		return errors.New(anchorHelp)
	}
	for _, t := range []reflect.Type{reflect.TypeFor[fields](), reflect.TypeFor[op]()} {
		for field := range t.Fields() {
			if field.Tag.Get("json") != name {
				continue
			}
			switch field.Type.Elem().Kind() {
			case reflect.Float64:
				return fmt.Errorf("%s must be a number", name)
			case reflect.Int:
				return fmt.Errorf("%s must be a whole number", name)
			case reflect.Bool:
				return fmt.Errorf("%s must be true or false", name)
			case reflect.String:
				return fmt.Errorf("%s must be a string", name)
			}
		}
	}
	return fmt.Errorf("%s has the wrong type", name)
}

// shapeKind returns the record type and geo kind of what add names.
func shapeKind(name string) (kind, geoKind string, err error) {
	if slices.Contains(editor.GeoKinds, name) {
		return doc.TypeGeo, name, nil
	}
	if _, ok := addFields[name]; ok && name != doc.TypeGeo {
		return name, "", nil
	}
	return "", "", fmt.Errorf("can't add %q; use card text note frame image draw highlight line arrow, or a geo shape: %s", name, strings.Join(editor.GeoKinds, " "))
}

func readPicture(src string) (*picture, error) {
	var data []byte
	name := "image"
	if rest, ok := strings.CutPrefix(src, "data:"); ok {
		_, b64, found := strings.Cut(rest, ";base64,")
		if !found {
			return nil, errors.New("src: only base64 data: URLs are supported")
		}
		var err error
		if data, err = base64.StdEncoding.DecodeString(b64); err != nil {
			return nil, fmt.Errorf("src: %w", err)
		}
	} else {
		path := src
		if rest, ok := strings.CutPrefix(path, "~/"); ok {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, err
			}
			path = filepath.Join(home, rest)
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("src: can't read %s", src)
		}
		if info.Size() > maxImage {
			return nil, fmt.Errorf("src: %s is over %d MB", src, maxImage>>20)
		}
		if data, err = os.ReadFile(path); err != nil {
			return nil, fmt.Errorf("src: can't read %s", src)
		}
		name = filepath.Base(path)
	}
	if len(data) > maxImage {
		return nil, fmt.Errorf("src: image is over %d MB", maxImage>>20)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("src: not a PNG, JPEG or GIF image")
	}
	return &picture{name: name, mime: http.DetectContentType(data), data: data, w: float64(cfg.Width), h: float64(cfg.Height)}, nil
}

// run is one batch on an editor.
type run struct {
	e       *editor.Editor
	style   fields
	added   []doc.ID
	changed map[doc.ID]bool
	deleted int
	// deletedInside is how many of those were held by what was asked to go.
	deletedInside int
	nextID        int
	camera        bool
	// before is how tall a card was, which may grow as it fits new text.
	before map[doc.ID]float64
	grown  map[doc.ID]*growth
	dry    bool
	quiet  bool
	// asked is the height a box was asked to have, or had before the batch.
	asked map[doc.ID]float64
	// warnings is all that's told, once the checks are done. found is what they
	// found, by how much it matters, and capped says that they stopped short.
	warnings []string
	found    [ranks][]string
	capped   bool
	// What is told after other warnings, for it not to crowd them out.
	shared, trunks []string
	// styles is the named ones, as they'll stand if the batch succeeds.
	styles map[string]*fields
	// failed is the ids and style names of ops that failed, for what uses them not
	// to be blamed too.
	failed, failedStyles map[string]bool
}

// errDry ends a dry run, whose batch rolls back like one that failed.
var errDry = errors.New("dry run")

// applyMode says how a batch runs and answers.
type applyMode struct {
	dry   bool // says what would come of it, and leaves everything as it was
	quiet bool // leaves out each shape's box
	// invalid is what decodeOps found wrong, with nil in ops in their place.
	invalid *batchError
	// styles is the named ones so far, which this doesn't change, and keep is told
	// what they come to when a batch that isn't dry succeeds.
	styles map[string]*fields
	keep   func(map[string]*fields)
	// check says, as the batch begins, whether it can go ahead at all.
	check func() error
}

// apply runs ops as one step of undo, and says what came of them.
func apply(e *editor.Editor, ops []*op, mode applyMode) (string, error) {
	dry := mode.dry
	r := &run{e: e, changed: map[doc.ID]bool{}, asked: map[doc.ID]float64{}, before: map[doc.ID]float64{}, grown: map[doc.ID]*growth{}, dry: dry, quiet: mode.quiet}
	r.styles = maps.Clone(mode.styles)
	bad := mode.invalid
	if bad == nil {
		bad = &batchError{}
	}
	r.failed, r.failedStyles = map[string]bool{}, map[string]bool{}
	for _, name := range bad.names {
		r.failed[name] = true
	}
	for _, name := range bad.styles {
		r.failedStyles[name] = true
	}
	var report string
	err := e.Batch(func() error {
		if mode.check != nil {
			if err := mode.check(); err != nil {
				return err
			}
		}
		for i, o := range ops {
			if o == nil {
				continue
			}
			err := r.do(o)
			if err == nil {
				continue
			}
			r.markFailed(o)
			if !errors.Is(err, errFollows) {
				bad.add(i, err)
			}
			// What comes after a page that isn't there would be on the wrong one.
			if o.verb == "page" || o.verb == "delPage" {
				break
			}
		}
		if len(bad.list) > 0 {
			return bad
		}
		r.followRoutes()
		r.settleArrows()
		if err := r.checkReach(); err != nil {
			return err
		}
		r.checkLayout()
		report = r.report()
		if dry {
			return errDry
		}
		return nil
	})
	if errors.Is(err, errDry) {
		return report, nil
	}
	if err != nil {
		// A panic on the way shouldn't hide what was found before it.
		if len(bad.list) > 0 {
			return "", bad
		}
		return "", err
	}
	if mode.keep != nil {
		mode.keep(r.styles)
	}
	r.reveal()
	return report, nil
}

// errFollows says that an op failed only for using what another one failed to make.
var errFollows = errors.New("follows from an earlier error")

// markFailed remembers what a failed op would have made.
func (r *run) markFailed(o *op) {
	if o.ID != nil {
		r.failed[*o.ID] = true
	}
	for _, item := range o.With {
		if item.ID != nil {
			r.failed[*item.ID] = true
		}
	}
	if o.verb == "style" && o.Name != nil {
		r.failedStyles[*o.Name] = true
	}
}

// isFailed says whether a name is that of what a failed op would have made, or of a
// part of it, as a card's are.
func (r *run) isFailed(name string) bool {
	if r.failed[name] {
		return true
	}
	for failed := range r.failed {
		if strings.HasPrefix(name, failed+"-") {
			return true
		}
	}
	return false
}

// checkReach refuses a batch that worked out a place no file can hold, as many
// copies far apart can come to, for what can't be saved not to get in.
func (r *run) checkReach() error {
	for id := range r.touched() {
		b := r.e.PageBounds(id)
		for _, n := range []float64{b.X, b.Y, b.W, b.H} {
			if math.IsNaN(n) || math.Abs(n) > maxReached {
				return fmt.Errorf("%s would end up too far out, at %s; nothing was changed", shortID(id), boxText(b))
			}
		}
	}
	return nil
}

func (r *run) do(o *op) error {
	// A selector can reach another page, for as long as this op takes.
	if t := o.targets(); t != nil && t.sel != nil && t.sel.Page != nil {
		p := findPage(r.e, *t.sel.Page)
		if p == nil {
			return fmt.Errorf("no page %q", *t.sel.Page)
		}
		was := r.e.Page
		r.e.Page = p.ID
		defer func() { r.e.Page = was }()
	}
	switch o.verb {
	case "add":
		if err := r.add(o); err != nil {
			return err
		}
		if o.Behind != nil || o.Over != nil {
			return r.orderBeside(r.added[len(r.added)-1:], &o.fields)
		}
	case "set":
		return r.set(o)
	case "del":
		return r.del(o)
	case "dup":
		return r.dup(o)
	case "group":
		return r.group(o)
	case "ungroup":
		ids, err := r.mutable(o.Ungroup)
		if err != nil {
			return err
		}
		if o.Ungroup.broad() {
			// A card comes apart only when it's named.
			ids = slices.DeleteFunc(ids, func(id doc.ID) bool {
				return r.e.Doc.Shapes[id].Type != doc.TypeGroup || r.cardOf(id) != nil
			})
			if len(ids) == 0 {
				return errors.New("there is no group among them")
			}
		}
		for _, id := range ids {
			if r.e.Doc.Shapes[id].Type != doc.TypeGroup {
				return fmt.Errorf("%s is not a group", shortID(id))
			}
		}
		r.e.Select(ids...)
		r.e.Ungroup()
		for _, id := range r.e.Selection() {
			r.changed[id] = true
		}
	case "arrange":
		return r.arrange(o)
	case "order":
		ids, err := r.mutable(o.Order)
		if err != nil {
			return err
		}
		if o.How == nil {
			return r.orderBeside(ids, &o.fields)
		}
		r.e.Select(ids...)
		r.e.Reorder(*o.How)
		r.markChanged(ids)
	case "flip":
		ids, err := r.mutable(o.Flip)
		if err != nil {
			return err
		}
		r.warnHeldArrows(ids)
		r.e.Select(ids...)
		r.e.Flip(*o.How == "h")
		r.markChanged(ids)
	case "camera":
		return r.setCamera(o)
	case "page":
		return r.page(o)
	case "delPage":
		p := findPage(r.e, *o.DelPage)
		if p == nil {
			return fmt.Errorf("no page %q", *o.DelPage)
		}
		if len(r.e.Pages()) < 2 {
			return errors.New("can't delete the only page")
		}
		r.e.DeletePage(p.ID)
	case "style":
		return r.setStyle(o)
	}
	return nil
}

// setStyle takes what a style op says: defaults for the rest of the batch, or with a
// name, a style that stays for later batches to use.
func (r *run) setStyle(o *op) error {
	f := fields{}
	if o.Style.Use != nil {
		named, err := r.namedStyle(*o.Style.Use)
		if err != nil {
			return err
		}
		merge(&f, named)
	}
	merge(&f, o.Style)
	f.Use = nil
	if o.Name == nil {
		merge(&r.style, &f)
		return nil
	}
	if r.styles == nil {
		r.styles = map[string]*fields{}
	}
	r.styles[*o.Name] = &f
	return nil
}

// namedStyle returns a style by its name, or says which there are.
func (r *run) namedStyle(name string) (*fields, error) {
	if f := r.styles[name]; f != nil {
		return f, nil
	}
	if r.failedStyles[name] {
		return nil, errFollows
	}
	known := "none yet"
	if len(r.styles) > 0 {
		known = strings.Join(slices.Sorted(maps.Keys(r.styles)), ", ")
	}
	return nil, fmt.Errorf(`no style %q (there are: %s); define it with {"style":{...},"name":%q}`, name, known, name)
}

// withUse returns what fields come to with the named style they use under them,
// of what list takes.
func (r *run) withUse(f *fields, list string) (*fields, error) {
	if f.Use == nil {
		return f, nil
	}
	named, err := r.namedStyle(*f.Use)
	if err != nil {
		return nil, err
	}
	out := keepAllowed(named, list)
	merge(&out, f)
	out.Use = nil
	return &out, nil
}

// keepAllowed returns fields without what list doesn't take.
func keepAllowed(f *fields, list string) fields {
	out := *f
	v := reflect.ValueOf(&out).Elem()
	for i := range v.NumField() {
		name := v.Type().Field(i).Tag.Get("json")
		if !allows(list, name) && !allows(placeFields, name) {
			v.Field(i).Set(reflect.Zero(v.Field(i).Type()))
		}
	}
	return out
}

// orderBeside moves shapes to just behind or over the shape fields name. Paint
// order is among what shares a parent, so a shape held by something the other isn't
// in has to go beside what holds that one: or, moving what's held, can't.
func (r *run) orderBeside(ids []doc.ID, f *fields) error {
	name, over := f.Behind, false
	if f.Over != nil {
		name, over = f.Over, true
	}
	target, err := r.current(*name)
	if err != nil {
		return err
	}
	type beside struct {
		ref doc.ID
		ids []doc.ID
	}
	byParent := map[doc.ID]*beside{}
	var parents []doc.ID
	for _, id := range ids {
		parent := r.e.Doc.Shapes[id].Parent
		ref := target
		for ref != nil && ref.Parent != parent {
			ref = r.e.Doc.Shapes[ref.Parent]
		}
		if ref == nil {
			return fmt.Errorf("%s is in %s and %s isn't, and only what shares a parent has an order; order %s instead", shortID(id), shortID(parent), *name, shortID(parent))
		}
		if ref.ID == id {
			return fmt.Errorf("%s can't go beside itself or what it holds", shortID(id))
		}
		if byParent[parent] == nil {
			byParent[parent] = &beside{ref: ref.ID}
			parents = append(parents, parent)
		}
		byParent[parent].ids = append(byParent[parent].ids, id)
	}
	for _, parent := range parents {
		b := byParent[parent]
		if slices.Contains(b.ids, b.ref) {
			return fmt.Errorf("%s can't go beside itself or what it holds", shortID(b.ref))
		}
		r.e.OrderBeside(parent, b.ids, b.ref, over)
	}
	r.markChanged(ids)
	return nil
}

func (r *run) markChanged(ids []doc.ID) {
	for _, id := range ids {
		r.changed[id] = true
	}
}

// merge lays src's fields over dst's.
func merge(dst, src *fields) {
	d, s := reflect.ValueOf(dst).Elem(), reflect.ValueOf(src).Elem()
	for i := range d.NumField() {
		if !s.Field(i).IsNil() {
			d.Field(i).Set(s.Field(i))
		}
	}
}

// --- names ---

func shapeID(name string) doc.ID {
	if strings.HasPrefix(name, "shape:") {
		return doc.ID(name)
	}
	return doc.ID("shape:" + name)
}

func shortID(id doc.ID) string { return strings.TrimPrefix(string(id), "shape:") }

// lookup returns a shape on any page, or says what is wrong.
func (r *run) lookup(name string) (*doc.Shape, error) {
	if len(r.failed) > 0 && r.isFailed(strings.TrimPrefix(name, "shape:")) {
		return nil, errFollows
	}
	if s := r.e.Doc.Shapes[shapeID(name)]; s != nil {
		return s, nil
	}
	return nil, fmt.Errorf("no shape %q", name)
}

// current returns a shape that must be on the current page.
func (r *run) current(name string) (*doc.Shape, error) {
	s, err := r.lookup(name)
	if err != nil {
		return nil, err
	}
	if r.e.PageOf(s.ID) != r.e.Page {
		return nil, fmt.Errorf("shape %q is on another page; go there first with {\"page\":...}", name)
	}
	return s, nil
}

// mutable resolves the ids of shapes about to change, none of them locked.
func (r *run) mutable(t *targets) ([]doc.ID, error) { return r.pick(t, false) }

// freshID returns a new shape id, the one asked for or a short one.
func (r *run) freshID(asked *string) (doc.ID, error) {
	if asked != nil {
		if *asked == "" || strings.ContainsAny(*asked, " \t\n") {
			return "", fmt.Errorf("bad id %q", *asked)
		}
		if *asked == everything {
			return "", fmt.Errorf(`bad id %q: it means all on the page`, *asked)
		}
		id := shapeID(*asked)
		if r.e.Doc.Shapes[id] != nil {
			where := ""
			if p := r.e.Doc.Page(r.e.PageOf(id)); p != nil {
				where = fmt.Sprintf(" on page %q", p.Name)
			}
			return "", fmt.Errorf("id %q is taken%s; ids are unique across pages, so choose another or leave it out", *asked, where)
		}
		return id, nil
	}
	return r.autoID(), nil
}

func (r *run) autoID() doc.ID {
	for {
		r.nextID++
		id := doc.ID("shape:s" + strconv.Itoa(r.nextID))
		if r.e.Doc.Shapes[id] == nil {
			return id
		}
	}
}

// parentOf resolves in: a frame or group, or the page without one.
func (r *run) parentOf(in *string) (doc.ID, error) {
	if in == nil || *in == "" {
		return r.e.Page, nil
	}
	s, err := r.current(*in)
	if err != nil {
		return "", err
	}
	if s.Type != doc.TypeFrame && s.Type != doc.TypeGroup {
		return "", fmt.Errorf("%s is a %s; shapes go in frames and groups", *in, s.Type)
	}
	return s.ID, nil
}

// --- adding ---

func (r *run) add(o *op) error {
	kind, geoKind, err := shapeKind(*o.Add)
	if err != nil {
		return err
	}
	if o.Use != nil {
		f, err := r.withUse(&o.fields, takenBy(kind, geoKind))
		if err != nil {
			return err
		}
		c := *o
		c.fields = *f
		o = &c
	}
	if o.GradientAngle != nil && o.Gradient == nil && r.style.Gradient == nil {
		return errors.New("gradientAngle goes with a gradient")
	}
	switch kind {
	case doc.TypeArrow:
		return r.addArrow(o)
	case doc.TypeDraw, doc.TypeHighlight:
		return r.addStroke(o, kind)
	case doc.TypeLine:
		return r.addLine(o)
	case cardKind:
		return r.addCard(o)
	}
	id, err := r.freshID(o.ID)
	if err != nil {
		return err
	}
	parent, err := r.parentOf(o.In)
	if err != nil {
		return err
	}
	s := doc.NewShape(id, kind)
	s.Geo = geoKind
	s.Parent, s.Index = parent, r.e.TopIndex(parent)
	r.styled(s, &o.fields)
	switch kind {
	case doc.TypeGeo:
		r.sizeGeo(s, &o.fields)
		if o.H != nil {
			r.asked[id] = *o.H
		}
	case doc.TypeNote:
		r.asked[id] = editor.NoteSize * s.Scale
	case doc.TypeFrame:
		s.W, s.H = 400, 300
		if o.W != nil {
			s.W = *o.W
		}
		if o.H != nil {
			s.H = *o.H
		}
	case doc.TypeImage:
		r.addPicture(s, o)
	}
	r.e.FitLabel(s)
	r.e.Put(s)
	if err := r.placeNew(s.ID, &o.fields, parent); err != nil {
		return err
	}
	r.added = append(r.added, id)
	return nil
}

// styled applies the batch's style then the op's own fields to a new shape,
// leaving out of the style what this kind of shape doesn't take.
func (r *run) styled(s *doc.Shape, f *fields) {
	style := keepAllowed(&r.style, takenBy(s.Type, s.Geo))
	assign(s, &style)
	assign(s, f)
}

// assign writes the fields that are a shape's own props onto it.
func assign(s *doc.Shape, f *fields) {
	str := func(dst *string, v *string) {
		if v != nil {
			*dst = *v
		}
	}
	color := func(dst *string, v *string) {
		if v != nil {
			*dst = colorValue(*v)
		}
	}
	color(&s.Color, f.Color)
	color(&s.LabelColor, f.LabelColor)
	assignFill(s, f)
	if f.Dash != nil && (*f.Dash != "none" || outlineOptional(s.Type)) {
		s.Dash = *f.Dash
	}
	if f.Padding != nil {
		padding := *f.Padding
		s.Padding = &padding
	}
	if f.Radius != nil {
		s.Radius = *f.Radius
	}
	if f.Shadow != nil {
		s.Shadow = nil
		// What casts nothing is no shadow.
		if f.Shadow.Blur != 0 || f.Shadow.X != 0 || f.Shadow.Y != 0 {
			shadow := *f.Shadow
			s.Shadow = &shadow
		}
	}
	str(&s.Size, f.Size)
	str(&s.Font, f.Font)
	str(&s.VAlign, f.VAlign)
	str(&s.Spline, f.Spline)
	str(&s.Kind, f.Kind)
	str(&s.HeadEnd, f.Head)
	str(&s.HeadStart, f.Tail)
	str(&s.URL, f.URL)
	str(&s.AltText, f.Alt)
	str(&s.Name, f.Name)
	if f.Align != nil {
		if s.Type == doc.TypeText {
			s.TextAlign = *f.Align
		} else {
			s.Align = *f.Align
		}
	}
	if f.Text != nil {
		f.Text.setOn(s)
	}
	if f.Opacity != nil {
		s.Opacity = *f.Opacity
	}
	if f.Scale != nil {
		s.Scale = *f.Scale
	}
	if f.Locked != nil {
		s.Locked = *f.Locked
	}
	if f.Bend != nil {
		s.Bend = *f.Bend
	}
	if s.Type == doc.TypeFrame {
		str(&s.FrameSize, f.Size)
		str(&s.FrameFont, f.Font)
	}
	if s.Type == doc.TypeText && f.W != nil {
		s.AutoSize = false
		s.W = *f.W / s.Scale
	}
	if f.Label != nil {
		s.LabelSide = ""
		if *f.Label != "center" {
			s.LabelSide = *f.Label
		}
	}
	// Anchors asked for take over from straight.
	if (f.Straight != nil && *f.Straight == "off") || f.FromAt != nil || f.ToAt != nil {
		s.Straight, s.StraightAt = "", nil
	} else if f.Straight != nil {
		s.Straight = *f.Straight
	}
	if f.At != nil {
		at := *f.At
		s.StraightAt = &at
	}
}

// assignFill writes what a shape fills with. A fill said alone takes over from a
// gradient, and a gradient needs a fill to be pressed on.
func assignFill(s *doc.Shape, f *fields) {
	if f.Fill != nil {
		s.Fill = colorValue(*f.Fill)
		s.Gradient = nil
	}
	angle := 180.0
	if g := s.Gradient; g != nil {
		angle = g.Angle
	}
	if f.GradientAngle != nil {
		angle = *f.GradientAngle
	}
	colors := []string(nil)
	switch {
	case f.Gradient != nil:
		colors = *f.Gradient
	case f.GradientAngle != nil && s.Gradient != nil:
		colors = s.Gradient.Colors
	}
	switch {
	case len(colors) > 0:
		s.Gradient = &doc.Gradient{Colors: colors, Angle: angle}
		if s.Fill == "none" || editor.IsHex(s.Fill) {
			s.Fill = "fill"
		}
	case f.Gradient != nil:
		s.Gradient = nil
	}
}

// colorValue returns a color or fill as a shape holds it: a hex in lower case.
func colorValue(v string) string {
	if editor.IsHex(v) {
		return strings.ToLower(v)
	}
	return v
}

// sizeGeo gives a geo shape the size asked for. What isn't asked for fits its
// label, with room for the corners of what isn't a rectangle.
func (r *run) sizeGeo(s *doc.Shape, f *fields) {
	const maxAuto = 360.0
	s.W, s.H = 160, 100
	if s.Text != "" {
		size := editor.LabelSize(s.Size) * s.Scale
		pad := 2 * editor.LabelPad(s)
		k := 1.0
		if s.Geo != "rectangle" && s.Geo != "x-box" && s.Geo != "check-box" {
			k = 1.4
		}
		b := r.e.TextBlockOf(s, s.Text, size, 0)
		s.W = math.Min(b.W*k+pad+size/2, maxAuto)
		s.H = size*editor.LineHeight*k + pad
		// Padding said is a size asked for: no more than it takes.
		if s.Padding == nil {
			s.W, s.H = math.Max(s.W, 120), math.Max(s.H, 72)
		}
	}
	if f.W != nil {
		s.W = *f.W
	}
	if f.H != nil {
		s.H = *f.H
	}
}

func (r *run) addPicture(s *doc.Shape, o *op) {
	p := o.picture
	asset := &doc.Asset{
		ID: doc.NewID("asset"), W: p.w, H: p.h, Name: p.name, Mime: p.mime,
		Src: "data:" + p.mime + ";base64," + base64.StdEncoding.EncodeToString(p.data),
	}
	r.e.PutAsset(asset)
	s.AssetID = asset.ID
	switch {
	case o.W != nil && o.H != nil:
		s.W, s.H = *o.W, *o.H
	case o.W != nil:
		s.W, s.H = *o.W, *o.W*p.h/p.w
	case o.H != nil:
		s.W, s.H = *o.H*p.w/p.h, *o.H
	default:
		k := math.Min(1, 800/math.Max(p.w, p.h))
		s.W, s.H = p.w*k, p.h*k
	}
}

// placeNew puts a new shape where its fields say.
func (r *run) placeNew(id doc.ID, f *fields, parent doc.ID) error {
	s := r.e.Doc.Shapes[id]
	b := r.e.Geometry(s).Bounds
	at, err := r.topLeft(f, b.W, b.H, func() geo.Vec { return r.freeSpot(parent, id) })
	if err != nil {
		return err
	}
	rot := r.e.PageMat(parent).Rotation()
	if f.Rot != nil {
		rot = *f.Rot * math.Pi / 180
	}
	r.placeBox(id, at, rot)
	return nil
}

// topLeft works out where a box goes: beside a shape, at x and y, or at def.
func (r *run) topLeft(f *fields, w, h float64, def func() geo.Vec) (geo.Vec, error) {
	gap := defaultGap
	if f.Gap != nil {
		gap = *f.Gap
	}
	var at geo.Vec
	beside := 0
	for _, rel := range []struct {
		ref *string
		pos func(b geo.Box) geo.Vec
	}{
		{f.Right, func(b geo.Box) geo.Vec { return geo.V(b.MaxX()+gap, b.Center().Y-h/2) }},
		{f.Left, func(b geo.Box) geo.Vec { return geo.V(b.X-gap-w, b.Center().Y-h/2) }},
		{f.Below, func(b geo.Box) geo.Vec { return geo.V(b.Center().X-w/2, b.MaxY()+gap) }},
		{f.Above, func(b geo.Box) geo.Vec { return geo.V(b.Center().X-w/2, b.Y-gap-h) }},
	} {
		if rel.ref == nil {
			continue
		}
		beside++
		ref, err := r.current(*rel.ref)
		if err != nil {
			return geo.Vec{}, err
		}
		at = rel.pos(r.e.PageBounds(ref.ID))
	}
	switch {
	case beside > 1:
		return geo.Vec{}, errors.New("use one of right, left, above, below")
	case beside == 0 && f.X != nil && f.Y != nil:
	case beside == 0:
		at = def()
	}
	if f.X != nil {
		at.X = *f.X
	}
	if f.Y != nil {
		at.Y = *f.Y
	}
	return at, nil
}

// freeSpot returns where a shape goes that says nothing of where: under what
// its parent holds, or in the corner of an empty frame.
func (r *run) freeSpot(parent, except doc.ID) geo.Vec {
	const pad = 40.0
	var box geo.Box
	seen := false
	for _, id := range r.e.Children(parent) {
		if id == except {
			continue
		}
		b := r.e.PageBounds(id)
		if seen {
			box = box.Union(b)
		} else {
			box, seen = b, true
		}
	}
	if seen {
		return geo.V(box.X, box.MaxY()+pad)
	}
	if doc.IsPageID(parent) {
		return geo.Vec{}
	}
	return r.e.PageMat(parent).Apply(geo.V(pad, pad))
}

// placeBox puts a shape so that its box, before it turns, has this top left in
// the page, and turns it around its center to a turn in the page.
func (r *run) placeBox(id doc.ID, topLeft geo.Vec, rot float64) {
	s := r.e.Doc.Shapes[id]
	b := r.e.Geometry(s).Bounds
	center := topLeft.Add(geo.V(b.W/2, b.H/2))
	origin := center.Sub(geo.Rotate(rot).Apply(b.Center()))
	pm := r.e.PageMat(s.Parent)
	local := pm.Invert().Apply(origin)
	c := s.Moved()
	c.X, c.Y, c.Rotation = local.X, local.Y, rot-pm.Rotation()
	r.e.Put(c)
}

// pathMat returns what moves and scales a path, around a box, to what fields
// ask for. A path that says nothing of where stays at its own coordinates.
func (r *run) pathMat(box geo.Box, f *fields) (geo.Mat, error) {
	sx, sy := 1.0, 1.0
	if f.W != nil && box.W > 0 {
		sx = *f.W / box.W
	}
	if f.H != nil && box.H > 0 {
		sy = *f.H / box.H
	}
	// A size given one way keeps the shape's proportions.
	switch {
	case f.W != nil && f.H == nil:
		sy = sx
	case f.H != nil && f.W == nil:
		sx = sy
	}
	at := geo.V(box.X*sx, box.Y*sy)
	if f.X != nil || f.Y != nil || f.Right != nil || f.Left != nil || f.Above != nil || f.Below != nil {
		var err error
		at, err = r.topLeft(f, box.W*sx, box.H*sy, func() geo.Vec { return at })
		if err != nil {
			return geo.Mat{}, err
		}
	}
	return geo.Translate(at.X-box.X*sx, at.Y-box.Y*sy).Mul(geo.Scale(sx, sy)), nil
}

// sampled returns an op's path, placed by its fields and sampled, in the page.
func (r *run) sampled(o *op) ([][]geo.Vec, []bool, error) {
	if o.path == nil {
		return nil, nil, errors.New(`needs d, an SVG path as "M0 0 C40 80 80 -80 120 0"`)
	}
	flat, _ := o.path.Flatten(16)
	var box geo.Box
	for i, sub := range flat {
		if i == 0 {
			box = geo.BoxOf(sub)
		} else {
			box = box.Union(geo.BoxOf(sub))
		}
	}
	if len(flat) == 0 {
		return nil, nil, errors.New("d draws nothing")
	}
	m, err := r.pathMat(box, &o.fields)
	if err != nil {
		return nil, nil, err
	}
	placed := o.path.Transform(m)
	subs, closed := placed.Sampled(sampleStep)
	if len(subs) > maxSubpaths {
		return nil, nil, fmt.Errorf("d has %d subpaths; the limit is %d", len(subs), maxSubpaths)
	}
	// Very long paths are sampled more coarsely rather than refused.
	for step := sampleStep * 2; pointCount(subs) > maxPoints; step *= 2 {
		subs, closed = placed.Sampled(step)
		if step > 1e6 {
			return nil, nil, errors.New("d has too many points")
		}
	}
	return subs, closed, nil
}

func pointCount(subs [][]geo.Vec) int {
	n := 0
	for _, sub := range subs {
		n += len(sub)
	}
	return n
}

// origin puts a shape, whose points run along the page's axes, at a point of
// the page, whatever its parent's turn. A path says where it goes in the page,
// and stays there.
func (r *run) origin(c *doc.Shape, at geo.Vec) {
	pm := r.e.PageMat(c.Parent)
	local := pm.Invert().Apply(at)
	c.X, c.Y, c.Rotation = local.X, local.Y, -pm.Rotation()
}

func (r *run) addStroke(o *op, kind string) error {
	parent, err := r.parentOf(o.In)
	if err != nil {
		return err
	}
	id, err := r.freshID(o.ID)
	if err != nil {
		return err
	}
	subs, closed, err := r.sampled(o)
	if err != nil {
		return err
	}
	ids := make([]doc.ID, 0, len(subs))
	for i, sub := range subs {
		sid := id
		if len(subs) > 1 {
			sid = doc.ID(fmt.Sprintf("%s-%d", id, i+1))
			if r.e.Doc.Shapes[sid] != nil {
				return fmt.Errorf("id %q is taken", shortID(sid))
			}
		}
		s := doc.NewShape(sid, kind)
		s.Parent, s.Index = parent, r.e.TopIndex(parent)
		r.styled(s, &o.fields)
		setStroke(s, sub, closed[i])
		r.origin(s, sub[0])
		r.e.Put(s)
		ids = append(ids, sid)
	}
	// Each subpath is a stroke of its own, held together by a group.
	if len(ids) > 1 {
		r.e.Select(ids...)
		r.e.GroupAs(id)
	}
	if o.Rot != nil {
		r.turn(id, *o.Rot*math.Pi/180)
	}
	r.added = append(r.added, id)
	return nil
}

// setStroke gives a freehand shape its points, relative to the first of them,
// which is its origin.
func setStroke(s *doc.Shape, pts []geo.Vec, closed bool) {
	o := pts[0]
	points := make([]doc.PointZ, len(pts))
	for i, p := range pts {
		points[i] = doc.PointZ{X: p.X - o.X, Y: p.Y - o.Y, Z: 0.5}
	}
	s.Segments = []doc.Segment{{Type: "free", Points: points, Dim: 2}}
	s.IsClosed = closed
	s.ScaleX, s.ScaleY = 1, 1
}

func (r *run) addLine(o *op) error {
	parent, err := r.parentOf(o.In)
	if err != nil {
		return err
	}
	id, err := r.freshID(o.ID)
	if err != nil {
		return err
	}
	pts, err := r.vertices(o)
	if err != nil {
		return err
	}
	s := doc.NewShape(id, doc.TypeLine)
	s.Parent, s.Index = parent, r.e.TopIndex(parent)
	r.styled(s, &o.fields)
	setVertices(s, pts)
	r.origin(s, pts[0])
	r.e.Put(s)
	if o.Rot != nil {
		r.turn(id, *o.Rot*math.Pi/180)
	}
	r.added = append(r.added, id)
	return nil
}

// vertices returns the points of a line's path, which must be straight: its
// vertices are what can be dragged.
func (r *run) vertices(o *op) ([]geo.Vec, error) {
	if o.path == nil {
		return nil, errors.New(`line needs d, its vertices as "M0 0 100 50 200 0"`)
	}
	for _, s := range o.path.Segs {
		if s.Op == geo.OpQuad || s.Op == geo.OpCube {
			return nil, errors.New(`line takes straight pieces; use spline:"cubic" to curve through its vertices, or add draw for a curved path`)
		}
	}
	subs, closed := o.path.Flatten(1)
	if len(subs) != 1 {
		return nil, errors.New("line takes one subpath; use draw for several")
	}
	if len(subs[0]) < 2 {
		return nil, errors.New("line needs at least 2 vertices")
	}
	pts := subs[0]
	if closed[0] {
		pts = append(slices.Clone(pts), pts[0])
	}
	m, err := r.pathMat(geo.BoxOf(pts), &o.fields)
	if err != nil {
		return nil, err
	}
	out := make([]geo.Vec, len(pts))
	for i, p := range pts {
		out[i] = m.Apply(p)
	}
	return out, nil
}

// setVertices gives a line its points, relative to the first of them, which
// is its origin.
func setVertices(s *doc.Shape, pts []geo.Vec) {
	o := pts[0]
	s.Points = make([]doc.LinePoint, len(pts))
	index := ""
	for i, p := range pts {
		index = doc.IndexBetween(index, "")
		s.Points[i] = doc.LinePoint{ID: index, Index: index, X: p.X - o.X, Y: p.Y - o.Y}
	}
}

func (r *run) addArrow(o *op) error {
	id, err := r.freshID(o.ID)
	if err != nil {
		return err
	}
	if o.From == nil && (o.X1 == nil || o.Y1 == nil) {
		return errors.New("arrow needs from, or x1 and y1")
	}
	if o.To == nil && (o.X2 == nil || o.Y2 == nil) {
		return errors.New("arrow needs to, or x2 and y2")
	}
	parent, err := r.arrowParent(o)
	if err != nil {
		return err
	}
	s := doc.NewShape(id, doc.TypeArrow)
	s.Parent, s.Index = parent, r.e.TopIndex(parent)
	r.styled(s, &o.fields)
	r.e.Put(s)
	if err := r.setEnds(s.ID, &o.fields); err != nil {
		return err
	}
	if err := r.setRoute(s.ID, &o.fields); err != nil {
		return err
	}
	r.added = append(r.added, id)
	return nil
}

// setRoute gives an elbow arrow what it passes through, and turns down what a
// kind of arrow would only store and ignore.
func (r *run) setRoute(id doc.ID, f *fields) error {
	s := r.e.Doc.Shapes[id]
	elbow := s.Kind == "elbow"
	if elbow && f.Bend != nil && *f.Bend != 0 {
		return errors.New("an elbow arrow can't bend; fromAt, toAt and via route it")
	}
	via := s.Via
	if f.Via != nil {
		m := r.e.PageMat(id).Invert()
		via = nil
		for _, p := range *f.Via {
			via = append(via, m.Apply(geo.V(p[0], p[1])))
		}
	}
	if !elbow {
		if f.Via != nil && len(via) > 0 {
			return errors.New(`via routes an elbow arrow; add "kind":"elbow"`)
		}
		via = nil
	}
	if len(via) > 0 && s.Straight != "" {
		return errors.New(`a straight arrow runs level, so it can't take via; straight:"off" first`)
	}
	if len(via) != len(s.Via) || f.Via != nil {
		c := s.Clone()
		c.SetVia(via)
		r.e.Put(c)
	}
	return nil
}

// followRoutes keeps routes right for what their arrows join, and tells of what
// that did to them beyond going along.
func (r *run) followRoutes() {
	c := r.e.SettleRoutes()
	for _, id := range slices.Concat(c.Moved, c.Cleared, c.Replaced, c.Around) {
		r.changed[id] = true
	}
	for _, id := range c.Cleared {
		r.warn("%s's via was cleared: what it joins moved, and the way without it is clear with fewer turns; via sets it again", shortID(id))
	}
	for _, id := range c.Replaced {
		r.warn("%s's via was replaced: what it joins moved, and this way is clear with fewer turns; via sets it again", shortID(id))
	}
	for _, id := range c.Around {
		r.warn("%s now goes around what's in its way, by a via; via sets another", shortID(id))
	}
}

// arrowParent returns where an arrow goes: as asked, or in the frame that
// holds both of what it joins, or on the page.
func (r *run) arrowParent(o *op) (doc.ID, error) {
	if o.In != nil {
		return r.parentOf(o.In)
	}
	if o.From != nil && o.To != nil {
		a, err := r.current(*o.From)
		if err != nil {
			return "", err
		}
		b, err := r.current(*o.To)
		if err != nil {
			return "", err
		}
		if a.Parent == b.Parent {
			if p := r.e.Doc.Shapes[a.Parent]; p != nil && p.Type == doc.TypeFrame {
				return p.ID, nil
			}
		}
	}
	return r.e.Page, nil
}

// setEnds binds or frees the ends of an arrow, as fields say.
func (r *run) setEnds(id doc.ID, f *fields) error {
	end := func(term string, target *string, at *[]float64, x, y *float64) error {
		// An anchor alone moves it on the shape it is bound to.
		if b := r.e.BindingOf(id, term); at != nil && target == nil && b != nil {
			name := shortID(b.To)
			target = &name
		}
		if at != nil && (target == nil || *target == "") {
			return fmt.Errorf("the anchor of the arrow's %s needs a shape to bind to", term)
		}
		switch {
		case target != nil && *target != "":
			t, err := r.current(*target)
			if err != nil {
				return err
			}
			if c := r.cardOf(t.ID); c != nil {
				t = c.box
			}
			if t.Type == doc.TypeGroup {
				if t, err = r.groupTarget(t); err != nil {
					return err
				}
			}
			switch t.Type {
			case doc.TypeArrow, doc.TypeLine, doc.TypeHighlight:
				return fmt.Errorf("arrows can't bind to a %s (%s)", t.Type, *target)
			}
			if t.ID == id {
				return errors.New("an arrow can't bind to itself")
			}
			anchor := geo.V(0.5, 0.5)
			if at != nil {
				anchor = geo.V((*at)[0], (*at)[1])
			}
			r.e.Bind(id, term, t.ID, anchor)
			// The end's own point keeps close to what it shows.
			b := r.e.Geometry(t).Bounds
			p := r.e.PageMat(t.ID).Apply(geo.V(b.X+anchor.X*b.W, b.Y+anchor.Y*b.H))
			r.setEnd(id, term, p)
		case target != nil:
			r.e.Unbind(id, term)
		}
		if x != nil || y != nil {
			if r.e.BindingOf(id, term) != nil {
				return fmt.Errorf("the arrow's %s is bound; free it with an empty from/to first", term)
			}
			p := r.endOf(id, term)
			if x != nil {
				p.X = *x
			}
			if y != nil {
				p.Y = *y
			}
			r.setEnd(id, term, p)
		}
		return nil
	}
	if err := end("start", f.From, f.FromAt, f.X1, f.Y1); err != nil {
		return err
	}
	if err := end("end", f.To, f.ToAt, f.X2, f.Y2); err != nil {
		return err
	}
	if r.endOf(id, "start") == r.endOf(id, "end") {
		return errors.New("the arrow's ends are at the same point")
	}
	return nil
}

// groupTarget returns what an arrow told to bind to a group binds to: the shape
// that is most of it, as the body of a cylinder drawn in several strokes. A
// group with no such shape has to be told which.
func (r *run) groupTarget(g *doc.Shape) (*doc.Shape, error) {
	// How much of a group's box a shape has to fill to stand for the group.
	const most = 0.6
	box := r.e.PageBounds(g.ID)
	var best *doc.Shape
	var area float64
	var names []string
	for _, s := range r.leaves(g.ID) {
		switch s.Type {
		case doc.TypeArrow, doc.TypeLine, doc.TypeHighlight:
			continue
		}
		// A card is named as a card: that's what an arrow binds to.
		if name := r.name(s.ID); len(names) < maxTargetsShown && !slices.Contains(names, name) {
			names = append(names, name)
		}
		b := r.e.PageBounds(s.ID)
		if a := b.W * b.H; a > area {
			best, area = s, a
		}
	}
	if best != nil && area >= most*box.W*box.H {
		return best, nil
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("group %s holds nothing an arrow can bind to", shortID(g.ID))
	}
	return nil, fmt.Errorf("group %s has no main shape for an arrow to bind to; bind to one in it: %s", shortID(g.ID), strings.Join(names, ", "))
}

// maxTargetsShown is how many of a group's shapes an error names.
const maxTargetsShown = 8

// endOf returns an end of an arrow's own points, in the page.
func (r *run) endOf(id doc.ID, term string) geo.Vec {
	s := r.e.Doc.Shapes[id]
	v := s.Start
	if term == "end" {
		v = s.End
	}
	return r.e.PageMat(id).Apply(v)
}

func (r *run) setEnd(id doc.ID, term string, page geo.Vec) {
	s := r.e.Doc.Shapes[id]
	v := r.e.PageMat(id).Invert().Apply(page)
	c := s.Clone()
	if term == "start" {
		c.Start = v
	} else {
		c.End = v
	}
	r.e.Put(c)
}

// --- changing ---

// turn turns a shape around its center, to a turn in the page.
func (r *run) turn(id doc.ID, rot float64) {
	s := r.e.Doc.Shapes[id]
	b := r.e.Geometry(s).Bounds
	center := r.e.PageMat(id).Apply(b.Center())
	r.placeBox(id, center.Sub(geo.V(b.W/2, b.H/2)), rot)
}

func (r *run) set(o *op) error {
	f := &o.fields
	if len(said(f)) == 0 {
		return errors.New("set says nothing to change")
	}
	if f.ID != nil || f.How != nil || f.N != nil || f.Rows != nil || f.Cols != nil {
		return errors.New(`set has no "id", "how", "n", "rows" or "cols"`)
	}
	ids, err := r.pick(o.Set, f.Locked != nil && !*f.Locked)
	if err != nil {
		return err
	}
	several := len(ids) > 1 || o.Set.broad()
	if several && f.saysBeside() {
		return errors.New("relative places are for one shape at a time; use x, y, dx or dy for several")
	}
	if several && f.X != nil && f.Y != nil {
		return errors.New("x and y together would stack several shapes; give one of them to line them up, or use dx, dy")
	}
	done, err := r.setAll(ids, o, o.Set.broad())
	if err != nil {
		return err
	}
	r.markChanged(done)
	if f.Behind != nil || f.Over != nil {
		return r.orderBeside(done, f)
	}
	return nil
}

func (r *run) setOne(id doc.ID, o *op) error {
	s := r.e.Doc.Shapes[id]
	f, err := r.withUse(&o.fields, r.setList(s))
	if err != nil {
		return err
	}
	// Only what the op says itself: a style is left out of what doesn't take it.
	for _, name := range said(&o.fields) {
		if err := r.refuses(s, o, name); err != nil {
			return err
		}
	}
	if c := r.cardOf(id); c != nil {
		if _, ok := r.before[id]; !ok {
			r.before[id] = r.e.Geometry(s).Bounds.H
		}
		rest, err := r.setCard(c, f)
		if err != nil {
			return err
		}
		f = rest
	}
	// Styles reach through a group to what it holds.
	for _, leaf := range r.leaves(id) {
		if _, ok := r.asked[leaf.ID]; !ok && (leaf.Type == doc.TypeGeo || leaf.Type == doc.TypeNote) {
			r.asked[leaf.ID] = r.e.Geometry(leaf).Bounds.H
		}
		c := leaf.Clone()
		if leaf.ID == id {
			assign(c, f)
		} else {
			assign(c, stylesFor(leaf, f))
		}
		r.e.FitLabel(c)
		r.e.Put(c)
	}
	s = r.e.Doc.Shapes[id]
	if s.Type == doc.TypeGroup && f.Locked != nil {
		c := s.Clone()
		c.Locked = *f.Locked
		r.e.Put(c)
	}
	if s.Type == doc.TypeArrow {
		if f.From != nil || f.To != nil || f.X1 != nil || f.Y1 != nil || f.X2 != nil || f.Y2 != nil || f.FromAt != nil || f.ToAt != nil {
			if err := r.setEnds(id, f); err != nil {
				return err
			}
		}
		if err := r.setRoute(id, f); err != nil {
			return err
		}
		straight := r.e.Doc.Shapes[id].Straight != ""
		if f.At != nil && !straight {
			return errors.New(`at goes with straight:"h" or "v"`)
		}
		if f.Straight != nil && straight && (r.e.BindingOf(id, "start") == nil || r.e.BindingOf(id, "end") == nil) {
			return errors.New("straight needs both ends bound, with from and to")
		}
		if f.DX != nil || f.DY != nil {
			if r.e.BindingOf(id, "start") != nil && r.e.BindingOf(id, "end") != nil {
				return errors.New("a bound arrow moves with what it joins")
			}
		}
	}
	if o.path != nil {
		if err := r.setPath(s, o); err != nil {
			return err
		}
	}
	if f.In != nil {
		parent, err := r.parentOf(f.In)
		if err != nil {
			return err
		}
		if parent == id || r.e.IsAncestor(id, parent) {
			return errors.New("a shape can't go inside itself")
		}
		r.e.Reparent(id, parent)
	}
	if o.path == nil && s.Type != doc.TypeText && (f.W != nil || f.H != nil) {
		r.resize(id, f)
		if f.H != nil && s.Type != doc.TypeGroup {
			r.asked[id] = *f.H
		}
	}
	s = r.e.Doc.Shapes[id]
	if f.X != nil || f.Y != nil || f.Right != nil || f.Left != nil || f.Above != nil || f.Below != nil {
		b := r.e.Geometry(s).Bounds
		center := r.e.PageMat(id).Apply(b.Center())
		now := center.Sub(geo.V(b.W/2, b.H/2))
		at, err := r.topLeft(f, b.W, b.H, func() geo.Vec { return now })
		if err != nil {
			return err
		}
		if f.Right == nil && f.Left == nil && f.Above == nil && f.Below == nil {
			if f.X == nil {
				at.X = now.X
			}
			if f.Y == nil {
				at.Y = now.Y
			}
		}
		r.e.MoveBy(id, at.Sub(now))
	}
	if f.DX != nil || f.DY != nil {
		var d geo.Vec
		if f.DX != nil {
			d.X = *f.DX
		}
		if f.DY != nil {
			d.Y = *f.DY
		}
		r.e.MoveBy(id, d)
	}
	if f.Rot != nil {
		r.turn(id, *f.Rot*math.Pi/180)
	}
	return nil
}

// refuses says why a shape can't take a field an op says, if it can't: nothing that
// would only be stored, to show nowhere.
func (r *run) refuses(s *doc.Shape, o *op, name string) error {
	const placing = "x y dx dy in right left above below gap behind over locked rot opacity use"
	f := &o.fields
	list := r.setList(s)
	if s.Type == doc.TypeArrow && allows("x y rot right left above below gap w h", name) {
		return fmt.Errorf("an arrow has no %q; move it with dx,dy or its ends", name)
	}
	if !allows(placing, name) && !allows(list, name) {
		return fmt.Errorf("%s has no %q; it takes %s %s", aKind(s), name, list, placing)
	}
	switch name {
	case "gap":
		if !f.saysBeside() {
			return errors.New(gapHelp)
		}
	case "dash":
		if *f.Dash == "none" && !outlineOptional(s.Type) {
			return fmt.Errorf(`a %s is its line, so it can't have dash "none"`, s.Type)
		}
	case "dx", "dy":
		if s.Type == doc.TypeArrow && r.e.BindingOf(s.ID, "start") != nil && r.e.BindingOf(s.ID, "end") != nil {
			return errors.New("a bound arrow moves with what it joins")
		}
	case "x1", "x2":
		if s.Type == doc.TypeDraw && f.Fn == nil {
			return errors.New("x1 and x2 are where a draw's fn runs, and this says none")
		}
	case "in":
		if t := r.e.Doc.Shapes[shapeID(*f.In)]; *f.In != "" && t != nil && (t.ID == s.ID || r.e.IsAncestor(s.ID, t.ID)) {
			return errors.New("a shape can't go inside itself")
		}
	case "fill", "gradient":
		// A new path says for itself whether it closes.
		closed := s.IsClosed
		if o.path != nil {
			closed = o.closes()
		}
		if s.Type == doc.TypeDraw && saysFill(f) && !closed {
			return errors.New(openFillHelp)
		}
	case "gradientAngle":
		if f.Gradient == nil && !slices.ContainsFunc(r.leaves(s.ID), func(l *doc.Shape) bool { return l.Gradient != nil }) {
			return errors.New("gradientAngle goes with a gradient, and it has none")
		}
	}
	// A group takes a style for what it holds, so that has to take it.
	if s.Type == doc.TypeGroup && r.cardOf(s.ID) == nil && !allows(placing, name) && isStyleField(name) {
		if !slices.ContainsFunc(r.leaves(s.ID), func(l *doc.Shape) bool { return allows(takenBy(l.Type, l.Geo), name) }) {
			return fmt.Errorf("nothing in group %s takes %q", shortID(s.ID), name)
		}
	}
	return nil
}

// aKind says what a shape is, with its article.
func aKind(s *doc.Shape) string {
	name := kindName(s)
	if strings.ContainsRune("aeiox", rune(name[0])) { // as x-box
		return "an " + name
	}
	return "a " + name
}

// setList returns what a shape takes, beside where it goes.
func (r *run) setList(s *doc.Shape) string {
	list := takenBy(s.Type, s.Geo)
	_, known := addFields[s.Type]
	switch {
	case s.Type == doc.TypeGroup:
		list = "w h color labelColor fill gradient gradientAngle dash size font align valign padding radius shadow"
		if r.cardOf(s.ID) != nil {
			list = addFields[cardKind]
		}
	case !known:
		list = "w h" // a type this app only keeps, as an embed
	}
	return list
}

// leaves returns the shape, or what a group holds, at any depth.
func (r *run) leaves(id doc.ID) []*doc.Shape {
	s := r.e.Doc.Shapes[id]
	if s.Type != doc.TypeGroup {
		return []*doc.Shape{s}
	}
	var out []*doc.Shape
	for _, k := range r.e.Descendants(id) {
		if c := r.e.Doc.Shapes[k]; c.Type != doc.TypeGroup {
			out = append(out, c)
		}
	}
	return out
}

// stylesFor keeps of fields the styles a type of shape takes.
func stylesFor(leaf *doc.Shape, f *fields) *fields {
	out := *f
	list := takenBy(leaf.Type, leaf.Geo)
	v := reflect.ValueOf(&out).Elem()
	for i := range v.NumField() {
		name := v.Type().Field(i).Tag.Get("json")
		if !isStyleField(name) || (name != "opacity" && !allows(list, name)) {
			v.Field(i).Set(reflect.Zero(v.Field(i).Type()))
		}
	}
	return &out
}

// resize scales a shape to the size asked for. A size given one way scales
// only that way, except where a kind of shape has to keep its proportions.
func (r *run) resize(id doc.ID, f *fields) {
	s := r.e.Doc.Shapes[id]
	b := r.e.Geometry(s).Bounds
	sx, sy := 1.0, 1.0
	if f.W != nil && b.W > 0 {
		sx = *f.W / b.W
	}
	if f.H != nil && b.H > 0 {
		sy = *f.H / b.H
	}
	if s.Type == doc.TypeNote && f.W == nil {
		sx = sy
	}
	r.e.Select(id)
	r.e.ScaleSelection(sx, sy)
	for _, leaf := range r.leaves(id) {
		c := leaf.Clone()
		r.e.FitLabel(c)
		r.e.Put(c)
	}
}

// setPath gives a freehand shape or a line a new path.
func (r *run) setPath(s *doc.Shape, o *op) error {
	c := s.Clone()
	var at geo.Vec
	switch s.Type {
	case doc.TypeDraw, doc.TypeHighlight:
		subs, closed, err := r.sampled(o)
		if err != nil {
			return err
		}
		if len(subs) != 1 {
			return errors.New("set takes one subpath in d; add a new shape for several")
		}
		setStroke(c, subs[0], closed[0])
		at = subs[0][0]
	case doc.TypeLine:
		pts, err := r.vertices(o)
		if err != nil {
			return err
		}
		setVertices(c, pts)
		at = pts[0]
	}
	r.origin(c, at)
	r.e.Put(c)
	return nil
}

func (r *run) del(o *op) error {
	ids, err := r.mutable(o.Del)
	if err != nil {
		// A page already clear is what was asked for.
		if o.Del.isEverything() && errors.Is(err, errPicksNothing) {
			return nil
		}
		return err
	}
	before := len(r.e.Doc.Shapes)
	r.warnLostBindings(ids)
	r.e.Select(ids...)
	r.e.Delete()
	// What they held went with them, and groups that were left with too little.
	gone := before - len(r.e.Doc.Shapes)
	r.deleted += gone
	r.deletedInside += max(gone-len(ids), 0)
	return nil
}

// warnLostBindings tells of arrows about to lose an end, staying where they are
// with it free, because what it's bound to is going.
func (r *run) warnLostBindings(ids []doc.ID) {
	going := map[doc.ID]bool{}
	for _, id := range ids {
		going[id] = true
		for _, k := range r.e.Descendants(id) {
			going[k] = true
		}
	}
	for _, id := range slices.Sorted(maps.Keys(going)) {
		for _, b := range r.e.BindingsTo(id) {
			if going[b.From] {
				continue
			}
			end := "from"
			if b.Terminal == "end" {
				end = "to"
			}
			r.warn("%s lost its %s, as what it joined was deleted; its end stays where it was, free", shortID(ownerOf(r.e, b.From)), end)
		}
	}
}

func (r *run) dup(o *op) error {
	ids, err := r.mutable(o.Dup)
	if err != nil {
		return err
	}
	var box geo.Box
	size := 0
	for i, id := range ids {
		if i == 0 {
			box = r.e.PageBounds(id)
		} else {
			box = box.Union(r.e.PageBounds(id))
		}
		size += 1 + len(r.e.Descendants(id))
	}
	offsets, err := copyOffsets(o, box)
	if err != nil {
		return err
	}
	if n := len(offsets) * size; n > maxMade {
		return fmt.Errorf("this would make %d shapes; a dup makes up to %d", n, maxMade)
	}
	if o.ID != nil && len(offsets) > 1 {
		return errors.New(`id names one copy; name several with with:[{"id":"a"},{"id":"b"}]`)
	}
	r.warnLostEnds(ids)
	for i, at := range offsets {
		var item *op
		if len(o.With) > 0 {
			item = o.With[min(i, len(o.With)-1)]
		}
		asked := o.ID
		if item != nil && item.ID != nil {
			asked = item.ID
		}
		if asked != nil {
			if len(ids) > 1 {
				return errors.New("an id names one copy of one shape, and this copies several")
			}
			if _, err := r.freshID(asked); err != nil {
				return err
			}
		}
		r.e.Select(ids...)
		copies := r.e.DuplicateAs(at, r.copyNames(asked))
		r.added = append(r.added, copies...)
		if item == nil {
			continue
		}
		over := *item
		over.ID = nil
		if len(said(&over.fields)) == 0 {
			continue
		}
		if _, err := r.setAll(copies, &over, o.Dup.broad()); err != nil {
			return err
		}
	}
	return nil
}

// copyOffsets returns how far each copy a dup asks for is from what it copies: along
// a line, or the cells of a grid, row by row, whose first is the original.
func copyOffsets(o *op, box geo.Box) ([]geo.Vec, error) {
	if o.Rows != nil || o.Cols != nil {
		return gridOffsets(o, box)
	}
	if o.Gap != nil {
		return nil, errors.New("gap goes with rows or cols; dx and dy space a line of copies")
	}
	n := max(len(o.With), 1)
	if o.N != nil {
		if len(o.With) > 1 && *o.N != len(o.With) {
			return nil, fmt.Errorf("n says %d copies and with lists %d; leave n out", *o.N, len(o.With))
		}
		n = *o.N
	}
	if n < 1 || n > maxCopies {
		return nil, fmt.Errorf("n must be from 1 to %d", maxCopies)
	}
	// Beside what it copies, unless asked otherwise.
	d := geo.V(box.W+copyGap, 0)
	if o.DX != nil || o.DY != nil {
		d = geo.Vec{}
		if o.DX != nil {
			d.X = *o.DX
		}
		if o.DY != nil {
			d.Y = *o.DY
		}
	}
	out := make([]geo.Vec, n)
	for i := range out {
		out[i] = d.Mul(float64(i + 1))
	}
	return out, nil
}

func gridOffsets(o *op, box geo.Box) ([]geo.Vec, error) {
	if o.N != nil {
		return nil, errors.New("n counts a line of copies, and rows and cols a grid's cells; use one or the other")
	}
	if o.Gap != nil && (o.DX != nil || o.DY != nil) {
		return nil, errors.New("gap is between cells and dx, dy from one to the next; use one or the other")
	}
	rows, cols := 1, 1
	if o.Rows != nil {
		rows = *o.Rows
	}
	if o.Cols != nil {
		cols = *o.Cols
	}
	n := rows*cols - 1
	if n < 1 {
		return nil, errors.New("a grid of one cell is the original, so it copies nothing")
	}
	if n > maxMade {
		return nil, fmt.Errorf("%d rows of %d make too many copies; a dup makes up to %d", rows, cols, maxMade)
	}
	if len(o.With) > 1 && len(o.With) != n {
		return nil, fmt.Errorf("%d rows of %d make %d copies, the original being the first cell, and with lists %d", rows, cols, n, len(o.With))
	}
	gap := copyGap
	if o.Gap != nil {
		gap = *o.Gap
	}
	d := geo.V(box.W+gap, box.H+gap)
	if o.DX != nil {
		d.X = *o.DX
	}
	if o.DY != nil {
		d.Y = *o.DY
	}
	out := make([]geo.Vec, 0, n)
	for row := range rows {
		for col := range cols {
			if row != 0 || col != 0 {
				out = append(out, geo.V(float64(col)*d.X, float64(row)*d.Y))
			}
		}
	}
	return out, nil
}

// copyNames returns what names a copy's shapes: the first as asked, and what it holds
// after it, when its name began with what held it, so that "c1-title" in "c1"
// becomes "c2-title" in "c2", to be set by a name that can be worked out.
func (r *run) copyNames(asked *string) func(from doc.ID) doc.ID {
	made := map[doc.ID]doc.ID{}
	return func(from doc.ID) doc.ID {
		var id doc.ID
		if asked != nil {
			id, asked = shapeID(*asked), nil
		} else if s := r.e.Doc.Shapes[from]; s != nil && made[s.Parent] != "" {
			if rest, ok := strings.CutPrefix(string(from), string(s.Parent)+"-"); ok {
				if name := made[s.Parent] + doc.ID("-"+rest); r.e.Doc.Shapes[name] == nil {
					id = name
				}
			}
		}
		if id == "" {
			id = r.autoID()
		}
		made[from] = id
		return id
	}
}

// warnHeldArrows tells of arrows a flip can't turn, as what holds both their ends
// isn't flipping with them.
func (r *run) warnHeldArrows(ids []doc.ID) {
	flipping := map[doc.ID]bool{}
	var all []doc.ID
	for _, id := range ids {
		all = append(append(all, id), r.e.Descendants(id)...)
	}
	for _, id := range all {
		flipping[id] = true
	}
	for _, id := range all {
		if r.e.Doc.Shapes[id].Type != doc.TypeArrow {
			continue
		}
		bs, be := r.e.BindingOf(id, "start"), r.e.BindingOf(id, "end")
		if bs != nil && be != nil && !flipping[bs.To] && !flipping[be.To] {
			r.warn("%s stayed as it was: what it joins wasn't flipped; flip that too", shortID(id))
		}
	}
}

// warnLostEnds tells of arrows about to be copied without what they join: a copy
// has nothing to bind to, so its ends come out free.
func (r *run) warnLostEnds(ids []doc.ID) {
	copied := map[doc.ID]bool{}
	var all []doc.ID
	for _, id := range ids {
		all = append(append(all, id), r.e.Descendants(id)...)
	}
	for _, id := range all {
		copied[id] = true
	}
	for _, id := range all {
		if r.e.Doc.Shapes[id].Type != doc.TypeArrow {
			continue
		}
		var lost []string
		if b := r.e.BindingOf(id, "start"); b != nil && !copied[b.To] {
			lost = append(lost, "from")
		}
		if b := r.e.BindingOf(id, "end"); b != nil && !copied[b.To] {
			lost = append(lost, "to")
		}
		if len(lost) > 0 {
			r.warn("the copy of %s lost its %s, as what it joins wasn't copied; dup that too, or set them on the copy", shortID(id), strings.Join(lost, " and "))
		}
	}
}

func (r *run) group(o *op) error {
	ids, err := r.mutable(o.Group)
	if err != nil {
		return err
	}
	if len(ids) < 2 {
		return errors.New("a group needs at least 2 shapes")
	}
	id, err := r.freshID(o.ID)
	if err != nil {
		return err
	}
	r.e.Select(ids...)
	r.e.GroupAs(id)
	// A shape already in a selected group doesn't count: then too few remain.
	if r.e.Doc.Shapes[id] == nil {
		return errors.New("a group needs at least 2 shapes outside one another")
	}
	r.added = append(r.added, id)
	return nil
}

func (r *run) arrange(o *op) error {
	ids, err := r.mutable(o.Arrange)
	if err != nil {
		return err
	}
	if o.Arrange.broad() {
		// An arrow goes with what it joins.
		ids = slices.DeleteFunc(ids, func(id doc.ID) bool { return r.e.Doc.Shapes[id].Type == doc.TypeArrow })
	}
	if *o.How == "grid" {
		return r.arrangeGrid(ids, o)
	}
	r.e.Select(ids...)
	switch *o.How {
	case "distribute-h", "distribute-v":
		if len(ids) < 3 {
			return errors.New("distributing needs at least 3 shapes")
		}
		r.e.Distribute(*o.How == "distribute-h")
	default:
		if len(ids) < 2 {
			return errors.New("aligning needs at least 2 shapes")
		}
		r.e.Align(*o.How)
	}
	r.markChanged(ids)
	return nil
}

// arrangeGrid lays shapes out in cells as big as the biggest of them, in the order
// they come, from the top left of the box they share.
func (r *run) arrangeGrid(ids []doc.ID, o *op) error {
	chosen := make(map[doc.ID]bool, len(ids))
	for _, id := range ids {
		chosen[id] = true
	}
	// What holds a shape takes it along.
	ids = slices.DeleteFunc(ids, func(id doc.ID) bool { return r.heldBy(id, chosen) })
	if len(ids) < 2 {
		return errors.New("a grid needs at least 2 shapes")
	}
	cols := int(math.Ceil(math.Sqrt(float64(len(ids)))))
	switch {
	case o.Rows != nil && o.Cols != nil:
		if *o.Rows**o.Cols < len(ids) {
			return fmt.Errorf("%d rows of %d hold %d shapes, and this has %d", *o.Rows, *o.Cols, *o.Rows**o.Cols, len(ids))
		}
		cols = *o.Cols
	case o.Cols != nil:
		cols = *o.Cols
	case o.Rows != nil:
		cols = (len(ids) + *o.Rows - 1) / *o.Rows
	}
	gap := copyGap
	if o.Gap != nil {
		gap = *o.Gap
	}
	var box geo.Box
	cell := geo.Vec{}
	for i, id := range ids {
		if s := r.e.Doc.Shapes[id]; s.Type == doc.TypeArrow {
			return fmt.Errorf("%s is an arrow, which goes with what it joins; leave it out of a grid", shortID(id))
		}
		b := r.e.PageBounds(id)
		if i == 0 {
			box = b
		} else {
			box = box.Union(b)
		}
		cell = geo.V(math.Max(cell.X, b.W), math.Max(cell.Y, b.H))
	}
	for i, id := range ids {
		b := r.e.PageBounds(id)
		at := geo.V(box.X+float64(i%cols)*(cell.X+gap), box.Y+float64(i/cols)*(cell.Y+gap))
		r.e.MoveBy(id, geo.V(at.X-b.X, at.Y-b.Y))
	}
	r.markChanged(ids)
	return nil
}

func (r *run) setCamera(o *op) error {
	r.camera = true
	t := o.Camera
	if !t.broad() && len(t.list) == 1 && t.list[0] == "fit" {
		if !r.dry {
			r.e.ZoomToFit()
		}
		return nil
	}
	if !t.broad() && len(t.list) == 0 {
		return errors.New(`camera takes "fit" or ids`)
	}
	ids, err := r.pick(t, true)
	if err != nil {
		return err
	}
	var box geo.Box
	for i, id := range ids {
		if i == 0 {
			box = r.e.PageBounds(id)
		} else {
			box = box.Union(r.e.PageBounds(id))
		}
	}
	if !r.dry {
		r.e.ZoomToBox(box, 2)
	}
	return nil
}

func findPage(e *editor.Editor, name string) *doc.Page {
	for _, p := range e.Pages() {
		if p.Name == name || p.ID == doc.ID(name) || p.ID == doc.ID("page:"+name) {
			return p
		}
	}
	return nil
}

func (r *run) page(o *op) error {
	name := strings.TrimSpace(*o.Page)
	if name == "" {
		return errors.New("page needs a name")
	}
	p := findPage(r.e, name)
	if p == nil {
		r.e.NewPage()
		r.e.RenamePage(r.e.Page, name)
	} else {
		r.e.SetPage(p.ID)
	}
	if o.Name != nil {
		if strings.TrimSpace(*o.Name) == "" {
			return errors.New("name must not be blank")
		}
		r.e.RenamePage(r.e.Page, *o.Name)
	}
	return nil
}

// --- what came of it ---

// report lists what was made, with where it ended up, for what comes next to
// be placed from.
func (r *run) report() string {
	const maxLines = 200
	r.added = slices.DeleteFunc(r.added, func(id doc.ID) bool { return r.e.Doc.Shapes[id] == nil })
	var b strings.Builder
	p := r.e.Doc.Page(r.e.Page)
	if r.dry {
		fmt.Fprintf(&b, "dry run, nothing changed: page %q\n", p.Name)
	} else {
		fmt.Fprintf(&b, "ok page %q\n", p.Name)
	}
	// What it made first, then what it changed, as each now is.
	added := make(map[doc.ID]bool, len(r.added))
	for _, id := range r.added {
		added[id] = true
	}
	var changed []doc.ID
	for id := range r.changed {
		if r.e.Doc.Shapes[id] != nil && !added[id] {
			changed = append(changed, id)
		}
	}
	slices.Sort(changed)
	if r.quiet {
		if len(r.added) > 0 {
			fmt.Fprintf(&b, "added %d: %s\n", len(r.added), idRanges(r.added))
		}
	} else if list := slices.Concat(r.added, changed); len(list) > 0 {
		for i, id := range list {
			if i == maxLines {
				fmt.Fprintf(&b, "+%d more, which read lists\n", len(list)-maxLines)
				break
			}
			if i == len(r.added) {
				b.WriteString("changed:\n")
			}
			fmt.Fprintf(&b, "%s %s\n", shortID(id), boxText(boxOf(r.e, id)))
		}
	}
	if len(changed) > 0 && r.quiet {
		fmt.Fprintf(&b, "changed %d\n", len(changed))
	}
	if r.deleted > 0 {
		if r.deletedInside > 0 {
			fmt.Fprintf(&b, "deleted %d, %d of them inside what you named\n", r.deleted, r.deletedInside)
		} else {
			fmt.Fprintf(&b, "deleted %d\n", r.deleted)
		}
	}
	if box, ok := r.e.ContentBounds(); ok {
		fmt.Fprintf(&b, "page bounds %s\n", boxText(box))
	}
	if len(r.warnings) > 0 {
		b.WriteString("warnings:\n")
		for i, w := range r.warnings {
			if i == maxWarnings {
				more := strconv.Itoa(len(r.warnings) - maxWarnings)
				if r.capped {
					more = "at least " + more
				}
				fmt.Fprintf(&b, "and %s more; read with check lists the page's\n", more)
				break
			}
			b.WriteString(w)
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// idRanges lists ids compactly: a run of ids an agent didn't choose, as s4-s9.
func idRanges(ids []doc.ID) string {
	number := func(id doc.ID) (int, bool) { return autoNumber(shortID(id)) }
	var out []string
	for i := 0; i < len(ids); {
		end := i
		if n, ok := number(ids[i]); ok {
			for end+1 < len(ids) {
				next, ok := number(ids[end+1])
				if !ok || next != n+end+1-i {
					break
				}
				end++
			}
		}
		if end-i >= 2 {
			out = append(out, shortID(ids[i])+"-"+shortID(ids[end]))
		} else {
			for k := i; k <= end; k++ {
				out = append(out, shortID(ids[k]))
			}
		}
		i = end + 1
	}
	return strings.Join(out, " ")
}

// reveal brings what was just added into view, so that it can be watched.
func (r *run) reveal() {
	if r.camera || len(r.added) == 0 {
		return
	}
	view := r.e.ViewBox()
	for _, id := range r.added {
		if r.e.PageOf(id) == r.e.Page && !view.ContainsBox(r.e.PageBounds(id)) {
			r.e.ZoomToFit()
			return
		}
	}
}
