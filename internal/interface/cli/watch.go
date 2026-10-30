package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/watch"
	"github.com/spf13/cobra"
)

// exitCode ends the process with a specific status after output has already
// been written. Execute exits with it without printing an error.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

const (
	exitTimeout = 2
	exitGap     = 3
	exitAborted = 130
)

var (
	watchTimeout     time.Duration
	watchFollow      bool
	watchThroughGaps bool
	armSignal        bool
	armChannel       string
	armMintChannel   bool
	armUntil         []string
	armPhrase        string
	armMintPhrase    bool
	signalKey        string
	signalFrom       string
)

var waitCmd = &cobra.Command{
	Use:   "wait <cursor>",
	Short: "Wait for something to happen in another session",
	Long: `Wait for an event on an armed watch and print it as JSON.

Arm a watch first with 'ccrider wait arm', which prints a cursor. Then pass
the cursor to 'ccrider wait'. Each result includes a cursor to continue from;
nothing is lost between calls. Deduplicate events by event_id.

Transcript watches follow a Claude Code or Codex session: a turn ending, a
new message, or a message containing a phrase. Signal watches wait for
another session to post to a channel with 'ccrider signal' or the MCP tool
send_session_signal.

Exit status: 0 events, 2 timed out (the printed cursor continues the watch),
3 gap or expired cursor (events may be missing; continue with the printed
cursor), 1 error.

With --follow, prints one JSON result per line and keeps waiting. It stops at
a gap or expired cursor unless --through-gaps is given.

Suited to running as a background command: an agent harness that wakes the
agent when a background command exits or prints can wait without spending
model calls.`,
	Args: cobra.ExactArgs(1),
	RunE: runWait,
}

var waitArmCmd = &cobra.Command{
	Use:   "arm [session]",
	Short: "Arm a watch and print its cursor",
	Long: `Arm a watch and print a JSON result with the cursor to wait on.

Transcript watch (Claude Code and Codex sessions):
  ccrider wait arm <session> --until turn_end
  ccrider wait arm <session> --until phrase --mint-phrase

Signal watch:
  ccrider wait arm --signal --mint-channel
  ccrider wait arm --signal --channel deploy-done

Then run 'ccrider wait <cursor>'.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runWaitArm,
}

var signalCmd = &cobra.Command{
	Use:   "signal <channel> <body>",
	Short: "Post a signal to a channel, waking sessions that wait on it",
	Args:  cobra.ExactArgs(2),
	RunE:  runSignal,
}

func init() {
	waitCmd.Flags().DurationVar(&watchTimeout, "timeout", 0, "Give up after this long (0 waits indefinitely)")
	waitCmd.Flags().BoolVar(&watchFollow, "follow", false, "Keep waiting and print each result as a JSON line")
	waitCmd.Flags().BoolVar(&watchThroughGaps, "through-gaps", false, "With --follow, continue past gaps and expired cursors")
	waitArmCmd.Flags().BoolVar(&armSignal, "signal", false, "Watch a signal channel")
	waitArmCmd.Flags().StringVar(&armChannel, "channel", "", "Signal channel to watch")
	waitArmCmd.Flags().BoolVar(&armMintChannel, "mint-channel", false, "Create a new random channel")
	waitArmCmd.Flags().StringSliceVar(&armUntil, "until", nil, "Transcript conditions: turn_end, message, phrase")
	waitArmCmd.Flags().StringVar(&armPhrase, "phrase", "", "Phrase for the phrase condition")
	waitArmCmd.Flags().BoolVar(&armMintPhrase, "mint-phrase", false, "Create a unique phrase for the phrase condition")
	signalCmd.Flags().StringVar(&signalKey, "key", "", "Idempotency key: resending with the same key and body doesn't post twice")
	signalCmd.Flags().StringVar(&signalFrom, "from", "", "Your session ID, reported to watchers as unverified")
	waitCmd.AddCommand(waitArmCmd)
	rootCmd.AddCommand(waitCmd)
	rootCmd.AddCommand(signalCmd)
}

type waitOutput struct {
	Status      string        `json:"status"`
	Events      []watch.Event `json:"events"`
	Cursor      string        `json:"cursor"`
	Channel     string        `json:"channel,omitempty"`
	Phrase      string        `json:"phrase,omitempty"`
	TargetState string        `json:"target_state,omitempty"`
	Gap         string        `json:"gap_reason,omitempty"`
	RolloutID   string        `json:"rollout_id,omitempty"`
}

func printWaitResult(w io.Writer, res watch.Result) error {
	out := waitOutput{Status: res.Status, Events: res.Events, Cursor: res.Cursor,
		Channel: res.Channel, Phrase: res.Phrase, TargetState: res.TargetState,
		Gap: res.GapReason, RolloutID: res.Source}
	if out.Events == nil {
		out.Events = []watch.Event{}
	}
	b, err := watch.MarshalJSON(out)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

func openWatcher() (*watch.Watcher, func(), error) {
	return openWatcherContext(context.Background())
}

// openWatcherContext opens the database, giving up when ctx ends. Opening
// can block behind another process's write lock; a wait with a timeout must
// not overrun it for that reason.
func openWatcherContext(ctx context.Context) (*watch.Watcher, func(), error) {
	type opened struct {
		database *db.DB
		err      error
	}
	ch := make(chan opened, 1)
	go func() {
		database, err := db.New(dbPath)
		ch <- opened{database, err}
	}()
	select {
	case o := <-ch:
		if o.err != nil {
			return nil, nil, fmt.Errorf("failed to open database: %w", o.err)
		}
		return &watch.Watcher{DB: o.database}, func() { _ = o.database.Close() }, nil
	case <-ctx.Done():
		// Close the database if the open finishes after we've given up.
		go func() {
			if o := <-ch; o.err == nil {
				_ = o.database.Close()
			}
		}()
		return nil, nil, ctx.Err()
	}
}

func runWaitArm(cmd *cobra.Command, args []string) error {
	spec := watch.Spec{Channel: armChannel, MintChannel: armMintChannel, Phrase: armPhrase, MintPhrase: armMintPhrase}
	switch {
	case armSignal && (len(args) > 0 || len(armUntil) > 0):
		return errors.New("--signal watches a channel; don't pass a session or --until")
	case armSignal:
		spec.Until = []string{watch.CondSignal}
	case len(args) == 0:
		return errors.New("pass a session to watch, or --signal")
	default:
		spec.SessionID = args[0]
		spec.Until = armUntil
		if len(spec.Until) == 0 {
			spec.Until = []string{watch.CondTurnEnd}
		}
	}
	watcher, closeDB, err := openWatcher()
	if err != nil {
		return err
	}
	defer closeDB()
	res, err := watcher.Arm(cmd.Context(), spec)
	if err != nil {
		return err
	}
	return printWaitResult(cmd.OutOrStdout(), res)
}

func runWait(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if watchTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, watchTimeout)
		defer cancel()
	}
	cur := args[0]
	out := cmd.OutOrStdout()

	watcher, closeDB, err := openWatcherContext(ctx)
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		if err := printWaitResult(out, watch.Result{Status: watch.StatusWaiting, Cursor: cur}); err != nil {
			return err
		}
		return exitCode(exitTimeout)
	case errors.Is(err, context.Canceled):
		return exitCode(exitAborted)
	case err != nil:
		return err
	}
	defer closeDB()
	for {
		res, err := watcher.Wait(ctx, cur, watch.Spec{}, watch.Limits{})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return exitCode(exitAborted)
			}
			return err
		}
		if err := printWaitResult(out, res); err != nil {
			return err
		}
		cur = res.Cursor
		switch res.Status {
		case watch.StatusWaiting:
			return exitCode(exitTimeout)
		case watch.StatusGap, watch.StatusCursorExpired:
			if !watchFollow || !watchThroughGaps {
				return exitCode(exitGap)
			}
		}
		if !watchFollow {
			return nil
		}
	}
}

func runSignal(cmd *cobra.Command, args []string) error {
	watcher, closeDB, err := openWatcher()
	if err != nil {
		return err
	}
	defer closeDB()
	sig, dup, err := watcher.SendSignal(cmd.Context(), db.SendSignalOptions{
		Channel: args[0], Body: args[1], IdempotencyKey: signalKey, FromSessionID: signalFrom,
	})
	if err != nil {
		return err
	}
	b, _ := watch.MarshalJSON(map[string]any{"signal_id": sig.ID, "channel": sig.Channel, "duplicate": dup})
	_, err = fmt.Fprintln(cmd.OutOrStdout(), string(b))
	return err
}
