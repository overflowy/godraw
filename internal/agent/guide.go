package agent

import (
	"fmt"
	"slices"
	"strings"

	"godraw/internal/editor"
)

// The guide is told in parts, so that an agent reads only what it needs. Each
// example in it runs in the tests.
var guideTopics = map[string]string{
	"": `Units are page pixels, y down. Later shapes paint over earlier ones.
Work: apply ops, view to check, set to fix. read shows what is already there.
Each apply is one undo step, and an error in it changes nothing. It answers with each new shape's box, to place the next from, then warnings of what looks wrong. Fix those.
Arguments of apply, beside ops: "dry":true gives the same answer, changes nothing, and ends with a token. "commit":TOKEN, in place of ops, then applies those ops, from the same page. "quiet":true leaves out each shape's box.
With several drawings open, read lists them; pass doc to pick one, or get the frontmost.
read says if the window is light or dark; view can show either.
Only the person at the app can save or name a drawing, from the File menu, so tell them when you finish. Quitting keeps unsaved drawings for the next launch.
Read only the topics you need: shapes (kinds and their fields), styles (colors, fills, fonts), place (positions), layout (text sizes, warnings, cards, containers), arrows (ends, elbows, routes), paths (curves, free forms), edit (changing what exists).`,

	"shapes": `Add with {"add":KIND,...}. Fields for each kind, besides those of topic place:
geo (` + strings.Join(editor.GeoKinds, " ") + `): w h text color labelColor fill dash size font align valign url. Without w,h it fits its text.
text: text w(wraps) color size font align scale. Without w it stays on one line per \n.
note: text color labelColor size font align valign scale url. 200 square, grows with its text.
card: title body w h color labelColor fill dash size font align. Title and wrapped body in a box that fits. See layout.
frame: name w h color size font. Clips what is in it. Put shapes in with "in". An arrow from inside to outside stays on the page, unclipped; one with both ends inside is clipped too, unless "in":"" takes it out. size gives its name a card title's size and its border the color; font is its name's.
image: src(file path or base64 data: URL; PNG, JPEG, GIF) w h alt url.
draw: d w h color fill dash size. Any path; fill shows when it closes. See paths.
highlight: d w h color size. Marker over other shapes.
line: d w h color dash size spline. Draggable vertices; spline:"cubic" curves through them.
arrow: from to, or x1 y1 x2 y2, and more: see arrows.
Example: [{"add":"rectangle","id":"a","text":"API","color":"blue","fill":"solid"},{"add":"ellipse","id":"b","text":"DB","right":"a"},{"add":"arrow","from":"a","to":"b","text":"reads"}]`,

	"styles": `color: ` + strings.Join(styleEnums["color"], " ") + ` (a fixed palette that adapts to dark mode; no hex). labelColor is the same list, for labels.
fill: ` + strings.Join(styleEnums["fill"], " ") + `. semi is plain paper (near black in the dark theme), solid a tint of the color, pattern and lined-fill hatch it, fill the color itself.
dash: ` + strings.Join(styleEnums["dash"], " ") + `. draw is hand-drawn; solid is crisp.
size: ` + strings.Join(styleEnums["size"], " ") + ` (stroke and text). font: ` + strings.Join(styleEnums["font"], " ") + `. scale multiplies text and notes.
align, valign: start middle end. opacity: 0 to 1. spline: line cubic.
arrow kind: arc elbow. head, tail: ` + strings.Join(styleEnums["head"], " ") + `.
{"style":{...}} sets defaults for the rest of the ops in that apply: [{"style":{"color":"blue","dash":"solid"}},{"add":"star","w":80,"h":80}]`,

	"place": `Every shape can say id in x y rot opacity locked right left above below gap. Arrows use their ends instead.
x,y are the top left of its box in page coordinates, always, even inside a frame.
right,left,above,below name a shape to sit beside, centered on it, gap apart (default 120). x or y overrides the other axis.
Neither: under what is already on the page, or in the frame. Paths stay at their own coordinates.
rot is degrees clockwise in the page, around its center. in names a frame or group to go in.
id is yours to choose, unique in the document; without one you get s1, s2...
Example: [{"add":"frame","id":"f","name":"Plan","x":0,"y":0,"w":500,"h":300},{"add":"note","id":"n","text":"Idea","in":"f","x":40,"y":60,"color":"yellow"},{"add":"text","text":"Then","right":"n","gap":24}]`,

	"layout": `Font px at size s m l xl: labels ` + sizes(editor.LabelSize) + `; text shapes ` + sizes(editor.TextSize) + `; arrow labels ` + sizes(editor.ArrowLabelSize) + `. A line is ` + num(editor.LineHeight) + ` times that high. A character averages, times that: draw 0.51, sans 0.44, serif 0.45, mono 0.62. Labels sit ` + num(editor.LabelPadding) + ` in from the box.
A box keeps its w: text wraps, and the box grows past h to hold it. Without h it fits.
To measure exactly, apply with "dry":true: {"add":"rectangle","text":"Hi","w":200} answers with the box it would get, {"add":"text","text":"Hi"} with the text's size. Try whole layouts the same way.
Warnings leave alone a box wholly inside another, unless it covers text, and overlaps of shapes without text. They speak of what that apply touched; read with "check":true lists those of the whole page, to see that a finished page is clean.
A label has one style. For a title and body, add a card: color is its box and title, labelColor its body, size its title, with the body smaller. Without w it fits, up to ` + num(cardMaxW) + ` wide. Set title, body, w or h and it sizes itself again. Arrows bind to it by its id.
To hold shapes without clipping them, group them with a rectangle behind them.
Example: [{"add":"card","id":"auth","title":"Auth","body":"Sign-in and sessions","color":"violet","x":0,"y":0},{"add":"rectangle","id":"db","text":"DB","right":"auth"},{"add":"arrow","from":"auth","to":"db","straight":"h","label":"above","text":"reads"}]`,

	"arrows": `arrow: from to fromAt toAt x1 y1 x2 y2 text color labelColor fill dash size font kind bend via head tail straight at label.
from,to bind to a shape, and it follows when that shape moves. Free ends use x1,y1 and x2,y2.
A group binds by the shape that is most of it, as a path's main stroke; if none is, the error names them.
fromAt,toAt are [x,y], 0 to 1 across the shape; the default is its center.
kind:"arc", the default, curves by bend, px off straight. kind:"elbow" runs in right angles, and takes no bend.
An elbow's anchor on an edge sets the side it leaves or arrives by: [0.5,0] top, [0.5,1] bottom, [0,0.5] left, [1,0.5] right.
via:[[x,y],...] are page points an elbow passes through; one often gets around a shape. They follow when both ends move alike; else they stay, or go if they only add turns to a clear route. via:[] clears them.
straight:"h" or "v" on an arrow with from and to runs it level where both shapes share height (or width), and keeps it so as they move or grow. at, 0 to 1, picks where: 0.25 and 0.75 for a pair. straight:"off" or fromAt,toAt end it.
label:"above" takes an arrow's label off its line; "center" puts it back.
Warnings tell of an arrow through a box, along its edge or over a frame's name, of a route crossing itself, and of arrows sharing a line; from one anchor, a trunk a tree may mean.
Example: [{"add":"card","id":"a","title":"A","x":0,"y":0},{"add":"card","id":"m","title":"M","right":"a"},{"add":"card","id":"b","title":"B","right":"m"},{"add":"arrow","from":"a","to":"b","kind":"elbow","fromAt":[0.5,0],"toAt":[0.5,0],"via":[[400,-60]]}]`,

	"paths": `d is an SVG path: M L H V C S Q T A Z, absolute or relative. "M0 0 C40 80 80 -80 120 0"
It uses page coordinates unless x,y or a relative place moves it. w or h scales it; one of them keeps proportions.
Close with Z to fill. Each M after the first makes a separate stroke, id-1, id-2 and on, and they come back as a group with the id; holes don't cut out, so paint them over.
Curves are cheap: prefer C and A to many L. Example: [{"add":"draw","id":"blob","d":"M0 40C0 0 80 0 80 40S0 80 0 40Z","fill":"solid","color":"violet","x":100,"y":100,"w":160}]`,

	"edit": `Each op has one verb. A list of ids or one id both work.
{"set":IDS,...} changes any field the shape takes: styles, text, name, w h, x y, dx dy, rot, in (empty leaves the frame), locked, d, and an arrow's ends (empty from or to frees it). Styles on a group reach what it holds.
One x or y lines several shapes up. {"del":IDS} deletes, {"del":"*"} clears the page.
{"dup":IDS,"dx":0,"dy":0,"n":1} copies; each copy is offset one more step, beside the original by default. A copied arrow stays bound only to what is copied with it.
{"group":IDS,"id":"g"} {"ungroup":IDS}
{"arrange":IDS,"how":HOW} ` + strings.Join(verbHows["arrange"], " ") + `
{"order":IDS,"how":HOW} ` + strings.Join(verbHows["order"], " ") + `
{"flip":IDS,"how":"h"} or "v"
{"page":"Name"} goes to that page, making it if new; add "name" to rename it. {"delPage":"Name"}
{"camera":"fit"} or {"camera":IDS} moves the view. New shapes out of view bring it along otherwise.
Example: [{"add":"star","id":"t","x":0,"y":0,"w":40,"h":40,"color":"yellow","fill":"fill"},{"dup":"t","dx":60,"n":4},{"set":"t","rot":20}]`,
}

// sizes lists a font size at each size style.
func sizes(of func(string) float64) string {
	out := make([]string, len(styleEnums["size"]))
	for i, size := range styleEnums["size"] {
		out[i] = num(of(size))
	}
	return strings.Join(out, " ")
}

// guide returns a topic of the guide.
func guide(topic string) (string, error) {
	text, ok := guideTopics[topic]
	if !ok {
		var names []string
		for name := range guideTopics {
			if name != "" {
				names = append(names, name)
			}
		}
		slices.Sort(names)
		return "", fmt.Errorf("no topic %q; use %s", topic, strings.Join(names, ", "))
	}
	return text, nil
}
