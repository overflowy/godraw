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
	"io"
	"log"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
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
	instructions = "Draw on the GoDraw canvas open on this machine: guide explains how, view shows the result."
	endpoint     = "/mcp"

	// How long a batch waits out a gesture or typing, and how long any call
	// waits for the main thread.
	busyWait = 4 * time.Second
	mainWait = 30 * time.Second

	maxRequest = 64 << 20
)

// The tools, as agents see them on every turn: kept to what it takes to start.
const (
	guideDesc  = "How to use apply. No topic: overview and topics."
	readDesc   = "What is on the canvas, as compact text. ids: full detail of those shapes. find: search by text or kind."
	applyDesc  = `Change the canvas: ops run in order as one undo step, and an error changes nothing. E.g. {"add":"rectangle","id":"a","x":0,"y":0,"text":"Hi","color":"blue"} {"add":"arrow","from":"a","to":"b"} {"add":"draw","d":"M0 0C50 80 100 -80 150 0"} {"set":"a","fill":"solid"} {"del":"a"}. Every shape, style and verb: guide.`
	viewDesc   = "See the page, some shapes or a box as a PNG, to check your work. Answers with the page box it shows."
	exportDesc = "Write the page, some shapes or a box to a .png or .svg file."

	docProp     = `"doc":{"type":"string"}`
	guideInput  = `{"type":"object","properties":{"topic":{"type":"string","enum":["shapes","styles","looks","reuse","grid","place","layout","arrows","paths","edit","select"]}}}`
	readInput   = `{"type":"object","properties":{` + docProp + `,"page":{"type":"string"},"ids":{"type":"array","items":{"type":"string"}},"find":{"type":"string"},"offset":{"type":"integer"},"limit":{"type":"integer"},"check":{"type":"boolean","description":"Warnings of the whole page."}}}`
	applyInput  = `{"type":"object","properties":{` + docProp + `,"ops":{"type":"array","items":{"type":"object"}},"dry":{"type":"boolean","description":"Answer without changing anything, and with a commit token."},"commit":{"type":"string","description":"Apply a dry run's ops, instead of sending ops."},"quiet":{"type":"boolean","description":"Leave out each shape's box."}}}`
	viewInput   = `{"type":"object","properties":{` + docProp + `,"page":{"type":"string"},"ids":{"type":"array","items":{"type":"string"}},"box":{"type":"array","items":{"type":"number"},"minItems":4,"maxItems":4,"description":"[x,y,w,h]"},"px":{"type":"integer","description":"Longest side. Default 768."},"theme":{"type":"string","enum":["light","dark"],"description":"Default: the window's."}}}`
	exportInput = `{"type":"object","required":["path"],"properties":{` + docProp + `,"path":{"type":"string","description":"Absolute."},"overwrite":{"type":"boolean"},"page":{"type":"string"},"ids":{"type":"array","items":{"type":"string"}},"box":{"type":"array","items":{"type":"number"}},"scale":{"type":"number"},"theme":{"type":"string","enum":["light","dark"]}}}`
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
	tool("export", exportDesc, exportInput, nil, s.export)

	mux := http.NewServeMux()
	// Web pages may not reach it, directly or by rebinding a name to this machine;
	// the handler checks the Host header by default.
	mux.Handle(endpoint, http.NewCrossOriginProtection().Handler(http.MaxBytesHandler(answerUnknown(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{
			Stateless: true, JSONResponse: true,
			// Images come in requests, as data: URLs. The SDK's own limit is 4 MiB.
			MaxRequestBodyBytes: maxRequest,
		},
	)), maxRequest)))
	return mux
}

// notHandled begins what the SDK says when it refuses a method it doesn't know.
const notHandled = "JSON RPC not handled"

// answerUnknown answers a method nobody knows as JSON-RPC says to, which the
// SDK does only from protocol 2026-07-28: before it, a call gets a plain 400,
// which a client can take for the server failing rather than for a feature to
// skip. A notification gets no answer, so it is accepted and dropped.
//
// It steps in after the SDK, so that what the SDK checks first still comes
// first, and what it knows needn't be listed here too. A batch stays refused, as does
// a call too long to be one nobody knows, of which it keeps only the start: the
// bodies that come through here can hold images.
func answerUnknown(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var sent capped
		r.Body = io.NopCloser(io.TeeReader(r.Body, &sent))
		held := &heldRefusal{ResponseWriter: w}
		next.ServeHTTP(held, r)
		if !held.refused {
			return
		}
		msg, err := jsonrpc.DecodeMessage(sent.buf.Bytes())
		req, isReq := msg.(*jsonrpc.Request)
		if err != nil || sent.over || !isReq || !bytes.HasPrefix(held.body.Bytes(), []byte(notHandled)) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write(held.body.Bytes()) // a client that's gone has nothing to be told
			return
		}
		if !req.IsCall() {
			w.Header().Del("Content-Type")
			w.WriteHeader(http.StatusAccepted)
			return
		}
		out, err := jsonrpc.EncodeMessage(&jsonrpc.Response{ID: req.ID, Error: &jsonrpc.Error{
			Code:    jsonrpc.CodeMethodNotFound,
			Message: fmt.Sprintf("method not found: %q", req.Method),
		}})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(out)
	})
}

// maxUnknownBody is more than a call to a method nobody knows should take.
const maxUnknownBody = 64 << 10

// capped keeps what's written to it, up to maxUnknownBody, and says if there was more.
type capped struct {
	buf  bytes.Buffer
	over bool
}

func (c *capped) Write(b []byte) (int, error) {
	if room := maxUnknownBody - c.buf.Len(); len(b) > room {
		c.over = true
		c.buf.Write(b[:room])
	} else {
		c.buf.Write(b)
	}
	return len(b), nil
}

// heldRefusal holds back a 400 until it is known what to answer instead.
type heldRefusal struct {
	http.ResponseWriter
	refused bool
	body    bytes.Buffer
}

func (w *heldRefusal) WriteHeader(code int) {
	if code == http.StatusBadRequest {
		w.refused = true
		return
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *heldRefusal) Write(b []byte) (int, error) {
	if w.refused {
		return w.body.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap lets the SDK flush a stream through it.
func (w *heldRefusal) Unwrap() http.ResponseWriter { return w.ResponseWriter }

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
	// styles is the named styles of each canvas, for as long as the app runs.
	styles map[string]map[string]*fields
}

// stylesOf returns the named styles of a canvas, which apply doesn't change.
func (s *server) stylesOf(canvas string) map[string]*fields {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.styles[canvas]
}

func (s *server) keepStyles(canvas string, styles map[string]*fields) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.styles == nil {
		s.styles = map[string]map[string]*fields{}
	}
	s.styles[canvas] = styles
}

// dryRun is what a dry run was asked, kept for an agent to apply it without
// sending it all again.
type dryRun struct {
	token, doc string
	page       doc.ID // where it began, which is what its answer was about
	ops        []*op
	// What its answer was worked out from, and has to hold for it to commit.
	revision uint64
	styles   map[string]*fields
}

// errStaleDry says that what a dry run answered no longer holds. The drawing's
// revision only goes up, so such a dry run is never of use again.
var errStaleDry = errors.New("the drawing changed since this dry run, so what it answered may no longer hold; nothing was changed. Send ops again, with dry to see them first")

// stillHolds says what is wrong if a dry run's answer is out of date. A commit runs
// the ops again, so it comes to what the answer said only from the same drawing.
func (d *dryRun) stillHolds(e *editor.Editor, styles map[string]*fields) error {
	if e.Revision() != d.revision || !reflect.DeepEqual(styles, d.styles) {
		return errStaleDry
	}
	return nil
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

func (s *server) keepDry(canvas string, page doc.ID, revision uint64, styles map[string]*fields, ops []*op) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nDry++
	// Not d, which is what document ids start with.
	d := &dryRun{token: "t" + strconv.Itoa(s.nDry), doc: canvas, page: page, ops: ops, revision: revision, styles: styles}
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
		return nil, errors.New("no drawing is open in GoDraw; apply opens a new one")
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
		return errors.New("GoDraw didn't answer in time; check with read before trying again")
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
	if len(a.IDs) == 0 && !a.Check {
		if names := slices.Sorted(maps.Keys(s.stylesOf(c.ID()))); len(names) > 0 && a.Find == "" {
			out += "\nstyles: " + strings.Join(names, " ")
		}
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
	var invalid *batchError
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
		if ops, err = decodeOps(a.Ops); err != nil && !errors.As(err, &invalid) {
			return nil, err
		}
	}
	var c Canvas
	if a.Doc == "" && !a.Dry && len(s.host.Canvases()) == 0 {
		// There's nothing to look at the rest against, and no call for a new window.
		if invalid != nil {
			return nil, invalid
		}
		if c, err = s.host.New(); err != nil {
			return nil, err
		}
	} else if c, err = s.canvas(a.Doc); err != nil {
		return nil, err
	}
	mode := applyMode{dry: a.Dry, quiet: a.Quiet, invalid: invalid, styles: s.stylesOf(c.ID())}
	if !a.Dry {
		mode.keep = func(styles map[string]*fields) { s.keepStyles(c.ID(), styles) }
	}
	if committing != nil {
		// As the batch begins: it first commits what a slider left open.
		mode.check = func() error { return committing.stillHolds(c.Editor(), mode.styles) }
	}
	var out string
	var began doc.ID
	var revision uint64
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
			out, err = apply(e, ops, mode)
			revision = e.Revision()
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
	if errors.Is(err, errStaleDry) {
		s.dropDry(a.Commit)
	}
	if err != nil {
		return nil, err
	}
	switch {
	case a.Dry:
		out += fmt.Sprintf("\nto apply this, call apply with commit %q and no ops, before the drawing changes", s.keepDry(c.ID(), began, revision, mode.styles, ops))
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

// exportArgs is what export asks for.
type exportArgs struct {
	Doc       string    `json:"doc"`
	Path      string    `json:"path"`
	Overwrite bool      `json:"overwrite"`
	Page      string    `json:"page"`
	IDs       names     `json:"ids"`
	Box       []float64 `json:"box"`
	Scale     float64   `json:"scale"`
	Theme     string    `json:"theme"`
}

// defaultExportScale is as sharp as the File menu's PNG.
const defaultExportScale = 2.0

// export writes a picture to a file. It's a tool of its own, as view says that it
// changes nothing, which clients take at its word.
func (s *server) export(ctx context.Context, raw json.RawMessage) ([]mcp.Content, error) {
	var a exportArgs
	if err := strict(raw, &a); err != nil {
		return nil, err
	}
	path, err := exportPath(a.Path)
	if err != nil {
		return nil, err
	}
	svg := strings.EqualFold(filepath.Ext(path), ".svg")
	if a.Theme != "" && a.Theme != "light" && a.Theme != "dark" {
		return nil, fmt.Errorf("theme %q: use light, dark", a.Theme)
	}
	if a.Scale < 0 || a.Scale > maxExportScale {
		return nil, fmt.Errorf("scale must be from 0 to %s", num(maxExportScale))
	}
	if a.Scale == 0 {
		a.Scale = defaultExportScale
	}
	c, err := s.canvas(a.Doc)
	if err != nil {
		return nil, err
	}
	var img *image.RGBA
	var data []byte
	var box geo.Box
	err = onMain(ctx, c, func() error {
		e := c.Editor()
		was := e.DarkMode
		defer func() { e.DarkMode = was }()
		if a.Theme != "" {
			e.DarkMode = a.Theme == "dark"
		}
		_, err := onPage(e, a.Page, func() (string, error) {
			b, ids, _, err := viewPlan(e, viewArgs{IDs: a.IDs, Box: a.Box})
			if err != nil {
				return "", err
			}
			box = b
			if svg {
				data = e.SVGOf(b, ids, e.DarkMode)
			} else {
				img = c.Render(b, ids, a.Scale)
			}
			return "", nil
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	size := "vector, its text in IBM Plex or Shantell Sans where those are installed, else the nearest font"
	if !svg {
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return nil, err
		}
		data = buf.Bytes()
		size = fmt.Sprintf("%dx%d px", img.Bounds().Dx(), img.Bounds().Dy())
	}
	if err := writeExport(path, data, a.Overwrite); err != nil {
		return nil, err
	}
	return text(fmt.Sprintf("wrote %s: %d bytes, %s, page box %s", path, len(data), size, boxText(box))), nil
}

// maxExportScale is sharper than a picture needs: a large page is limited anyway.
const maxExportScale = 8.0

// exportPath checks where a picture is to go, and says it in full.
func exportPath(path string) (string, error) {
	if path == "" {
		return "", errors.New("export needs path, absolute and ending .png or .svg")
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, rest)
	}
	// Relative to what? The app's folder is nothing an agent knows of.
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path %q must be absolute", path)
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".svg":
	default:
		return "", fmt.Errorf("path %q must end .png or .svg", path)
	}
	return filepath.Clean(path), nil
}

// writeExport writes a picture. It only replaces a file when asked to, and then
// whole: a failure leaves what was there.
func writeExport(path string, data []byte, overwrite bool) error {
	if !overwrite {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s exists; overwrite:true replaces it", path)
		}
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(path) // what's half written is no use, and an error says why
		}
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".godraw-export-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }() // gone already if the rename worked
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(f.Name(), 0o644)
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
