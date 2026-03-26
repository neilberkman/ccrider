package tui

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/neilberkman/ccrider/internal/core/config"
	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/export"
)

func (m Model) updateExportDialog(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		path := m.exportInput.Value()
		if path == "" {
			m.err = fmt.Errorf("no export path specified")
			return m, nil
		}

		// Resolve relative paths against the session's project path or cwd
		if !filepath.IsAbs(path) {
			if m.exportSessionProject != "" {
				path = filepath.Join(m.exportSessionProject, path)
			} else {
				cwd, _ := os.Getwd()
				path = filepath.Join(cwd, path)
			}
		}

		finalPath := path
		sessionID := m.exportSessionID
		projectPath := m.exportSessionProject

		// Go back to previous view immediately
		if m.currentSession != nil {
			m.mode = detailView
		} else {
			m.mode = listView
		}

		return m, performExport(m.db, sessionID, projectPath, finalPath)

	case "esc":
		// Cancel - go back to previous view
		if m.currentSession != nil {
			m.mode = detailView
		} else {
			m.mode = listView
		}
		return m, nil
	}

	// Pass other keys to the text input
	var cmd tea.Cmd
	m.exportInput, cmd = m.exportInput.Update(msg)
	return m, cmd
}

func (m Model) viewExportDialog() string {
	return fmt.Sprintf(`
%s

%s

  %s

  Enter: export | Esc: cancel
`,
		titleStyle.Render("Export Session"),
		timestampStyle.Render("Export path:"),
		m.exportInput.View(),
	)
}

// performExport runs the export in a goroutine and returns a message.
func performExport(database *db.DB, sessionID, projectPath, filePath string) tea.Cmd {
	return func() tea.Msg {
		content, err := export.GenerateMarkdown(database, sessionID)
		if err != nil {
			return exportCompletedMsg{success: false, err: err}
		}

		// Check if file exists - allow overwrite for now (TUI confirmation is the dialog itself)
		if err := export.WriteExport(content, filePath, true); err != nil {
			return exportCompletedMsg{success: false, err: err}
		}

		// Remember the export directory in config
		cfg, _ := config.Load()
		if cfg != nil {
			dir := filepath.Dir(filePath)
			cfg.LastExportDir = dir
			if projectPath != "" {
				if cfg.RepoExportDirs == nil {
					cfg.RepoExportDirs = make(map[string]string)
				}
				cfg.RepoExportDirs[projectPath] = dir
			}
			_ = config.Save(cfg)
		}

		return exportCompletedMsg{success: true, filePath: filePath}
	}
}

// resolveDefaultExportPath determines the prefilled export path for the dialog.
func resolveDefaultExportPath(sessionID, projectPath string) string {
	// Try config-remembered paths first
	cfg, _ := config.Load()
	if cfg != nil {
		// Per-repo remembered directory
		if projectPath != "" {
			if dir, ok := cfg.RepoExportDirs[projectPath]; ok {
				return filepath.Join(dir, export.DefaultFilename(sessionID))
			}
		}

		// Global remembered directory (only for non-repo sessions)
		if projectPath == "" && cfg.LastExportDir != "" {
			return filepath.Join(cfg.LastExportDir, export.DefaultFilename(sessionID))
		}
	}

	// Default: repo-local export path
	if projectPath != "" {
		return export.RepoExportPath(sessionID, projectPath)
	}

	// Non-repo, no remembered path: just the filename
	return export.DefaultFilename(sessionID)
}
