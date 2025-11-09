package tui

import (
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/yourusername/ccrider/internal/core/db"
)

type viewMode int

const (
	listView viewMode = iota
	detailView
	searchView
	helpView
)

type Model struct {
	db       *db.DB
	mode     viewMode
	list     list.Model
	viewport viewport.Model
	width    int
	height   int
	err      error

	// Current session data
	sessions       []sessionItem
	currentSession *sessionDetail

	// Search state
	searchInput        textinput.Model
	searchResults      []searchResult
	searchSelectedIdx  int
	searchViewOffset   int // First visible result index (for scrolling)

	// In-session search state
	inSessionSearch      textinput.Model
	inSessionSearchMode  bool
	inSessionMatches     []int // message indices that match
	inSessionMatchIdx    int   // current match index

	// Launch state (for exec after quit)
	LaunchSessionID   string
	LaunchProjectPath string
	LaunchLastCwd     string
	LaunchUpdatedAt   string
	LaunchFork        bool
}

type sessionItem struct {
	ID          string
	Summary     string
	Project     string
	MessageCount int
	UpdatedAt   string
	CreatedAt   string
}

type sessionDetail struct {
	Session   sessionItem
	Messages  []messageItem
	LastCwd   string // Last working directory from messages
	UpdatedAt string // When session was last active
}

type messageItem struct {
	Type      string
	Content   string
	Timestamp string
}

type searchResult struct {
	SessionID    string
	Summary      string
	Project      string
	UpdatedAt    string
	Matches      []matchInfo
}

type matchInfo struct {
	MessageType string
	Snippet     string
	Sequence    int
}

func New(database *db.DB) Model {
	ti := textinput.New()
	ti.Placeholder = "Search messages..."
	ti.Focus()
	ti.CharLimit = 200
	ti.Width = 50

	inSessionTi := textinput.New()
	inSessionTi.Placeholder = "Search in session..."
	inSessionTi.CharLimit = 200
	inSessionTi.Width = 50

	return Model{
		db:              database,
		mode:            listView,
		searchInput:     ti,
		inSessionSearch: inSessionTi,
	}
}

func (m Model) Init() tea.Cmd {
	return loadSessions(m.db)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.MouseMsg:
		// Handle mouse wheel scrolling in search view
		if m.mode == searchView {
			if msg.Type == tea.MouseWheelDown {
				return handleSearchMouseWheel(m, true), nil
			} else if msg.Type == tea.MouseWheelUp {
				return handleSearchMouseWheel(m, false), nil
			}
		}

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			if m.mode == listView {
				return m, tea.Quit
			}
			// In other views, go back to list
			m.mode = listView
			return m, nil

		case "?":
			m.mode = helpView
			return m, nil
		}

		// Mode-specific key handling
		switch m.mode {
		case listView:
			return m.updateList(msg)
		case detailView:
			return m.updateDetail(msg)
		case searchView:
			return m.updateSearch(msg)
		case helpView:
			return m.updateHelp(msg)
		}

	case sessionsLoadedMsg:
		m.sessions = msg.sessions
		m.list = createSessionList(msg.sessions, m.width, m.height)
		return m, nil

	case sessionDetailLoadedMsg:
		m.currentSession = &msg.detail
		m.viewport = createViewport(msg.detail, m.width, m.height)
		m.mode = detailView
		return m, nil

	case searchResultsMsg:
		m.searchResults = msg.results
		return m, nil

	case sessionLaunchedMsg:
		if msg.success {
			// Store launch info for CLI to exec after quit
			m.LaunchSessionID = msg.sessionID
			m.LaunchProjectPath = msg.projectPath
			m.LaunchLastCwd = msg.lastCwd
			m.LaunchUpdatedAt = msg.updatedAt
			m.LaunchFork = msg.fork
			return m, tea.Quit
		} else {
			// Failed to launch - show error
			m.err = msg.err
			return m, nil
		}

	case errMsg:
		m.err = msg.err
		return m, nil
	}

	return m, nil
}

func (m Model) View() string {
	if m.err != nil {
		return "Error: " + m.err.Error() + "\n\nPress q to quit"
	}

	switch m.mode {
	case listView:
		return m.viewList()
	case detailView:
		return m.viewDetail()
	case searchView:
		return m.viewSearch()
	case helpView:
		return m.viewHelp()
	}

	return ""
}
