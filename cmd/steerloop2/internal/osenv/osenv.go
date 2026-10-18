// Package osenv is the real cli.Environment: the process's own env and
// streams.
package osenv

import (
	"io"
	"os"
)

// Real reads from the running process.
type Real struct{}

func (Real) Getenv(key string) string { return os.Getenv(key) }
func (Real) Stdin() io.Reader         { return os.Stdin }
func (Real) Stdout() io.Writer        { return os.Stdout }
func (Real) Stderr() io.Writer        { return os.Stderr }

func (Real) StdinIsTerminal() bool {
	stat, err := os.Stdin.Stat()
	return err == nil && stat.Mode()&os.ModeCharDevice != 0
}
