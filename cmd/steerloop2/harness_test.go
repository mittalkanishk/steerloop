package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

var update = flag.Bool("update", false, "rewrite golden files")

const (
	testdata = "testdata/"
	toolsYML = testdata + "tools.yaml"
)

// CLISuite runs steerloop2 the way a shell would: with its own stdin,
// environment and terminal, reading back what it printed and how it exited.
// Every test starts with nothing piped in, no terminal and no variables, so
// a test states only what is particular to its own scenario.
type CLISuite struct {
	suite.Suite
	stdin    io.Reader   // what the command reads; nil means nothing is piped in
	stdout   *syncBuffer // what the current run prints; readable while it runs
	stderr   *syncBuffer
	terminal bool              // stdin reports as a terminal, so the command prompts on it
	vars     map[string]string // the command's environment
}

func TestCLISuite(t *testing.T) {
	suite.Run(t, new(CLISuite))
}

func (s *CLISuite) SetupTest() { s.reset() }

func (s *CLISuite) SetupSubTest() { s.reset() }

func (s *CLISuite) reset() {
	s.stdin = nil
	s.terminal = false
	s.vars = map[string]string{}
	s.stdout, s.stderr = &syncBuffer{}, &syncBuffer{}
}

// testEnv is the cli.Environment a test run sees.
type testEnv struct {
	stdin    io.Reader
	stdout   io.Writer
	stderr   io.Writer
	terminal bool
	vars     map[string]string
}

func (e testEnv) Getenv(key string) string { return e.vars[key] }
func (e testEnv) Stdout() io.Writer        { return e.stdout }
func (e testEnv) Stderr() io.Writer        { return e.stderr }
func (e testEnv) StdinIsTerminal() bool    { return e.terminal }

func (e testEnv) Stdin() io.Reader {
	if e.stdin == nil {
		return strings.NewReader("")
	}
	return e.stdin
}

// result is what one run of the command left behind.
type result struct {
	code           int
	stdout, stderr string
}

// run invokes the command and waits for it to exit.
func (s *CLISuite) run(args ...string) result {
	return <-s.start(args...)
}

// start invokes the command in the background, into fresh output buffers the
// test can read while it runs. The result arrives on the channel when the
// command exits.
//
// `run` records to <repo>/run-recordings by default. A test run must not
// write there, so recording is turned off unless the test sets its own path.
func (s *CLISuite) start(args ...string) <-chan result {
	if len(args) > 0 && args[0] == "run" && !hasFlag(args, "output-record-path") {
		args = append([]string{"run", "--output-record-path="}, args[1:]...)
	}

	stdout, stderr := &syncBuffer{}, &syncBuffer{}
	s.stdout, s.stderr = stdout, stderr
	env := testEnv{stdin: s.stdin, stdout: stdout, stderr: stderr, terminal: s.terminal, vars: s.vars}

	done := make(chan result, 1)
	go func() {
		code := newApp(env).Run(context.Background(), args)
		done <- result{code: code, stdout: stdout.String(), stderr: stderr.String()}
	}()
	return done
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == "--"+name || strings.HasPrefix(a, "--"+name+"=") {
			return true
		}
	}
	return false
}

// typing runs the command as if a person were at the terminal: each reply is
// written to stdin once the given text has appeared on stderr. Closing stdin
// afterwards ends the terminal's steering.
func (s *CLISuite) typing(args []string, exchanges ...[2]string) result {
	s.T().Helper()
	pr, pw := io.Pipe()
	s.stdin, s.terminal = pr, true
	done := s.start(args...)

	for _, ex := range exchanges {
		waitFor, reply := ex[0], ex[1]
		s.waitForStderr(waitFor)
		_, err := pw.Write([]byte(reply + "\n"))
		s.Require().NoError(err)
	}
	pw.Close()

	select {
	case res := <-done:
		return res
	case <-time.After(15 * time.Second):
		s.Require().FailNowf("run did not finish", "stderr:\n%s", s.stderr.String())
		return result{}
	}
}

// waitForStderr blocks until the running command has printed text.
func (s *CLISuite) waitForStderr(text string) {
	s.T().Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(s.stderr.String(), text) {
		s.Require().Truef(time.Now().Before(deadline), "never saw %q in:\n%s", text, s.stderr.String())
		time.Sleep(5 * time.Millisecond)
	}
}

// script names a scripted model from testdata.
func script(name string) string { return "script:" + testdata + name }

// writeFile puts content in a file of the test's temp dir and returns its path.
func (s *CLISuite) writeFile(name, content string) string {
	s.T().Helper()
	path := filepath.Join(s.T().TempDir(), name)
	s.Require().NoError(os.WriteFile(path, []byte(content), 0o600))
	return path
}

var (
	timestampRE = regexp.MustCompile(`"timestamp":"[^"]+"`)

	// A tool result omits duration_ms when the tool finishes within a
	// millisecond, so the field is dropped rather than zeroed.
	durationRE = regexp.MustCompile(`,"duration_ms":\d+`)

	recordedRE = regexp.MustCompile(`(?m)^recorded to .*\n`)
)

// normalise strips what differs between two runs of the same script.
func normalise(jsonl string) string {
	jsonl = timestampRE.ReplaceAllString(jsonl, `"timestamp":"T"`)
	return durationRE.ReplaceAllString(jsonl, "")
}

// checkGolden compares got with testdata/golden/name, or rewrites the file
// under -update.
func (s *CLISuite) checkGolden(name, got string) {
	s.T().Helper()
	path := filepath.Join(testdata, "golden", name)
	if *update {
		s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o755))
		s.Require().NoError(os.WriteFile(path, []byte(got), 0o644))
		return
	}
	want, err := os.ReadFile(path)
	s.Require().NoError(err, "run the tests with -update to create golden files")
	s.Equal(string(want), got)
}

// event is one JSONL line of a run, as the tests read it back.
type event struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func (s *CLISuite) parseEvents(jsonl string) []event {
	s.T().Helper()
	var out []event
	for _, line := range strings.Split(strings.TrimSpace(jsonl), "\n") {
		var e event
		s.Require().NoError(json.Unmarshal([]byte(line), &e), line)
		out = append(out, e)
	}
	return out
}

func eventsOfType(events []event, typ string) []event {
	var out []event
	for _, e := range events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func (s *CLISuite) doneReason(events []event) string {
	s.T().Helper()
	dones := eventsOfType(events, "done")
	s.Require().Len(dones, 1)
	var d struct {
		Reason string `json:"reason"`
	}
	s.Require().NoError(json.Unmarshal(dones[0].Data, &d))
	return d.Reason
}

// requestOf is the messages an llm_turn sent to the model, as JSON. The
// event holds them as bytes, which JSON writes as base64.
func (s *CLISuite) requestOf(turn event) string {
	s.T().Helper()
	var d struct {
		RequestMessagesJSON []byte `json:"request_messages_json"`
	}
	s.Require().NoError(json.Unmarshal(turn.Data, &d))
	return string(d.RequestMessagesJSON)
}

// syncBuffer is a bytes.Buffer safe to write from the command while the test
// reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
