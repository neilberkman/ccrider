package ccsessions

import (
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/zeebo/blake3"
)

// DeterministicUUID hashes key to a stable 32-hex-char message id for
// providers whose source lines carry no usable per-message id. The same key
// always produces the same id, which is what lets re-imports upsert messages
// instead of duplicating them — so the key a parser builds (and this hash)
// must never change once a provider has shipped.
func DeterministicUUID(key string) string {
	h := blake3.New()
	_, _ = h.Write([]byte(key))
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:16])
}

// summaryRunes caps fallback summaries derived from message text.
const summaryRunes = 120

// FirstUserSummary returns the fallback session summary: the first human
// message, truncated to 120 runes. Every provider without a native title uses
// this one rule so summaries display consistently across providers.
func FirstUserSummary(messages []ParsedMessage) string {
	for _, m := range messages {
		if m.Sender != "human" || m.TextContent == "" {
			continue
		}
		runes := []rune(m.TextContent)
		if len(runes) > summaryRunes {
			return string(runes[:summaryRunes])
		}
		return m.TextContent
	}
	return ""
}

// TextFromItems extracts and joins the text of a JSON content-item array
// (e.g. [{"type":"text","text":"..."}]), keeping only items whose type is in
// accepted. Items are joined with a blank line, the shared convention for how
// multi-part messages are flattened for search indexing.
func TextFromItems(raw json.RawMessage, accepted ...string) string {
	var items []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return ""
	}
	var texts []string
	for _, item := range items {
		if item.Text == "" {
			continue
		}
		for _, t := range accepted {
			if item.Type == t {
				texts = append(texts, item.Text)
				break
			}
		}
	}
	return strings.Join(texts, "\n\n")
}
