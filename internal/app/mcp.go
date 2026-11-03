package app

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

const (
	// mcpDialogWidth is as wide as the dialog gets. The modal keeps it within
	// the window as the window is now, which a width worked out here from the
	// last frame's geometry would not.
	mcpDialogWidth = 720
	// mcpDialogInset is the padding of the modal on each side.
	mcpDialogInset = 20
	// mcpFieldChrome is what a copy field takes from its text's width: its
	// padding, the button and the gap before it.
	mcpFieldChrome = 16 + 32 + 8
	mcpFieldFont   = 12
)

// mcpCopiedFor is how long a copy button says that it copied.
var mcpCopiedFor = 2 * time.Second

// MCPStatus is how the app's MCP server is doing: its URL once it listens, or
// what keeps it from listening.
type MCPStatus struct {
	URL     string
	Problem string
}

// ClaudeCommand returns the command that connects Claude Code to the server,
// for the project it runs in.
func (s MCPStatus) ClaudeCommand() string {
	return "claude mcp add --scope project --transport http godraw " + s.URL
}

// ShowMCP opens the dialog on connecting agents.
func (a *App) ShowMCP() { a.mcpOpen = true }

// mcpState returns the color and the words for how the server is doing.
func mcpState(t *ui.Theme, st MCPStatus) (ui.Color, string) {
	switch {
	case st.Problem != "":
		return t.Danger, "Not running"
	case st.URL != "":
		return t.Success, "Running"
	}
	return t.TextMuted, "Starting"
}

// mcpButton builds the button that shows the server's state, and opens the
// dialog.
func (a *App) mcpButton(c *ui.Context) {
	if a.MCP == nil {
		return
	}
	t := c.Theme()
	dot, state := mcpState(t, a.MCP())
	tip := "MCP server: " + strings.ToLower(state)
	b := ui.ButtonBase(c.Key("mcp")).Height(34).Padding(0, 8).Radius(7).Gap(6).AlignItems(ui.Center).Label(tip).Tooltip(tip)
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		ui.Box(c).Size(8, 8).Radius(4).Background(dot)
		ui.Text(c, "MCP").FontSize(12).SingleLine()
	})
	b.OnClick(a.ShowMCP)
}

// mcpDialog builds the dialog, with what to copy to connect an agent.
func (a *App) mcpDialog(c *ui.Context) {
	if a.MCP == nil {
		return
	}
	if !a.mcpOpen {
		a.mcpCopied = ""
	}
	st := a.MCP()
	t := c.Theme()
	ui.Modal(c, &a.mcpOpen, func() {
		dot, state := mcpState(t, st)
		ui.Row(c.Key("mcp-head")).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Connect an agent").FontSize(16).Bold().SingleLine()
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(8, 8).Radius(4).Background(dot)
				ui.Text(c, state).FontSize(12).TextColor(t.TextMuted).SingleLine()
			})
			ui.Box(c).Grow(1)
			// Its icon, not its box, lines up with the edge of what is below.
			iconButton(c, "mcp-close", "x", "Close", false).Margin(-8, -8, -8, 0).OnClick(func() { a.mcpOpen = false })
		})
		switch {
		case st.Problem != "":
			ui.Text(c, st.Problem).FontSize(13).TextColor(t.TextMuted)
		case st.URL != "":
			ui.Text(c, "Agents can draw on this canvas over MCP while GoDraw is open.").FontSize(13).TextColor(t.TextMuted)
			a.mcpField(c, "Claude Code", "Run this in your project's folder, then approve the server in a new session there.", st.ClaudeCommand())
			a.mcpField(c, "Other MCP clients", "Add this URL as a Streamable HTTP server.", st.URL)
		}
	}).Width(mcpDialogWidth).Gap(16)
}

// mcpField builds a titled line of text to copy, with a note on what to do
// with it.
func (a *App) mcpField(c *ui.Context, title, note, text string) {
	t := c.Theme()
	ui.Column(c.Key(title)).Gap(6).Children(func() {
		ui.Text(c, title).FontSize(13).Bold()
		ui.Row(c).Padding(4, 4, 4, 12).Gap(8).Radius(9).AlignItems(ui.Center).Background(t.Surface).Children(func() {
			box := ui.Box(c).Grow(1).Basis(0).Selectable()
			box.Children(func() {
				ui.Text(c, a.mcpShown(c, text)).Font(family("mono")).FontSize(mcpFieldFont)
			})
			a.mcpCopyButton(c, title, text)
		})
		ui.Text(c, note).FontSize(12).TextColor(t.TextMuted)
	})
}

// mcpShown returns text as a field shows it. A command too long for the field
// breaks before its last word, the URL, which would otherwise break inside.
// The break is one a shell reads through, for a command selected by hand.
func (a *App) mcpShown(c *ui.Context, text string) string {
	w, _ := c.Size()
	room := min(mcpDialogWidth, w-2*mcpDialogInset) - 2*mcpDialogInset - mcpFieldChrome
	if a.meas.measure(text, "mono", mcpFieldFont, 0).W <= float64(room) {
		return text
	}
	if i := strings.LastIndexByte(text, ' '); i > 0 {
		return text[:i] + " \\\n" + text[i+1:]
	}
	return text
}

// mcpCopyButton builds the button that copies a field's text. It keeps its
// size and place as it tells that it copied.
func (a *App) mcpCopyButton(c *ui.Context, title, text string) {
	t := c.Theme()
	copied := a.mcpCopied == title
	glyph, tip := "copy", "Copy"
	if copied {
		glyph, tip = "check", "Copied"
	}
	b := ui.ButtonBase(c.Key("copy")).Size(32, 32).Radius(5).Center().Label(tip + " " + title).Tooltip(tip)
	switch {
	case b.Pressed():
		b.Background(t.SurfacePressed)
	case b.Hovered():
		b.Background(t.SurfaceHover)
	}
	if copied {
		b.TextColor(t.Success)
	}
	b.Children(func() { ui.Icon(c, icon(glyph)).Size(16, 16) })
	b.OnClick(func() {
		c.Services().WriteClipboard(text)
		a.mcpCopied = title
		a.mcpCopies++
		copies := a.mcpCopies
		time.AfterFunc(mcpCopiedFor, func() {
			a.host.Update(func() {
				if a.mcpCopies == copies { // not if another copy came since
					a.mcpCopied = ""
				}
			})
		})
	})
}
