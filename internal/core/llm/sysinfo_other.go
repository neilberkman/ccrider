// +build !darwin

package llm

// GetSystemMemoryGB returns total system memory in GB (non-macOS fallback)
func GetSystemMemoryGB() (int, error) {
	// TODO: Implement for Linux/Windows
	return 16, nil // Conservative default
}

// GetOptimalContextSize returns the optimal context window size based on system memory
func GetOptimalContextSize() int {
	// Conservative default for non-macOS
	return 16384
}
