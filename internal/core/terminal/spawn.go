package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Spawner handles spawning new terminal windows with commands
type Spawner struct {
	// Optional override from config
	CustomCommand string
}

// SpawnConfig contains the command to run in a new terminal
type SpawnConfig struct {
	WorkingDir string
	Command    string // Full shell command to execute
}

// Spawn opens a new terminal window and runs the command
func (s *Spawner) Spawn(cfg SpawnConfig) error {
	// Use custom command if configured
	if s.CustomCommand != "" {
		return s.spawnCustom(cfg)
	}

	// Auto-detect terminal
	termProgram := os.Getenv("TERM_PROGRAM")

	switch termProgram {
	case "ghostty":
		return s.spawnGhostty(cfg)
	case "iTerm.app":
		return s.spawnITerm(cfg)
	case "Apple_Terminal":
		return s.spawnTerminalApp(cfg)
	case "WezTerm":
		return s.spawnWezTerm(cfg)
	case "kitty":
		return s.spawnKitty(cfg)
	default:
		// Try to detect by checking for CLI tools
		if _, err := exec.LookPath("ghostty"); err == nil {
			return s.spawnGhostty(cfg)
		}
		if _, err := exec.LookPath("wezterm"); err == nil {
			return s.spawnWezTerm(cfg)
		}
		if _, err := exec.LookPath("kitty"); err == nil {
			return s.spawnKitty(cfg)
		}

		// Last resort: macOS Terminal.app via open
		return s.spawnTerminalApp(cfg)
	}
}

func (s *Spawner) spawnGhostty(cfg SpawnConfig) error {
	// Ghostty: +new-window opens in existing instance
	cmd := exec.Command("ghostty",
		"+new-window",
		"--working-directory="+cfg.WorkingDir,
		"-e", "bash", "-l", "-c", cfg.Command,
	)
	return cmd.Start()
}

func (s *Spawner) spawnITerm(cfg SpawnConfig) error {
	// iTerm2: Use AppleScript
	script := fmt.Sprintf(`
tell application "iTerm"
	create window with default profile
	tell current session of current window
		write text "cd %s && %s"
	end tell
end tell
`, shellEscape(cfg.WorkingDir), shellEscape(cfg.Command))

	cmd := exec.Command("osascript", "-e", script)
	return cmd.Start()
}

func (s *Spawner) spawnTerminalApp(cfg SpawnConfig) error {
	// Terminal.app: Use AppleScript
	script := fmt.Sprintf(`
tell application "Terminal"
	do script "cd %s && %s"
	activate
end tell
`, shellEscape(cfg.WorkingDir), shellEscape(cfg.Command))

	cmd := exec.Command("osascript", "-e", script)
	return cmd.Start()
}

func (s *Spawner) spawnWezTerm(cfg SpawnConfig) error {
	// WezTerm: wezterm cli spawn (if remote control enabled)
	cmd := exec.Command("wezterm", "cli", "spawn",
		"--cwd", cfg.WorkingDir,
		"--", "bash", "-l", "-c", cfg.Command,
	)
	return cmd.Start()
}

func (s *Spawner) spawnKitty(cfg SpawnConfig) error {
	// Kitty: kitty @ launch (if remote control enabled)
	cmd := exec.Command("kitty", "@", "launch",
		"--type=os-window",
		"--cwd="+cfg.WorkingDir,
		"bash", "-l", "-c", cfg.Command,
	)
	return cmd.Start()
}

func (s *Spawner) spawnCustom(cfg SpawnConfig) error {
	// Custom command template, replace placeholders
	// Template vars: {cwd}, {command}
	cmdStr := s.CustomCommand
	cmdStr = strings.ReplaceAll(cmdStr, "{cwd}", cfg.WorkingDir)
	cmdStr = strings.ReplaceAll(cmdStr, "{command}", cfg.Command)

	cmd := exec.Command("bash", "-c", cmdStr)
	return cmd.Start()
}

// shellEscape escapes a string for safe use in shell commands
func shellEscape(s string) string {
	// Simple escape: wrap in single quotes, escape single quotes
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
