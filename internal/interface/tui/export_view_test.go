package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func TestExportDialog_EKeyOpensDialog_ListView(t *testing.T) {
	m := newTestModel()
	m.mode = listView

	// Simulate having a selected session in the list
	sessions := []sessionItem{{
		ID:      "test-session-1234",
		Summary: "Fix auth bug",
		Project: "/home/user/myrepo",
	}}
	m.sessions = sessions
	m.list = createSessionList(sessions, 80, 24)

	// Press 'e'
	result, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	model := result.(Model)

	if model.mode != exportDialogView {
		t.Errorf("expected exportDialogView, got %d", model.mode)
	}
	if model.exportSessionID != "test-session-1234" {
		t.Errorf("exportSessionID = %q, want %q", model.exportSessionID, "test-session-1234")
	}
	if model.exportSessionProject != "/home/user/myrepo" {
		t.Errorf("exportSessionProject = %q, want %q", model.exportSessionProject, "/home/user/myrepo")
	}

	// Default path should be repo-local
	val := model.exportInput.Value()
	if !strings.Contains(val, ".ccrider/exports/") {
		t.Errorf("export input = %q, want path containing .ccrider/exports/", val)
	}
	if !strings.Contains(val, "/home/user/myrepo") {
		t.Errorf("export input = %q, want path containing repo root", val)
	}
}

func TestExportDialog_ShiftEKeyOpensBlankDialog_ListView(t *testing.T) {
	m := newTestModel()
	m.mode = listView

	sessions := []sessionItem{{
		ID:      "test-session-1234",
		Summary: "Fix auth bug",
		Project: "/home/user/myrepo",
	}}
	m.sessions = sessions
	m.list = createSessionList(sessions, 80, 24)

	// Press 'E'
	result, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'E'}})
	model := result.(Model)

	if model.mode != exportDialogView {
		t.Errorf("expected exportDialogView, got %d", model.mode)
	}

	// Path should be blank for 'E'
	if model.exportInput.Value() != "" {
		t.Errorf("export input should be blank for 'E', got %q", model.exportInput.Value())
	}
}

func TestExportDialog_EKeyOpensDialog_DetailView(t *testing.T) {
	m := newTestModel()
	m.mode = detailView
	m.currentSession = &sessionDetail{
		Session: sessionItem{
			ID:      "detail-session-5678",
			Summary: "Refactor DB",
			Project: "/home/user/otherrepo",
		},
	}

	// Press 'e'
	result, _ := m.updateDetail(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	model := result.(Model)

	if model.mode != exportDialogView {
		t.Errorf("expected exportDialogView, got %d", model.mode)
	}
	if model.exportSessionID != "detail-session-5678" {
		t.Errorf("exportSessionID = %q, want %q", model.exportSessionID, "detail-session-5678")
	}

	val := model.exportInput.Value()
	if !strings.Contains(val, "/home/user/otherrepo") {
		t.Errorf("export input = %q, want path containing repo root", val)
	}
}

func TestExportDialog_EscCancels(t *testing.T) {
	m := newTestModel()
	m.mode = exportDialogView
	m.exportSessionID = "test-session-1234"

	// Press Esc
	result, _ := m.updateExportDialog(tea.KeyMsg{Type: tea.KeyEscape})
	model := result.(Model)

	// Should go back to list view (no current session)
	if model.mode != listView {
		t.Errorf("expected listView after Esc, got %d", model.mode)
	}
}

func TestExportDialog_EscReturnsToDetailView(t *testing.T) {
	m := newTestModel()
	m.mode = exportDialogView
	m.exportSessionID = "test-session-1234"
	m.currentSession = &sessionDetail{
		Session: sessionItem{ID: "test-session-1234"},
	}

	// Press Esc
	result, _ := m.updateExportDialog(tea.KeyMsg{Type: tea.KeyEscape})
	model := result.(Model)

	// Should go back to detail view since currentSession is set
	if model.mode != detailView {
		t.Errorf("expected detailView after Esc, got %d", model.mode)
	}
}

func TestExportDialog_EmptyPathShowsError(t *testing.T) {
	m := newTestModel()
	m.mode = exportDialogView
	m.exportSessionID = "test-session-1234"
	m.exportInput.SetValue("")

	// Press Enter with empty path
	result, _ := m.updateExportDialog(tea.KeyMsg{Type: tea.KeyEnter})
	model := result.(Model)

	if model.err == nil {
		t.Error("expected error for empty path")
	}
	if !strings.Contains(model.err.Error(), "no export path") {
		t.Errorf("error = %q, want 'no export path' message", model.err.Error())
	}
}

func TestExportDialog_QKeyNotIntercepted(t *testing.T) {
	// When in export dialog mode, 'q' should NOT quit the app
	m := newTestModel()
	m.mode = exportDialogView
	m.exportInput.Focus()
	m.exportInput.SetValue("")

	// The global handler should not intercept 'q' in exportDialogView
	// We test this by checking that mode-specific handling runs
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	model := result.(Model)

	// Should still be in export dialog (not quit)
	if model.mode != exportDialogView {
		t.Errorf("'q' should not leave exportDialogView, got mode %d", model.mode)
	}
}

func TestExportDialog_NonRepoSession_NoDefaultPath(t *testing.T) {
	// Non-repo sessions should get just the filename, not a full path
	path := resolveDefaultExportPath("test-session-1234", "")
	if path != "session-test-ses.md" {
		t.Errorf("resolveDefaultExportPath() = %q, want just filename for non-repo session", path)
	}
}

func TestExportDialog_RepoSession_DefaultPath(t *testing.T) {
	path := resolveDefaultExportPath("test-session-1234", "/home/user/myrepo")
	if !strings.Contains(path, "/home/user/myrepo/.ccrider/exports/") {
		t.Errorf("resolveDefaultExportPath() = %q, want repo-local path", path)
	}
	if !strings.HasSuffix(path, "session-test-ses.md") {
		t.Errorf("resolveDefaultExportPath() = %q, want session filename suffix", path)
	}
}

func TestStatusMsg_DisplayedWithoutErrorPrefix(t *testing.T) {
	m := newTestModel()
	m.statusMsg = "Exported session to /path/to/file.md"

	view := m.View()
	if strings.Contains(view, "Error:") {
		t.Error("status message should NOT contain 'Error:' prefix")
	}
	if !strings.Contains(view, "Exported session to /path/to/file.md") {
		t.Error("status message not shown in view")
	}
	if !strings.Contains(view, "esc") {
		t.Error("status view should show esc hint")
	}
}

func TestStatusMsg_EscClears(t *testing.T) {
	m := newTestModel()
	m.statusMsg = "Exported session to /path/to/file.md"

	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	model := result.(Model)

	if model.statusMsg != "" {
		t.Errorf("statusMsg should be cleared after Esc, got %q", model.statusMsg)
	}
}

func TestExportCompletedMsg_Success_SetsStatusMsg(t *testing.T) {
	m := newTestModel()

	result, _ := m.Update(exportCompletedMsg{
		success:  true,
		filePath: "/home/user/exported.md",
	})
	model := result.(Model)

	if model.statusMsg == "" {
		t.Error("statusMsg should be set on export success")
	}
	if strings.Contains(model.statusMsg, "Error") {
		t.Error("success status should not contain 'Error'")
	}
	if model.err != nil {
		t.Error("err should be nil on export success")
	}
}

func TestExportCompletedMsg_Failure_SetsError(t *testing.T) {
	m := newTestModel()

	result, _ := m.Update(exportCompletedMsg{
		success: false,
		err:     errForTest("permission denied"),
	})
	model := result.(Model)

	if model.err == nil {
		t.Error("err should be set on export failure")
	}
	if model.statusMsg != "" {
		t.Error("statusMsg should not be set on failure")
	}
}

// newTestModel creates a minimal Model for testing.
func newTestModel() Model {
	m := Model{
		mode: listView,
	}
	// Initialize text inputs
	m.searchInput = newTextInput("Search...", 200, 50)
	m.inSessionSearch = newTextInput("Search in session...", 200, 50)
	m.exportInput = newTextInput("path/to/export.md", 500, 60)
	return m
}

func newTextInput(placeholder string, charLimit, width int) textinput.Model {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.CharLimit = charLimit
	ti.Width = width
	return ti
}

type testError string

func errForTest(msg string) error {
	return testError(msg)
}

func (e testError) Error() string {
	return string(e)
}
