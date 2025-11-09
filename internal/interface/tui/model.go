package tui

import (
	"github.com/charmbracelet/bubbles/list"
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
	Session  sessionItem
	Messages []messageItem
}

type messageItem struct {
	Type      string
	Content   string
	Timestamp string
}

func New(database *db.DB) Model {
	return Model{
		db:   database,
		mode: listView,
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
