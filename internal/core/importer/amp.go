package importer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/neilberkman/ccrider/pkg/ampsessions"
	"github.com/neilberkman/ccrider/pkg/ccsessions"
)

const (
	ampProvider         = "amp"
	ampDefaultPageSize  = 100
	ampCommandTimeout   = 30 * time.Second
	ampProcessWaitDelay = 2 * time.Second
)

type ampRunFunc func(context.Context, ...string) ([]byte, error)

type ampClient struct {
	run      ampRunFunc
	pageSize int
	timeout  time.Duration
}

func newAmpClient() *ampClient {
	return &ampClient{run: runAmp, pageSize: ampDefaultPageSize, timeout: ampCommandTimeout}
}

func runAmp(ctx context.Context, args ...string) ([]byte, error) {
	return runAmpWithWaitDelay(ctx, ampProcessWaitDelay, args...)
}

func runAmpWithWaitDelay(ctx context.Context, waitDelay time.Duration, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "amp", args...)
	cmd.WaitDelay = waitDelay
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, runErr := cmd.Output()
	if runErr == nil {
		return output, nil
	}
	if ctx.Err() == nil && errors.Is(runErr, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success() {
		// WaitDelay can close stdout while a forked child is still writing.
		// Every Amp command consumed here returns one JSON value, so only accept
		// output that was complete before the pipe was closed.
		if json.Valid(output) {
			return output, nil
		}
		return nil, fmt.Errorf("run amp command: %w: incomplete JSON output", runErr)
	}
	message := strings.TrimSpace(stderr.String())
	if ctxErr := ctx.Err(); ctxErr != nil {
		if message != "" {
			return nil, fmt.Errorf("run amp command: %w (process: %v): %s", ctxErr, runErr, message)
		}
		return nil, fmt.Errorf("run amp command: %w (process: %v)", ctxErr, runErr)
	}
	if message != "" {
		return nil, fmt.Errorf("run amp command: %w: %s", runErr, message)
	}
	return nil, fmt.Errorf("run amp command: %w", runErr)
}

type ampThreadListItem struct {
	ID           string `json:"id"`
	Updated      string `json:"updated"`
	UpdatedAt    string `json:"updatedAt"`
	MessageCount *int   `json:"messageCount"`
}

func (item ampThreadListItem) revision() (string, error) {
	updated := strings.TrimSpace(item.Updated)
	if updated == "" {
		updated = strings.TrimSpace(item.UpdatedAt)
	}
	if updated == "" {
		return "", fmt.Errorf("thread %s has no updated timestamp", item.ID)
	}
	if item.MessageCount == nil {
		return "", fmt.Errorf("thread %s has no message count", item.ID)
	}
	if *item.MessageCount < 0 {
		return "", fmt.Errorf("thread %s has invalid message count %d", item.ID, *item.MessageCount)
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", item.ID, updated, *item.MessageCount)))
	return hex.EncodeToString(sum[:]), nil
}

func (c *ampClient) listThreads(ctx context.Context) ([]RemoteSessionRef, error) {
	seen := map[string]struct{}{}
	var refs []RemoteSessionRef
	for offset := 0; ; offset += c.pageSize {
		commandCtx, cancel := context.WithTimeout(ctx, c.timeout)
		output, err := c.run(commandCtx, "threads", "list", "--json", "--include-archived", "--limit", strconv.Itoa(c.pageSize), "--offset", strconv.Itoa(offset))
		cancel()
		if err != nil {
			return nil, fmt.Errorf("list Amp threads: %w", err)
		}
		var page []ampThreadListItem
		if err := json.Unmarshal(output, &page); err != nil {
			return nil, fmt.Errorf("parse Amp thread list at offset %d: %w", offset, err)
		}
		added := 0
		for _, item := range page {
			if strings.TrimSpace(item.ID) == "" {
				return nil, fmt.Errorf("parse Amp thread list at offset %d: thread id is missing", offset)
			}
			revision, err := item.revision()
			if err != nil {
				return nil, fmt.Errorf("parse Amp thread list at offset %d: %w", offset, err)
			}
			if _, ok := seen[item.ID]; ok {
				continue
			}
			seen[item.ID] = struct{}{}
			refs = append(refs, RemoteSessionRef{ImportID: item.ID, Revision: revision})
			added++
		}
		if len(page) < c.pageSize {
			break
		}
		if added == 0 {
			return nil, fmt.Errorf("list Amp threads: pagination made no progress at offset %d", offset)
		}
	}
	return refs, nil
}

func (c *ampClient) exportThread(ctx context.Context, threadID string) (*ccsessions.ParsedSession, error) {
	commandCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	output, err := c.run(commandCtx, "threads", "export", threadID)
	if err != nil {
		return nil, fmt.Errorf("export Amp thread %s: %w", threadID, err)
	}
	session, err := ampsessions.Parse(output)
	if err != nil {
		return nil, fmt.Errorf("parse Amp thread %s: %w", threadID, err)
	}
	if strings.TrimSpace(session.SessionID) != strings.TrimSpace(threadID) {
		return nil, fmt.Errorf("parse Amp thread %s: export contained thread id %q", threadID, session.SessionID)
	}
	return session, nil
}
