package llm

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/neilberkman/ccrider/internal/core/db"
)

// MetadataExtractor extracts issue IDs and file paths from messages
type MetadataExtractor struct{}

// NewMetadataExtractor creates a new metadata extractor
func NewMetadataExtractor() *MetadataExtractor {
	return &MetadataExtractor{}
}

// Issue ID patterns - common formats like ENA-1234, PROJ-123, #123
var issuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`\b([A-Z]{2,10}-\d+)\b`),            // JIRA style: ENA-1234, PROJ-123
	regexp.MustCompile(`\b(GH-\d+|gh-\d+)\b`),              // GitHub: GH-123
	regexp.MustCompile(`(?:issue|bug|ticket)\s*#?(\d+)\b`), // Generic: issue #123, bug 456
}

// File path patterns
var filePatterns = []*regexp.Regexp{
	// Unix-style paths
	regexp.MustCompile(`(?:^|[\s"'\(\[\{])(/[a-zA-Z0-9_\-./]+\.[a-zA-Z0-9]+)`),
	// Relative paths with extension
	regexp.MustCompile(`(?:^|[\s"'\(\[\{])(\./[a-zA-Z0-9_\-./]+\.[a-zA-Z0-9]+)`),
	regexp.MustCompile(`(?:^|[\s"'\(\[\{])([a-zA-Z0-9_\-]+/[a-zA-Z0-9_\-./]+\.[a-zA-Z0-9]+)`),
	// Common code file patterns
	regexp.MustCompile(`\b([a-zA-Z0-9_\-]+\.(go|py|js|ts|tsx|jsx|rb|ex|exs|rs|java|c|cpp|h|hpp|css|scss|html|json|yaml|yml|toml|md|sql))\b`),
}

// Common file extensions to look for
var codeExtensions = map[string]bool{
	".go": true, ".py": true, ".js": true, ".ts": true, ".tsx": true, ".jsx": true,
	".rb": true, ".ex": true, ".exs": true, ".rs": true, ".java": true,
	".c": true, ".cpp": true, ".h": true, ".hpp": true, ".cs": true,
	".php": true, ".swift": true, ".kt": true, ".scala": true,
	".css": true, ".scss": true, ".less": true, ".html": true,
	".json": true, ".yaml": true, ".yml": true, ".toml": true,
	".xml": true, ".sql": true, ".sh": true, ".bash": true,
	".md": true, ".txt": true, ".dockerfile": true,
}

// ExtractIssues extracts issue IDs from messages
func (e *MetadataExtractor) ExtractIssues(messages []Message) []db.SessionIssue {
	issueMap := make(map[string]*db.SessionIssue) // lowercase ID -> issue

	for seq, msg := range messages {
		content := msg.Content

		for _, pattern := range issuePatterns {
			matches := pattern.FindAllStringSubmatch(content, -1)
			for _, match := range matches {
				if len(match) > 1 {
					issueID := strings.ToUpper(match[1])
					lowerID := strings.ToLower(issueID)

					if existing, ok := issueMap[lowerID]; ok {
						existing.LastMentionSeq = seq
						existing.MentionCount++
					} else {
						issueMap[lowerID] = &db.SessionIssue{
							IssueID:         issueID,
							FirstMentionSeq: seq,
							LastMentionSeq:  seq,
							MentionCount:    1,
						}
					}
				}
			}
		}
	}

	var issues []db.SessionIssue
	for _, issue := range issueMap {
		issues = append(issues, *issue)
	}
	return issues
}

// ExtractFiles extracts file paths from messages
func (e *MetadataExtractor) ExtractFiles(messages []Message) []db.SessionFile {
	fileMap := make(map[string]*db.SessionFile) // normalized path -> file

	for seq, msg := range messages {
		content := msg.Content

		// Extract from patterns
		for _, pattern := range filePatterns {
			matches := pattern.FindAllStringSubmatch(content, -1)
			for _, match := range matches {
				if len(match) > 1 {
					filePath := strings.TrimSpace(match[1])
					if isValidFilePath(filePath) {
						addFile(fileMap, filePath, seq)
					}
				}
			}
		}

		// Also look for common tool use patterns
		extractToolFilePaths(content, fileMap, seq)
	}

	var files []db.SessionFile
	for _, file := range fileMap {
		files = append(files, *file)
	}
	return files
}

func addFile(fileMap map[string]*db.SessionFile, filePath string, seq int) {
	// Normalize path
	filePath = filepath.Clean(filePath)
	fileName := filepath.Base(filePath)

	if existing, ok := fileMap[filePath]; ok {
		existing.LastMentionSeq = seq
		existing.MentionCount++
	} else {
		fileMap[filePath] = &db.SessionFile{
			FilePath:        filePath,
			FileName:        fileName,
			FirstMentionSeq: seq,
			LastMentionSeq:  seq,
			MentionCount:    1,
		}
	}
}

func isValidFilePath(path string) bool {
	// Must have a valid extension
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" || !codeExtensions[ext] {
		return false
	}

	// Filter out obvious non-paths
	if strings.Contains(path, "http://") || strings.Contains(path, "https://") {
		return false
	}

	// Must be reasonable length
	if len(path) < 3 || len(path) > 500 {
		return false
	}

	return true
}

// extractToolFilePaths extracts file paths from tool use patterns in Claude Code sessions
func extractToolFilePaths(content string, fileMap map[string]*db.SessionFile, seq int) {
	// Common tool patterns in Claude Code:
	// - "Read file: /path/to/file"
	// - "Edit /path/to/file"
	// - "file_path: /path/to/file"
	// - "Writing to /path/to/file"

	toolPatterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:read|edit|write|create|delete|viewing?)\s+(?:file:?\s*)?([/~][^\s"'\n]+\.[a-zA-Z0-9]+)`),
		regexp.MustCompile(`(?i)file_path["']?\s*[:=]\s*["']?([/~][^\s"'\n]+\.[a-zA-Z0-9]+)`),
		regexp.MustCompile(`(?i)(?:modified|created|deleted|opened)\s+([/~][^\s"'\n]+\.[a-zA-Z0-9]+)`),
	}

	for _, pattern := range toolPatterns {
		matches := pattern.FindAllStringSubmatch(content, -1)
		for _, match := range matches {
			if len(match) > 1 {
				filePath := strings.Trim(match[1], `"'`)
				if isValidFilePath(filePath) {
					addFile(fileMap, filePath, seq)
				}
			}
		}
	}
}

// IsIssueID checks if a string looks like an issue ID
func IsIssueID(s string) bool {
	s = strings.TrimSpace(s)
	for _, pattern := range issuePatterns {
		if pattern.MatchString(s) {
			return true
		}
	}
	return false
}
