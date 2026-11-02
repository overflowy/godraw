# godraw

A local, native whiteboard in the manner of [tldraw](https://github.com/tldraw/tldraw),
written in Go and drawn with [MyGo](https://github.com/egoist/mygo)'s native UI: no web
view, no JavaScript. It reads and writes tldraw's `.tldr` files.

godraw is an independent reimplementation. It contains no tldraw code and isn't
affiliated with tldraw, Inc.

## Run

```sh
go run .                    # a new drawing
go run . drawing.tldr       # open a file
go tool mygo build          # package build/<platform>/godraw.app and a .dmg
go test ./...
```

## Try the showcase

```sh
go run . examples/showcase.tldr
```

Three pages: a poster with a live diagram (drag a box and its arrows follow), a generative
flow field of 1,700 pen strokes, and a sheet of every style. `go run ./examples/showcase`
regenerates it.

## What it does

**Tools.** Select, hand, draw, eraser, arrow, text, note, image, line, highlighter, laser,
frame, and 20 geo shapes. Keep a tool selected with `Q`. Press `?` for every shortcut.

**Editing.** Multiple selection and brush selection, move, resize, rotate, flip, duplicate,
alt-drag to copy, snapping (hold `⌘`), grid, group and ungroup, lock, reorder, align and
distribute, copy and paste, undo and redo, and multiple pages.

**Shapes.**
- Hand-drawn, dashed, dotted and solid strokes
- None, semi, solid, pattern and full fills
- Four sizes and four fonts
- Opacity
- Labels on shapes, notes and arrows
- Arrows that bind to shapes and follow them, bend, or route as elbows, with eight arrowheads
- Lines with draggable vertices and curves
- Frames that clip and hold their contents

**Documents.** MyGo's document system gives each drawing its own window, plus autosave,
crash recovery, Open Recent, and pick-up of changes made to the file by other apps.

**Export.** PNG and SVG, of the selection or the page.

**View.** Pan, zoom and pinch, zoom to fit, minimap, dark mode, focus mode.

## Compatibility with tldraw

Files are written at the versions of `@tldraw/tlschema` 5.6.0, and tldraw migrates them as
it opens them. Against that package's validators, the app's output passes for every record
type it writes, and the test fixture `internal/doc/testdata/tldraw.tldr` was made with
tldraw's own encoder.

What the app doesn't model is kept and written back: shapes of unknown types (embeds,
bookmarks and video show as labeled boxes), record `meta`, and rich text you haven't edited.

Known differences:

- Rich text shows as plain text. Bold, lists and so on survive until you edit that text,
  which then saves as plain paragraphs.
- Clipboard data is godraw's own, so shapes don't paste across to tldraw. Use `.tldr` files
  to move drawings between the two.
- Hand-drawn strokes and arrow routing are drawn by this app's own algorithms, so they are
  similar to tldraw's, not identical. The colors and sizes are tldraw's.
- SVG export names its fonts instead of embedding them.
- Not implemented: cropping images, note "clone" handles, video playback, multiplayer.

## Drawing from an agent

While godraw runs, it serves [MCP](https://modelcontextprotocol.io) at
`http://127.0.0.1:7463/mcp`. The server lives in the app's process, so it starts when you
launch godraw and is gone when you quit. The **MCP** button at the top left shows its
state, and opens a dialog with the command and URL to copy. MCP Server… in the File menu and
in the ⋯ menu opens the same dialog. Connect any MCP client to that URL:

```sh
claude mcp add --scope project --transport http godraw http://127.0.0.1:7463/mcp
```

Run that in your project's folder. It writes the project's `.mcp.json`, and Claude Code asks
you to approve the server in the next session there. This repository already has that file. A client that only
speaks stdio can use `npx -y mcp-remote http://127.0.0.1:7463/mcp`. A client connected
before you launched godraw has to reconnect (`/mcp` in Claude Code). Set `GODRAW_MCP_PORT`
to use another port.

| Tool | What it does |
| --- | --- |
| `apply` | Runs a list of ops: add, set, del, dup, group, arrange, pages, camera. A call is one undo step, and an error in it changes nothing |
| `read` | The canvas as compact text, one line per shape, paged and searchable |
| `view` | The page, some shapes or a box as a PNG, 768 px on its longest side by default |
| `guide` | The reference for `apply`, a topic at a time |

```json
[{"add":"rectangle","id":"a","text":"API","color":"blue","fill":"solid"},
 {"add":"cloud","id":"b","text":"Store","right":"a"},
 {"add":"arrow","from":"a","to":"b","text":"writes"},
 {"add":"draw","d":"M0 200 C80 120 160 280 240 200","dash":"solid"}]
```

The four tool definitions come to about 500 tokens, and a test holds them to a budget. The
rest is on demand: `guide` by topic, `read` by page of results, and errors that list the
valid values. `d` takes an SVG path, so an agent can draw any outline with few tokens.

Agents work on the frontmost drawing unless they pass `doc`, and they wait while you drag
or type. Their shapes are ordinary shapes: you can select, edit and undo them, and they save
to `.tldr` like the rest.

The server listens on this machine only and refuses requests from web pages, but it has no
login, so other programs on the machine can use it. `src` on an image reads PNG, JPEG and
GIF files by path.

## Layout

| Path | What it is |
| --- | --- |
| `main.go` | Documents, menus and the window |
| `agent.go` | Starts the MCP server and gives it the open windows |
| `internal/geo` | Vectors, matrices, paths, hit tests; no dependencies |
| `internal/doc` | Records, fractional indices, and the `.tldr` format |
| `internal/editor` | Camera, selection, tools and gestures, undo, and what each shape draws; no UI dependency, so it is tested without a window |
| `internal/app` | The native UI: canvas painter, panels, keyboard, export |
| `internal/agent` | The MCP server and what its tools do |
| `internal/assets` | Embedded font and icons |
| `e2e.go` | With `-tags e2e`, lets another process drive the app over MyGo's automation socket |

Committed records are immutable: an edit replaces a record. So undo holds the records
themselves, a snapshot for saving is a copy of the maps, and undoing back to what the file
has leaves the document unedited. Each shape's display list is cached on its record and
reused across moves and zooms.

## Credits

- [Shantell Sans](https://github.com/arrowtype/shantell-sans), the handwriting font (SIL OFL)
- [Lucide](https://lucide.dev), the icons (ISC)

Licenses are in `internal/assets`.
