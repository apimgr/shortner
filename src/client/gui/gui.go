//go:build gui && (linux || darwin || windows)

// Package gui provides the native desktop client for platforms supported by
// gogpu/ui. BSD builds intentionally use the TUI.
package gui

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/gogpu/gg"
	_ "github.com/gogpu/gg/gpu"
	"github.com/gogpu/gg/integration/ggcanvas"
	"github.com/gogpu/gogpu"
	"github.com/gogpu/ui/app"
	"github.com/gogpu/ui/core/button"
	"github.com/gogpu/ui/core/dialog"
	"github.com/gogpu/ui/core/textfield"
	"github.com/gogpu/ui/primitives"
	"github.com/gogpu/ui/render"
	"github.com/gogpu/ui/state"
	"github.com/gogpu/ui/theme/material3"
	"github.com/gogpu/ui/widget"

	"github.com/apimgr/shortner/src/client/api"
	clientconfig "github.com/apimgr/shortner/src/client/config"
)

// SetupOptions carries the first-run server details into the GUI wizard.
type SetupOptions struct {
	CurrentServer string
	CurrentToken  string
	UserAgent     string
	APIVersion    string
}

// SetupResult contains the values confirmed by the GUI setup wizard.
type SetupResult struct {
	Server string
	Token  string
}

// ErrSetupCancelled identifies a GUI window closed before connection testing.
var ErrSetupCancelled = errors.New("setup cancelled")

// RunDashboard starts the native client window with link creation and listing.
func RunDashboard(client *api.Client, server string) error {
	if client == nil {
		return fmt.Errorf("GUI requires a configured API client")
	}
	return runWindow("Shortner", func(theme *material3.Theme, window *app.Window) *primitives.BoxWidget {
		return dashboard(client, server, theme, window)
	})
}

// RunSetup opens the first-run server and token form and verifies connectivity
// before returning the values for the normal config-save path.
func RunSetup(ctx context.Context, opts SetupOptions) (SetupResult, error) {
	var result SetupResult
	var completed bool
	var failure error
	err := runWindow("Shortner setup", func(theme *material3.Theme, window *app.Window) *primitives.BoxWidget {
		server := opts.CurrentServer
		token := opts.CurrentToken
		status := state.NewSignal("Enter the server URL and optional token, then test the connection.")
		serverField := textfield.New(
			textfield.InitialValue(server),
			textfield.Placeholder("https://shortner.example"),
			textfield.A11yLabel("Server URL"),
			textfield.Validation(func(value string) string {
				if !clientconfig.ValidateServerURL(strings.TrimSpace(value)) {
					return "Enter a valid HTTP or HTTPS server URL."
				}
				return ""
			}),
			textfield.OnChange(func(v string) { server = strings.TrimSpace(v) }),
		)
		tokenField := textfield.New(
			textfield.InitialValue(token),
			textfield.Placeholder("Optional bearer token"),
			textfield.InputTypeOpt(textfield.TypePassword),
			textfield.A11yLabel("Optional bearer token"),
			textfield.OnChange(func(v string) { token = strings.TrimSpace(v) }),
		)
		return primitives.Box(
			primitives.Text("Connect to a Shortner server").FontSize(24).Bold(),
			primitives.Text("The token is stored in your user config with owner-only permissions."),
			serverField,
			tokenField,
			primitives.Text("").ContentSignal(status),
			button.New(button.TextOpt("Test and save"), button.A11yHint("Verify the server and save these settings"), button.PainterOpt(material3.ButtonPainter{Theme: theme}), button.OnClick(func() {
				if err := ctx.Err(); err != nil {
					failure = err
					status.Set("Setup was cancelled.")
					return
				}
				if !clientconfig.ValidateServerURL(server) {
					status.Set("Enter a valid HTTP or HTTPS server URL.")
					return
				}
				probe := api.New(api.Options{BaseURL: server, APIVersion: opts.APIVersion, UserAgent: opts.UserAgent, Token: token})
				health, err := probe.Health(ctx)
				if err != nil {
					status.Set("Connection failed: " + err.Error())
					showError(window, theme, "Connection failed", err.Error())
					return
				}
				if health.Status != "healthy" {
					message := "Server responded, but its health status is " + health.Status + "."
					status.Set(message)
					showError(window, theme, "Server is not healthy", message)
					return
				}
				result = SetupResult{Server: server, Token: token}
				completed = true
				status.Set("Connection verified. Close this window to save configuration.")
			})),
		).Padding(24).Gap(12)
	})
	if err != nil {
		return SetupResult{}, err
	}
	if failure != nil {
		return SetupResult{}, failure
	}
	if !completed {
		return SetupResult{}, ErrSetupCancelled
	}
	return result, nil
}

func dashboard(client *api.Client, server string, theme *material3.Theme, window *app.Window) *primitives.BoxWidget {
	url := ""
	slug := ""
	updatedURL := ""
	status := state.NewSignal("Create a short link or refresh the public link list.")
	urlField := textfield.New(
		textfield.Placeholder("https://destination.example"),
		textfield.A11yLabel("Destination URL"),
		textfield.Validation(func(value string) string {
			if !clientconfig.ValidateServerURL(strings.TrimSpace(value)) {
				return "Enter a valid HTTP or HTTPS URL."
			}
			return ""
		}),
		textfield.OnChange(func(v string) { url = strings.TrimSpace(v) }),
	)
	create := button.New(button.TextOpt("Shorten URL"), button.PainterOpt(material3.ButtonPainter{Theme: theme}), button.OnClick(func() {
		if !clientconfig.ValidateServerURL(url) {
			showError(window, theme, "Invalid destination URL", "Enter a valid HTTP or HTTPS URL before shortening it.")
			return
		}
		created, err := client.CreateLink(context.Background(), url, "", "")
		if err != nil {
			status.Set("Could not create link: " + err.Error())
			showError(window, theme, "Could not create link", err.Error())
			return
		}
		status.Set(fmt.Sprintf("Created %s (save the owner token now: %s)", created.ShortURL, created.OwnerToken))
	}), button.A11yHint("Create a short link from the destination URL"))
	refresh := button.New(button.TextOpt("Refresh links"), button.PainterOpt(material3.ButtonPainter{Theme: theme}), button.OnClick(func() {
		links, err := client.ListLinks(context.Background(), 1, 10)
		if err != nil {
			status.Set("Could not load links: " + err.Error())
			showError(window, theme, "Could not refresh links", err.Error())
			return
		}
		rows := make([]string, 0, len(links.Data))
		for _, link := range links.Data {
			rows = append(rows, link.ShortCode+"  "+link.DestinationURL)
		}
		if len(rows) == 0 {
			status.Set("No public links found.")
			return
		}
		status.Set(strings.Join(rows, "\n"))
	}), button.A11yHint("Load the latest public links from the server"))
	slugField := textfield.New(textfield.Placeholder("Short code"), textfield.A11yLabel("Short code for link actions"), textfield.OnChange(func(v string) { slug = strings.TrimSpace(v) }))
	updatedURLField := textfield.New(
		textfield.Placeholder("New destination URL"),
		textfield.A11yLabel("New destination URL"),
		textfield.Validation(func(value string) string {
			if strings.TrimSpace(value) != "" && !clientconfig.ValidateServerURL(strings.TrimSpace(value)) {
				return "Enter a valid HTTP or HTTPS URL."
			}
			return ""
		}),
		textfield.OnChange(func(v string) { updatedURL = strings.TrimSpace(v) }),
	)
	getLink := button.New(button.TextOpt("Get link"), button.PainterOpt(material3.ButtonPainter{Theme: theme}), button.OnClick(func() {
		if slug == "" {
			showError(window, theme, "Short code required", "Enter a short code first.")
			return
		}
		link, err := client.GetLink(context.Background(), slug)
		if err != nil {
			status.Set("Could not load link: " + err.Error())
			showError(window, theme, "Could not load link", err.Error())
			return
		}
		expires := "never"
		if link.ExpiresAt != nil {
			expires = *link.ExpiresAt
		}
		status.Set(fmt.Sprintf("%s\n%s\nCreated: %s\nExpires: %s\nClicks: %d", link.ShortURL, link.DestinationURL, link.CreatedAt, expires, link.ClickCount))
	}), button.A11yHint("Read a link by its short code"))
	updateLink := button.New(button.TextOpt("Update destination"), button.PainterOpt(material3.ButtonPainter{Theme: theme}), button.OnClick(func() {
		if slug == "" || !clientconfig.ValidateServerURL(updatedURL) {
			showError(window, theme, "Link details required", "Enter a short code and a valid new destination URL.")
			return
		}
		link, err := client.UpdateLink(context.Background(), slug, updatedURL, "")
		if err != nil {
			status.Set("Could not update link: " + err.Error())
			showError(window, theme, "Could not update link", err.Error())
			return
		}
		status.Set("Updated " + link.ShortURL + " to " + link.DestinationURL)
	}), button.A11yHint("Change the destination for the selected short code"))
	deleteLink := button.New(button.TextOpt("Delete link"), button.PainterOpt(material3.ButtonPainter{Theme: theme}), button.OnClick(func() {
		if slug == "" {
			showError(window, theme, "Short code required", "Enter a short code first.")
			return
		}
		if err := client.DeleteLink(context.Background(), slug); err != nil {
			status.Set("Could not delete link: " + err.Error())
			showError(window, theme, "Could not delete link", err.Error())
			return
		}
		status.Set("Deleted link " + slug)
	}), button.A11yHint("Permanently delete the selected link"))
	showStats := button.New(button.TextOpt("Statistics"), button.PainterOpt(material3.ButtonPainter{Theme: theme}), button.OnClick(func() {
		if slug == "" {
			showError(window, theme, "Short code required", "Enter a short code first.")
			return
		}
		stats, err := client.GetStats(context.Background(), slug)
		if err != nil {
			status.Set("Could not load statistics: " + err.Error())
			showError(window, theme, "Could not load statistics", err.Error())
			return
		}
		status.Set(fmt.Sprintf("Statistics for %s\nTotal clicks: %d\nReferrers: %d\nRecent clicks: %d", slug, stats.TotalClicks, len(stats.Referrers), len(stats.Recent)))
	}), button.A11yHint("Read click statistics for the selected link"))
	health := button.New(button.TextOpt("Server health"), button.PainterOpt(material3.ButtonPainter{Theme: theme}), button.OnClick(func() {
		result, err := client.Health(context.Background())
		if err != nil {
			status.Set("Could not read server health: " + err.Error())
			showError(window, theme, "Health check failed", err.Error())
			return
		}
		status.Set("Server health: " + result.Status)
	}), button.A11yHint("Check the configured server health"))
	return primitives.Box(
		primitives.Text("Shortner").FontSize(28).Bold(),
		primitives.Text("Server: "+server),
		urlField,
		primitives.HBox(create, refresh).Gap(12),
		slugField,
		updatedURLField,
		primitives.HBox(getLink, updateLink).Gap(12),
		primitives.HBox(deleteLink, showStats, health).Gap(12),
		primitives.Text("").ContentSignal(status),
	).Padding(24).Gap(16)
}

func runWindow(title string, build func(*material3.Theme, *app.Window) *primitives.BoxWidget) error {
	window := gogpu.NewApp(gogpu.DefaultConfig().WithTitle(title).WithSize(900, 640))
	var theme *material3.Theme
	if systemThemeIsDark() {
		theme = material3.NewDark(widget.Hex(0x6750A4))
	} else {
		theme = material3.New(widget.Hex(0x6750A4))
	}
	ui := app.New(app.WithWindowProvider(window), app.WithPlatformProvider(window), app.WithEventSource(window.EventSource()), app.WithTheme(theme.AsTheme()))
	ui.SetRoot(build(theme, ui.Window()))
	var canvas *ggcanvas.Canvas
	window.OnDraw(func(dc *gogpu.Context) {
		w, h := dc.Width(), dc.Height()
		if w <= 0 || h <= 0 {
			return
		}
		if canvas == nil {
			provider := window.GPUContextProvider()
			if provider == nil {
				return
			}
			var err error
			canvas, err = ggcanvas.New(provider, w, h)
			if err != nil {
				log.Printf("GUI canvas initialization failed: %v", err)
				return
			}
		}
		ui.Frame()
		cw, ch := canvas.Size()
		if cw != w || ch != h {
			if err := canvas.Resize(w, h); err != nil {
				log.Printf("GUI canvas resize failed: %v", err)
				return
			}
			cw, ch = canvas.Size()
		}
		canvas.Draw(func(cc *gg.Context) {
			background := theme.Colors.Background
			cc.SetRGBA(float64(background.R), float64(background.G), float64(background.B), float64(background.A))
			cc.DrawRectangle(0, 0, float64(cw), float64(ch))
			cc.Fill()
			ui.Window().DrawTo(render.NewCanvas(cc, cw, ch))
		})
		if err := canvas.Render(dc.RenderTarget()); err != nil {
			log.Printf("GUI frame render failed: %v", err)
		}
	})
	window.OnClose(func() { gg.CloseAccelerator() })
	return window.Run()
}

func showError(window *app.Window, theme *material3.Theme, title, message string) {
	alert := dialog.New(
		dialog.Title(title),
		dialog.Content(primitives.Text(message)),
		dialog.Actions(dialog.Action{Label: "OK", Default: true}),
		dialog.PainterOpt(material3.DialogPainter{Theme: theme}),
	)
	alert.Show(window.Context())
}
