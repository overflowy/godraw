// Package agent lets agents draw: an MCP server, which lives only as long as
// the app that runs it, over the canvases of its open windows.
//
// Its tools are few and cheap to carry. What each can do is told when asked
// for (guide), by what it answers, and by its errors, which say what would
// have been right.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"log"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"godraw/internal/doc"
	"godraw/internal/editor"
	"godraw/internal/geo"
)

// Canvas is a drawing open in a window.
type Canvas interface {
	// ID names the canvas for as long as it is open.
	ID() string
	// Title returns what its window is called.
	Title() string
	// Editor returns its editor, which belongs to the main thread.
	Editor() *editor.Editor
	// Render draws shapes in a box of the page, on the main thread.
	Render(box geo.Box, ids []doc.ID, scale float64) *image.RGBA
	// Run runs fn on the main thread, and draws a frame after it.
	Run(fn func())
}

// Host is what holds the canvases.
type Host interface {
	// Canvases returns the open canvases, the frontmost first.
	Canvases() []Canvas
	// New opens a new drawing and returns its canvas.
	New() (Canvas, error)
}

const (
	instructions = "Draw on the godraw canvas open on this machine: guide explains how, view shows the result."
	endpoint     = "/mcp"

	// How long a batch waits out a gesture or typing, and how long any call
	// waits for the main thread.
	busyWait = 4 * time.Second
	mainWait = 30 * time.Second

	maxRequest = 64 << 20
)

// The tools, as agents see them on every turn: kept to what it takes to start.
const (
	guideDesc = "How to use apply. No topic: overview and topics."
	readDesc  = "What is on the canvas, as compact text. ids: full detail of those shapes. find: search by text or kind."
	applyDesc = `Change the canvas: ops run in order as one undo step, and an error changes nothing. E.g. {"add":"rectangle","id":"a","x":0,"y":0,"text":"Hi","color":"blue"} {"add":"arrow","from":"a","to":"b"} {"add":"draw","d":"M0 0C50 80 100 -80 150 0"} {"set":"a","fill":"solid"} {"del":"a"}. Every shape, style and verb: guide.`
	viewDesc  = "See the page, some shapes or a box as a PNG, to check your work. Answers with the page box it shows."

	docProp    = `"doc":{"type":"string"}`
	guideInput = `{"type":"object","properties":{"topic":{"type":"string","enum":["shapes","styles","place","layout","arrows","paths","edit"]}}}`
	readInput  = `{"type":"object","properties":{` + docProp + `,"page":{"type":"string"},"ids":{"type":"array","items":{"type":"string"}},"find":{"type":"string"},"offset":{"type":"integer"},"limit":{"type":"integer"},"check":{"type":"boolean","description":"Warnings of the whole page."}}}`
	applyInput = `{"type":"object","properties":{` + docProp + `,"ops":{"type":"array","items":{"type":"object"}},"dry":{"type":"boolean","description":"Answer without changing anything, and with a commit token."},"commit":{"type":"string","description":"Apply a dry run's ops, instead of sending ops."},"quiet":{"type":"boolean","description":"Leave out each shape's box."}}}`
	viewInput  = `{"type":"object","properties":{` + docProp + `,"page":{"type":"string"},"ids":{"type":"array","items":{"type":"string"}},"box":{"type":"array","items":{"type":"number"},"description":"[x,y,w,h]"},"px":{"type":"integer","description":"Longest side. Default 768."},"theme":{"type":"string","enum":["light","dark"],"description":"Default: the window's."}}}`
)

// Handler returns the MCP server over HTTP.
func Handler(h Host, version string) http.Handler {
	srv := mcp.NewServer(&mcp.Implementation{Name: "godraw", Version: version}, &mcp.ServerOptions{Instructions: instructions})
	s := &server{host: h}
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}
	tool := func(name, desc, input string, ann *mcp.ToolAnnotations, fn func(context.Context, json.RawMessage) ([]mcp.Content, error)) {
		srv.AddTool(&mcp.Tool{Name: name, Description: desc, InputSchema: json.RawMessage(input), Annotations: ann},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				content, err := fn(ctx, req.Params.Arguments)
				if err != nil {
					// An agent reads a tool's error and corrects itself.
					return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
				}
				return &mcp.CallToolResult{Content: content}, nil
			})
	}
	tool("guide", guideDesc, guideInput, readOnly, s.guide)
	tool("read", readDesc, readInput, readOnly, s.read)
	tool("apply", applyDesc, applyInput, nil, s.apply)
	tool("view", viewDesc, viewInput, readOnly, s.view)

	mux := http.NewServeMux()
	mux.Handle(endpoint, http.MaxBytesHandler(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{
			Stateless: true, JSONResponse: true,
			// Web pages may not reach it, directly or by rebinding a name to
			// this machine; the handler checks the Host header by default.
			CrossOriginProtection: http.NewCrossOriginProtection(),
		},
	), maxRequest))
	return mux
}

// Start listens on addr and serves until the process ends, so that the server
// is up exactly while the app is. It returns the server's URL.
func Start(addr string, h Host, version string) (string, error) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	url := "http://" + l.Addr().String() + endpoint
	log.Printf("godraw: MCP server at %s", url)
	s := &http.Server{Handler: Handler(h, version), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := s.Serve(l); err != nil {
			log.Printf("godraw: MCP server stopped: %v", err)
		}
	}()
	return url, nil
}

type server struct {
	host Host

	mu    sync.Mutex
	dries []*dryRun // the latest, the oldest first
	nDry  int
}

// dryRun is what a dry run was asked, kept for an agent to apply it without
// sending it all again.
type dryRun struct {
	token, doc string
	page       doc.ID // where it began, which is what its answer was about
	ops        []*op
}

// onItsPage says what is wrong if the dry run can't commit on the current page.
func (d *dryRun) onItsPage(e *editor.Editor) error {
	if e.Page == d.page {
		return nil
	}
	p := e.Doc.Page(d.page)
	if p == nil {
		return errors.New("the page this dry run was on is gone; send ops again")
	}
	return fmt.Errorf("this dry run was on page %q; go back with {\"page\":%q} and commit again, or send ops again", p.Name, p.Name)
}

// How many dry runs stay to be committed: ops can hold images.
const keptDries = 4

func (s *server) keepDry(canvas string, page doc.ID, ops []*op) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nDry++
	// Not d, which is what document ids start with.
	d := &dryRun{token: "t" + strconv.Itoa(s.nDry), doc: canvas, page: page, ops: ops}
	s.dries = append(s.dries, d)
	if len(s.dries) > keptDries {
		s.dries = slices.Delete(s.dries, 0, len(s.dries)-keptDries)
	}
	return d.token
}

func (s *server) findDry(token string) *dryRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.dries {
		if d.token == token {
			return d
		}
	}
	return nil
}

func (s *server) dropDry(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dries = slices.DeleteFunc(s.dries, func(d *dryRun) bool { return d.token == token })
}

// strict decodes tool arguments, refusing what it doesn't know.
func strict(raw json.RawMessage, into any) error {
	if len(raw) == 0 {
		return nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(into); err != nil {
		return errors.New(strings.TrimPrefix(err.Error(), "json: "))
	}
	return nil
}

func text(s string) []mcp.Content { return []mcp.Content{&mcp.TextContent{Text: s}} }

// canvas finds the canvas asked for.
func (s *server) canvas(name string) (Canvas, error) {
	all := s.host.Canvases()
	if len(all) == 0 {
		return nil, errors.New("no drawing is open in godraw; apply opens a new one")
	}
	if name == "" {
		return all[0], nil
	}
	var known []string
	for _, c := range all {
		if c.ID() == name {
			return c, nil
		}
		known = append(known, c.ID())
	}
	return nil, fmt.Errorf("no drawing %q; open: %s", name, strings.Join(known, ", "))
}

// onMain runs fn on the canvas's main thread and waits for it. A panic in what
// an agent asked for fails the call, not the app.
func onMain(ctx context.Context, c Canvas, fn func() error) error {
	done := make(chan error, 1)
	c.Run(func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("internal error: %v", r)
			}
		}()
		done <- fn()
	})
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(mainWait):
		return errors.New("godraw didn't answer in time; check with read before trying again")
	}
}

func (s *server) guide(_ context.Context, raw json.RawMessage) ([]mcp.Content, error) {
	var a struct {
		Topic string `json:"topic"`
	}
	if err := strict(raw, &a); err != nil {
		return nil, err
	}
	t, err := guide(a.Topic)
	if err != nil {
		return nil, err
	}
	return text(t), nil
}

func (s *server) read(ctx context.Context, raw json.RawMessage) ([]mcp.Content, error) {
	var a readArgs
	if err := strict(raw, &a); err != nil {
		return nil, err
	}
	c, err := s.canvas(a.Doc)
	if err != nil {
		return nil, err
	}
	others := ""
	if all := s.host.Canvases(); len(all) > 1 {
		var open []string
		for _, o := range all {
			open = append(open, fmt.Sprintf("%s=%q", o.ID(), o.Title()))
		}
		others = " of " + strings.Join(open, " ")
	}
	title := c.Title()
	var out string
	err = onMain(ctx, c, func() error {
		var err error
		out, err = read(c.Editor(), a, title, others)
		return err
	})
	if err != nil {
		return nil, err
	}
	return text(out), nil
}

func (s *server) apply(ctx context.Context, raw json.RawMessage) ([]mcp.Content, error) {
	var a struct {
		Doc    string          `json:"doc"`
		Ops    json.RawMessage `json:"ops"`
		Dry    bool            `json:"dry"`
		Commit string          `json:"commit"`
		Quiet  bool            `json:"quiet"`
	}
	if err := strict(raw, &a); err != nil {
		return nil, err
	}
	var ops []*op
	var committing *dryRun
	var err error
	switch {
	case a.Commit != "":
		// A client that still has ops down as required sends an empty list.
		if !noOps(a.Ops) || a.Dry {
			return nil, errors.New("commit applies a dry run's ops, so it takes neither ops nor dry")
		}
		if committing = s.findDry(a.Commit); committing == nil {
			return nil, fmt.Errorf("no dry run %q to commit: only the latest %d are kept, and each commits once; send ops again", a.Commit, keptDries)
		}
		ops, a.Doc = committing.ops, committing.doc
	case len(a.Ops) == 0:
		return nil, errors.New(`apply needs ops, or {"commit":TOKEN} from a dry run in place of them`)
	default:
		// Paths and images are done with before the window is held up.
		if ops, err = decodeOps(a.Ops); err != nil {
			return nil, err
		}
	}
	var c Canvas
	if a.Doc == "" && !a.Dry && len(s.host.Canvases()) == 0 {
		if c, err = s.host.New(); err != nil {
			return nil, err
		}
	} else if c, err = s.canvas(a.Doc); err != nil {
		return nil, err
	}
	var out string
	var began doc.ID
	deadline := time.Now().Add(busyWait)
	for {
		err = onMain(ctx, c, func() error {
			e := c.Editor()
			if committing != nil {
				if err := committing.onItsPage(e); err != nil {
					return err
				}
			}
			began = e.Page
			var err error
			out, err = apply(e, ops, applyMode{dry: a.Dry, quiet: a.Quiet})
			return err
		})
		if !errors.Is(err, editor.ErrBusy) {
			break
		}
		if time.Now().After(deadline) {
			return nil, errors.New("the user is drawing or typing on the canvas; nothing was changed, try again shortly")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	if err != nil {
		return nil, err
	}
	switch {
	case a.Dry:
		out += fmt.Sprintf("\nto apply this, call apply with commit %q and no ops", s.keepDry(c.ID(), began, ops))
	case a.Commit != "":
		s.dropDry(a.Commit)
	}
	return text(out), nil
}

// noOps says whether ops were left out, or sent empty.
func noOps(raw json.RawMessage) bool {
	var list []json.RawMessage
	return len(raw) == 0 || (json.Unmarshal(raw, &list) == nil && len(list) == 0)
}

func (s *server) view(ctx context.Context, raw json.RawMessage) ([]mcp.Content, error) {
	var a viewArgs
	if err := strict(raw, &a); err != nil {
		return nil, err
	}
	if a.Theme != "" && a.Theme != "light" && a.Theme != "dark" {
		return nil, fmt.Errorf("theme %q: use light, dark", a.Theme)
	}
	c, err := s.canvas(a.Doc)
	if err != nil {
		return nil, err
	}
	var img *image.RGBA
	var box geo.Box
	var scale float64
	var theme string
	err = onMain(ctx, c, func() error {
		e := c.Editor()
		// For this picture only: no frame comes before it goes back.
		was := e.DarkMode
		defer func() { e.DarkMode = was }()
		if a.Theme != "" {
			e.DarkMode = a.Theme == "dark"
		}
		theme = themeName(e)
		_, err := onPage(e, a.Page, func() (string, error) {
			b, ids, k, err := viewPlan(e, a)
			if err != nil {
				return "", err
			}
			box, scale = b, k
			img = c.Render(b, ids, k)
			return "", nil
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	caption := fmt.Sprintf("shows page box %s at %s px per unit, %s theme", boxText(box), num(scale), theme)
	return []mcp.Content{
		&mcp.ImageContent{MIMEType: "image/png", Data: buf.Bytes()},
		&mcp.TextContent{Text: caption},
	}, nil
}
