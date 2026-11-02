package agent

import (
	"errors"
	"math"
	"slices"

	"godraw/internal/doc"
	"godraw/internal/editor"
	"godraw/internal/geo"
)

// A card is a title and body in a box: a group of a rectangle and two text
// shapes, each knowing its part by its meta. A label has one style, and drawing
// this by hand takes many ops and offsets worked out from guessed text sizes.
// To everything else it is an ordinary group.

const (
	cardKind = "card"
	cardPad  = 16.0
	cardGap  = 6.0
	cardMinW = 160.0
	cardMaxW = 360.0
	// What makes a body smaller than a title with no smaller size to go to.
	cardSmallBody = 0.8
)

// card is the parts of a card. A card may lack a title or a body.
type card struct {
	group, box, head, body *doc.Shape
}

// cardOf returns the parts of a card, or nil if id isn't one.
func cardOf(e *editor.Editor, id doc.ID) *card {
	s := e.Doc.Shapes[id]
	if s == nil || s.Type != doc.TypeGroup {
		return nil
	}
	c := &card{group: s}
	for _, k := range e.Children(id) {
		part := e.Doc.Shapes[k]
		var role string
		if !part.MetaValue(doc.MetaCard, &role) {
			continue
		}
		switch role {
		case "box":
			c.box = part
		case "head":
			c.head = part
		case "body":
			c.body = part
		}
	}
	if c.box == nil {
		return nil
	}
	return c
}

func (r *run) cardOf(id doc.ID) *card { return cardOf(r.e, id) }

// ownerOf returns the card a part belongs to, whose id is the one an agent
// chose, or else the shape itself.
func ownerOf(e *editor.Editor, id doc.ID) doc.ID {
	s := e.Doc.Shapes[id]
	var role string
	if s != nil && s.MetaValue(doc.MetaCard, &role) {
		if p := e.Doc.Shapes[s.Parent]; p != nil && p.Type == doc.TypeGroup {
			return p.ID
		}
	}
	return id
}

// smaller returns the size style a step below, for a card's body.
func smaller(size string) string {
	sizes := styleEnums["size"]
	return sizes[max(slices.Index(sizes, size)-1, 0)]
}

// styleBody gives a card's body the size for a title of a size style: a step
// below it, or at the smallest, the same style scaled down.
func styleBody(body *doc.Shape, title string) {
	body.Size = smaller(title)
	body.Scale = 1
	if body.Size == title {
		body.Scale = cardSmallBody
	}
}

// stylePart applies to a card's part what its fields say of that part.
func stylePart(part *doc.Shape, role string, f *fields) {
	switch role {
	case "box":
		assign(part, &fields{Color: f.Color, Fill: f.Fill, Dash: f.Dash, Opacity: f.Opacity})
	case "head":
		assign(part, &fields{Color: f.Color, Font: f.Font, Size: f.Size, Align: f.Align, Opacity: f.Opacity})
	case "body":
		assign(part, &fields{Color: f.LabelColor, Font: f.Font, Align: f.Align, Opacity: f.Opacity})
		if f.Size != nil {
			styleBody(part, *f.Size)
		}
	}
}

// newCardText adds a title or body to a card's parent.
func (r *run) newCardText(id, parent doc.ID, role, text string, f *fields) *doc.Shape {
	s := doc.NewShape(id, doc.TypeText)
	s.Parent, s.Index = parent, r.e.TopIndex(parent)
	s.AutoSize = false
	if role == "body" {
		styleBody(s, s.Size)
	}
	stylePart(s, role, f)
	s.SetText(text)
	s.SetMeta(doc.MetaCard, role)
	r.e.Put(s)
	return s
}

func (r *run) addCard(o *op) error {
	f := r.style
	merge(&f, &o.fields)
	if (f.Title == nil || *f.Title == "") && (f.Body == nil || *f.Body == "") {
		return errors.New("a card needs a title or body")
	}
	id, err := r.freshID(o.ID)
	if err != nil {
		return err
	}
	parent, err := r.parentOf(o.In)
	if err != nil {
		return err
	}
	part := func(suffix string) (doc.ID, error) {
		pid := id + doc.ID("-"+suffix)
		if r.e.Doc.Shapes[pid] != nil {
			return "", errors.New("id " + shortID(pid) + " is taken")
		}
		return pid, nil
	}
	spot := r.freeSpot(parent, "")

	boxID, err := part("box")
	if err != nil {
		return err
	}
	box := doc.NewShape(boxID, doc.TypeGeo)
	box.Geo, box.Size = "rectangle", "s"
	box.Parent, box.Index = parent, r.e.TopIndex(parent)
	stylePart(box, "box", &f)
	box.SetMeta(doc.MetaCard, "box")
	if o.H != nil {
		box.SetMeta(doc.MetaCardH, *o.H)
		r.asked[boxID] = *o.H
	}
	r.e.Put(box)

	ids := []doc.ID{boxID}
	c := &card{box: box}
	if f.Title != nil && *f.Title != "" {
		pid, err := part("head")
		if err != nil {
			return err
		}
		c.head = r.newCardText(pid, parent, "head", *f.Title, &f)
		ids = append(ids, pid)
	}
	if f.Body != nil && *f.Body != "" {
		pid, err := part("body")
		if err != nil {
			return err
		}
		c.body = r.newCardText(pid, parent, "body", *f.Body, &f)
		ids = append(ids, pid)
	}
	w := r.naturalCardW(c)
	if o.W != nil {
		w = *o.W
	}
	r.fitCard(c, w)

	// Now that its size is known, it goes where it was asked to, and the rest
	// follows the box.
	c.box = r.e.Doc.Shapes[boxID]
	at, err := r.topLeft(&o.fields, c.box.W, c.box.H, func() geo.Vec { return spot })
	if err != nil {
		return err
	}
	r.placeBox(boxID, at, r.e.PageMat(parent).Rotation())
	c.box = r.e.Doc.Shapes[boxID]
	r.refreshParts(c)
	r.fitCard(c, c.box.W)

	r.e.Select(ids...)
	r.e.GroupAs(id)
	if o.Locked != nil {
		g := r.e.Doc.Shapes[id].Clone()
		g.Locked = *o.Locked
		r.e.Put(g)
	}
	if o.Rot != nil {
		r.turn(id, *o.Rot*math.Pi/180)
	}
	r.added = append(r.added, id)
	return nil
}

// refreshParts takes the latest of a card's texts, which fitting replaces.
func (r *run) refreshParts(c *card) {
	if c.head != nil {
		c.head = r.e.Doc.Shapes[c.head.ID]
	}
	if c.body != nil {
		c.body = r.e.Doc.Shapes[c.body.ID]
	}
}

// naturalCardW returns how wide a card holds its texts on a line each, within
// reason.
func (r *run) naturalCardW(c *card) float64 {
	w := 0.0
	for _, t := range []*doc.Shape{c.head, c.body} {
		if t != nil {
			w = math.Max(w, r.e.Measure(t.Text, t.Font, editor.TextSize(t.Size)*t.Scale, 0).W)
		}
	}
	return math.Max(cardMinW, math.Min(w+2*cardPad+1, cardMaxW))
}

// fitCard lays a card out at a width: its texts wrap inside the box, in order,
// and the box is as tall as they need or as was asked for.
func (r *run) fitCard(c *card, w float64) {
	box := c.box.Clone()
	y := box.Y + cardPad
	for _, t := range []*doc.Shape{c.head, c.body} {
		if t == nil {
			continue
		}
		k := t.Clone()
		k.AutoSize = false
		k.W = math.Max(w-2*cardPad, 8) / k.Scale
		k.X, k.Y, k.Rotation = box.X+cardPad, y, 0
		r.e.Put(k)
		y += r.e.Geometry(k).Bounds.H + cardGap
	}
	h := y - cardGap + cardPad - box.Y
	var asked float64
	if box.MetaValue(doc.MetaCardH, &asked) {
		h = math.Max(h, asked)
	}
	box.W, box.H, box.GrowY = w, h, 0
	r.e.Put(box)
	c.box = box
	r.refreshParts(c)
}

// setCard does what fields say of a card's parts, and returns what is left for
// the card as a group: where it goes, and how it turns.
func (r *run) setCard(c *card, f *fields) (*fields, error) {
	if f.Text != nil {
		return nil, errors.New("a card has title and body, not text")
	}
	rest := *f
	rest.Title, rest.Body, rest.W, rest.H = nil, nil, nil, nil
	rest.Color, rest.LabelColor, rest.Fill, rest.Dash, rest.Size, rest.Font, rest.Align = nil, nil, nil, nil, nil, nil, nil

	parts := []struct {
		role string
		part **doc.Shape
		text *string
	}{{"box", &c.box, nil}, {"head", &c.head, f.Title}, {"body", &c.body, f.Body}}
	for _, p := range parts {
		switch {
		case p.text != nil && *p.text == "" && *p.part != nil:
			other := c.head
			if p.role == "head" {
				other = c.body
			}
			if other == nil {
				return nil, errors.New("a card needs a title or body")
			}
			r.e.Select((*p.part).ID)
			r.e.Delete()
			*p.part = nil
		case p.text != nil && *p.text == "":
		case *p.part != nil:
			k := (*p.part).Clone()
			stylePart(k, p.role, f)
			if p.text != nil {
				k.SetText(*p.text)
			}
			r.e.Put(k)
			*p.part = k
		case p.text != nil:
			*p.part = r.newCardText(r.autoID(), c.group.ID, p.role, *p.text, f)
		}
	}
	w := c.box.W
	if f.W != nil {
		w = *f.W
	}
	if f.H != nil {
		k := c.box.Clone()
		k.SetMeta(doc.MetaCardH, *f.H)
		r.e.Put(k)
		c.box = k
		r.asked[k.ID] = *f.H
	}
	r.fitCard(c, w)
	return &rest, nil
}
