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

	switch {
	case memGB >= 64:
		// Absolute units get the full 128K
		return 131072 // 128K - model's maximum!
	case memGB >= 32:
		// Beefy machines get 64K
		return 65536
	case memGB >= 24:
		// Decent machines get 32K
		return 32768
	case memGB >= 16:
		// Standard machines get 16K
		return 16384
	case memGB >= 8:
		// Budget machines get 8K
		return 8192
	default:
		// Really constrained machines get 4K
		return 4096
	}
}
