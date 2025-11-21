package llm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DaemonInfo holds information about a running daemon
type DaemonInfo struct {
	PID       int
	Model     string
	StartTime time.Time
	IsRunning bool
}

// DaemonManager handles daemon lifecycle
type DaemonManager struct {
	pidFile  string
	lockFile string
}

// NewDaemonManager creates a new daemon manager
func NewDaemonManager() (*DaemonManager, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get config dir: %w", err)
	}

	daemonDir := filepath.Join(configDir, "ccrider", "daemon")
	if err := os.MkdirAll(daemonDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create daemon dir: %w", err)
	}

	return &DaemonManager{
		pidFile:  filepath.Join(daemonDir, "daemon.pid"),
		lockFile: filepath.Join(daemonDir, "daemon.lock"),
	}, nil
}

// GetStatus returns the current daemon status
func (dm *DaemonManager) GetStatus() (*DaemonInfo, error) {
	// Check if PID file exists
	data, err := os.ReadFile(dm.pidFile)
	if err != nil {
		if os.IsNotExist(err) {
			return &DaemonInfo{IsRunning: false}, nil
		}
		return nil, fmt.Errorf("failed to read PID file: %w", err)
	}

	// Parse PID file format: PID|MODEL|TIMESTAMP
	parts := strings.Split(strings.TrimSpace(string(data)), "|")
	if len(parts) != 3 {
		// Invalid PID file, daemon likely crashed
		os.Remove(dm.pidFile)
		return &DaemonInfo{IsRunning: false}, nil
	}

	pid, err := strconv.Atoi(parts[0])
	if err != nil {
		os.Remove(dm.pidFile)
		return &DaemonInfo{IsRunning: false}, nil
	}

	startTime, err := time.Parse(time.RFC3339, parts[2])
	if err != nil {
		startTime = time.Now() // Fallback
	}

	// Check if process is actually running
	isRunning := dm.isProcessRunning(pid)
	if !isRunning {
		// Process died, clean up stale PID file
		os.Remove(dm.pidFile)
		return &DaemonInfo{IsRunning: false}, nil
	}

	return &DaemonInfo{
		PID:       pid,
		Model:     parts[1],
		StartTime: startTime,
		IsRunning: true,
	}, nil
}

// isProcessRunning checks if a process with given PID is running
func (dm *DaemonManager) isProcessRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	// Send signal 0 to check if process exists
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// WritePIDFile writes daemon info to PID file
func (dm *DaemonManager) WritePIDFile(pid int, model string) error {
	data := fmt.Sprintf("%d|%s|%s\n", pid, model, time.Now().Format(time.RFC3339))
	return os.WriteFile(dm.pidFile, []byte(data), 0644)
}

// RemovePIDFile removes the PID file
func (dm *DaemonManager) RemovePIDFile() error {
	err := os.Remove(dm.pidFile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Stop stops the running daemon
func (dm *DaemonManager) Stop() error {
	info, err := dm.GetStatus()
	if err != nil {
		return fmt.Errorf("failed to get daemon status: %w", err)
	}

	if !info.IsRunning {
		return fmt.Errorf("daemon is not running")
	}

	// Send SIGTERM for graceful shutdown
	process, err := os.FindProcess(info.PID)
	if err != nil {
		return fmt.Errorf("failed to find process: %w", err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to send SIGTERM: %w", err)
	}

	// Wait up to 5 seconds for graceful shutdown
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		if !dm.isProcessRunning(info.PID) {
			dm.RemovePIDFile()
			return nil
		}
	}

	// If still running, send SIGKILL
	if err := process.Signal(syscall.SIGKILL); err != nil {
		return fmt.Errorf("failed to kill daemon: %w", err)
	}

	dm.RemovePIDFile()
	return nil
}

// StartBackground starts the daemon in the background
func (dm *DaemonManager) StartBackground(model string, interval string, aggressive bool) error {
	// Check if already running
	info, err := dm.GetStatus()
	if err != nil {
		return fmt.Errorf("failed to check daemon status: %w", err)
	}

	if info.IsRunning {
		return fmt.Errorf("daemon already running (PID %d, model %s)", info.PID, info.Model)
	}

	// Get path to current executable
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Prepare daemon command
	args := []string{"summarize", "--daemon", "--model", model, "--interval", interval}
	if aggressive {
		args = append(args, "--aggressive")
	}
	cmd := exec.Command(executable, args...)

	// Detach from parent process
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Redirect output to log file
	configDir, _ := os.UserConfigDir()
	logDir := filepath.Join(configDir, "ccrider", "daemon")
	os.MkdirAll(logDir, 0755)

	logFile := filepath.Join(logDir, "daemon.log")
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer f.Close()

	cmd.Stdout = f
	cmd.Stderr = f

	// Start the daemon
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	// Write PID file
	if err := dm.WritePIDFile(cmd.Process.Pid, model); err != nil {
		// Kill the process if we can't write PID file
		cmd.Process.Kill()
		return fmt.Errorf("failed to write PID file: %w", err)
	}

	// Detach from the process
	cmd.Process.Release()

	// Give it a moment to initialize
	time.Sleep(100 * time.Millisecond)

	// Verify it's running
	if !dm.isProcessRunning(cmd.Process.Pid) {
		return fmt.Errorf("daemon failed to start (check %s for errors)", logFile)
	}

	return nil
}

// Restart restarts the daemon with new settings
func (dm *DaemonManager) Restart(model string, interval string, aggressive bool) error {
	// Stop if running
	info, err := dm.GetStatus()
	if err == nil && info.IsRunning {
		if err := dm.Stop(); err != nil {
			return fmt.Errorf("failed to stop daemon: %w", err)
		}
	}

	// Start with new settings
	return dm.StartBackground(model, interval, aggressive)
}

// FormatUptime formats duration as human-readable uptime
func FormatUptime(d time.Duration) string {
	d = d.Round(time.Second)

	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour

	hours := d / time.Hour
	d -= hours * time.Hour

	minutes := d / time.Minute
	d -= minutes * time.Minute

	seconds := d / time.Second

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}
