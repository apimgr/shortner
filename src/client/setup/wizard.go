// Package setup implements the client's first-run configuration wizard.
// AI.md PART 32 makes the wizard the client's only configuration surface —
// there is no Web UI for a CLI — so it must run wherever the client can be
// used interactively.
package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/apimgr/shortner/src/client/api"
	"github.com/apimgr/shortner/src/common/display"
)

// Mode is the interface the wizard runs in.
type Mode int

const (
	// ModeError means neither a display nor a terminal is available, so no
	// interactive setup is possible.
	ModeError Mode = iota
	// ModeTUI is the terminal wizard.
	ModeTUI
	// ModeGUI is the native graphical wizard.
	ModeGUI
)

// ErrSetupCancelled is returned when the user aborts the wizard.
var ErrSetupCancelled = errors.New("setup cancelled")

// Options configures a wizard run.
type Options struct {
	In            io.Reader
	Out           io.Writer
	Err           io.Writer
	BinaryName    string
	CurrentServer string
	CurrentToken  string
	UserAgent     string
	APIVersion    string
}

// Result is the configuration the user confirmed.
type Result struct {
	Server string
	Token  string
}

// SelectSetupMode picks the wizard interface, following AI.md PART 32's
// priority order: a remote session always gets the TUI even when X11
// forwarding is available, then a local display gets the GUI, then any
// terminal gets the TUI.
func SelectSetupMode() Mode {
	env := display.DetectDisplayEnv()

	if env.IsSSH || env.IsMosh {
		return ModeTUI
	}
	if env.HasDisplay {
		return ModeGUI
	}
	if env.IsTerminal {
		return ModeTUI
	}
	return ModeError
}

// Run launches the setup wizard and returns the confirmed configuration.
func Run(ctx context.Context, opts Options) (Result, error) {
	switch SelectSetupMode() {
	case ModeError:
		return Result{}, fmt.Errorf("cannot run setup without a terminal: pass --server URL, or set %s_SERVER_PRIMARY", "SHORTNER")
	case ModeGUI:
		// The native GUI wizard ships behind the `gui` build tag (AI.md
		// PART 32 forbids Electron or a TUI wrapper); the default CGO-free
		// build runs the terminal wizard, which is feature-identical.
		return runTUI(ctx, opts)
	default:
		return runTUI(ctx, opts)
	}
}

// field identifies which input has focus.
type field int

const (
	fieldServer field = iota
	fieldToken
)

// testResultMsg carries the outcome of a connection test.
type testResultMsg struct {
	health *api.Health
	err    error
}

// wizardModel is the bubbletea model for the terminal wizard.
type wizardModel struct {
	ctx        context.Context
	opts       Options
	inputs     []textinput.Model
	focus      field
	testing    bool
	tested     bool
	health     *api.Health
	err        error
	cancelled  bool
	confirmed  bool
	statusLine string
}

// runTUI drives the bubbletea wizard.
func runTUI(ctx context.Context, opts Options) (Result, error) {
	server := textinput.New()
	server.Prompt = "Server URL:  "
	server.Placeholder = "https://"
	server.CharLimit = 2048
	server.SetValue(opts.CurrentServer)
	server.Focus()

	token := textinput.New()
	token.Prompt = "API token:   "
	token.Placeholder = "optional"
	token.CharLimit = 512
	token.EchoMode = textinput.EchoPassword
	token.EchoCharacter = '*'
	token.SetValue(opts.CurrentToken)

	m := wizardModel{
		ctx:    ctx,
		opts:   opts,
		inputs: []textinput.Model{server, token},
	}

	program := tea.NewProgram(m,
		tea.WithContext(ctx),
		tea.WithInput(opts.In),
		tea.WithOutput(opts.Out),
	)
	final, err := program.Run()
	if err != nil {
		return Result{}, err
	}

	result, ok := final.(wizardModel)
	if !ok || result.cancelled || !result.confirmed {
		return Result{}, ErrSetupCancelled
	}
	return Result{
		Server: result.inputs[fieldServer].Value(),
		Token:  result.inputs[fieldToken].Value(),
	}, nil
}

// Init starts the wizard with a blinking cursor.
func (m wizardModel) Init() tea.Cmd {
	return textinput.Blink
}

// testConnection validates the server URL by fetching its health document,
// which is the wizard's "Test Connection" action.
func (m wizardModel) testConnection() tea.Cmd {
	server := m.inputs[fieldServer].Value()
	token := m.inputs[fieldToken].Value()
	userAgent := m.opts.UserAgent
	apiVersion := m.opts.APIVersion
	ctx := m.ctx

	return func() tea.Msg {
		client := api.New(api.Options{
			BaseURL:    server,
			APIVersion: apiVersion,
			UserAgent:  userAgent,
			Token:      token,
			Timeout:    15 * time.Second,
		})
		health, err := client.Health(ctx)
		return testResultMsg{health: health, err: err}
	}
}

// Update handles keys and the connection-test result.
func (m wizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case testResultMsg:
		m.testing = false
		m.tested = msg.err == nil
		m.health = msg.health
		m.err = msg.err
		if msg.err == nil && msg.health != nil {
			m.statusLine = fmt.Sprintf("Connected to %s %s (%s)", msg.health.Project.Name, msg.health.Version, msg.health.Status)
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.cancelled = true
			return m, tea.Quit
		case tea.KeyTab, tea.KeyDown:
			m.focus = m.nextField()
			return m, m.refocus()
		case tea.KeyShiftTab, tea.KeyUp:
			m.focus = m.prevField()
			return m, m.refocus()
		case tea.KeyEnter:
			if m.inputs[fieldServer].Value() == "" {
				m.err = errors.New("a server URL is required")
				return m, nil
			}
			if !m.tested {
				m.testing = true
				m.err = nil
				m.statusLine = "Testing connection ..."
				return m, m.testConnection()
			}
			m.confirmed = true
			return m, tea.Quit
		}
		switch msg.String() {
		case "ctrl+t":
			m.testing = true
			m.err = nil
			m.statusLine = "Testing connection ..."
			return m, m.testConnection()
		}
	}

	// Editing any field invalidates the previous test result.
	var cmd tea.Cmd
	m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
	if _, isKey := msg.(tea.KeyMsg); isKey {
		m.tested = false
	}
	return m, cmd
}

// nextField moves focus forward.
func (m wizardModel) nextField() field {
	if m.focus == fieldServer {
		return fieldToken
	}
	return fieldServer
}

// prevField moves focus backward.
func (m wizardModel) prevField() field {
	return m.nextField()
}

// refocus applies the focus change to the inputs.
func (m *wizardModel) refocus() tea.Cmd {
	for i := range m.inputs {
		if field(i) == m.focus {
			m.inputs[i].Focus()
			continue
		}
		m.inputs[i].Blur()
	}
	return textinput.Blink
}

// View renders the wizard.
func (m wizardModel) View() string {
	var b []byte
	appendLine := func(format string, args ...any) {
		b = append(b, []byte(fmt.Sprintf(format+"\n", args...))...)
	}

	appendLine("%s setup", m.opts.BinaryName)
	appendLine("")
	appendLine("No server configured. Let's set one up.")
	appendLine("")
	appendLine("%s", m.inputs[fieldServer].View())
	appendLine("%s", m.inputs[fieldToken].View())
	appendLine("")

	switch {
	case m.err != nil:
		appendLine("Error: %v", m.err)
	case m.testing:
		appendLine("Testing connection ...")
	case m.statusLine != "":
		appendLine("%s", m.statusLine)
	}

	appendLine("")
	if m.tested {
		appendLine("enter: save and continue   tab: switch field   esc: cancel")
	} else {
		appendLine("enter: test connection   ctrl+t: retest   tab: switch field   esc: cancel")
	}
	return string(b)
}
