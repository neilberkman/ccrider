// +build darwin

package llm

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// GetSystemMemoryGB returns total system memory in GB (macOS specific)
func GetSystemMemoryGB() (int, error) {
	cmd := exec.Command("sysctl", "-n", "hw.memsize")
	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("failed to get system memory: %w", err)
	}

	memBytes, err := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse memory size: %w", err)
	}

	memGB := int(memBytes / 1024 / 1024 / 1024)
	return memGB, nil
}

// GetOptimalContextSize returns the optimal context window size based on system memory
func GetOptimalContextSize() int {
	memGB, err := GetSystemMemoryGB()
	if err != nil {
		// Fallback to conservative default
		return 8192
	}

	// Memory-based context size mapping
	// Based on benchmark data: ~400MB per doubling from 4K baseline
	// 4K   = 5GB
	// 8K   = 5.4GB
	// 16K  = 5.9GB
	// 32K  = 7GB
	// 64K  = ~9GB (estimated)
	// 128K = ~13GB (estimated)

	// Reduced from 128K - very large contexts can hang during initialization
	// 32K is a good balance between capacity and performance
	switch {
	case memGB >= 64:
		return 32768 // 32K - fast and efficient even on absolute units
	case memGB >= 32:
		return 32768
	case memGB >= 24:
		return 16384
	case memGB >= 16:
		return 8192
	case memGB >= 8:
		return 8192
	default:
		return 4096
	}
}
