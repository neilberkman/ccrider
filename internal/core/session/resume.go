package session

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cbroglie/mustache"
	"github.com/dustin/go-humanize"
	"github.com/neilberkman/ccrider/internal/core/config"
)

// BuildResumeCommand builds the complete claude command with config flags and resume prompt
func BuildResumeCommand(sessionID, projectPath, lastCwd, updatedAt string, fork bool) (string, error) {
	// Load config
	cfg, err := config.Load()
	if err != nil {
		return "", fmt.Errorf("failed to load config: %w", err)
	}

	// Build template data for resume prompt
	updatedTime, _ := time.Parse("2006-01-02 15:04:05", updatedAt)
	if updatedTime.IsZero() {
		updatedTime, _ = time.Parse(time.RFC3339, updatedAt)
	}

	timeSince := "unknown"
	if !updatedTime.IsZero() {
		timeSince = humanize.Time(updatedTime)
	}

	// Check if we're already in the right directory
	sameDir := (lastCwd == projectPath)

	templateData := map[string]interface{}{
		"last_updated":        updatedAt,
		"last_cwd":            lastCwd,
		"time_since":          timeSince,
		"project_path":        projectPath,
		"same_directory":      sameDir,
		"different_directory": !sameDir,
	}

	// Render the resume prompt
	resumePrompt, err := mustache.Render(cfg.ResumePromptTemplate, templateData)
	if err != nil {
		// Fall back to simple prompt if template fails
		resumePrompt = fmt.Sprintf("Resuming session. You were last in: %s", lastCwd)
	}

	// Write prompt to temp file to avoid shell escaping issues
	tmpfile, err := os.CreateTemp("", "ccrider-prompt-*.txt")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	// Note: Caller is responsible for cleaning up temp file

	if _, err := tmpfile.Write([]byte(resumePrompt)); err != nil {
		_ = tmpfile.Close()
		_ = os.Remove(tmpfile.Name())
		return "", fmt.Errorf("failed to write prompt: %w", err)
	}
	_ = tmpfile.Close()

	// Build claude command with config flags
	flags := ""
	if len(cfg.ClaudeFlags) > 0 {
		flags = " " + strings.Join(cfg.ClaudeFlags, " ")
	}

	var cmd string
	if fork {
		cmd = fmt.Sprintf("claude%s --resume %s --fork-session \"$(cat %s)\"", flags, sessionID, tmpfile.Name())
	} else {
		cmd = fmt.Sprintf("claude%s --resume %s \"$(cat %s)\"", flags, sessionID, tmpfile.Name())
	}

	return cmd, nil
}
