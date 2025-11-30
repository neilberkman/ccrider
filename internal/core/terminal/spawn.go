package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
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
	Message    string // Optional message to show before command (e.g., "Starting Claude Code...")
}

// Spawn opens a new terminal window and runs the command
func (s *Spawner) Spawn(cfg SpawnConfig) error {
	// Use custom command if configured
	if s.CustomCommand != "" {
		return s.spawnCustom(cfg)
	}

	// Auto-detect terminal
	termProgram := os.Getenv("TERM_PROGRAM")

	// Check TERM_PROGRAM env var first
	switch termProgram {
	case "ghostty":
		if runtime.GOOS == "darwin" {
			return s.spawnGhostty(cfg)
		}
	case "iTerm.app":
		return s.spawnITerm(cfg)
	case "Apple_Terminal":
		return s.spawnTerminalApp(cfg)
	case "WezTerm":
		return s.spawnWezTerm(cfg)
	case "kitty":
		return s.spawnKitty(cfg)
	}

	// Try to detect by checking for CLI tools
	if _, err := exec.LookPath("wezterm"); err == nil {
		return s.spawnWezTerm(cfg)
	}
	if _, err := exec.LookPath("kitty"); err == nil {
		return s.spawnKitty(cfg)
	}

	// Linux-specific terminals
	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("gnome-terminal"); err == nil {
			return s.spawnGnomeTerminal(cfg)
		}
		if _, err := exec.LookPath("konsole"); err == nil {
			return s.spawnKonsole(cfg)
		}
		if _, err := exec.LookPath("xterm"); err == nil {
			return s.spawnXTerm(cfg)
		}
	}

	// macOS-specific fallbacks
	if runtime.GOOS == "darwin" {
		if _, err := exec.LookPath("ghostty"); err == nil {
			return s.spawnGhostty(cfg)
		}
		// Last resort: macOS Terminal.app
		return s.spawnTerminalApp(cfg)
	}

	return fmt.Errorf("could not detect supported terminal. Set terminal_command in config")
}

func (s *Spawner) spawnGhostty(cfg SpawnConfig) error {
	// Ghostty on macOS: Use clipboard + paste approach
	// Save current clipboard, use it, then restore
	fullCmd := fmt.Sprintf("cd %s", cfg.WorkingDir)
	if cfg.Message != "" {
		fullCmd += fmt.Sprintf(" && echo '%s'", cfg.Message)
	}
	fullCmd += fmt.Sprintf(" && %s", cfg.Command)

	// Save current clipboard
	savedClip, _ := exec.Command("pbpaste").Output()

	// Copy command to clipboard
	clipCmd := exec.Command("pbcopy")
	clipCmd.Stdin = strings.NewReader(fullCmd)
	if err := clipCmd.Run(); err != nil {
		return fmt.Errorf("failed to copy to clipboard: %w", err)
	}

	script := `
tell application "Ghostty" to activate
delay 0.2

tell application "System Events"
	tell process "Ghostty"
		keystroke "t" using command down
		delay 0.3
		keystroke "v" using command down
		delay 0.1
		keystroke return
	end tell
end tell
`

	cmd := exec.Command("osascript", "-e", script)
	if err := cmd.Start(); err != nil {
		return err
	}

	// Restore clipboard in background after a delay
	go func() {
		_ = exec.Command("sleep", "1").Run()
		restoreCmd := exec.Command("pbcopy")
		restoreCmd.Stdin = strings.NewReader(string(savedClip))
		_ = restoreCmd.Run()
	}()

	return nil
}

func (s *Spawner) spawnITerm(cfg SpawnConfig) error {
	// iTerm2: Use AppleScript
	cmdStr := fmt.Sprintf("cd %s", cfg.WorkingDir)
	if cfg.Message != "" {
		// Escape single quotes in message for shell
		safeMsg := strings.ReplaceAll(cfg.Message, "'", "'\\''")
		cmdStr += fmt.Sprintf(" && echo '%s'", safeMsg)
	}
	cmdStr += fmt.Sprintf(" && %s", cfg.Command)

	script := fmt.Sprintf(`
tell application "iTerm"
	create window with default profile
	tell current session of current window
		write text %s
	end tell
end tell
`, appleScriptEscape(cmdStr))

	cmd := exec.Command("osascript", "-e", script)
	return cmd.Start()
}

func (s *Spawner) spawnTerminalApp(cfg SpawnConfig) error {
	// Terminal.app: Use AppleScript to open new window
	// Build command string (will be executed by shell in Terminal)
	cmdStr := fmt.Sprintf("cd %s", cfg.WorkingDir)
	if cfg.Message != "" {
		// Escape single quotes in message for shell
		safeMsg := strings.ReplaceAll(cfg.Message, "'", "'\\''")
		cmdStr += fmt.Sprintf(" && echo '%s'", safeMsg)
	}
	cmdStr += fmt.Sprintf(" && %s", cfg.Command)

	// Use AppleScript escaping (double quotes and backslashes)
	// Just open a new window - tabs would require System Events/Accessibility permissions
	script := fmt.Sprintf(`
tell application "Terminal"
	do script %s
	activate
end tell
`, appleScriptEscape(cmdStr))

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

func (s *Spawner) spawnGnomeTerminal(cfg SpawnConfig) error {
	// GNOME Terminal
	cmd := exec.Command("gnome-terminal",
		"--working-directory="+cfg.WorkingDir,
		"--",
		"bash", "-l", "-c", cfg.Command,
	)
	return cmd.Start()
}

func (s *Spawner) spawnKonsole(cfg SpawnConfig) error {
	// KDE Konsole
	cmd := exec.Command("konsole",
		"--workdir", cfg.WorkingDir,
		"-e", "bash", "-l", "-c", cfg.Command,
	)
	return cmd.Start()
}

func (s *Spawner) spawnXTerm(cfg SpawnConfig) error {
	// XTerm - basic but universally available
	fullCmd := fmt.Sprintf("cd %s && %s", cfg.WorkingDir, cfg.Command)
	cmd := exec.Command("xterm",
		"-e", "bash", "-l", "-c", fullCmd,
	)
	return cmd.Start()
}

// appleScriptEscape escapes a string for use in AppleScript do script command
func appleScriptEscape(s string) string {
	// AppleScript uses backslash for escaping quotes and backslashes
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return "\"" + s + "\""
}
