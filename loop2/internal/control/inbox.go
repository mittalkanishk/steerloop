// Package control holds the controls sent to a running loop: stop, inject a
// message, change settings.
package control

import (
	"context"
	"sync"

	"github.com/mittalkanishk/steerloop/loop2/internal/state"
)

// Kind classifies a control.
type Kind int

const (
	Stop Kind = iota
	Inject
	ModifyConfig
)

// Control is one queued control.
type Control struct {
	Kind    Kind
	Message string       // for Inject
	Change  state.Change // for ModifyConfig
}

// Settings receives setting changes. → state.Run
type Settings interface {
	Apply(c state.Change)
}

// Notifier hears about controls applied between iterations. → engine.Engine
type Notifier interface {
	Injected(message string)
	ConfigChanged()
}

const queueSize = 8

// Inbox queues controls until the loop is ready for them. Stop has its own
// channel so it can never be lost to a full queue.
type Inbox struct {
	queue    chan Control
	stop     chan struct{}
	stopOnce sync.Once
	settings Settings
}

func New(settings Settings) *Inbox {
	return &Inbox{
		queue:    make(chan Control, queueSize),
		stop:     make(chan struct{}),
		settings: settings,
	}
}

// Send queues a control. It returns false when the queue is full.
func (in *Inbox) Send(c Control) bool {
	select {
	case in.queue <- c:
		return true
	default:
		return false
	}
}

// Stop asks the loop to stop. Safe to call more than once.
func (in *Inbox) Stop() {
	in.stopOnce.Do(func() { close(in.stop) })
}

func (in *Inbox) StopRequested() bool {
	select {
	case <-in.stop:
		return true
	default:
		return false
	}
}

// Drain applies every queued control and tells n about each. It returns true
// when one of them was a stop.
func (in *Inbox) Drain(n Notifier) bool {
	for {
		select {
		case c := <-in.queue:
			switch c.Kind {
			case Stop:
				return true
			case Inject:
				if c.Message != "" {
					n.Injected(c.Message)
				}
			case ModifyConfig:
				in.settings.Apply(c.Change)
				n.ConfigChanged()
			}
		default:
			return false
		}
	}
}

// WaitInject blocks until an Inject arrives and returns its message. ok is
// false when the loop is stopped or ctx ends first. Setting changes that
// arrive meanwhile are applied without notice.
func (in *Inbox) WaitInject(ctx context.Context) (string, bool) {
	for {
		select {
		case c := <-in.queue:
			switch c.Kind {
			case Stop:
				return "", false
			case Inject:
				return c.Message, true
			case ModifyConfig:
				in.settings.Apply(c.Change)
			}
		case <-in.stop:
			return "", false
		case <-ctx.Done():
			return "", false
		}
	}
}
