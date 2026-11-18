package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/ansonnchan/overtok/internal/application"
	tea "github.com/charmbracelet/bubbletea"
)

const (
	// MinimumWidth is the narrowest supported terminal width.
	MinimumWidth = 48
	wideWidth    = 88
)

// Section identifies one top-level local application area.
type Section int

const (
	// OverviewSection summarizes local usage and freshness.
	OverviewSection Section = iota
	// SourcesSection shows local adapter and source health.
	SourcesSection
	// ModelsSection shows local model usage and attribution.
	ModelsSection
	// SettingsSection explains controls, settings, and privacy.
	SettingsSection
)

var sections = []Section{OverviewSection, SourcesSection, ModelsSection, SettingsSection}

// ContentState identifies the semantic state of the active section.
type ContentState string

const (
	// StateLoading means the application is reading the active snapshot.
	StateLoading ContentState = "loading"
	// StateEmpty means the snapshot contains no displayable records.
	StateEmpty ContentState = "empty"
	// StateReady means the snapshot is complete and healthy.
	StateReady ContentState = "ready"
	// StateWarning means content is available with an actionable warning.
	StateWarning ContentState = "warning"
	// StateError means the application could not load the active snapshot.
	StateError ContentState = "error"
)

// Page contains presentation-ready, privacy-safe content for one section.
type Page struct {
	Lines   []string
	Warning string
}

// Application supplies committed local state and owns refresh behavior.
// Implementations keep collection and persistence outside the TUI package.
type Application interface {
	Load(context.Context) (application.Dashboard, error)
	Refresh(context.Context) error
}

// Config controls environment-sensitive shell behavior.
type Config struct {
	NoColor bool
}

// Model is the Bubble Tea model for the local application shell.
type Model struct {
	ctx        context.Context
	app        Application
	styles     styleSet
	section    Section
	state      ContentState
	page       Page
	errMessage string
	width      int
	height     int
	help       bool
	refreshing bool
	requestID  uint64
}

type loadResult struct {
	section   Section
	requestID uint64
	dashboard application.Dashboard
	err       error
}

type refreshResult struct {
	err error
}

// NewModel constructs a shell whose data access is limited to app.
func NewModel(ctx context.Context, app Application, config Config) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	state := StateLoading
	if app == nil {
		state = StateEmpty
	}
	return Model{
		ctx:       ctx,
		app:       app,
		styles:    newStyles(config.NoColor),
		section:   OverviewSection,
		state:     state,
		requestID: 1,
	}
}

// Init loads the initial application snapshot.
func (m Model) Init() tea.Cmd {
	return m.load(m.section, m.requestID)
}

// Update applies terminal, navigation, help, refresh, and load messages.
func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		m.width = message.Width
		m.height = message.Height
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(message)
	case loadResult:
		// A slow request must not replace a newer selection, including when the
		// user leaves and returns to the same tab before the first load finishes.
		if message.section != m.section || message.requestID != m.requestID {
			return m, nil
		}
		m.refreshing = false
		m.errMessage = ""
		m.page = buildPage(message.section, message.dashboard)
		switch {
		case message.err != nil:
			m.state = StateError
			m.errMessage = "Could not load local data"
		case m.page.Warning != "":
			m.state = StateWarning
		case len(m.page.Lines) == 0:
			m.state = StateEmpty
		default:
			m.state = StateReady
		}
		return m, nil
	case refreshResult:
		if message.err != nil {
			m.refreshing = false
			m.state = StateError
			m.errMessage = "Could not refresh local data"
			return m, nil
		}
		m.requestID++
		return m, m.load(m.section, m.requestID)
	default:
		return m, nil
	}
}

func (m Model) updateKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "?":
		m.help = !m.help
		return m, nil
	case "1":
		return m.activate(OverviewSection)
	case "2":
		return m.activate(SourcesSection)
	case "3":
		return m.activate(ModelsSection)
	case "4":
		return m.activate(SettingsSection)
	case "tab", "right", "l", "j", "down":
		return m.activate(sections[(int(m.section)+1)%len(sections)])
	case "shift+tab", "left", "h", "k", "up":
		return m.activate(sections[(int(m.section)+len(sections)-1)%len(sections)])
	case "r":
		if m.app == nil || m.refreshing {
			return m, nil
		}
		m.refreshing = true
		return m, m.refresh()
	default:
		return m, nil
	}
}

func (m Model) activate(section Section) (tea.Model, tea.Cmd) {
	if section == m.section {
		return m, nil
	}
	m.section = section
	m.help = false
	m.state = StateLoading
	m.page = Page{}
	m.errMessage = ""
	m.requestID++
	return m, m.load(section, m.requestID)
}

func (m Model) load(section Section, requestID uint64) tea.Cmd {
	if m.app == nil {
		return nil
	}
	return func() tea.Msg {
		dashboard, err := m.app.Load(m.ctx)
		return loadResult{section: section, requestID: requestID, dashboard: dashboard, err: err}
	}
}

func (m Model) refresh() tea.Cmd {
	return func() tea.Msg {
		return refreshResult{err: m.app.Refresh(m.ctx)}
	}
}

// View renders the current shell without reading application state.
func (m Model) View() string {
	return m.render()
}

// Run starts the terminal application with explicit streams.
func Run(ctx context.Context, app Application, input io.Reader, output io.Writer, config Config) error {
	if input == nil || output == nil {
		return errors.New("run TUI: input and output are required")
	}
	program := tea.NewProgram(
		NewModel(ctx, app, config),
		tea.WithContext(ctx),
		tea.WithInput(input),
		tea.WithOutput(output),
		tea.WithAltScreen(),
	)
	if _, err := program.Run(); err != nil {
		return fmt.Errorf("run TUI: %w", err)
	}
	return nil
}

func sectionName(section Section) string {
	switch section {
	case OverviewSection:
		return "Overview"
	case SourcesSection:
		return "Sources"
	case ModelsSection:
		return "Models"
	case SettingsSection:
		return "Settings"
	default:
		return "Unknown"
	}
}

func cleanLine(line string) string {
	return strings.ReplaceAll(strings.ReplaceAll(line, "\r", " "), "\n", " ")
}
