package metadata

import (
	"regexp"
	"strings"
)

// SessionMetadata represents extracted metadata from a session
type SessionMetadata struct {
	IssueIDs  map[string]*IssueOccurrence
	FilePaths map[string]*FileOccurrence
}

// IssueOccurrence tracks where an issue ID appears
type IssueOccurrence struct {
	IssueID            string
	FirstMentionIndex  int
	LastMentionIndex   int
	MentionCount       int
}

// FileOccurrence tracks where a file path appears
type FileOccurrence struct {
	FilePath           string
	MentionCount       int
	LastModifiedIndex  int
}

// Extractor extracts structured metadata from messages
type Extractor struct {
	issuePatterns []*regexp.Regexp
	filePatterns  []*regexp.Regexp
}

// NewExtractor creates a new metadata extractor
func NewExtractor() *Extractor {
	return &Extractor{
		issuePatterns: compileIssuePatterns(),
		filePatterns:  compileFilePatterns(),
	}
}

// Extract extracts metadata from message texts
func (e *Extractor) Extract(messages []MessageText) *SessionMetadata {
	metadata := &SessionMetadata{
		IssueIDs:  make(map[string]*IssueOccurrence),
		FilePaths: make(map[string]*FileOccurrence),
	}

	for _, msg := range messages {
		// Extract issue IDs
		issues := e.extractIssueIDs(msg.Text)
		for _, issueID := range issues {
			normalizedID := normalizeIssueID(issueID)

			if occ, exists := metadata.IssueIDs[normalizedID]; exists {
				occ.LastMentionIndex = msg.Index
				occ.MentionCount++
			} else {
				metadata.IssueIDs[normalizedID] = &IssueOccurrence{
					IssueID:           normalizedID,
					FirstMentionIndex: msg.Index,
					LastMentionIndex:  msg.Index,
					MentionCount:      1,
				}
			}
		}

		// Extract file paths
		files := e.extractFilePaths(msg.Text)
		for _, filePath := range files {
			if occ, exists := metadata.FilePaths[filePath]; exists {
				occ.MentionCount++
				// Check if this looks like a modification (contains keywords)
				if isModificationContext(msg.Text, filePath) {
					occ.LastModifiedIndex = msg.Index
				}
			} else {
				lastModified := -1
				if isModificationContext(msg.Text, filePath) {
					lastModified = msg.Index
				}

				metadata.FilePaths[filePath] = &FileOccurrence{
					FilePath:          filePath,
					MentionCount:      1,
					LastModifiedIndex: lastModified,
				}
			}
		}
	}

	return metadata
}

// extractIssueIDs finds issue IDs in text
func (e *Extractor) extractIssueIDs(text string) []string {
	seen := make(map[string]bool)
	var issues []string

	for _, pattern := range e.issuePatterns {
		matches := pattern.FindAllStringSubmatch(text, -1)
		for _, match := range matches {
			if len(match) > 1 {
				issueID := match[1]
				normalizedID := normalizeIssueID(issueID)

				if !seen[normalizedID] && isValidIssueID(normalizedID) {
					issues = append(issues, issueID)
					seen[normalizedID] = true
				}
			}
		}
	}

	return issues
}

// extractFilePaths finds file paths in text
func (e *Extractor) extractFilePaths(text string) []string {
	seen := make(map[string]bool)
	var files []string

	for _, pattern := range e.filePatterns {
		matches := pattern.FindAllStringSubmatch(text, -1)
		for _, match := range matches {
			if len(match) > 1 {
				filePath := strings.TrimSpace(match[1])

				if !seen[filePath] && isValidFilePath(filePath) {
					files = append(files, filePath)
					seen[filePath] = true
				}
			}
		}
	}

	return files
}

// MessageText represents a message with its index
type MessageText struct {
	Index int
	Text  string
}

// Issue ID pattern compilation
func compileIssuePatterns() []*regexp.Regexp {
	return []*regexp.Regexp{
		// Linear-style: ENA-6530, ena-6530
		regexp.MustCompile(`\b([A-Z]+-\d+)\b`),
		regexp.MustCompile(`\b([a-z]+-\d+)\b`),

		// GitHub-style: #1234
		regexp.MustCompile(`#(\d{2,})`), // At least 2 digits to avoid false positives

		// Explicit mentions: "issue: 1234", "issue #1234"
		regexp.MustCompile(`(?i)issue[:\s]+#?(\d+)`),

		// JIRA-style: PROJ-123
		regexp.MustCompile(`\b([A-Z]{2,10}-\d+)\b`),
	}
}

// File path pattern compilation
func compileFilePatterns() []*regexp.Regexp {
	return []*regexp.Regexp{
		// Quoted paths: "path/to/file.ext"
		regexp.MustCompile(`"([a-zA-Z0-9_\-/.]+\.[a-zA-Z0-9]+)"`),

		// Backtick paths: `path/to/file.ext`
		regexp.MustCompile("`([a-zA-Z0-9_\\-/.]+\\.[a-zA-Z0-9]+)`"),

		// Unquoted paths (more restrictive to avoid false positives)
		// Must have at least one slash and an extension
		regexp.MustCompile(`\b([a-zA-Z0-9_\-]+(?:/[a-zA-Z0-9_\-]+)+\.[a-zA-Z0-9]{2,5})\b`),

		// Common patterns: internal/core/db/schema.go:123
		regexp.MustCompile(`([a-zA-Z0-9_\-]+(?:/[a-zA-Z0-9_\-]+)+\.[a-zA-Z0-9]{2,5}):\d+`),
	}
}

// normalizeIssueID converts issue IDs to lowercase for consistent storage
func normalizeIssueID(issueID string) string {
	return strings.ToLower(issueID)
}

// isValidIssueID filters out common false positives
func isValidIssueID(issueID string) bool {
	// Filter out common false positives
	lower := strings.ToLower(issueID)

	// Too short
	if len(lower) < 3 {
		return false
	}

	// Common false positives from tech terms
	falsePositives := []string{
		"utf-8", "iso-8859", "us-ascii", "x-www",
		"application-json", "text-plain", "user-agent",
		"en-us", "fr-fr", "de-de",
	}

	for _, fp := range falsePositives {
		if lower == fp {
			return false
		}
	}

	// Check if it's a numeric-only issue (like #1234)
	if strings.HasPrefix(issueID, "#") {
		// Already validated by regex requiring at least 2 digits
		return true
	}

	// For letter-number patterns, ensure it looks reasonable
	parts := strings.Split(lower, "-")
	if len(parts) == 2 {
		// First part should be letters, second should be numbers
		if len(parts[0]) >= 2 && len(parts[1]) >= 1 {
			return true
		}
	}

	return false
}

// isValidFilePath filters out false positives
func isValidFilePath(path string) bool {
	// Too short
	if len(path) < 5 {
		return false
	}

	// Must contain a slash (relative or absolute path)
	if !strings.Contains(path, "/") {
		// Allow some common patterns without slashes if they have extensions
		// e.g., "config.yaml" or "README.md"
		if !hasValidExtension(path) {
			return false
		}
	}

	// Must have valid extension
	if !hasValidExtension(path) {
		return false
	}

	// Filter out URLs
	if strings.Contains(path, "://") {
		return false
	}

	// Filter out common false positives
	if strings.Contains(path, "@") || strings.Contains(path, " ") {
		return false
	}

	return true
}

// hasValidExtension checks if path has a common file extension
func hasValidExtension(path string) bool {
	validExts := []string{
		// Code
		".go", ".ex", ".exs", ".eex", ".leex", ".heex",
		".js", ".ts", ".jsx", ".tsx",
		".py", ".rb", ".java", ".c", ".cpp", ".h", ".hpp",
		".rs", ".php", ".swift", ".kt",

		// Config & Data
		".json", ".yaml", ".yml", ".toml", ".ini", ".xml",
		".sql", ".env",

		// Docs
		".md", ".txt", ".rst", ".adoc",

		// Web
		".html", ".css", ".scss", ".sass", ".less",

		// Other
		".sh", ".bash", ".zsh", ".fish",
		".graphql", ".proto",
	}

	lowerPath := strings.ToLower(path)
	for _, ext := range validExts {
		if strings.HasSuffix(lowerPath, ext) {
			return true
		}
	}

	return false
}

// isModificationContext checks if message contains modification keywords
func isModificationContext(text, filePath string) bool {
	// Look for modification-related keywords near the file path
	modKeywords := []string{
		"edit", "modify", "update", "change", "write", "create",
		"add", "remove", "delete", "fix", "patch",
		"modified", "updated", "changed", "created", "edited",
	}

	lower := strings.ToLower(text)

	// Simple heuristic: check if any keyword appears in the same sentence/context
	// We'll consider finding the file path index and checking nearby text
	fileIdx := strings.Index(lower, strings.ToLower(filePath))
	if fileIdx == -1 {
		return false
	}

	// Check 100 characters before and after the file path mention
	start := fileIdx - 100
	if start < 0 {
		start = 0
	}
	end := fileIdx + len(filePath) + 100
	if end > len(lower) {
		end = len(lower)
	}

	context := lower[start:end]

	for _, keyword := range modKeywords {
		if strings.Contains(context, keyword) {
			return true
		}
	}

	return false
}
