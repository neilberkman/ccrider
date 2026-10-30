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
// cap minus room for the cursor, channel, instruction and other fields, each
// of which is bounded (a channel is at most 200 characters).
func waitEventBytes() int { return maxResponseBytes() - 8192 }

// watchJSON encodes a watch response the same way the core sizes events.
func watchJSON(v any) string {
	b, err := watch.MarshalJSON(v)
	if err != nil {
		return `{"error":"encode_failed"}`
	}
	return string(b)
}

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
	RolloutID   string        `json:"rollout_id,omitempty"`
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
		mcp.WithDescription(`Wait for something to happen in another coding agent session, in any harness.

Two kinds of watch:
- Transcript watch (Claude Code and Codex sessions): until any of "turn_end" (the session finishes a turn; outcome is completed, failed, refused or interrupted), "message" (it writes any new message), "phrase" (it writes a message containing a phrase). Needs session_id. Works without the other session's cooperation, except phrase.
- Signal watch (any harness): until ["signal"]. Another session posts to a channel with send_session_signal, or runs: ccrider signal <channel> <message>.

Flow:
1. Arm: call without a cursor. Returns immediately with status "armed", a cursor, and target_state (running, idle or unknown) for transcript watches. Use mint_phrase or mint_channel to get a unique phrase or channel to hand to the other session.
2. Wait: call with the cursor. Returns as soon as something matches (status "event"), or at max_wait_seconds with status "waiting". Always call again with the returned cursor; nothing is lost between calls. A turn already running when you armed reports its end.

Other statuses: "gap" means the transcript was rewritten, truncated or replaced, or the Codex thread moved to another rollout file (rollout_id); events in between may be missing, so read the transcript with get_session_messages if it matters, then continue with the fresh cursor. "cursor_expired" means signals were pruned before you read them. "busy" means too many waits are running; retry with the same cursor. Deduplicate events by event_id.

max_wait_seconds defaults to 20, which fits every known client's tool timeout. Raise it only if your client allows longer tool calls.`),
		mcp.WithString("session_id", mcp.Description("Session to watch, for transcript conditions. Full IDs and bare UUIDs work.")),
		mcp.WithArray("until", mcp.WithStringItems(mcp.Enum(watch.CondTurnEnd, watch.CondMessage, watch.CondPhrase, watch.CondSignal)),
			mcp.Description(`Conditions to wait for: any of turn_end, message, phrase; or signal alone. Required when arming; optional with a cursor, and must match it if given.`)),
		mcp.WithString("phrase", mcp.Description("For the phrase condition: text the other session's assistant message must contain (literal, case-sensitive). Omit and set mint_phrase=true to get a unique one.")),
		mcp.WithBoolean("mint_phrase", mcp.Description("When arming a phrase watch, create a unique phrase to give the other session.")),
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

		// The deadline covers everything the call does, arming included.
		callCtx, cancelCall := context.WithTimeout(ctx, max(wait-waitResponseMargin, 100*time.Millisecond))
		defer cancelCall()

		if args.Cursor == "" {
			res, err := watcher.Arm(callCtx, spec)
			if err != nil {
				return watchError(err), nil
			}
			return watchResult(res), nil
		}
		if spec.MintChannel || spec.MintPhrase {
			return watchError(&watch.Error{Code: watch.CodeInvalidArgument, Message: "mint options apply only when arming"}), nil
		}

		if !slots.tryAcquire() {
			return mcp.NewToolResultText(watchJSON(WaitForSessionResponse{
				Status: "busy", Events: []watch.Event{}, Cursor: args.Cursor,
				Instruction: "Too many waits are running in this ccrider server. Retry with the same cursor.",
			})), nil
		}
		defer slots.release()

		stopProgress := startProgress(callCtx, request)
		res, err := watcher.Wait(callCtx, args.Cursor, spec, watch.Limits{MaxBytes: waitEventBytes()})
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
		// An explicit empty body is a valid signal; a missing one is a mistake.
		raw, _ := request.Params.Arguments.(map[string]interface{})
		if _, ok := raw["channel"].(string); !ok {
			return watchError(&watch.Error{Code: watch.CodeInvalidArgument, Message: "channel is required"}), nil
		}
		if _, ok := raw["body"].(string); !ok {
			return watchError(&watch.Error{Code: "invalid_body", Message: "body is required; pass an empty string for a signal with no message"}), nil
		}
		sig, dup, err := watcher.SendSignal(ctx, db.SendSignalOptions{
			Channel: args.Channel, Body: args.Body,
			IdempotencyKey: args.IdempotencyKey, FromSessionID: args.FromSessionID,
		})
		switch {
		case errors.Is(err, db.ErrIdempotencyConflict):
			return watchError(&watch.Error{Code: "idempotency_conflict", Message: err.Error()}), nil
		case errors.Is(err, db.ErrInvalidSignalBody):
			return watchError(&watch.Error{Code: "invalid_body", Message: err.Error()}), nil
		case errors.Is(err, db.ErrInvalidSignal):
			return watchError(&watch.Error{Code: watch.CodeInvalidArgument, Message: err.Error()}), nil
		case err != nil:
			return mcp.NewToolResultError(fmt.Sprintf("send signal failed: %v", err)), nil
		}
		return mcp.NewToolResultText(watchJSON(SendSessionSignalResponse{
			SignalID: sig.ID, Channel: sig.Channel, Duplicate: dup,
		})), nil
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
		RolloutID: res.Source,
	}
	if out.Events == nil {
		out.Events = []watch.Event{}
	}
	switch res.Status {
	case watch.StatusArmed:
		switch {
		case res.Channel != "":
			out.Instruction = "Armed. Ask the other session to call send_session_signal with channel '" + res.Channel + "' (or run: ccrider signal " + res.Channel + " <message>), then call wait_for_session with this cursor."
		case res.Phrase != "":
			out.Instruction = "Armed. Ask the other session to include the exact phrase '" + res.Phrase + "' in a message when it's done, then call wait_for_session with this cursor."
		default:
			out.Instruction = "Armed. Call wait_for_session with this cursor."
		}
	case watch.StatusGap:
		out.Instruction = "Continuity was lost (" + res.GapReason + "); events since the previous cursor may be missing. Continue with this cursor."
	case watch.StatusWaiting:
		out.Instruction = "Nothing yet. Call wait_for_session again with this cursor."
	case watch.StatusCursorExpired:
		out.Instruction = "Signals were pruned before this watch read them. Continue with the fresh cursor."
	}
	text := watchJSON(out)
	if len(text) > maxResponseBytes() {
		// Field limits make this unreachable. If it happens anyway, report it
		// without handing back the advanced cursor, so nothing is skipped.
		return watchError(fmt.Errorf("result of %d bytes exceeds the response limit; retry with the same cursor", len(text)))
	}
	return mcp.NewToolResultText(text)
}

func watchError(err error) *mcp.CallToolResult {
	var we *watch.Error
	if errors.As(err, &we) {
		return mcp.NewToolResultError(watchJSON(map[string]string{"error": we.Code, "message": we.Message}))
	}
	if errors.Is(err, context.Canceled) {
		return mcp.NewToolResultError("wait cancelled")
	}
	return mcp.NewToolResultError(fmt.Sprintf("wait failed: %v", err))
}
