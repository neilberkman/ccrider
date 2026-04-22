package cli

import (
	"fmt"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/export"
	"github.com/spf13/cobra"
)

var viewCmd = &cobra.Command{
	Use:   "view <session-id>",
	Short: "View a session as markdown (stdout)",
	Args:  cobra.ExactArgs(1),
	RunE:  runView,
}

func init() {
	rootCmd.AddCommand(viewCmd)
}

func runView(cmd *cobra.Command, args []string) error {
	database, err := db.New(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer func() {
		_ = database.Close()
	}()

	content, err := export.GenerateMarkdown(database, args[0])
	if err != nil {
		return err
	}

	fmt.Print(content)
	return nil
}
