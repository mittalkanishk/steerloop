package replaycmd

import (
	"context"
	"time"
)

type ReplayClock struct{}

func (c *ReplayClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
