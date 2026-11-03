package main

import (
	"errors"
	"fmt"
	"image"
	"log"
	"net"
	"os"
	"slices"
	"sync"
	"syscall"

	"github.com/egoist/mygo"

	"godraw/internal/agent"
	"godraw/internal/app"
	"godraw/internal/doc"
	"godraw/internal/editor"
	"godraw/internal/geo"
)

// mcpPort is where the MCP server listens, on this machine only, unless
// GODRAW_MCP_PORT says otherwise. It is fixed so that agents can keep it in
// their settings.
const mcpPort = "7463"

// canvas is a window as agents see it.
type canvas struct {
	id string
	w  *window
}

func (c *canvas) ID() string             { return c.id }
func (c *canvas) Title() string          { return c.w.win.Title() }
func (c *canvas) Editor() *editor.Editor { return c.w.app.Editor() }
func (c *canvas) Run(fn func())          { c.w.Update(fn) }

func (c *canvas) Render(box geo.Box, ids []doc.ID, scale float64) *image.RGBA {
	return c.w.app.RenderImage(box, ids, scale)
}

// canvases keeps the open windows for the server, whose requests come on
// goroutines of their own, the frontmost first.
type canvases struct {
	mu     sync.Mutex
	list   []agent.Canvas
	n      int
	docs   *mygo.Documents[*doc.Drawing]
	status app.MCPStatus
}

// Status returns how the server is doing, for the windows to show.
func (s *canvases) Status() app.MCPStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *canvases) add(w *window) *canvas {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	c := &canvas{id: fmt.Sprintf("d%d", s.n), w: w}
	s.list = slices.Insert(s.list, 0, agent.Canvas(c))
	return c
}

func (s *canvases) remove(c *canvas) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = slices.DeleteFunc(s.list, func(o agent.Canvas) bool { return o == c })
}

func (s *canvases) front(c *canvas) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := slices.Index(s.list, agent.Canvas(c)); i > 0 {
		s.list = slices.Insert(slices.Delete(s.list, i, i+1), 0, agent.Canvas(c))
	}
}

// Canvases returns the open canvases, the frontmost first.
func (s *canvases) Canvases() []agent.Canvas {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.list)
}

// New opens a new drawing and returns its canvas.
func (s *canvases) New() (agent.Canvas, error) {
	s.docs.New()
	if all := s.Canvases(); len(all) > 0 {
		return all[0], nil
	}
	return nil, errors.New("couldn't open a new drawing")
}

// serveMCP runs the MCP server for as long as the app runs, and tells the
// windows how that went. Another copy of the app may hold the port: this one
// then does without.
func serveMCP(s *canvases) {
	port := os.Getenv("GODRAW_MCP_PORT")
	if port == "" {
		port = mcpPort
	}
	addr := net.JoinHostPort("127.0.0.1", port)
	version := mygo.App.Version()
	if version == "" {
		version = "dev" // go run and go build have none
	}
	var st app.MCPStatus
	url, err := agent.Start(addr, s, version)
	switch {
	case err == nil:
		st.URL = url
	case errors.Is(err, syscall.EADDRINUSE):
		st.Problem = fmt.Sprintf("Port %s is in use, probably by another copy of godraw. Quit that copy and open this one again, or set GODRAW_MCP_PORT to another port.", port)
	default:
		st.Problem = err.Error()
	}
	if err != nil {
		log.Printf("godraw: no MCP server: %v", err)
	}
	s.mu.Lock()
	s.status = st
	open := slices.Clone(s.list)
	s.mu.Unlock()
	for _, c := range open {
		c.Run(func() {}) // draws a frame, which shows it
	}
}
