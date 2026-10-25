// Package osenv is the real cli.Environment: the process's own env and
// streams.
package osenv

import (
	"io"
	"os"

	"github.com/mattn/go-isatty"
)

// Real reads from the running process.
type Real struct{}

func (Real) Getenv(key string) string { return os.Getenv(key) }
func (Real) Stdin() io.Reader         { return os.Stdin }
func (Real) Stdout() io.Writer        { return os.Stdout }
func (Real) Stderr() io.Writer        { return os.Stderr }

// StdinIsTerminal reports whether a person can type on stdin. It asks the
// OS whether stdin is a terminal: a character-device check alone would
// count < /dev/null as one, and a run would then wait for answers no one
// can type.
func (Real) StdinIsTerminal() bool {
	fd := os.Stdin.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}
