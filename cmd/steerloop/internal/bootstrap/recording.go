package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mittalkanishk/steerloop/internal/eventlog"
	"github.com/mittalkanishk/steerloop/loop/event"
)

// defaultRecordDir is where runs are recorded unless --output-record-path
// says otherwise. A relative folder sits under the repo root.
const defaultRecordDir = "run-recordings"

// Recording is the JSONL file a run, or every turn of a chat, writes each
// event to, for `steerloop replay`. It is an EventSink.
type Recording struct {
	file  *os.File
	jsonl *eventlog.JSONL
}

// OpenRecording creates the file for path:
//   - a path ending in .jsonl is that file, created or truncated;
//   - anything else is a folder, created if needed, that gets one new
//     <timestamp>-<random>.jsonl file per run. A relative folder sits under
//     the repo root.
//
// Files in a folder are created readable by the owner only: a recording
// holds prompts and tool output.
func OpenRecording(path string, now time.Time) (*Recording, error) {
	file, err := createRecordFile(path, now)
	if err != nil {
		return nil, fmt.Errorf("record: %w", err)
	}
	return &Recording{
		file:  file,
		jsonl: eventlog.NewJSONL(file),
	}, nil
}

func (r *Recording) Write(evt event.Event) {
	r.jsonl.Write(evt)
}

// Path is where the recording is, to pass to `steerloop replay --path`.
func (r *Recording) Path() string {
	return r.file.Name()
}

func (r *Recording) Close() error {
	return r.file.Close()
}

func createRecordFile(path string, now time.Time) (*os.File, error) {
	if strings.HasSuffix(path, ".jsonl") {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		return os.Create(path)
	}

	dir := path
	if !filepath.IsAbs(dir) {
		root, err := repoRoot()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(root, dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	// CreateTemp adds a random suffix, so two runs in the same second never
	// write to the same file.
	return os.CreateTemp(dir, now.UTC().Format("20060102T150405Z")+"-*.jsonl")
}

// repoRoot is the nearest directory, from the current one up, that holds
// .git (a folder, or a file in a worktree). Outside a repo it is the
// current directory.
func repoRoot() (string, error) {
	start, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for dir := start; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		if dir == filepath.Dir(dir) {
			return start, nil
		}
	}
}
