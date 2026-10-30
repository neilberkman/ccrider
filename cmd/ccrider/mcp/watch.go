package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/watch"
)

const (
	// stdioWorkers is the stdio server's tool worker pool. maxConcurrentWaits
	// leaves the rest free, so search and other tools stay responsive while
	// waits are held open.
	stdioWorkers       = 16
	maxConcurrentWaits = 8

	defaultWaitSeconds = 20
	maxWaitSeconds     = 600
	// waitResponseMargin is reserved out of max_wait_seconds for building
	// and sending the response before the client's own timeout.
	waitResponseMargin = 500 * time.Millisecond
	progressInterval   = 10 * time.Second
)

// waitEventBytes is the event budget per result: the server-wide response
// cap minus room for the cursor and other fields.
func waitEventBytes() int { return maxResponseBytes() - 4096 }

type WaitForSessionArgs struct {
	SessionID      string   `json:"session_id,omitempty"`
	Until          []string `json:"until,omitempty"`
	Phrase         string   `json:"phrase,omitempty"`
	MintPhrase     bool     `json:"mint_phrase,omitempty"`
	Channel        string   `json:"channel,omitempty"`
	MintChannel    bool     `json:"mint_channel,omitempty"`
	Cursor         string   `json:"cursor,omitempty"`
	MaxWaitSeconds *float64 `json:"max_wait_seconds,omitempty"`
}

type WaitForSessionResponse struct {
	Status      string        `json:"status"`
	Events      []watch.Event `json:"events"`
	Cursor      string        `json:"cursor"`
	Phrase      string        `json:"phrase,omitempty"`
	Channel     string        `json:"channel,omitempty"`
	TargetState string        `json:"target_state,omitempty"`
	GapReason   string        `json:"gap_reason,omitempty"`
	Instruction string        `json:"instruction,omitempty"`
}

type SendSessionSignalArgs struct {
	Channel        string `json:"channel"`
	Body           string `json:"body"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	FromSessionID  string `json:"from_session_id,omitempty"`
}

type SendSessionSignalResponse struct {
	SignalID  int64  `json:"signal_id"`
	Channel   string `json:"channel"`
	Duplicate bool   `json:"duplicate"`
}

// waitSlots limits concurrent waits per server process.
type waitSlots chan struct{}

func (s waitSlots) tryAcquire() bool {
	select {
	case s <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s waitSlots) release() { <-s }

func registerWatchTools(s *server.MCPServer, database *db.DB) {
	watcher := &watch.Watcher{DB: database}
	slots := make(waitSlots, maxConcurrentWaits)

	s.AddTool(mcp.NewTool("wait_for_session",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(false),
		mcp.WithDescription(`Wait for something to happen in another coding agent session, in any harness. Currently supports signal watches: wait until a message is posted to a channel with send_session_signal.

Flow:
1. Arm: call without a cursor, with until=["signal"] and either a channel or mint_channel=true. Returns immediately with status "armed", a cursor and the channel.
2. Tell the other session (or the user) to call send_session_signal on that channel when done, or to run: ccrider signal <channel> <message>.
3. Wait: call with the cursor. Returns as soon as a signal arrives (status "event"), or at max_wait_seconds with status "waiting". Always call again with the returned cursor; nothing is lost between calls.

Other statuses: "cursor_expired" means signals were pruned before you read them (retention is 7 days); continue with the fresh cursor. "busy" means too many waits are running; retry with the same cursor. Deduplicate events by event_id.

max_wait_seconds defaults to 20, which fits every known client's tool timeout. Raise it only if your client allows longer tool calls.`),
		mcp.WithArray("until", mcp.WithStringItems(mcp.Enum(watch.CondSignal)),
			mcp.Description(`Conditions to wait for. Currently only ["signal"]. Required when arming; optional with a cursor, and must match it if given.`)),
		mcp.WithString("channel", mcp.Description("Signal channel to watch. Omit and set mint_channel=true to get a new random channel.")),
		mcp.WithBoolean("mint_channel", mcp.Description("When arming, create a new random channel.")),
		mcp.WithString("cursor", mcp.Description("Cursor from the previous result. Omit to arm a new watch.")),
		mcp.WithNumber("max_wait_seconds", mcp.Description("Longest time to wait before returning status waiting (default 20, maximum 600).")),
	), makeWaitForSessionHandler(watcher, slots))

	s.AddTool(mcp.NewTool("send_session_signal",
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(false),
		mcp.WithDescription("Post a signal to a channel so sessions waiting on it with wait_for_session wake up. Use when another session asked you to signal a channel, for example when your task is done. Put a short result or summary in body. Pass idempotency_key if you might retry, so a retry doesn't post twice."),
		mcp.WithString("channel", mcp.Required(), mcp.Description("Channel name you were given.")),
		mcp.WithString("body", mcp.Required(), mcp.Description("Message for the waiting session, up to 16 KiB.")),
		mcp.WithString("idempotency_key", mcp.Description("Optional key; resending with the same key and body returns the original signal instead of posting again.")),
		mcp.WithString("from_session_id", mcp.Description("Optional: your own session ID, if you know it. Reported to the watcher as unverified.")),
	), makeSendSessionSignalHandler(watcher))
}

func makeWaitForSessionHandler(watcher *watch.Watcher, slots waitSlots) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args WaitForSessionArgs
		if raw, ok := request.Params.Arguments.(map[string]interface{}); ok {
			value := map[string]interface{}{"max_wait_seconds": raw["max_wait_seconds"]}
			coerceStringNumbers(value)
			if value["max_wait_seconds"] != nil {
				raw["max_wait_seconds"] = value["max_wait_seconds"]
			}
		}
		if err := decodeArgs(request, &args); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
		}
		wait := time.Duration(defaultWaitSeconds) * time.Second
		if args.MaxWaitSeconds != nil {
			if *args.MaxWaitSeconds < 1 || *args.MaxWaitSeconds > maxWaitSeconds {
				return watchError(&watch.Error{Code: watch.CodeInvalidArgument, Message: fmt.Sprintf("max_wait_seconds must be between 1 and %d", maxWaitSeconds)}), nil
			}
			wait = time.Duration(*args.MaxWaitSeconds * float64(time.Second))
		}
		spec := watch.Spec{
			SessionID: args.SessionID, Until: args.Until, Phrase: args.Phrase, Channel: args.Channel,
			MintPhrase: args.MintPhrase, MintChannel: args.MintChannel,
		}

		if args.Cursor == "" {
			res, err := watcher.Arm(ctx, spec)
			if err != nil {
				return watchError(err), nil
			}
			return watchResult(res), nil
		}
		if spec.MintChannel || spec.MintPhrase {
			return watchError(&watch.Error{Code: watch.CodeInvalidArgument, Message: "mint options apply only when arming"}), nil
		}

		if !slots.tryAcquire() {
			return mcp.NewToolResultText(string(mustMarshal(WaitForSessionResponse{
				Status: "busy", Events: []watch.Event{}, Cursor: args.Cursor,
				Instruction: "Too many waits are running in this ccrider server. Retry with the same cursor.",
			}))), nil
		}
		defer slots.release()

		waitCtx, cancel := context.WithTimeout(ctx, max(wait-waitResponseMargin, 100*time.Millisecond))
		defer cancel()
		stopProgress := startProgress(waitCtx, request)
		res, err := watcher.Wait(waitCtx, args.Cursor, spec, watch.Limits{MaxBytes: waitEventBytes()})
		stopProgress()
		if err != nil {
			return watchError(err), nil
		}
		return watchResult(res), nil
	}
}

// startProgress sends progress notifications while a wait is open, if the
// client asked for them. Progress values strictly increase. Nothing relies on
// the client resetting its timeout because of them. The returned function
// stops the notifications and returns once no more will be sent.
func startProgress(ctx context.Context, request mcp.CallToolRequest) func() {
	if request.Params.Meta == nil || request.Params.Meta.ProgressToken == nil {
		return func() {}
	}
	srv := server.ServerFromContext(ctx)
	if srv == nil {
		return func() {}
	}
	token := request.Params.Meta.ProgressToken
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(progressInterval)
		defer ticker.Stop()
		n := 0
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				n++
				_ = srv.SendNotificationToClient(ctx, string(mcp.MethodNotificationProgress), map[string]any{
					"progressToken": token,
					"progress":      n,
					"message":       "waiting",
				})
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

func makeSendSessionSignalHandler(watcher *watch.Watcher) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args SendSessionSignalArgs
		if err := decodeArgs(request, &args); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
		}
		sig, dup, err := watcher.SendSignal(ctx, db.SendSignalOptions{
			Channel: args.Channel, Body: args.Body,
			IdempotencyKey: args.IdempotencyKey, FromSessionID: args.FromSessionID,
		})
		switch {
		case errors.Is(err, db.ErrIdempotencyConflict):
			return watchError(&watch.Error{Code: "idempotency_conflict", Message: err.Error()}), nil
		case errors.Is(err, db.ErrInvalidSignal):
			return watchError(&watch.Error{Code: watch.CodeInvalidArgument, Message: err.Error()}), nil
		case err != nil:
			return mcp.NewToolResultError(fmt.Sprintf("send signal failed: %v", err)), nil
		}
		return mcp.NewToolResultText(string(mustMarshal(SendSessionSignalResponse{
			SignalID: sig.ID, Channel: sig.Channel, Duplicate: dup,
		}))), nil
	}
}

func decodeArgs(request mcp.CallToolRequest, out any) error {
	b, err := json.Marshal(request.Params.Arguments)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func watchResult(res watch.Result) *mcp.CallToolResult {
	out := WaitForSessionResponse{
		Status: res.Status, Events: res.Events, Cursor: res.Cursor, Phrase: res.Phrase,
		Channel: res.Channel, TargetState: res.TargetState, GapReason: res.GapReason,
	}
	if out.Events == nil {
		out.Events = []watch.Event{}
	}
	switch res.Status {
	case watch.StatusArmed:
		out.Instruction = "Armed. Ask the other session to call send_session_signal with channel '" + res.Channel + "' (or run: ccrider signal " + res.Channel + " <message>), then call wait_for_session with this cursor."
	case watch.StatusWaiting:
		out.Instruction = "Nothing yet. Call wait_for_session again with this cursor."
	case watch.StatusCursorExpired:
		out.Instruction = "Signals were pruned before this watch read them. Continue with the fresh cursor."
	}
	return mcp.NewToolResultText(string(mustMarshal(out)))
}

func watchError(err error) *mcp.CallToolResult {
	var we *watch.Error
	if errors.As(err, &we) {
		return mcp.NewToolResultError(string(mustMarshal(map[string]string{"error": we.Code, "message": we.Message})))
	}
	if errors.Is(err, context.Canceled) {
		return mcp.NewToolResultError("wait cancelled")
	}
	return mcp.NewToolResultError(fmt.Sprintf("wait failed: %v", err))
}
