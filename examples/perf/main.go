// Perf opens a window on a large page and moves the camera on a script, for
// measuring frames as they are on the GPU. It prints how long the frames of each
// part of the script took, and what took it.
//
//	go run ./examples/perf [-n 500] [-file some.tldr] [-fit=false]
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"regexp"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"godraw/internal/app"
	"godraw/internal/doc"
	"godraw/internal/editor"
	"godraw/internal/geo"
	"godraw/internal/stress"
)

type host struct{ win *mygo.Window }

func (h *host) Changed(*doc.Drawing) {}
func (h *host) Update(fn func())     { h.win.Update(fn) }
func (h *host) Window() *mygo.Window { return h.win }

// part is a stretch of the script: what moves, for each frame the window can show.
type part struct {
	name  string
	frame func(a *app.App)
	// end, if any, finishes what the part began.
	end func(a *app.App)
}

var script = []part{
	{name: "pan", frame: func(a *app.App) { a.Editor().Pan(1.37, 0.61) }},
	{name: "pan-back", frame: func(a *app.App) { a.Editor().Pan(-1.37, -0.61) }},
	{name: "zoom-in", frame: func(a *app.App) { a.ZoomGesture(geo.V(640, 410), 1.008, time.Now()) }},
	{name: "zoom-out", frame: func(a *app.App) { a.ZoomGesture(geo.V(640, 410), 1/1.008, time.Now()) }},
	{name: "hover", frame: func(a *app.App) { hover(a.Editor()) }},
	dragger("drag-one", func(e *editor.Editor) { e.SelectNone() }),
	dragger("drag-all", func(e *editor.Editor) { e.SelectAll() }),
}

const partMark = "perf: part "

var (
	hoverAt  = 0.0
	moveTime time.Duration
	moves    int
)

// hover moves the pointer around the window.
func hover(e *editor.Editor) {
	hoverAt += 1.3
	e.PointerMove(100+math.Mod(hoverAt*3, 1000), 100+math.Mod(hoverAt*2, 600), editor.Mods{}, hoverAt)
}

// dragger returns a part that drags what is in the middle of the window around,
// the selection if there's one, and tells how many shapes went with it.
func dragger(name string, prepare func(e *editor.Editor)) part {
	var at float64
	started := false
	before := map[doc.ID]geo.Vec{}
	return part{name: name, frame: func(a *app.App) {
		e := a.Editor()
		p := findShape(e)
		if !started {
			started = true
			prepare(e)
			for id, s := range e.Doc.Shapes {
				before[id] = geo.V(s.X, s.Y)
			}
			e.PointerDown(p.X, p.Y, 0, 1, editor.Mods{}, 0)
		}
		at += 1 / 60.0
		// Round and round, at whole points as a pointer's are.
		t0 := time.Now()
		e.PointerMove(p.X+math.Round(40*math.Cos(at*3)-40), p.Y+math.Round(40*math.Sin(at*3)), editor.Mods{}, at)
		moveTime += time.Since(t0)
		moves++
	}, end: func(a *app.App) {
		fmt.Fprintf(os.Stderr, "perf: %s took %v a move, %d moves\n", name, moveTime/time.Duration(max(moves, 1)), moves)
		moveTime, moves = 0, 0
		e := a.Editor()
		moved := 0
		for id, s := range e.Doc.Shapes {
			if before[id] != geo.V(s.X, s.Y) {
				moved++
			}
		}
		fmt.Fprintf(os.Stderr, "perf: %s moved %d shapes of %d\n", name, moved, len(e.Doc.Shapes))
		p := findShape(e)
		e.PointerUp(p.X, p.Y, editor.Mods{}, at)
		e.Undo()
	}}
}

// findShape returns where on the screen the filled box nearest the middle of the
// window was: at the corner of a selection of it all, a press would resize it.
var found *geo.Vec

func findShape(e *editor.Editor) geo.Vec {
	if found == nil {
		found = &geo.Vec{}
		mid, best := geo.V(e.VW/2, e.VH/2), math.Inf(1)
		for _, id := range e.Children(e.Page) {
			if s := e.Doc.Shapes[id]; s.Type == doc.TypeGeo && s.Fill != "none" {
				p := e.ToScreen(e.PageBounds(id).Center())
				if d := p.Dist(mid); d < best {
					*found, best = p, d
				}
			}
		}
	}
	return *found
}

func main() {
	n := flag.Int("n", 500, "shapes on the page")
	file := flag.String("file", "", "a drawing to open instead")
	seconds := flag.Float64("t", 3, "seconds of each part")
	fit := flag.Bool("fit", true, "fit the page in the window, rather than show it at 100%")
	profile := flag.String("cpuprofile", "", "write a CPU profile of the script here")
	only := flag.String("only", "", "run only the part of the script of this name")
	startZ := flag.Float64("z", 0, "the zoom to start at, rather than fitting the page or 100%")
	flag.Parse()
	if os.Getenv("MYGO_FRAME_STATS") == "" {
		measure(*seconds)
		return
	}

	d := stress.Diagram(*n)
	if *file != "" {
		f, err := os.Open(*file)
		if err != nil {
			log.Fatal(err)
		}
		if d, err = doc.Read(f); err != nil {
			log.Fatal(err)
		}
		if err := f.Close(); err != nil {
			log.Fatal(err)
		}
	}
	h := &host{}
	a := app.New(d, h)
	mygo.App.WhenReady(func() {
		h.win = mygo.NewWindow(mygo.WindowOptions{Width: 1280, Height: 820, Content: ui.View(a.View), Hidden: true, AlwaysOnTop: true})
		// On top, as a window out of sight draws nothing, and without the focus, as
		// someone may be using the machine.
		h.win.ShowInactive()
		go func() {
			time.Sleep(time.Second)
			h.win.Update(func() {
				if *startZ > 0 {
					a.Editor().SetCam(editor.Camera{X: 40 / *startZ, Y: 90 / *startZ, Z: *startZ})
				} else if *fit {
					a.Editor().ZoomToFit()
				} else {
					a.Editor().SetCam(editor.Camera{X: 40, Y: 90, Z: 1})
				}
			})
			time.Sleep(500 * time.Millisecond)
			if *profile != "" {
				f, err := os.Create(*profile)
				if err != nil {
					log.Fatal(err)
				}
				// The file closes with the process, at the end of this function.
				if err := pprof.StartCPUProfile(f); err != nil {
					log.Fatal(err)
				}
			}
			for _, p := range script {
				if *only != "" && p.name != *only {
					continue
				}
				fmt.Fprintln(os.Stderr, partMark+p.name)
				end := time.Now().Add(time.Duration(*seconds * float64(time.Second)))
				for time.Now().Before(end) {
					// One at a time, as a window can't draw more than it's given time for.
					done := make(chan struct{})
					h.win.Update(func() {
						p.frame(a)
						close(done)
					})
					<-done
					time.Sleep(time.Second / 120)
				}
				if p.end != nil {
					h.win.Update(func() { p.end(a) })
				}
				time.Sleep(100 * time.Millisecond)
				fmt.Fprintln(os.Stderr, partMark)
				time.Sleep(50 * time.Millisecond)
			}
			pprof.StopCPUProfile()
			os.Exit(0)
		}()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

var frameLine = regexp.MustCompile(`took ([\d.]+) ms: build ([\d.]+).*layout ([\d.]+), paint ([\d.]+), present ([\d.]+).*?(\d+) allocations`)

// frame is a frame's total, its phases and its allocations.
type frame [6]float64

// measure runs the script in a process with frame stats on, and tells what they say.
func measure(seconds float64) {
	cmd := exec.Command(os.Args[0], os.Args[1:]...)
	cmd.Env = append(os.Environ(), "MYGO_FRAME_STATS=all")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		log.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}
	current := ""
	var order []string
	frames := map[string][]frame{}
	sc := bufio.NewScanner(stderr)
	for sc.Scan() {
		line := sc.Text()
		if name, ok := strings.CutPrefix(line, partMark); ok {
			if current = name; name != "" {
				order = append(order, name)
			}
			continue
		}
		m := frameLine.FindStringSubmatch(line)
		if m == nil {
			if !strings.Contains(line, "mygo: frame") {
				fmt.Fprintln(os.Stderr, line)
			}
			continue
		}
		if current == "" {
			continue
		}
		var f frame
		for i := range f {
			f[i], _ = strconv.ParseFloat(m[i+1], 64)
		}
		frames[current] = append(frames[current], f)
	}
	if err := cmd.Wait(); err != nil {
		log.Print(err)
	}
	fmt.Printf("%-10s %6s %5s %6s %6s %6s %6s %7s %7s %7s\n", "part", "frames", "fps", "p50", "p95", "max", "build", "paint", "present", "allocs")
	for _, name := range order {
		fs := frames[name]
		if float64(len(fs)) < seconds*20 {
			fmt.Printf("%-10s %6d frames: out of sight, or far too slow\n", name, len(fs))
			continue
		}
		sort.Slice(fs, func(i, j int) bool { return fs[i][0] < fs[j][0] })
		avg := func(i int) float64 {
			t := 0.0
			for _, f := range fs {
				t += f[i]
			}
			return t / float64(len(fs))
		}
		fmt.Printf("%-10s %6d %5.0f %6.1f %6.1f %6.1f %6.1f %7.1f %7.1f %7.0f\n", name, len(fs), float64(len(fs))/seconds,
			fs[len(fs)/2][0], fs[len(fs)*95/100][0], fs[len(fs)-1][0], avg(1), avg(3), avg(4), avg(5))
	}
}
