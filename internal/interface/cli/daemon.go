package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/llm"
	"github.com/spf13/cobra"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Manage the unified background daemon",
	Long: `Control the unified daemon that handles:
  - File watching for auto-sync
  - LLM-powered auto-summarization
  - Background processing

The daemon keeps the LLM loaded in memory for instant summarization.`,
}

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the daemon in the background",
	Long: `Start the unified daemon as a background process.

The daemon will:
  1. Watch ~/.claude/projects/ for new/changed sessions
  2. Auto-sync sessions to SQLite database
  3. Auto-summarize sessions using loaded LLM (fast!)`,
	RunE: daemonStart,
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the running daemon",
	RunE:  daemonStop,
}

var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status",
	RunE:  daemonStatus,
}

var daemonRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the daemon",
	RunE:  daemonRestart,
}

var daemonInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install daemon to start at login (macOS only)",
	RunE:  daemonInstall,
}

var daemonUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Uninstall daemon from login items",
	RunE:  daemonUninstall,
}

var daemonPauseCmd = &cobra.Command{
	Use:   "pause",
	Short: "Pause background summarization (file watching continues)",
	RunE:  daemonPause,
}

var daemonResumeCmd = &cobra.Command{
	Use:   "resume",
	Short: "Resume background summarization",
	RunE:  daemonResume,
}

var (
	daemonModel      string
	daemonInterval   string
	daemonAggressive bool
)

func init() {
	rootCmd.AddCommand(daemonCmd)

	daemonCmd.AddCommand(daemonStartCmd)
	daemonCmd.AddCommand(daemonStopCmd)
	daemonCmd.AddCommand(daemonStatusCmd)
	daemonCmd.AddCommand(daemonRestartCmd)
	daemonCmd.AddCommand(daemonPauseCmd)
	daemonCmd.AddCommand(daemonResumeCmd)
	daemonCmd.AddCommand(daemonInstallCmd)
	daemonCmd.AddCommand(daemonUninstallCmd)

	daemonStartCmd.Flags().StringVar(&daemonModel, "model", "qwen-1.5b", "Model to use (qwen-1.5b or llama-8b)")
	daemonStartCmd.Flags().StringVar(&daemonInterval, "interval", "5m", "Batch summarization interval")
	daemonStartCmd.Flags().BoolVar(&daemonAggressive, "aggressive", false, "Aggressive backfill mode (~30 sessions/min vs gentle ~1/min)")

	daemonRestartCmd.Flags().StringVar(&daemonModel, "model", "qwen-1.5b", "Model to use (qwen-1.5b or llama-8b)")
	daemonRestartCmd.Flags().StringVar(&daemonInterval, "interval", "5m", "Batch summarization interval")
}

func daemonStart(cmd *cobra.Command, args []string) error {
	dm, err := llm.NewDaemonManager()
	if err != nil {
		return fmt.Errorf("failed to create daemon manager: %w", err)
	}

	// Check if already running
	info, err := dm.GetStatus()
	if err != nil {
		return fmt.Errorf("failed to check daemon status: %w", err)
	}

	if info.IsRunning {
		return fmt.Errorf("daemon already running (PID %d, model %s, uptime %s)",
			info.PID, info.Model, llm.FormatUptime(time.Since(info.StartTime)))
	}

	fmt.Printf("Starting unified daemon...\n")
	fmt.Printf("  Model: %s\n", daemonModel)
	fmt.Printf("  Watch: ~/.claude/projects/\n")
	if daemonAggressive {
		fmt.Printf("  Mode: AGGRESSIVE (~30 sessions/min)\n")
	} else {
		fmt.Printf("  Mode: GENTLE (~1 session/min)\n")
	}
	fmt.Println()

	err = dm.StartBackground(daemonModel, daemonInterval, daemonAggressive)
	if err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	// Get updated status
	info, err = dm.GetStatus()
	if err != nil {
		return fmt.Errorf("daemon started but failed to get status: %w", err)
	}

	// Show log location
	configDir, _ := os.UserConfigDir()
	logFile := filepath.Join(configDir, "ccrider", "daemon", "daemon.log")

	fmt.Printf("✓ Daemon started successfully\n")
	fmt.Printf("  PID: %d\n", info.PID)
	fmt.Printf("  Logs: %s\n", logFile)
	fmt.Println()
	fmt.Println("Use 'ccrider daemon status' to check daemon state")
	fmt.Println("Use 'ccrider daemon stop' to stop the daemon")

	return nil
}

func daemonStop(cmd *cobra.Command, args []string) error {
	dm, err := llm.NewDaemonManager()
	if err != nil {
		return fmt.Errorf("failed to create daemon manager: %w", err)
	}

	info, err := dm.GetStatus()
	if err != nil {
		return fmt.Errorf("failed to check daemon status: %w", err)
	}

	if !info.IsRunning {
		fmt.Println("Daemon is not running")
		return nil
	}

	fmt.Printf("Stopping daemon (PID %d)...\n", info.PID)

	err = dm.Stop()
	if err != nil {
		return fmt.Errorf("failed to stop daemon: %w", err)
	}

	fmt.Println("✓ Daemon stopped")
	return nil
}

func daemonStatus(cmd *cobra.Command, args []string) error {
	dm, err := llm.NewDaemonManager()
	if err != nil {
		return fmt.Errorf("failed to create daemon manager: %w", err)
	}

	info, err := dm.GetStatus()
	if err != nil {
		return fmt.Errorf("failed to check daemon status: %w", err)
	}

	if !info.IsRunning {
		fmt.Println("Daemon Status: NOT RUNNING")
		fmt.Println()
		fmt.Println("⚠️  LLM operations will be SLOW without the daemon")
		fmt.Println("   (model loads from scratch each time)")
		fmt.Println()
		fmt.Println("Start daemon: ccrider daemon start")
		return nil
	}

	uptime := time.Since(info.StartTime)

	fmt.Println("Daemon Status: RUNNING")
	fmt.Println()
	fmt.Printf("  PID:        %d\n", info.PID)
	fmt.Printf("  Model:      %s\n", info.Model)
	fmt.Printf("  Uptime:     %s\n", llm.FormatUptime(uptime))
	fmt.Printf("  Started:    %s\n", info.StartTime.Format("2006-01-02 15:04:05"))
	fmt.Println()

	// Show database stats
	database, err := db.New(dbPath)
	if err == nil {
		defer database.Close()

		total, summarized, pending, err := database.GetSummarizationStats()
		if err == nil {
			fmt.Println("Summarization:")
			fmt.Printf("  Total sessions:     %d\n", total)
			fmt.Printf("  Summarized:         %d (%.1f%%)\n", summarized, float64(summarized)/float64(total)*100)
			fmt.Printf("  Pending:            %d\n", pending)
		}
	}

	// Show log location
	configDir, _ := os.UserConfigDir()
	logFile := filepath.Join(configDir, "ccrider", "daemon", "daemon.log")
	fmt.Println()
	fmt.Printf("  Logs: %s\n", logFile)

	return nil
}

func daemonRestart(cmd *cobra.Command, args []string) error {
	dm, err := llm.NewDaemonManager()
	if err != nil {
		return fmt.Errorf("failed to create daemon manager: %w", err)
	}

	info, err := dm.GetStatus()
	if err != nil {
		return fmt.Errorf("failed to check daemon status: %w", err)
	}

	if info.IsRunning {
		fmt.Printf("Stopping daemon (PID %d)...\n", info.PID)
		if err := dm.Stop(); err != nil {
			return fmt.Errorf("failed to stop daemon: %w", err)
		}
		fmt.Println("✓ Daemon stopped")
		time.Sleep(500 * time.Millisecond) // Give it a moment
	}

	fmt.Printf("Starting daemon with model %s...\n", daemonModel)
	err = dm.StartBackground(daemonModel, daemonInterval, daemonAggressive)
	if err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	info, _ = dm.GetStatus()
	fmt.Printf("✓ Daemon restarted (PID %d)\n", info.PID)

	return nil
}

func daemonInstall(cmd *cobra.Command, args []string) error {
	// TODO: Implement launchd plist installation
	fmt.Println("Installing daemon to start at login...")
	fmt.Println()
	fmt.Println("⚠️  Startup installation not yet implemented")
	fmt.Println("   Coming soon: automatic daemon start via launchd")
	return nil
}

func daemonUninstall(cmd *cobra.Command, args []string) error {
	// TODO: Implement launchd plist removal
	fmt.Println("⚠️  Startup uninstallation not yet implemented")
	return nil
}

func daemonPause(cmd *cobra.Command, args []string) error {
	// Get pause file path
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home dir: %w", err)
	}
	pauseFile := filepath.Join(home, ".config", "ccrider", "daemon", "paused")

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(pauseFile), 0755); err != nil {
		return fmt.Errorf("failed to create config dir: %w", err)
	}

	// Create pause file
	if err := os.WriteFile(pauseFile, []byte("paused\n"), 0644); err != nil {
		return fmt.Errorf("failed to create pause file: %w", err)
	}

	fmt.Println("✓ Daemon summarization paused")
	fmt.Println("  (file watching continues, no new summarizations will run)")
	fmt.Println("  Use 'ccrider daemon resume' to resume summarization")

	return nil
}

func daemonResume(cmd *cobra.Command, args []string) error {
	// Get pause file path
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home dir: %w", err)
	}
	pauseFile := filepath.Join(home, ".config", "ccrider", "daemon", "paused")

	// Check if paused
	if _, err := os.Stat(pauseFile); os.IsNotExist(err) {
		fmt.Println("Daemon is not paused")
		return nil
	}

	// Remove pause file
	if err := os.Remove(pauseFile); err != nil {
		return fmt.Errorf("failed to remove pause file: %w", err)
	}

	fmt.Println("✓ Daemon summarization resumed")
	fmt.Println("  Background summarization will continue")

	return nil
}
