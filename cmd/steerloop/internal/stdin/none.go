package stdin

import "context"

// None is used when nobody is at a terminal: nothing reads stdin.
type None struct{}

func (None) Read(context.Context) {}
