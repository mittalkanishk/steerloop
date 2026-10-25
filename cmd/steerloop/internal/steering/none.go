package steering

import (
	"context"

	"github.com/mittalkanishk/steerloop/loop"
)

// None is used when nobody is at a terminal.
type None struct{}

func (None) Steer(context.Context, *loop.Loop) {}
