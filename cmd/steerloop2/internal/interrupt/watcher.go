// Package interrupt maps Ctrl-C onto a running loop.
package interrupt

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/mittalkanishk/steerloop/loop2"
)

// Watcher: the first Ctrl-C stops the loop after its current step, a second
// aborts outright.
type Watcher struct {
	stderr  io.Writer
	aborted atomic.Bool
}

func NewWatcher(stderr io.Writer) *Watcher {
	return &Watcher{stderr: stderr}
}

func (w *Watcher) Watch(ctx context.Context, cancel context.CancelFunc, loop *loop2.Loop) {
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	// First press: stop gently.
	select {
	case <-ctx.Done():
		return
	case <-signals:
	}
	fmt.Fprintln(w.stderr, "\nstopping after the current step (interrupt again to abort)")
	loop.Stop()

	// Second press: abort.
	select {
	case <-ctx.Done():
		return
	case <-signals:
	}
	w.aborted.Store(true)
	cancel()
}

func (w *Watcher) Aborted() bool {
	return w.aborted.Load()
}
