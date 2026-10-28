// Package interrupt maps Ctrl-C onto a session.
package interrupt

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/mittalkanishk/steerloop/loop"
)

// Watcher: while a turn runs, the first Ctrl-C stops its loop after the
// current iteration and a second aborts outright. At the prompt between
// turns, one Ctrl-C ends the session.
type Watcher struct {
	stderr  io.Writer
	running atomic.Pointer[loop.Loop] // the current turn's loop; nil at the prompt
	aborted atomic.Bool
}

func NewWatcher(stderr io.Writer) *Watcher {
	return &Watcher{stderr: stderr}
}

// Track says which loop a press stops: the turn's loop, or nil at the prompt.
func (w *Watcher) Track(l *loop.Loop) {
	w.running.Store(l)
}

// Watch handles presses until ctx ends. cancel ends the whole session.
func (w *Watcher) Watch(ctx context.Context, cancel context.CancelFunc) {
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	var stopped *loop.Loop // the loop an earlier press already stopped
	for {
		select {
		case <-ctx.Done():
			return
		case <-signals:
		}

		// First press during a turn: stop gently.
		l := w.running.Load()
		if l != nil && l != stopped {
			fmt.Fprintln(w.stderr, "\nstopping after the current iteration (interrupt again to abort)")
			l.Stop()
			stopped = l
			continue
		}

		// A second press, or a press at the prompt: abort.
		if l == nil {
			fmt.Fprintln(w.stderr) // end the "❯ " line
		}
		w.aborted.Store(true)
		cancel()
		return
	}
}

func (w *Watcher) Aborted() bool {
	return w.aborted.Load()
}
