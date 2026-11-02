package app

import (
	"fmt"
	"math"
	"sync"

	"github.com/egoist/mygo/ui"

	"godraw/internal/assets"
	"godraw/internal/editor"
	"godraw/internal/geo"
)

var (
	iconCache = map[string]*ui.SVG{}
	iconMu    sync.Mutex
)

func icon(name string) *ui.SVG {
	iconMu.Lock()
	defer iconMu.Unlock()
	if s, ok := iconCache[name]; ok {
		return s
	}
	data, err := assets.Icons.ReadFile("icons/" + name + ".svg")
	if err != nil {
		panic(err)
	}
	s := ui.MustParseSVG(data)
	iconCache[name] = s
	return s
}

// panelStyle gives an element the look of a floating panel.
func panelStyle(c *ui.Context, e ui.Element) ui.Element {
	t := c.Theme()
	shadow := ui.RGBA(0, 0, 0, 0.14)
	if t.Dark {
		shadow = ui.RGBA(0, 0, 0, 0.5)
	}
	// Being above the canvas in the tree, it keeps the pointer from it.
	return e.Radius(10).Background(t.Background).Border(1, t.Border.Alpha(0.5)).
		Shadow(0, 2, 12, 0, shadow)
}

// iconButton builds a square button showing an icon.
func iconButton(c *ui.Context, key, name, tip string, active bool) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(key)).Size(34, 34).Radius(7).Center().Label(tip).Tooltip(tip)
	switch {
	case active:
		b.Background(t.Accent).TextColor(t.AccentText)
	case b.Pressed():
		b.Background(t.SurfacePressed)
	case b.Hovered():
		b.Background(t.SurfaceHover)
	}
	b.Children(func() { ui.Icon(c, icon(name)).Size(18, 18) })
	return b
}

// toolbar builds the row of tools at the bottom.
func (a *App) toolbar(c *ui.Context) {
	e := a.ed
	tools := []struct {
		tool editor.Tool
		icon string
		tip  string
	}{
		{editor.ToolSelect, "mouse-pointer-2", "Select — V"},
		{editor.ToolHand, "hand", "Hand — H"},
		{editor.ToolDraw, "pencil", "Draw — D"},
		{editor.ToolEraser, "eraser", "Eraser — E"},
		{editor.ToolArrow, "arrow-up-right", "Arrow — A"},
		{editor.ToolText, "type", "Text — T"},
		{editor.ToolNote, "sticky-note", "Note — N"},
	}
	after := []struct {
		tool editor.Tool
		icon string
		tip  string
	}{
		{editor.ToolLine, "minus", "Line — L"},
		{editor.ToolHighlight, "highlighter", "Highlighter — Shift+D"},
		{editor.ToolLaser, "zap", "Laser — K"},
		{editor.ToolFrame, "frame", "Frame — F"},
	}
	wrap := ui.Row(c.Key("toolbar-wrap")).Absolute().Left(0).Right(0).Bottom(14).Justify(ui.Center).PassThrough()
	wrap.Children(func() {
		panelStyle(c, ui.Row(c.Key("toolbar"))).Padding(4).Gap(2).FocusGroup(ui.Horizontal).Children(func() {
			for _, t := range tools {
				b := iconButton(c, string(t.tool), t.icon, t.tip, e.Tool == t.tool)
				tool := t.tool
				b.OnClick(func() { e.SetTool(tool); a.refocus(c) })
			}
			if iconButton(c, "image", "image", "Insert image — ⌘U", false).Clicked() {
				a.InsertImageFromDialog()
			}
			a.geoButton(c)
			for _, t := range after {
				b := iconButton(c, string(t.tool), t.icon, t.tip, e.Tool == t.tool)
				tool := t.tool
				b.OnClick(func() { e.SetTool(tool); a.refocus(c) })
			}
			ui.Box(c).Size(1, 24).Margin(0, 3).Background(c.Theme().Border)
			lockIcon := "lock-open"
			if e.ToolLocked {
				lockIcon = "lock"
			}
			if iconButton(c, "tool-lock", lockIcon, "Keep tool selected — Q", e.ToolLocked).Clicked() {
				e.ToolLocked = !e.ToolLocked
				a.refocus(c)
			}
		})
	})
}

func (a *App) refocus(c *ui.Context) { a.canvasHandle.Focus(c) }

// geoIcon draws a geo shape, in a box, for buttons.
func geoIcon(c *ui.Context, kind string) ui.Element {
	return ui.Box(c).Size(18, 18).Draw(func(p *ui.Painter, r ui.Rect) {
		col := p.Theme().Text
		drawGeoIcon(p, kind, r, col)
	})
}

func drawGeoIcon(p *ui.Painter, kind string, r ui.Rect, col ui.Color) {
	pts := editor.GeoOutlineOf(kind, float64(r.W)-2, float64(r.H)-2)
	var path ui.Path
	for i, q := range pts {
		x, y := r.X+1+float32(q.X), r.Y+1+float32(q.Y)
		if i == 0 {
			path.MoveTo(x, y)
		} else {
			path.LineTo(x, y)
		}
	}
	path.Close()
	switch kind {
	case "x-box":
		path.MoveTo(r.X+1, r.Y+1).LineTo(r.X+r.W-1, r.Y+r.H-1).MoveTo(r.X+r.W-1, r.Y+1).LineTo(r.X+1, r.Y+r.H-1)
	case "check-box":
		path.MoveTo(r.X+5, r.Y+r.H/2).LineTo(r.X+r.W/2-1, r.Y+r.H-5).LineTo(r.X+r.W-4, r.Y+5)
	}
	p.StrokePath(&path, 1.6, col)
}

// geoButton builds the button of the geo tool, with its menu of shapes.
func (a *App) geoButton(c *ui.Context) {
	e := a.ed
	t := c.Theme()
	active := e.Tool == editor.ToolGeo
	b := ui.ButtonBase(c.Key("geo")).Size(44, 34).Radius(7).Center().Gap(2).Label("Geo shapes").Tooltip("Rectangle — R · Ellipse — O")
	switch {
	case active:
		b.Background(t.Accent).TextColor(t.AccentText)
	case b.Hovered():
		b.Background(t.SurfaceHover)
	}
	kind := e.Next.Geo
	b.Children(func() {
		ui.Box(c).Size(18, 18).Draw(func(p *ui.Painter, r ui.Rect) {
			col := t.Text
			if active {
				col = t.AccentText
			}
			drawGeoIcon(p, kind, r, col)
		})
		ui.Icon(c, icon("chevron-down")).Size(10, 10)
	})
	if b.Clicked() {
		if active {
			a.geoOpen = !a.geoOpen
		} else {
			e.SetGeoTool(e.Next.Geo)
			a.geoOpen = true
		}
	}
	ui.Popover(c, b, &a.geoOpen, func() {
		ui.Grid(c).ColumnTracks(ui.Fixed(34), ui.Fixed(34), ui.Fixed(34), ui.Fixed(34), ui.Fixed(34)).Gap(2).Children(func() {
			for _, k := range editor.GeoKinds {
				cell := ui.ButtonBase(c.Key("g-"+k)).Size(34, 34).Radius(7).Center().Label(k)
				if e.Next.Geo == k {
					cell.Background(t.Accent.Alpha(0.2))
				} else if cell.Hovered() {
					cell.Background(t.SurfaceHover)
				}
				cell.Children(func() { geoIcon(c, k) })
				kind := k
				if cell.Clicked() {
					e.SetGeoTool(kind)
					a.geoOpen = false
					a.refocus(c)
				}
			}
		})
	})
}

// --- style panel ---

func (a *App) stylePanel(c *ui.Context) {
	e := a.ed
	colorV, showColor := e.SharedStyle(editor.StyleColor)
	fillV, showFill := e.SharedStyle(editor.StyleFill)
	dashV, showDash := e.SharedStyle(editor.StyleDash)
	sizeV, showSize := e.SharedStyle(editor.StyleSize)
	fontV, showFont := e.SharedStyle(editor.StyleFont)
	alignV, showAlign := e.SharedStyle(editor.StyleAlign)
	valignV, showV := e.SharedStyle(editor.StyleVAlign)
	kindV, showKind := e.SharedStyle(editor.StyleKind)
	headS, showHeads := e.SharedStyle(editor.StyleHeadS)
	headE, _ := e.SharedStyle(editor.StyleHeadE)
	splineV, showSpline := e.SharedStyle(editor.StyleSpline)
	if !showColor && !showFill && !showDash && !showSize && !showFont {
		return
	}
	panel := panelStyle(c, ui.Column(c.Key("styles"))).Absolute().Top(12).Right(12).Width(180).Padding(8).Gap(8)
	panel.Children(func() {
		if showColor {
			a.colorGrid(c, colorV)
		}
		a.opacityRow(c)
		if showFill {
			a.fillRow(c, fillV)
		}
		if showDash {
			a.dashRow(c, dashV)
		}
		if showSize {
			a.textRow(c, "size", editor.StyleSize, sizeV, []string{"s", "m", "l", "xl"}, []string{"S", "M", "L", "XL"}, nil)
		}
		if showFont {
			a.textRow(c, "font", editor.StyleFont, fontV, []string{"draw", "sans", "serif", "mono"}, []string{"Aa", "Aa", "Aa", "Aa"}, []string{"draw", "sans", "serif", "mono"})
		}
		if showAlign {
			a.iconRow(c, "align", editor.StyleAlign, alignV, []string{"start", "middle", "end"}, []string{"align-left", "align-center", "align-right"})
		}
		if showV {
			a.iconRow(c, "valign", editor.StyleVAlign, valignV, []string{"start", "middle", "end"}, []string{"align-start-horizontal", "align-center-horizontal", "align-end-horizontal"})
		}
		if showKind {
			a.textRow(c, "kind", editor.StyleKind, kindV, []string{"arc", "elbow"}, []string{"Curve", "Elbow"}, nil)
		}
		if showHeads {
			a.headRow(c, "Start", editor.StyleHeadS, headS)
			a.headRow(c, "End", editor.StyleHeadE, headE)
		}
		if showSpline {
			a.textRow(c, "spline", editor.StyleSpline, splineV, []string{"line", "cubic"}, []string{"Line", "Curve"}, nil)
		}
	})
}

func (a *App) colorGrid(c *ui.Context, cur string) {
	e := a.ed
	pal := e.Palette()
	ui.Grid(c.Key("colors")).Columns(4).Gap(2).Children(func() {
		for _, name := range editor.Colors {
			b := ui.ButtonBase(c.Key("c-" + name)).Height(32).Radius(6).Center().Label(name).Tooltip(name)
			if cur == name {
				b.Background(c.Theme().SurfacePressed)
			} else if b.Hovered() {
				b.Background(c.Theme().SurfaceHover)
			}
			col := toUI(pal.Swatch(name).Solid)
			b.Children(func() {
				dot := ui.Box(c).Size(16, 16).Radius(8).Background(col)
				if name == "white" {
					dot.Border(1, c.Theme().Border)
				}
			})
			v := name
			b.OnClick(func() { e.SetStyle(editor.StyleColor, v); a.refocus(c) })
		}
	})
}

func (a *App) opacityRow(c *ui.Context) {
	e := a.ed
	if !a.opacityLive {
		a.opacity = e.SharedOpacity()
	}
	s := ui.Slider(c.Key("opacity"), &a.opacity, 0.1, 1).Label("Opacity").Tooltip(fmt.Sprintf("Opacity %d%%", int(math.Round(a.opacity*100))))
	if s.Changed() {
		a.opacityLive = true
		e.SetOpacity(a.opacity, true)
	}
	if a.opacityLive && !s.Pressed() {
		a.opacityLive = false
		e.CommitOpen()
	}
}

// segment builds a row of options, one chosen.
func segment(c *ui.Context, key string, n int, chosen func(i int) bool, label func(i int) string, cell func(i int), pick func(i int)) {
	t := c.Theme()
	ui.Row(c.Key(key)).Gap(2).Children(func() {
		for i := 0; i < n; i++ {
			b := ui.ButtonBase(c.Key(fmt.Sprint(key, i))).Grow(1).Basis(0).Height(30).Radius(6).Center().Label(label(i)).Tooltip(label(i))
			if chosen(i) {
				b.Background(t.SurfacePressed)
			} else if b.Hovered() {
				b.Background(t.SurfaceHover)
			}
			i := i
			b.Children(func() { cell(i) })
			b.OnClick(func() { pick(i) })
		}
	})
}

func (a *App) fillRow(c *ui.Context, cur string) {
	e := a.ed
	vals := []string{"none", "semi", "solid", "pattern", "fill"}
	segment(c, "fill", len(vals), func(i int) bool { return vals[i] == cur }, func(i int) string { return "Fill: " + vals[i] }, func(i int) {
		v := vals[i]
		ui.Box(c).Size(16, 16).Draw(func(p *ui.Painter, r ui.Rect) { drawFillIcon(p, v, r) })
	}, func(i int) { e.SetStyle(editor.StyleFill, vals[i]); a.refocus(c) })
}

func drawFillIcon(p *ui.Painter, v string, r ui.Rect) {
	t := p.Theme()
	path := (&ui.Path{}).Circle(r.X+r.W/2, r.Y+r.H/2, r.W/2-1)
	switch v {
	case "semi":
		p.FillPath((&ui.Path{}).Circle(r.X+r.W/2, r.Y+r.H/2, r.W/2-1), t.Surface)
	case "solid":
		p.FillPath((&ui.Path{}).Circle(r.X+r.W/2, r.Y+r.H/2, r.W/2-1), t.Text.Alpha(0.25))
	case "fill":
		p.FillPath((&ui.Path{}).Circle(r.X+r.W/2, r.Y+r.H/2, r.W/2-1), t.Text)
	case "pattern":
		cx, cy, rad := r.X+r.W/2, r.Y+r.H/2, r.W/2-1
		for _, o := range []float32{-4, 0, 4} {
			// Chords of the circle along a diagonal.
			hw := float32(math.Sqrt(float64(rad*rad - o*o/2)))
			dx := hw * 0.7071
			p.StrokePath((&ui.Path{}).MoveTo(cx+o*0.7071+dx, cy+o*0.7071-dx).LineTo(cx+o*0.7071-dx, cy+o*0.7071+dx), 1.2, t.Text)
		}
	}
	p.StrokePath(path, 1.6, t.Text)
}

func (a *App) dashRow(c *ui.Context, cur string) {
	e := a.ed
	vals := []string{"draw", "dashed", "dotted", "solid"}
	segment(c, "dash", len(vals), func(i int) bool { return vals[i] == cur }, func(i int) string { return "Stroke: " + vals[i] }, func(i int) {
		v := vals[i]
		ui.Box(c).Size(22, 16).Draw(func(p *ui.Painter, r ui.Rect) {
			path := &ui.Path{}
			y := r.Y + r.H/2
			if v == "draw" {
				path.MoveTo(r.X+1, y+2).CubeTo(r.X+6, y-4, r.X+14, y+4, r.X+r.W-1, y-2)
			} else {
				path.MoveTo(r.X+1, y).LineTo(r.X+r.W-1, y)
			}
			switch v {
			case "dashed":
				path.Dash(0, 4, 3)
			case "dotted":
				path.Dash(0, 0.1, 4)
			}
			p.StrokePath(path, 2, p.Theme().Text)
		})
	}, func(i int) { e.SetStyle(editor.StyleDash, vals[i]); a.refocus(c) })
}

func (a *App) textRow(c *ui.Context, key string, prop editor.StyleProp, cur string, vals, labels, fonts []string) {
	e := a.ed
	segment(c, key, len(vals), func(i int) bool { return vals[i] == cur }, func(i int) string { return vals[i] }, func(i int) {
		t := ui.Text(c, labels[i]).SingleLine()
		if fonts != nil {
			t.Font(family(fonts[i])).FontSize(15)
		} else if len(labels[i]) <= 2 {
			t.Bold()
		}
	}, func(i int) { e.SetStyle(prop, vals[i]); a.refocus(c) })
}

func (a *App) iconRow(c *ui.Context, key string, prop editor.StyleProp, cur string, vals, icons []string) {
	e := a.ed
	segment(c, key, len(vals), func(i int) bool { return vals[i] == cur }, func(i int) string { return vals[i] }, func(i int) {
		ui.Icon(c, icon(icons[i])).Size(16, 16)
	}, func(i int) { e.SetStyle(prop, vals[i]); a.refocus(c) })
}

func (a *App) headRow(c *ui.Context, label string, prop editor.StyleProp, cur string) {
	e := a.ed
	heads := []string{"none", "arrow", "triangle", "square", "dot", "diamond", "inverted", "bar"}
	ui.Row(c.Key("head-" + label)).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(12).Width(32).TextColor(c.Theme().TextMuted)
		ui.Select(c.Key("sel-"+label), &cur, heads).Grow(1).Label(label + " arrowhead").OnChange(func() {
			e.SetStyle(prop, cur)
			a.refocus(c)
		})
	})
}

// --- top left ---

func (a *App) topLeft(c *ui.Context) {
	e := a.ed
	t := c.Theme()
	panelStyle(c, ui.Row(c.Key("topleft"))).Absolute().Top(12).Left(12).Padding(4).Gap(2).AlignItems(ui.Center).Children(func() {
		page := e.Doc.Page(e.Page)
		name := "Page"
		if page != nil {
			name = page.Name
		}
		b := ui.ButtonBase(c.Key("pages")).Height(34).Padding(0, 8).Radius(7).Gap(6).AlignItems(ui.Center).Label("Pages").Tooltip("Pages")
		if b.Hovered() {
			b.Background(t.SurfaceHover)
		}
		b.Children(func() {
			ui.Icon(c, icon("layers")).Size(16, 16)
			ui.Text(c, name).SingleLine().MaxWidth(120)
			ui.Icon(c, icon("chevron-down")).Size(12, 12)
		})
		if b.Clicked() {
			a.pagesOpen = !a.pagesOpen
		}
		ui.Popover(c, b, &a.pagesOpen, func() { a.pagesMenu(c) })

		undo := iconButton(c, "undo", "undo-2", "Undo — ⌘Z", false)
		if !e.CanUndo() {
			undo.Opacity(0.35)
		}
		undo.OnClick(func() { e.Undo(); a.refocus(c) })
		redo := iconButton(c, "redo", "redo-2", "Redo — ⇧⌘Z", false)
		if !e.CanRedo() {
			redo.Opacity(0.35)
		}
		redo.OnClick(func() { e.Redo(); a.refocus(c) })
		has := len(e.Selection()) > 0
		del := iconButton(c, "delete", "trash-2", "Delete — ⌫", false)
		dup := iconButton(c, "duplicate", "copy", "Duplicate — ⌘D", false)
		if !has {
			del.Opacity(0.35)
			dup.Opacity(0.35)
		}
		del.OnClick(func() { e.Delete(); a.refocus(c) })
		dup.OnClick(func() { e.Duplicate(); a.refocus(c) })
		iconButton(c, "more", "ellipsis", "More", false).PopoverMenuButton(a.moreMenu)
	})
}

// moreMenu is the menu of what else there is to do.
func (a *App) moreMenu(m *ui.Menu) {
	e := a.ed
	if m.Item("Export as PNG…").Chosen() {
		a.ExportPNG()
	}
	if m.Item("Export as SVG…").Chosen() {
		a.ExportSVG()
	}
	m.Separator()
	if m.Item("Show Grid").Checked(e.Grid).Chosen() {
		e.Grid = !e.Grid
	}
	if m.Item("Always Snap").Checked(e.Snap).Chosen() {
		e.Snap = !e.Snap
	}
	if m.Item("Minimap").Checked(a.minimap).Chosen() {
		a.minimap = !a.minimap
	}
	if m.Item("Focus Mode").Checked(e.FocusMode).Shortcut(ui.Cmd, ui.KeyPeriod).Chosen() {
		e.FocusMode = !e.FocusMode
	}
	m.Submenu("Theme", func(m *ui.Menu) {
		if m.Item("System").Radio(a.theme == "").Chosen() {
			a.theme = ""
		}
		if m.Item("Light").Radio(a.theme == "light").Chosen() {
			a.theme = "light"
		}
		if m.Item("Dark").Radio(a.theme == "dark").Chosen() {
			a.theme = "dark"
		}
	})
	m.Separator()
	if m.Item("Keyboard Shortcuts").Shortcut(ui.Shift, ui.KeySlash).Chosen() {
		a.helpOpen = true
	}
}

func (a *App) pagesMenu(c *ui.Context) {
	e := a.ed
	t := c.Theme()
	ui.Column(c).Width(240).Gap(2).Children(func() {
		for _, p := range e.Pages() {
			id := p.ID
			row := ui.Row(c.Key(string(id))).Height(32).Radius(6).Gap(4).AlignItems(ui.Center).PaddingX(4)
			row.Children(func() {
				if a.renamingPage == id {
					in := ui.TextInput(c.Key("rename"), &a.renameText).Grow(1).AutoFocus().Label("Page name")
					if in.Submitted() || c.Shortcut(0, ui.KeyEscape) {
						e.RenamePage(id, a.renameText)
						a.renamingPage = ""
					}
					return
				}
				b := ui.ButtonBase(c.Key("go")).Grow(1).Height(28).Radius(5).PaddingX(6).AlignItems(ui.Center).Justify(ui.Start).Gap(6).Label(p.Name)
				if b.Hovered() {
					b.Background(t.SurfaceHover)
				}
				b.Children(func() {
					mark := ui.Icon(c, icon("check")).Size(14, 14)
					if id != e.Page {
						mark.Opacity(0)
					}
					ui.Text(c, p.Name).SingleLine()
				})
				if b.DoubleClicked() {
					a.renamingPage, a.renameText = id, p.Name
				} else if b.Clicked() {
					e.SetPage(id)
				}
				if len(e.Pages()) > 1 {
					d := ui.ButtonBase(c.Key("del")).Size(24, 24).Radius(5).Center().Label("Delete page").Tooltip("Delete page")
					if d.Hovered() {
						d.Background(t.SurfaceHover)
					}
					d.Children(func() { ui.Icon(c, icon("x")).Size(14, 14) })
					d.OnClick(func() { e.DeletePage(id) })
				}
			})
		}
		ui.Divider(c)
		add := ui.ButtonBase(c.Key("add")).Height(30).Radius(6).PaddingX(8).Gap(6).AlignItems(ui.Center).Justify(ui.Start).Label("New page")
		if add.Hovered() {
			add.Background(t.SurfaceHover)
		}
		add.Children(func() {
			ui.Icon(c, icon("plus")).Size(14, 14)
			ui.Text(c, "New page")
		})
		add.OnClick(func() {
			e.NewPage()
			a.pagesOpen = false
			a.refocus(c)
		})
	})
}

// --- zoom ---

func (a *App) zoomPanel(c *ui.Context) {
	e := a.ed
	t := c.Theme()
	ui.Column(c.Key("bottomleft")).Absolute().Left(12).Bottom(14).Gap(6).AlignItems(ui.Start).PassThrough().Children(func() {
		if a.minimap {
			a.minimapView(c)
		}
		panelStyle(c, ui.Row(c.Key("zoom"))).Padding(4).Gap(2).AlignItems(ui.Center).Children(func() {
			if iconButton(c, "map", "map", "Minimap", a.minimap).Clicked() {
				a.minimap = !a.minimap
				a.refocus(c)
			}
			iconButton(c, "zoom-out", "minus", "Zoom out — ⌘-", false).OnClick(func() { e.ZoomOut(); a.refocus(c) })
			b := ui.ButtonBase(c.Key("zoom-reset")).Width(52).Height(34).Radius(7).Center().Label("Zoom").Tooltip("Reset zoom — ⇧0 · Double click to fit")
			if b.Hovered() {
				b.Background(t.SurfaceHover)
			}
			b.Children(func() { ui.Text(c, fmt.Sprintf("%d%%", int(math.Round(e.Cam().Z*100)))).FontSize(12).SingleLine() })
			if b.DoubleClicked() {
				e.ZoomToFit()
			} else if b.Clicked() {
				e.ResetZoom()
			}
			iconButton(c, "zoom-in", "plus", "Zoom in — ⌘=", false).OnClick(func() { e.ZoomIn(); a.refocus(c) })
			iconButton(c, "zoom-fit", "maximize", "Zoom to fit — ⇧1", false).OnClick(func() { e.ZoomToFit(); a.refocus(c) })
			iconButton(c, "help", "circle-help", "Keyboard shortcuts — ?", false).OnClick(func() { a.helpOpen = true })
		})
	})
}

func (a *App) minimapView(c *ui.Context) {
	e := a.ed
	const w, h = 200, 130
	content, ok := e.ContentBounds()
	view := e.ViewBox()
	if ok {
		content = content.Union(view)
	} else {
		content = view
	}
	content = content.Expand(math.Max(content.W, content.H) * 0.05)
	k := math.Min(float64(w)/content.W, float64(h)/content.H)
	toMap := func(p geo.Vec) (float32, float32) {
		return float32((p.X-content.X)*k + (float64(w)-content.W*k)/2), float32((p.Y-content.Y)*k + (float64(h)-content.H*k)/2)
	}
	pal := e.Palette()
	box := panelStyle(c, ui.Box(c.Key("minimap"))).Size(w, h).Clip()
	box.HandleInput(func(ev ui.InputEvent) bool {
		switch ev.Kind {
		case ui.InputPointerDown, ui.InputPointerMove:
			if ev.Kind == ui.InputPointerMove && ev.Button < 0 {
				return true
			}
			px := (float64(ev.X)-(float64(w)-content.W*k)/2)/k + content.X
			py := (float64(ev.Y)-(float64(h)-content.H*k)/2)/k + content.Y
			cam := e.Cam()
			e.SetCam(editor.Camera{X: e.VW/2/cam.Z - px, Y: e.VH/2/cam.Z - py, Z: cam.Z})
			return true
		case ui.InputPointerUp:
			return true
		}
		return false
	})
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		fill := toUI(pal.Text.Alpha(0.16))
		for _, id := range e.Children(e.Page) {
			b := e.PageBounds(id)
			x, y := toMap(geo.V(b.X, b.Y))
			rect := ui.Rect{X: r.X + x, Y: r.Y + y, W: max(float32(b.W*k), 1), H: max(float32(b.H*k), 1)}
			p.Fill(rect, fill, 1)
			if e.IsSelected(id) {
				p.Stroke(rect, toUI(pal.Selection), 1, 1)
			}
		}
		x, y := toMap(geo.V(view.X, view.Y))
		rect := ui.Rect{X: r.X + x, Y: r.Y + y, W: float32(view.W * k), H: float32(view.H * k)}
		p.Fill(rect, toUI(pal.Selection.Alpha(0.1)), 2)
		p.Stroke(rect, toUI(pal.Selection), 2, 1)
	})
}

// --- help ---

func (a *App) helpDialog(c *ui.Context) {
	ui.Modal(c, &a.helpOpen, func() {
		ui.Text(c, "Keyboard shortcuts").FontSize(16).Bold()
		ui.Grid(c).ColumnTracks(ui.Fixed(290), ui.Fixed(290)).GapX(24).GapY(4).Children(func() {
			for _, g := range [][2]string{
				{"V", "Select"}, {"⇧1", "Zoom to fit"},
				{"H, or hold Space", "Hand"}, {"⇧2", "Zoom to selection"},
				{"D", "Draw"}, {"⇧0", "Zoom to 100%"},
				{"⇧D", "Highlighter"}, {"⌘= / ⌘-", "Zoom in / out"},
				{"E", "Eraser"}, {"⌘' / ⌘.", "Grid / focus mode"},
				{"A", "Arrow"}, {"⌘/", "Dark mode"},
				{"T", "Text"}, {"⌘G / ⇧⌘G", "Group / ungroup"},
				{"N", "Note"}, {"⇧L", "Lock"},
				{"R / O", "Rectangle / ellipse"}, {"⌘D", "Duplicate"},
				{"L", "Line"}, {"[ / ]", "Send backward / bring forward"},
				{"F", "Frame"}, {"⌘[ / ⌘]", "Send to back / bring to front"},
				{"K", "Laser"}, {"⌥A ⌥H ⌥D", "Align left / center / right"},
				{"Q", "Keep tool selected"}, {"⌥W ⌥V ⌥S", "Align top / middle / bottom"},
				{"⌘U", "Insert image"}, {"⇧H / ⇧V", "Flip"},
				{"Enter", "Edit text"}, {"Arrows", "Nudge (⇧ for 10)"},
				{"Shift while dragging", "Keep proportions, 15° steps"}, {"⌘ while dragging", "Snap, or free an arrow"},
				{"⌥ while dragging", "Copy, or resize from the center"}, {"⌘ with a brush", "Select only what is wrapped"},
			} {
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, g[0]).Width(130).TextColor(c.Theme().TextMuted).FontSize(12)
					ui.Text(c, g[1]).FontSize(12)
				})
			}
		})
		ui.Row(c).Justify(ui.End).Children(func() {
			if ui.PrimaryButton(c, "Close").Clicked() {
				a.helpOpen = false
			}
		})
	})
}
