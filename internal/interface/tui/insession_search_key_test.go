package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// Regression for the "k gets blocked" bug: while typing a query into the
// in-session search box (search mode active, NOT navigation mode), the letters
// "j" and "k" must be inserted into the text input, not hijacked as vim-style
// viewport scroll commands. Any query containing j or k was silently losing
// those characters.
func TestInSessionSearchAcceptsJAndK(t *testing.T) {
	ti := textinput.New()
	ti.Focus()
	m := Model{
		mode:                detailView,
		inSessionSearchMode: true, // typing in the search box
		inSessionSearch:     ti,
		width:               80,
		currentSession: &sessionDetail{
			Messages: []messageItem{{Type: "user", Content: "kotopia jest"}},
		},
	}

	for _, r := range "kj" {
		model, _ := m.updateDetail(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = model.(Model)
	}

	if got := m.inSessionSearch.Value(); got != "kj" {
		t.Errorf("in-session search value = %q, want %q (j/k must type, not scroll)", got, "kj")
	}
}
