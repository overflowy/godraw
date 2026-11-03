package agent

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"godraw/internal/editor"
)

// The guide is told in parts, so that an agent reads only what it needs. Each
// example in it runs in the tests.
var guideTopics = map[string]string{
	"": `Units are page pixels, y down. Later shapes paint over earlier ones.
Work: apply ops, view to check, set to fix. read shows what is already there.
Each apply is one undo step, and an error in it changes nothing. It answers with each new shape's box, to place the next from, then warnings of what looks wrong. Fix those.
An apply with several errors lists them all. Arguments of apply, beside ops: "dry":true gives the same answer, changes nothing, and ends with a token. "commit":TOKEN, in place of ops, then applies those ops, from the same page, and refuses if the drawing changed in between, as its answer may no longer hold. "quiet":true lists only ids, not boxes.
With several drawings open, read lists them; pass doc to pick one, or get the frontmost.
read says if the window is light or dark; view can show either.
Only the person at the app can save or name a drawing, from the File menu, so tell them when you finish; export writes a picture of it to a PNG or SVG file you name. Quitting keeps unsaved drawings for the next launch.
Read only the topics you need: shapes (kinds and their fields), styles (colors, fills, fonts), looks (corners, shadows, glows, mixed text), reuse (copies with changes, named styles), grid (rows and columns), place (positions), layout (text sizes, warnings, cards, containers), arrows (ends, elbows, routes), paths (curves, formulas, free forms), edit (changing what exists), select (many shapes at once, by run, kind or page).`,

	"shapes": `Add with {"add":KIND,...}. Fields for each kind, besides those of topic place:
geo (` + strings.Join(editor.GeoKinds, " ") + `): w h text color labelColor fill dash size font align valign radius(rectangle only) shadow url. Without w,h it fits its text.
text: text w(wraps) color size font align scale. Without w it stays on one line per \n.
note: text color labelColor size font align valign scale shadow url. 200 square, grows with its text.
card: title body w h color labelColor fill dash size font align radius shadow. Title and wrapped body in a box that fits. See layout.
frame: name w h color size font. Clips what is in it. Put shapes in with "in". An arrow from inside to outside stays on the page, unclipped; one with both ends inside is clipped too, unless "in":"" takes it out. size gives its name a card title's size and its border the color; font is its name's.
image: src(file path or base64 data: URL; PNG, JPEG, GIF) w h alt shadow url.
draw: d or fn x1 x2, w h color fill dash size shadow. Any path or formula; fill shows when it closes. See paths.
highlight: d w h color size. Marker over other shapes.
line: d w h color dash size spline shadow. Draggable vertices; spline:"cubic" curves through them.
arrow: from to, or x1 y1 x2 y2, and more: see arrows.
Example: [{"add":"rectangle","id":"a","text":"API","color":"blue","fill":"solid"},{"add":"ellipse","id":"b","text":"DB","right":"a"},{"add":"arrow","from":"a","to":"b","text":"reads"}]`,

	"styles": `color: ` + strings.Join(styleEnums["color"], " ") + ` (a palette that adapts to dark mode), or "#rrggbb", the same in both themes. labelColor is the same, for labels.
fill: ` + strings.Join(styleEnums["fill"], " ") + `, or "#rrggbb". semi is plain paper (near black in the dark theme), solid a tint of the color, pattern and lined-fill hatch it, fill the color itself. A hex fills with that color whatever the outline's: {"color":"#7aa2ff","fill":"#10163a"}.
gradient:["blue","#ffffff"] fills with 2 to ` + strconv.Itoa(maxGradient) + ` colors, evenly spaced; [] clears it, as does a fill. gradientAngle is in degrees, as in CSS: 180, the default, runs top to bottom, 90 left to right.
dash: ` + strings.Join(styleEnums["dash"], " ") + `. draw is hand-drawn; solid is crisp; none has no outline, for shapes that fill.
size: ` + strings.Join(styleEnums["size"], " ") + ` (stroke and text). font: ` + strings.Join(styleEnums["font"], " ") + `. scale multiplies text and notes.
align, valign: start middle end. opacity: 0 to 1. spline: line cubic.
arrow kind: arc elbow. head, tail: ` + strings.Join(styleEnums["head"], " ") + `.
{"style":{...}} sets defaults for the rest of the ops in that apply: [{"style":{"color":"blue","dash":"solid"}},{"add":"star","w":80,"h":80}]. To keep them for later applies, name them: see reuse.`,

	"looks": `radius rounds a rectangle's or card's corners, in px: {"add":"rectangle","radius":12,"w":120,"h":60}. 0 squares them.
shadow:{"x":0,"y":4,"blur":12,"color":"#000000","opacity":0.35}, each optional, on geo, card, note, draw, line and image. No offset and a bright color is a glow: {"blur":24,"color":"#7aa2ff","opacity":0.7}. It stays under the shape if that fills; one that doesn't, or a line, glows along its line. A shadow with no blur or offset clears it.
text, title and body take a string, or a list of strings and runs, to mix styles: ["E = mc",{"text":"2","sup":true}]. A run takes text font color scale sup sub; what it leaves out is the shape's. scale, ` + ratio(minRunScale) + ` to ` + ratio(maxRunScale) + `, multiplies its size. Lines break at spaces, not inside a word that spans runs.
Example: [{"add":"card","title":"Energy","body":["E = mc",{"text":"2","sup":true,"color":"violet"}],"radius":10,"fill":"#10163a","color":"#7aa2ff","labelColor":"#ffffff","shadow":{"blur":24,"color":"#7aa2ff"},"x":0,"y":0}]`,

	"reuse": `dup copies with changes, for many things of one form: with is a list of what each copy changes, one copy per item, each dx further on. It takes what set takes, and an id. A single object changes every copy (n says how many). Without with, id names a single copy.
A shape in a group named from it, as c1-title in c1, copies as c2-title in c2, for a set to reach: {"set":"c2-title","text":"Two"}. A card's parts are c1-head, c1-body and c1-box.
Named styles last across applies, for this drawing while the app runs, and read lists them: {"style":{"font":"serif","size":"s","scale":0.85,"color":"#c9d1ff"},"name":"body"} defines one, then "use":"body" on add or set or in with lays it under the op's own fields, leaving out what a kind doesn't take. A style can use another, and {"style":{"use":"body"}} makes one the batch's defaults. style takes color labelColor fill gradient gradientAngle dash size font bodyFont align valign padding radius shadow scale spline opacity.
Example: [{"style":{"font":"serif","size":"s","color":"blue"},"name":"body"},{"add":"text","id":"t1","text":"One","use":"body","x":0,"y":0},{"dup":"t1","dy":40,"with":[{"id":"t2","text":"Two"},{"id":"t3","text":"Three","color":"red"}]}]`,

	"grid": `{"dup":IDS,"rows":3,"cols":4} copies into a grid, row by row, the original as its top left cell, so this makes 11 copies. One of rows, cols is a line. Cells are the size of what's copied with gap between, 20 unless said, or dx and dy from one cell to the next. with lists what each copy changes, or a single object changes them all.
{"arrange":IDS,"how":"grid","cols":4} lays out shapes that exist, in the order named, from the top left of their box, in cells as big as the biggest, gap apart. rows or cols says how many; neither is near square.
Name shapes to go on by a run: a dup quiet answers s1-s11, and {"set":"s1-s11","color":"red"} changes them. See select.
Example: [{"add":"rectangle","id":"cell","x":0,"y":0,"w":40,"h":40,"color":"blue"},{"dup":"cell","rows":3,"cols":4,"gap":8},{"set":{"kind":"rectangle"},"fill":"solid"}]`,

	"place": `Every shape can say id in x y rot opacity locked right left above below gap behind over. Arrows use their ends instead.
x,y are the top left of its box in page coordinates, always, even inside a frame.
right,left,above,below name a shape to sit beside, centered on it, gap apart (default 120). x or y overrides the other axis.
Neither: under what is already on the page, or in the frame. Paths stay at their own coordinates.
rot is degrees clockwise in the page, around its center. in names a frame or group to go in.
behind,over name a shape to paint just under or above; without them a new shape is on top.
id is yours to choose, unique in the document; without one you get s1, s2...
Example: [{"add":"frame","id":"f","name":"Plan","x":0,"y":0,"w":500,"h":300},{"add":"note","id":"n","text":"Idea","in":"f","x":40,"y":60,"color":"yellow"},{"add":"text","text":"Then","right":"n","gap":24}]`,

	"layout": `Font px at size s m l xl: labels ` + sizes(editor.LabelSize) + `; text shapes ` + sizes(editor.TextSize) + `; arrow labels ` + sizes(editor.ArrowLabelSize) + `. A line is ` + ratio(editor.LineHeight) + ` times that high. A character averages, times that: ` + charWidths(func(c CharWidth) float64 { return c.Sentence }) + `; a short title up to ` + charWidths(func(c CharWidth) float64 { return c.Title }) + `. Labels sit ` + num(editor.LabelPadding) + ` in from the box; padding sets that on a shape or note: a 44 wide badge keeps its size with padding:0.
A box keeps its w: text wraps, and the box grows past h to hold it. Without h it fits.
To measure exactly, apply with "dry":true: {"add":"rectangle","text":"Hi","w":200} answers with the box it would get, {"add":"text","text":"Hi"} with the text's size.
Warnings tell of text or a line that hardly shows in either theme, of arrows through a box or sharing a line, and of more. They leave alone a box wholly inside another, unless it covers text, and overlaps of shapes without text. They speak of what that apply touched; read with "check":true lists those of the whole page.
A label has one style. For a title and body, add a card: color is its box and title, labelColor its body, size its title, with the body smaller, font both and bodyFont the body. Without w it fits, up to ` + num(cardMaxW) + ` wide. Set title, body, w or h and it sizes itself again. Arrows bind to it by its id.
To hold shapes without clipping them, group them with a rectangle behind them.
Example: [{"add":"card","id":"auth","title":"Auth","body":"Sign-in and sessions","color":"violet","x":0,"y":0},{"add":"rectangle","id":"db","text":"DB","right":"auth"},{"add":"arrow","from":"auth","to":"db","straight":"h","label":"above","text":"reads"}]`,

	"arrows": `arrow: from to fromAt toAt x1 y1 x2 y2 text color labelColor fill dash size font kind bend via head tail straight at label.
from,to bind to a shape, and it follows when that shape moves. Free ends use x1,y1 and x2,y2.
A group binds by the shape that is most of it, as a path's main stroke; if none is, the error names them.
fromAt,toAt are [x,y], 0 to 1 across the shape; the default is its center.
kind:"arc", the default, curves by bend, px off straight: positive bulges to the right of travel (running left to right, down), negative to the left. kind:"elbow" runs in right angles, without bend.
An elbow's anchor on an edge sets the side it leaves or arrives by: [0.5,0] top, [0.5,1] bottom, [0,0.5] left, [1,0.5] right.
via:[[x,y],...] are page points an elbow passes through. They follow when both ends move alike; else they stay, unless a clear route has fewer turns. An elbow goes around what moves into it. via:[] clears them.
straight:"h" or "v" on an arrow with from and to runs it level where both shapes share height (or width), and keeps it so as they move or grow. at, 0 to 1, picks where: 0.25 and 0.75 for a pair. straight:"off" or fromAt,toAt end it.
A label opens a gap in its line, with what's behind showing. label:"above" takes it off the line; "center" puts it back.
Example: [{"add":"card","id":"a","title":"A","x":0,"y":0},{"add":"card","id":"m","title":"M","right":"a"},{"add":"card","id":"b","title":"B","right":"m"},{"add":"arrow","from":"a","to":"b","kind":"elbow","fromAt":[0.5,0],"toAt":[0.5,0],"via":[[400,-60]]}]`,

	"paths": `d is an SVG path: M L H V C S Q T A Z, absolute or relative. "M0 0 C40 80 80 -80 120 0"
It uses page coordinates unless x,y or a relative place moves it. w or h scales it; one of them keeps proportions.
fn on a draw is a formula in place of d: page y of page x, from x1 to x2, so + is down. {"add":"draw","fn":"300+44*sin(tau*x/88)*gauss(x,400,80)","x1":100,"x2":700,"dash":"solid"} is a wave packet centered on y=300. It takes + - * / % ^, ` + fnNames + `; gauss(x,center,width) is a bell of height 1. It breaks where it has no value.
Close with Z to fill. Each M after the first makes a separate stroke, id-1, id-2 and on, and they come back as a group with the id; holes don't cut out, so paint them over.
Curves are cheap: prefer C and A to many L. Example: [{"add":"draw","id":"blob","d":"M0 40C0 0 80 0 80 40S0 80 0 40Z","fill":"solid","color":"violet","x":100,"y":100,"w":160}]`,

	"select": `Every verb that names shapes can pick them instead. set, dup and del are what it's most for.
A run as an answer prints it: "s4-s503", in a list or alone. Each has to be there; a shape with that very id comes first.
"*" is all on the page, but not what that holds.
{"kind":"rectangle"} is every one of a kind, {"kind":["note","text"]} of several, at any depth, a card being one card and not its parts. Kinds: group card text note frame image draw highlight line arrow, geo for every geo shape, or one of those.
{"page":"Two"} reaches another page without going there, alone like "*", or with kind.
What's picked takes what it can of an op. What can't take a field, and what's locked or held by what is, are left out with a warning, and it's an error if none takes it. Ids are stricter: one that can't take it fails the op.
Example: [{"add":"rectangle","x":0,"y":0},{"add":"note","text":"Hi","x":300,"y":0},{"set":"*","color":"red"},{"set":{"kind":"note"},"dy":40},{"dup":{"kind":"rectangle"},"dy":120}]`,

	"edit": `Each op has one verb. IDS is one id, a list of ids, or what picks many: see select.
{"set":IDS,...} changes any field the shape takes: styles, text, name, w h, x y, dx dy, rot, in (empty leaves the frame), locked, d, and an arrow's ends (empty from or to frees it). Styles on a group reach what it holds.
One x or y lines several shapes up. {"del":IDS} deletes, with what those hold, {"del":"*"} clears the page. An arrow bound to what goes stays, with that end free.
{"dup":IDS,"dx":0,"dy":0,"n":1} copies; each copy is offset one more step, beside the original by default. A copied arrow stays bound only to what is copied with it. id and with change copies: see reuse.
{"group":IDS,"id":"g"} {"ungroup":IDS}
{"arrange":IDS,"how":HOW} ` + strings.Join(verbHows["arrange"], " ") + ` (see grid)
{"order":IDS,"how":HOW} ` + strings.Join(verbHows["order"], " ") + `, or {"order":IDS,"behind":"x"} and "over":"x" for just under or above x. read lists shapes bottom first.
{"flip":IDS,"how":"h"} or "v". An arrow only turns if what it joins flips with it.
{"page":"Name"} goes to that page, making it if new; add "name" to rename it. {"delPage":"Name"}
{"camera":"fit"} or {"camera":IDS} moves the view. New shapes out of view bring it along otherwise.
Example: [{"add":"star","id":"t","x":0,"y":0,"w":40,"h":40,"color":"yellow","fill":"fill"},{"dup":"t","dx":60,"n":4},{"set":"t","rot":20}]`,
}

// CharWidth is how wide a font's characters run, in its size, on average: in a
// sentence, and at most in a short title, whose capitals and short words run wider.
// A test holds these to the real fonts.
type CharWidth struct{ Sentence, Title float64 }

// CharWidths is what the guide says of each font.
var CharWidths = []struct {
	Font string
	CharWidth
}{
	{"draw", CharWidth{0.53, 0.63}},
	{"sans", CharWidth{0.46, 0.55}},
	{"serif", CharWidth{0.48, 0.58}},
	{"mono", CharWidth{0.6, 0.6}},
}

// charWidths lists what of CharWidths a part of the guide says.
func charWidths(of func(CharWidth) float64) string {
	out := make([]string, len(CharWidths))
	for i, c := range CharWidths {
		out[i] = c.Font + " " + ratio(of(c.CharWidth))
	}
	return strings.Join(out, ", ")
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
