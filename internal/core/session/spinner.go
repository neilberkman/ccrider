package session

import (
	"fmt"
	"io"
	"os"
	"time"
)

// Spinner shows a simple spinning animation while waiting
type Spinner struct {
	writer  io.Writer
	message string
	stop    chan bool
}

// NewSpinner creates a new spinner with a message
func NewSpinner(message string) *Spinner {
	return &Spinner{
		writer:  os.Stderr,
		message: message,
		stop:    make(chan bool),
	}
}

// Start begins the spinner animation in a goroutine
func (s *Spinner) Start() {
	go func() {
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		i := 0

		for {
			select {
			case <-s.stop:
				// Clear the line
				fmt.Fprintf(s.writer, "\r\033[K")
				return
			default:
				// Print frame
				fmt.Fprintf(s.writer, "\r%s %s", frames[i], s.message)
				i = (i + 1) % len(frames)
				time.Sleep(80 * time.Millisecond)
			}
		}
	}()
}

// Stop stops the spinner and clears the line
func (s *Spinner) Stop() {
	s.stop <- true
	close(s.stop)
}
