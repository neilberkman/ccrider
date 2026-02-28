package importer

import (
	"os"
	"path/filepath"

	"github.com/neilberkman/ccrider/pkg/ccsessions"
	"github.com/neilberkman/ccrider/pkg/codexsessions"
)

// Source describes a session directory to import.
type Source struct {
	Path          string
	ParseFn       ParseFunc
	Provider      string
	SkipSubagents bool
}

// DefaultSources returns the standard import sources (Claude + Codex).
// Codex is only included if its directory exists.
func DefaultSources() []Source {
	home, err := os.UserHomeDir()
	if err != nil {
		return []Source{}
	}

	sources := []Source{
		{
			Path:          filepath.Join(home, ".claude", "projects"),
			ParseFn:       ccsessions.ParseFile,
			Provider:      "claude",
			SkipSubagents: true,
		},
	}

	codexPath := filepath.Join(home, ".codex", "sessions")
	if _, err := os.Stat(codexPath); err == nil {
		sources = append(sources, Source{
			Path:          codexPath,
			ParseFn:       codexsessions.ParseFile,
			Provider:      "codex",
			SkipSubagents: false,
		})
	}

	return sources
}

// SyncAll imports from all default sources. Silent (nil progress) for background use.
func (i *Importer) SyncAll(force bool) error {
	for _, src := range DefaultSources() {
		if _, err := i.ImportDirectory(src.Path, nil, force, src.SkipSubagents, src.ParseFn, src.Provider); err != nil {
			return err
		}
	}
	return nil
}
