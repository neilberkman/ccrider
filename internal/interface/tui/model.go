package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/neilberkman/ccrider/internal/core/db"
)

type viewMode int

const (
	listView viewMode = iota
	detailView
	searchView
	helpView
	terminalFallbackView
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

	// Project filter state
	projectFilterEnabled bool
	currentDirectory     string

	// Search state
	searchInput       textinput.Model
	searchResults     []searchResult
	searchSelectedIdx int
	searchViewOffset  int // First visible result index (for scrolling)

	// In-session search state
	inSessionSearch     textinput.Model
	inSessionSearchMode bool
	inSessionMatches    []int // message indices that match
	inSessionMatchIdx   int   // current match index
	matchLines          []int // line numbers where each match occurs (for scrolling)

	// Launch state (for exec after quit)
	LaunchSessionID   string
	LaunchProjectPath string
	LaunchLastCwd     string
	LaunchUpdatedAt   string
	LaunchSummary     string
	LaunchFork        bool

	// Terminal fallback state (when can't spawn terminal)
	fallbackSessionID   string
	fallbackProjectPath string
	fallbackLastCwd     string
	fallbackUpdatedAt   string
	fallbackSummary     string

	// Sync progress state
	syncing          bool
	syncProgress     float64 // 0-100
	syncCurrentFile  string
	syncTotal        int
	syncCurrent      int
	savedCursorIndex int // Preserve cursor position during sync
}

type sessionItem struct {
	ID                string
	Summary           string
	Project           string
	MessageCount      int
	UpdatedAt         string
	CreatedAt         string
	MatchesCurrentDir bool // True if session project matches current working directory
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
	SessionID string
	Summary   string
	Project   string
	UpdatedAt string
	Matches   []matchInfo
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

	// Create empty list initially (will be populated when sessions load)
	emptyList := list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0)
	emptyList.Title = "Claude Code Sessions"

	// Get current working directory for filtering
	currentDir, _ := os.Getwd()

	return Model{
		db:                   database,
		mode:                 listView,
		list:                 emptyList,
		searchInput:          ti,
		inSessionSearch:      inSessionTi,
		projectFilterEnabled: false, // Disabled by default
		currentDirectory:     currentDir,
		syncing:              true, // Start with syncing=true
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		loadSessions(m.db, m.projectFilterEnabled, m.currentDirectory),
		syncSessions(m.db, m.projectFilterEnabled, m.currentDirectory),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		// Update list dimensions if it's been created
		if m.list.Paginator.TotalPages > 0 {
			m.list.SetSize(m.width, m.height-4)
		}

		// Update viewport dimensions if it's been created
		if m.viewport.Height > 0 {
			m.viewport.Width = m.width
			m.viewport.Height = m.height - 8
		}

		return m, nil

	case tea.MouseMsg:
		// Handle mouse wheel scrolling
		if msg.Type == tea.MouseWheelDown || msg.Type == tea.MouseWheelUp {
			switch m.mode {
			case searchView:
				return handleSearchMouseWheel(m, msg.Type == tea.MouseWheelDown), nil
			case listView:
				// Pass mouse events to the list
				var cmd tea.Cmd
				m.list, cmd = m.list.Update(msg)
				return m, cmd
			case detailView:
				// Pass mouse events to the viewport
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(msg)
				return m, cmd
			}
		}

	case tea.KeyMsg:
		// If showing an error, esc clears it and goes back to previous view
		if m.err != nil {
			if msg.String() == "esc" {
				m.err = nil
				// Go back to the view we were in before the error
				// If we're in terminalFallbackView, go back to where we came from
				if m.mode == terminalFallbackView {
					if m.currentSession != nil {
						m.mode = detailView
					} else {
						m.mode = listView
					}
				}
				return m, nil
			}
			if msg.String() == "q" {
				return m, tea.Quit
			}
		}

		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit

		case "q":
			// Only intercept 'q' when NOT in a text input mode
			if m.mode == searchView {
				// In search view, let 'q' be typed into search input
				// Don't intercept it here
			} else if m.mode == detailView && m.inSessionSearchMode {
				// In detail view search mode, let 'q' be typed into search input
				// Don't intercept it here
			} else {
				// Normal 'q' handling for non-input modes
				if m.mode == listView {
					return m, tea.Quit
				}
				// In other views, go back to list
				m.mode = listView
				return m, nil
			}

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
		case terminalFallbackView:
			return m.updateTerminalFallback(msg)
		}

	case syncProgressMsg:
		// Update sync progress
		m.syncing = true // Set syncing flag so progress bar shows
		m.syncCurrent = msg.current
		m.syncTotal = msg.total
		m.syncCurrentFile = msg.sessionName
		// Chain another command to keep waiting for progress via channel
		return m, syncSubscribe(msg.ch, msg.db, msg.filterByProject, msg.projectPath)

	case sessionsLoadedMsg:
		m.sessions = msg.sessions
		m.list = createSessionList(msg.sessions, m.width, m.height)

		// If we were syncing, restore cursor position and clear sync flag
		if m.syncing {
			m.syncing = false
			// Restore cursor position if valid
			if m.savedCursorIndex >= 0 && m.savedCursorIndex < len(msg.sessions) {
				m.list.Select(m.savedCursorIndex)
			}
		}
		return m, nil

	case sessionDetailLoadedMsg:
		m.currentSession = &msg.detail
		m.viewport = createViewport(msg.detail, m.width, m.height)
		m.mode = detailView
		return m, nil

	case sessionLaunchInfoMsg:
		// Got session info for quick launch (from list view 'o' key)
		return m, openInNewTerminal(
			msg.sessionID,
			msg.projectPath,
			msg.lastCwd,
			msg.updatedAt,
			msg.summary,
		)

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
			m.LaunchSummary = msg.summary
			m.LaunchFork = msg.fork
			return m, tea.Quit
		} else {
			// Show message (could be error or info like "written to file")
			if msg.message != "" {
				// Check if this is an info message (starts with "Command written")
				if strings.HasPrefix(msg.message, "Command written") {
					m.err = fmt.Errorf("Success: %s", msg.message)
				} else {
					m.err = fmt.Errorf("%s", msg.message)
				}
			} else {
				m.err = msg.err
			}
			return m, nil
		}

	case terminalSpawnedMsg:
		// Show feedback only on failure
		// On success, just clear error and keep TUI usable
		if !msg.success {
			// Check if this is the "can't spawn terminal" error
			if msg.err != nil && strings.Contains(msg.err.Error(), "could not detect supported terminal") {
				// Switch to fallback view with options
				m.mode = terminalFallbackView
				m.fallbackSessionID = msg.sessionID
				m.fallbackProjectPath = msg.projectPath
				m.fallbackLastCwd = msg.lastCwd
				m.fallbackUpdatedAt = msg.updatedAt
				m.fallbackSummary = msg.summary
				m.err = nil
			} else {
				m.err = fmt.Errorf("Failed to open session: %v", msg.err)
			}
		} else {
			m.err = nil // Clear any previous errors
		}
		// TUI stays open - user can continue browsing and launching more sessions
		return m, nil

	case errMsg:
		m.err = msg.err
		return m, nil
	}

	return m, nil
}

func (m Model) View() string {
	if m.err != nil {
		errMsg := m.err.Error()

		// Handle success messages
		if strings.HasPrefix(errMsg, "Success: ") {
			msg := strings.TrimPrefix(errMsg, "Success: ")
			return msg + "\n\nPress esc to go back"
		}

		// Handle "NoClipboard:" prefix (from fallback view)
		if strings.HasPrefix(errMsg, "NoClipboard: ") {
			cmd := strings.TrimPrefix(errMsg, "NoClipboard: ")
			return "Cannot copy to clipboard in this environment.\n\nCommand:\n\n" + cmd + "\n\nPress esc to go back"
		}

		// Handle "Command:" prefix (from direct 'c' key press)
		if strings.HasPrefix(errMsg, "Command: ") {
			cmd := strings.TrimPrefix(errMsg, "Command: ")
			return cmd + "\n\nPress esc to go back"
		}

		return "Error: " + errMsg + "\n\nPress esc to go back | q to quit"
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
	case terminalFallbackView:
		return m.viewTerminalFallback()
	}

	return ""
}
