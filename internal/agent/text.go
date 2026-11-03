package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"godraw/internal/doc"
)

const (
	runsHelp = `a text is a string, or a list of strings and runs, as ["10",{"text":"-34","sup":true}]`
	// How far a run's scale goes: past this it's no longer a part of the same text.
	minRunScale, maxRunScale = 0.25, 4.0
)

// textValue is what text, title and body say: plain text, or runs in styles of their
// own.
type textValue struct {
	plain string
	runs  []doc.Run
}

func (t *textValue) UnmarshalJSON(b []byte) error {
	if err := json.Unmarshal(b, &t.plain); err == nil {
		return nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(b, &items); err != nil {
		return errors.New(runsHelp)
	}
	if len(items) == 0 {
		return errors.New(runsHelp)
	}
	styled := false
	for _, item := range items {
		var plain string
		if json.Unmarshal(item, &plain) == nil {
			t.runs = append(t.runs, doc.Run{Text: plain})
			continue
		}
		d := json.NewDecoder(bytes.NewReader(item))
		d.DisallowUnknownFields()
		var r doc.Run
		if err := d.Decode(&r); err != nil {
			return fmt.Errorf(`%s, each run taking text font color scale sup sub: %s`, runsHelp, strings.TrimPrefix(err.Error(), "json: "))
		}
		if err := checkRun(&r); err != nil {
			return err
		}
		t.runs = append(t.runs, r)
		styled = true
	}
	if !styled {
		var out strings.Builder
		for _, r := range t.runs {
			out.WriteString(r.Text)
		}
		t.plain, t.runs = out.String(), nil
	}
	return nil
}

func checkRun(r *doc.Run) error {
	if r.Text == "" {
		return errors.New(`a run needs text`)
	}
	if r.Font != "" && !slices.Contains(styleEnums["font"], r.Font) {
		return fmt.Errorf("run font %q: use %s", r.Font, strings.Join(styleEnums["font"], ", "))
	}
	if r.Color != "" && !isColor(r.Color) {
		return fmt.Errorf("run color %q: use a color of the palette, or #rrggbb", r.Color)
	}
	if r.Scale != 0 && (r.Scale < minRunScale || r.Scale > maxRunScale) {
		return fmt.Errorf("run scale must be from %s to %s", ratio(minRunScale), ratio(maxRunScale))
	}
	if r.Sup && r.Sub {
		return errors.New("use one of sup, sub")
	}
	return nil
}

// isEmpty says whether it says nothing, or there's nothing to say.
func (t *textValue) isEmpty() bool { return t == nil || (t.plain == "" && len(t.runs) == 0) }

// setOn gives a shape this text.
func (t *textValue) setOn(s *doc.Shape) {
	if len(t.runs) > 0 {
		s.SetRuns(t.runs)
		return
	}
	s.SetText(t.plain)
}

// with is what copies of a shape differ by: for all of them, or a list with one for
// each.
type with []*op

func (w *with) UnmarshalJSON(b []byte) error {
	var items []json.RawMessage
	if json.Unmarshal(b, &items) != nil {
		items = []json.RawMessage{b}
	}
	if len(items) == 0 {
		return errors.New(`with is what a copy changes, as {"title":"B"}, or a list of those, one for each copy`)
	}
	for i, item := range items {
		o, err := decodeWith(item)
		if err != nil {
			if len(items) > 1 {
				return fmt.Errorf("with[%d]: %w", i, err)
			}
			return fmt.Errorf("with: %w", err)
		}
		*w = append(*w, o)
	}
	return nil
}

// decodeWith reads what a copy changes. It's checked against what the copy takes as
// the batch runs, as a set is.
func decodeWith(raw json.RawMessage) (*op, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	o := &op{}
	if err := d.Decode(&o.fields); err != nil {
		if wrong, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
			return nil, wrongType(wrong)
		}
		return nil, errors.New(strings.TrimPrefix(err.Error(), "json: "))
	}
	if err := checkValues(&o.fields); err != nil {
		return nil, err
	}
	if o.Src != nil {
		return nil, errors.New("an image's src can't change; add a new image")
	}
	if err := o.readPath(); err != nil {
		return nil, err
	}
	return o, nil
}
