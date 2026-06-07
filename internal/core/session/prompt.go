package session

import (
	"fmt"
	"strings"
	"time"

	"github.com/cbroglie/mustache"
	"github.com/dustin/go-humanize"
)

// ParseSessionTime parses a session timestamp in either of the formats ccrider
// stores ("2006-01-02 15:04:05" or RFC3339). Returns the zero time if neither
// format matches.
func ParseSessionTime(s string) time.Time {
	t, _ := time.Parse("2006-01-02 15:04:05", s)
	if t.IsZero() {
		t, _ = time.Parse(time.RFC3339, s)
	}
	return t
}

// RenderResumePrompt renders the resume prompt template for a session. The
// prompt content is identical no matter which interface launches the resume,
// so rendering lives in core. Falls back to a minimal prompt if the template
// fails to render.
func RenderResumePrompt(template, projectPath, lastCwd, updatedAt string) string {
	updatedTime := ParseSessionTime(updatedAt)

	timeSince := "unknown"
	if !updatedTime.IsZero() {
		timeSince = humanize.Time(updatedTime)
	}

	sameDir := lastCwd == projectPath
	data := map[string]interface{}{
		"last_updated":        updatedAt,
		"last_cwd":            lastCwd,
		"time_since":          timeSince,
		"project_path":        projectPath,
		"same_directory":      sameDir,
		"different_directory": !sameDir,
	}

	prompt, err := mustache.Render(template, data)
	if err != nil {
		return fmt.Sprintf("Resuming session. You were last in: %s", lastCwd)
	}
	return prompt
}

// RenderResumePromptOneLine renders the resume prompt with newlines flattened
// to spaces, for embedding in a single-line shell command.
func RenderResumePromptOneLine(template, projectPath, lastCwd, updatedAt string) string {
	return strings.NewReplacer("\n", " ", "\r", " ").Replace(
		RenderResumePrompt(template, projectPath, lastCwd, updatedAt))
}
