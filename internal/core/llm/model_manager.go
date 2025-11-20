package llm

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ModelManager handles model downloads and caching
type ModelManager struct {
	cacheDir string
}

// ModelInfo represents a model configuration
type ModelInfo struct {
	Name string
	URL  string
	Size string // Human-readable size
}

// Available models registry
var modelRegistry = map[string]ModelInfo{
	"qwen-1.5b": {
		Name: "Qwen 2.5 1.5B Instruct (Q4)",
		URL:  "https://huggingface.co/Qwen/Qwen2.5-1.5B-Instruct-GGUF/resolve/main/qwen2.5-1.5b-instruct-q4_k_m.gguf",
		Size: "~900MB",
	},
	"llama-8b": {
		Name: "Llama 3.1 8B Instruct (Q4)",
		URL:  "https://huggingface.co/bartowski/Meta-Llama-3.1-8B-Instruct-GGUF/resolve/main/Meta-Llama-3.1-8B-Instruct-Q4_K_M.gguf",
		Size: "~4.7GB",
	},
}

// NewModelManager creates a new model manager
func NewModelManager() (*ModelManager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	cacheDir := filepath.Join(home, ".cache", "ccrider", "models")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	return &ModelManager{cacheDir: cacheDir}, nil
}

// GetCacheDir returns the model cache directory
func (m *ModelManager) GetCacheDir() string {
	return m.cacheDir
}

// EnsureModel downloads model if needed, returns path to GGUF file
func (m *ModelManager) EnsureModel(modelName string) (string, error) {
	info, ok := modelRegistry[modelName]
	if !ok {
		return "", fmt.Errorf("unknown model: %s (available: %s)",
			modelName, strings.Join(m.ListModels(), ", "))
	}

	filename := filepath.Base(info.URL)
	localPath := filepath.Join(m.cacheDir, filename)

	// Check if already downloaded
	if stat, err := os.Stat(localPath); err == nil && stat.Size() > 0 {
		return localPath, nil
	}

	fmt.Printf("Downloading %s (%s)...\n", info.Name, info.Size)
	fmt.Printf("This will take a few minutes on first use.\n\n")

	if err := m.downloadWithProgress(info.URL, localPath); err != nil {
		// Clean up partial download
		os.Remove(localPath)
		os.Remove(localPath + ".tmp")
		return "", fmt.Errorf("download failed: %w", err)
	}

	fmt.Printf("\n✓ Model downloaded to %s\n", localPath)
	return localPath, nil
}

// ListModels returns available model names
func (m *ModelManager) ListModels() []string {
	models := make([]string, 0, len(modelRegistry))
	for name := range modelRegistry {
		models = append(models, name)
	}
	return models
}

// GetModelInfo returns information about a model
func (m *ModelManager) GetModelInfo(modelName string) (ModelInfo, error) {
	info, ok := modelRegistry[modelName]
	if !ok {
		return ModelInfo{}, fmt.Errorf("unknown model: %s", modelName)
	}
	return info, nil
}

// downloadWithProgress downloads a file with progress tracking
func (m *ModelManager) downloadWithProgress(url, dest string) error {
	// Create temporary file
	tmpFile := dest + ".tmp"
	out, err := os.Create(tmpFile)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer out.Close()

	// Make HTTP request
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	// Progress tracking
	size := resp.ContentLength
	counter := &WriteCounter{Total: size}

	// Copy with progress
	_, err = io.Copy(out, io.TeeReader(resp.Body, counter))
	if err != nil {
		return fmt.Errorf("download interrupted: %w", err)
	}

	// Ensure all data is written
	if err := out.Sync(); err != nil {
		return fmt.Errorf("failed to sync file: %w", err)
	}

	// Move to final location
	if err := os.Rename(tmpFile, dest); err != nil {
		return fmt.Errorf("failed to finalize file: %w", err)
	}

	return nil
}

// WriteCounter tracks download progress
type WriteCounter struct {
	Total   int64
	Current int64
}

func (wc *WriteCounter) Write(p []byte) (int, error) {
	n := len(p)
	wc.Current += int64(n)
	wc.PrintProgress()
	return n, nil
}

func (wc *WriteCounter) PrintProgress() {
	if wc.Total <= 0 {
		// Size unknown, just show bytes
		fmt.Printf("\rDownloading: %d MB", wc.Current/1024/1024)
		return
	}

	pct := float64(wc.Current) / float64(wc.Total) * 100
	currentMB := wc.Current / 1024 / 1024
	totalMB := wc.Total / 1024 / 1024

	fmt.Printf("\rDownloading: %.1f%% (%d/%d MB)", pct, currentMB, totalMB)
}
