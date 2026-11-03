# GoDraw

A local whiteboard written in Go. It draws with [MyGo](https://github.com/egoist/mygo)'s native
UI, so there's no web view and no JavaScript.

<img width="2328" height="1620" alt="screenshot" src="https://github.com/user-attachments/assets/84073b6b-1f1b-4630-a81a-08c42b26435f" />

Drawings are `.godraw` files. They're plain JSON with one line for each page, shape, binding and
image, so they diff well.

## Install

On macOS and Linux:

```sh
curl -fsSL https://github.com/overflowy/godraw/releases/latest/download/install.sh | sh
```

On Windows, in PowerShell:

```powershell
irm https://github.com/overflowy/godraw/releases/latest/download/install.ps1 | iex
```

The scripts check each download before installing it. Disk images and Windows installers are on
the [releases page](https://github.com/overflowy/godraw/releases), but the apps aren't signed by a
registered developer: macOS blocks an app from a downloaded disk image, and Windows shows a
SmartScreen warning.

## Run

```sh
go run .                    # a new drawing
go run . drawing.godraw     # open a file
go tool mygo build          # package build/<platform>/GoDraw.app and a .dmg
go test ./...
task check                  # show every format, vet and lint issue, changing nothing
task fix                    # apply what the formatters and linters can fix themselves
```

## Try the showcase

```sh
go run . examples/showcase.godraw
```

The showcase has three pages: a poster with a live diagram whose arrows follow any box you drag,
a generative flow field of 1,700 pen strokes, and a sheet of every style.
`go run ./examples/showcase` regenerates it.

## Measure it

```sh
go run ./examples/perf -n 500            # a page of 500 shapes, fitted to the window
go run ./examples/perf -file my.godraw   # or a drawing of your own
```

This opens a window that stays on top without taking the focus. It moves the camera and drags
shapes on a script, then prints how long the frames of each part took on the GPU.

`go test ./internal/app -bench BenchmarkCanvas -run '^$'` measures the same interactions without
a window. Profile that one.

## Release

Set `version` in `mygo.json`, commit, then push a tag with that version:

```sh
git tag v0.2.0
git push origin v0.2.0
```

The Release workflow runs the tests, then builds macOS, Windows and Linux for arm64 and amd64 and
uploads them to a draft release. Look it over and publish it. The install scripts use the latest
published release. The workflow signs the downloads with the `MYGO_UPDATER_PRIVATE_KEY` repository
secret, from `go tool mygo keygen`.

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

**Documents.** Each drawing gets its own window, with autosave, crash recovery and Open Recent.
MyGo's document system handles these, and picks up changes other apps make to the file.

**Export.** PNG and SVG, of the selection or the page.

**View.** Pan, zoom and pinch, zoom to fit, minimap, dark mode, focus mode.

## Drawing from an agent

While GoDraw runs, it serves [MCP](https://modelcontextprotocol.io) at
`http://127.0.0.1:7463/mcp`. Connect any MCP client to that URL. For Claude Code, run this in your
project's folder:

```sh
claude mcp add --scope project --transport http godraw http://127.0.0.1:7463/mcp
```

The **MCP** button at the top left shows the server's state and has the command and URL to copy.
Set `GODRAW_MCP_PORT` to use another port.

Agents draw with `apply`, look with `read` and `view`, write files with `export`, and learn the
rest from `guide`. Their shapes are ordinary shapes you can edit and undo. Agents can't save or
name a drawing.

The server listens on this machine only and refuses requests from web pages. It has no login, so
other programs on the machine can use it, and they can read PNG, JPEG and GIF files by path.

## Credits

- [Shantell Sans](https://github.com/arrowtype/shantell-sans), the handwriting font (SIL OFL)
- [IBM Plex](https://github.com/IBM/plex), the sans, serif and mono fonts (SIL OFL)
- [Lucide](https://lucide.dev), the icons (ISC)

Licenses are in `internal/assets`.
