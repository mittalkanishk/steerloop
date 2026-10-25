package runcmd

import (
	"fmt"
	"os"
)

// openControlFile opens --control-file for steering.Control.
//
// A FIFO is opened read-write. Read-only would block until a writer
// connects, and would see end of input as soon as the first writer closed:
// `echo '{"type":"inject",...}' > ctl` would work once, and the next echo
// would hang. Holding a write end of its own keeps the FIFO open for the
// whole run; run closes it when the loop ends.
func openControlFile(path string) (*os.File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("control file: %w", err)
	}
	flag := os.O_RDONLY
	if info.Mode()&os.ModeNamedPipe != 0 {
		flag = os.O_RDWR
	}
	f, err := os.OpenFile(path, flag, 0)
	if err != nil {
		return nil, fmt.Errorf("control file: %w", err)
	}
	return f, nil
}
