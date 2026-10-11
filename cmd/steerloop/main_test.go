package main

import (
	"bytes"
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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite golden files")

const (
	testdata = "../../testdata/"
	toolsYML = testdata + "tools.yaml"
)

// syncBuffer is a bytes.Buffer that can be read while another goroutine writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

type result struct {
	code           int
	stdout, stderr string
}

type runner struct {
	stdin      io.Reader
	isTerminal bool
	vars       map[string]string
	stdout     *syncBuffer
	stderr     *syncBuffer
}

func newRunner() *runner {
	return &runner{stdout: &syncBuffer{}, stderr: &syncBuffer{}}
}

func (r *runner) run(args ...string) result {
	e := env{
		stdin:           r.stdin,
		stdout:          r.stdout,
		stderr:          r.stderr,
		stdinIsTerminal: r.isTerminal,
		getenv:          func(k string) string { return r.vars[k] },
	}
	code := dispatch(e, args)
	return result{code: code, stdout: r.stdout.String(), stderr: r.stderr.String()}
}

func cli(args ...string) result { return newRunner().run(args...) }

func script(name string) string { return "script:" + testdata + name }

var (
	timestampRE = regexp.MustCompile(`"timestamp":"[^"]+"`)
	durationRE  = regexp.MustCompile(`"duration_ms":\d+`)
)

func normalise(jsonl string) string {
	jsonl = timestampRE.ReplaceAllString(jsonl, `"timestamp":"T"`)
	return durationRE.ReplaceAllString(jsonl, `"duration_ms":0`)
}

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join(testdata, "golden", name)
	if *update {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "run the tests with -update to create golden files")
	assert.Equal(t, string(want), got)
}

type event struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func parseEvents(t *testing.T, jsonl string) []event {
	t.Helper()
	var out []event
	for _, line := range strings.Split(strings.TrimSpace(jsonl), "\n") {
		var e event
		require.NoError(t, json.Unmarshal([]byte(line), &e), line)
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

func doneReason(t *testing.T, events []event) string {
	t.Helper()
	dones := eventsOfType(events, "done")
	require.Len(t, dones, 1)
	var d struct {
		Reason string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(dones[0].Data, &d))
	return d.Reason
}

// C1: recorded output of each scripted run matches its golden file.
func TestRun_GoldenJSONL(t *testing.T) {
	cases := map[string][]string{
		"tool_then_answer": {"--model", script("tool_then_answer.json"), "--tools", toolsYML, "hi"},
		"finish_tool":      {"--model", script("finish_tool.json"), "--tools", toolsYML, "--stop", "finish-tool", "do the task"},
		"model_error":      {"--model", script("model_error.json"), "hi"},
		"max_iterations":   {"--model", script("never_ends.json"), "--tools", toolsYML, "--max-iterations", "2", "loop"},
		"ask_unanswered":   {"--model", script("ask_user.json"), "--ask", "pick"},
		"confirm_no_one":   {"--model", script("confirm.json"), "--tools", toolsYML, "clean up"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			res := cli(append([]string{"run", "--output", "jsonl"}, args...)...)
			checkGolden(t, name+".jsonl", normalise(res.stdout))
		})
	}
}

// C2: each outcome has its own exit code.
func TestRun_ExitCodes(t *testing.T) {
	stop := filepath.Join(t.TempDir(), "stop.jsonl")
	require.NoError(t, os.WriteFile(stop, []byte(`{"type":"stop","wait_ms":100}`+"\n"), 0o600))

	cases := []struct {
		name string
		want int
		args []string
	}{
		{"finished", exitOK, []string{"--model", script("tool_then_answer.json"), "--tools", toolsYML, "hi"}},
		{"model error", exitError, []string{"--model", script("model_error.json"), "hi"}},
		{"max iterations", exitMaxIterations, []string{"--model", script("never_ends.json"), "--tools", toolsYML, "--max-iterations", "2", "x"}},
		{"timeout", exitTimeout, []string{"--model", script("nap.json"), "--tools", toolsYML, "--total-timeout", "200ms", "x"}},
		{"awaiting human", exitAwaitingHuman, []string{"--model", script("ask_user.json"), "--ask", "x"}},
		{"stopped", exitStopped, []string{"--model", script("slow_tools.json"), "--tools", toolsYML, "--control", stop, "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := cli(append([]string{"run"}, tc.args...)...)
			assert.Equal(t, tc.want, res.code, res.stderr)
		})
	}
}

func TestRun_UsageErrors(t *testing.T) {
	cases := map[string][]string{
		"--model is required":             {"run", "hi"},
		"--stop must be":                  {"run", "--model", "script:x", "--stop", "never", "hi"},
		"--output must be":                {"run", "--model", "script:x", "--output", "xml", "hi"},
		"--max-iterations must be":        {"run", "--model", "script:x", "--max-iterations", "0", "hi"},
		"a prompt is required":            {"run", "--model", script("tool_then_answer.json")},
		"unknown provider":                {"run", "--model", "mystery:m", "hi"},
		"no such file":                    {"run", "--model", "script:/no/such.json", "hi"},
		"invalid manifest":                {"run", "--model", script("tool_then_answer.json"), "--tools", testdata + "tool_then_answer.json", "hi"},
		"read system prompt":              {"run", "--model", script("tool_then_answer.json"), "--system", "/no/such/file", "hi"},
		"open control file":               {"run", "--model", script("tool_then_answer.json"), "--control", "/no/such/file", "hi"},
		"open record file":                {"run", "--model", script("tool_then_answer.json"), "--record", "/no/such/dir/r.jsonl", "hi"},
		"flag provided but not defined":   {"run", "--nope"},
		"unknown command":                 {"fly"},
		"usage: steerloop replay":         {"replay"},
		"usage: steerloop tools validate": {"tools", "check"},
		"Usage:\n  steerloop run":         {},
		"which --ask also provides":       {"run", "--model", script("ask_user.json"), "--tools", writeManifest(t, "ask_user"), "--ask", "hi"},
		"recording is empty":              {"replay", writeFile(t, "empty.jsonl", "\n")},
		"line 1:":                         {"replay", writeFile(t, "bad.jsonl", "{not json\n")},
		`missing "type"`:                  {"replay", writeFile(t, "untyped.jsonl", "{}\n")},
		"recording has no done event":     {"replay", writeFile(t, "nodone.jsonl", `{"type":"thinking","data":{}}`+"\n")},
		"no such file or directory":       {"replay", "/no/such/recording.jsonl"},
	}
	for want, args := range cases {
		t.Run(want, func(t *testing.T) {
			res := cli(args...)
			assert.NotEqual(t, exitOK, res.code)
			assert.Contains(t, res.stderr, want)
		})
	}
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func writeManifest(t *testing.T, toolName string) string {
	return writeFile(t, "tools.yaml", "tools:\n  - name: "+toolName+"\n    description: A tool.\n    command: [\"true\"]\n")
}

func TestHelpVersionAndValidate(t *testing.T) {
	res := cli("help")
	assert.Equal(t, exitOK, res.code)
	assert.Contains(t, res.stdout, "steerloop replay")

	res = cli("run", "-h")
	assert.Equal(t, exitOK, res.code)
	assert.Contains(t, res.stderr, "-max-iterations")

	res = cli("version")
	assert.Equal(t, exitOK, res.code)
	assert.Contains(t, res.stdout, "steerloop ")

	res = cli("tools", "validate", toolsYML)
	assert.Equal(t, exitOK, res.code)
	assert.Contains(t, res.stdout, "ok: 4 tool(s)")
	assert.Contains(t, res.stdout, "danger: echo (asks before running)")

	res = cli("tools", "validate", writeFile(t, "bad.yaml", "tools:\n  - name: \"no good\"\n    command: []\n"))
	assert.Equal(t, exitError, res.code)
	assert.Contains(t, res.stderr, "name must be")
	assert.Contains(t, res.stderr, "command is required")
}

func TestRun_TextOutputAndPromptFromStdin(t *testing.T) {
	r := newRunner()
	r.stdin = strings.NewReader("  hi from stdin \n")
	res := r.run("run", "--model", script("tool_then_answer.json"), "--tools", toolsYML,
		"--system", writeFile(t, "system.txt", "Be brief."))
	require.Equal(t, exitOK, res.code, res.stderr)
	assert.Equal(t, "The tool printed: hello from the tool\n", res.stdout, "only the final content goes to stdout")
	assert.Contains(t, res.stderr, "→ say")
	assert.Contains(t, res.stderr, "← say: hello from the tool")
	assert.Contains(t, res.stderr, "done: finished · 2 step(s) · 310 tokens")
	assert.NotContains(t, res.stderr, "assistant:", "the final reply is not printed twice")

	r = newRunner()
	r.stdin = strings.NewReader("hi from stdin")
	res = r.run("run", "--model", script("tool_then_answer.json"), "--tools", toolsYML, "--output", "jsonl",
		"--system", writeFile(t, "system.txt", "Be brief."))
	turn := eventsOfType(parseEvents(t, res.stdout), "llm_turn")[0]
	assert.Contains(t, string(turn.Data), `{"role":"system","content":"Be brief."},{"role":"user","content":"hi from stdin"}`)
}

func TestRun_FinishToolTextShowsNarration(t *testing.T) {
	res := cli("run", "--model", script("finish_tool.json"), "--tools", toolsYML, "--stop", "finish-tool", "--verbose", "go")
	require.Equal(t, exitOK, res.code, res.stderr)
	assert.Equal(t, `{"result":"all done"}`+"\n", res.stdout)
	assert.Contains(t, res.stderr, "assistant: Let me think about that first.")
	assert.Contains(t, res.stderr, "level=DEBUG", "--verbose writes the loop's debug log")
}

// C3: controls from a file steer the run.
func TestRun_ControlFileSteersTheRun(t *testing.T) {
	control := writeFile(t, "control.jsonl", strings.Join([]string{
		`{"type":"inject","message":"use metric units","wait_ms":100}`,
		`{"type":"modify_config","temperature":0.1}`,
		`{"type":"launch"}`,
		`not json`,
		``,
	}, "\n"))
	res := cli("run", "--model", script("slow_tools.json"), "--tools", toolsYML, "--control", control, "--output", "jsonl", "go")
	require.Equal(t, exitOK, res.code, res.stderr)

	turns := eventsOfType(parseEvents(t, res.stdout), "llm_turn")
	require.Len(t, turns, 2)
	assert.NotContains(t, string(turns[0].Data), "metric units", "the first call was already in flight")
	assert.Contains(t, string(turns[1].Data), `{"role":"user","content":"use metric units"}`)
	assert.Contains(t, res.stderr, `control line 3 ignored: unknown type "launch"`)
	assert.Contains(t, res.stderr, "control line 4 ignored")

	raise := writeFile(t, "raise.jsonl", `{"type":"modify_config","max_iterations":1,"wait_ms":100}`+"\n")
	res = cli("run", "--model", script("slow_tools.json"), "--tools", toolsYML, "--control", raise, "--output", "jsonl", "go")
	assert.Equal(t, exitMaxIterations, res.code)
	assert.Equal(t, "max_iterations", doneReason(t, parseEvents(t, res.stdout)))
}

// terminal runs the CLI as if a person were typing: each reply is written to
// stdin once the given text has appeared on stderr.
func terminal(t *testing.T, args []string, exchanges ...[2]string) result {
	t.Helper()
	pr, pw := io.Pipe()
	r := newRunner()
	r.stdin, r.isTerminal = pr, true

	done := make(chan result, 1)
	go func() { done <- r.run(args...) }()

	for _, ex := range exchanges {
		waitFor, reply := ex[0], ex[1]
		deadline := time.Now().Add(10 * time.Second)
		for !strings.Contains(r.stderr.String(), waitFor) {
			require.True(t, time.Now().Before(deadline), "never saw %q in:\n%s", waitFor, r.stderr.String())
			time.Sleep(5 * time.Millisecond)
		}
		_, err := pw.Write([]byte(reply + "\n"))
		require.NoError(t, err)
	}
	select {
	case res := <-done:
		pw.Close()
		return res
	case <-time.After(15 * time.Second):
		t.Fatalf("run did not finish; stderr:\n%s", r.stderr.String())
		return result{}
	}
}

// C5: ask_user and confirm prompts are answered from the terminal.
func TestRun_AskUserOnTerminal(t *testing.T) {
	res := terminal(t, []string{"run", "--model", script("ask_user.json"), "--ask", "pick"},
		[2]string{"? Which colour? [red / blue]", "blue"})
	require.Equal(t, exitOK, res.code, res.stderr)
	assert.Equal(t, "You chose blue.\n", res.stdout)
	assert.Contains(t, res.stderr, "← ask_user: blue")
}

func TestRun_ConfirmOnTerminal(t *testing.T) {
	args := []string{"run", "--model", script("confirm.json"), "--tools", toolsYML, "clean"}

	res := terminal(t, args, [2]string{`? Run danger {"target":"build/"}? [y / n]`, "y"})
	require.Equal(t, exitOK, res.code, res.stderr)
	assert.Contains(t, res.stderr, "← danger: removed build/")

	res = terminal(t, args, [2]string{"? Run danger", "no thanks"})
	require.Equal(t, exitOK, res.code, res.stderr)
	assert.Contains(t, res.stderr, "← danger: The user declined to run this tool.")
	assert.NotContains(t, res.stderr, "removed build/")

	res = cli(append([]string{"run", "--yes", "--model", script("confirm.json"), "--tools", toolsYML}, "clean")...)
	assert.Contains(t, res.stderr, "← danger: removed build/", "--yes approves without asking")

	res = cli("run", "--model", script("confirm.json"), "--tools", toolsYML, "clean")
	assert.Contains(t, res.stderr, "nobody is available", "with no terminal the tool is not run")
	assert.NotContains(t, res.stderr, "removed build/")
}

func TestRun_SlashCommandsOnTerminal(t *testing.T) {
	res := terminal(t, []string{"run", "--model", script("ask_user.json"), "--ask", "pick"},
		[2]string{"? Which colour?", "/bogus"},
		[2]string{"unknown command /bogus", "/max nope"},
		[2]string{"usage: /max", "/temp x"},
		[2]string{"usage: /temp", "/reflect maybe"},
		[2]string{"usage: /reflect", "/max 9"},
		[2]string{"usage: /reflect", "/temp 0.4"},
		[2]string{"usage: /reflect", "/reflect off"},
		[2]string{"usage: /reflect", ""},
		[2]string{"usage: /reflect", "/stop"},
	)
	assert.Equal(t, exitStopped, res.code, res.stderr)
	assert.Contains(t, res.stderr, "done: stopped")
}

// C6: a replay prints what the live run printed, without a model or a tool.
func TestReplay_MatchesLiveRun(t *testing.T) {
	for _, tc := range [][]string{
		{"--model", script("tool_then_answer.json"), "--tools", toolsYML, "hi"},
		{"--model", script("finish_tool.json"), "--tools", toolsYML, "--stop", "finish-tool", "go"},
		{"--model", script("never_ends.json"), "--tools", toolsYML, "--max-iterations", "2", "x"},
		{"--model", script("ask_user.json"), "--ask", "x"},
		{"--model", script("model_error.json"), "x"},
	} {
		recording := filepath.Join(t.TempDir(), "run.jsonl")
		live := cli(append([]string{"run", "--record", recording}, tc...)...)

		// Replay from a directory where neither the script nor the manifest
		// can be found, to show nothing is re-run.
		abs, err := filepath.Abs(recording)
		require.NoError(t, err)
		replay := cli("replay", abs)

		assert.Equal(t, live.stdout, replay.stdout)
		assert.Equal(t, live.stderr, replay.stderr)
		assert.Equal(t, live.code, replay.code)
	}
}

// C7: a missing key fails before any call, and a key never reaches the output.
func TestRun_APIKeys(t *testing.T) {
	res := cli("run", "--model", "anthropic:some-model", "hi")
	assert.Equal(t, exitUsage, res.code)
	assert.Contains(t, res.stderr, "ANTHROPIC_API_KEY is not set")
	res = cli("run", "--model", "openai:some-model", "hi")
	assert.Contains(t, res.stderr, "OPENAI_API_KEY is not set")

	const secret = "sk-test-secret-value-123"
	recording := filepath.Join(t.TempDir(), "run.jsonl")
	r := newRunner()
	r.vars = map[string]string{"ANTHROPIC_API_KEY": secret, "OPENAI_API_KEY": secret}
	res = r.run("run", "--verbose", "--record", recording, "--model", script("tool_then_answer.json"), "--tools", toolsYML, "hi")
	require.Equal(t, exitOK, res.code)
	recorded, err := os.ReadFile(recording)
	require.NoError(t, err)
	for _, out := range []string{res.stdout, res.stderr, string(recorded)} {
		assert.NotContains(t, out, secret)
	}
}

// C8: one real model call. Runs only when STEERLOOP_LIVE=1 and a key is set.
func TestRun_LiveSmoke(t *testing.T) {
	if os.Getenv("STEERLOOP_LIVE") != "1" {
		t.Skip("set STEERLOOP_LIVE=1 and STEERLOOP_LIVE_MODEL (for example anthropic:<model>) to run")
	}
	modelSpec := os.Getenv("STEERLOOP_LIVE_MODEL")
	require.NotEmpty(t, modelSpec, "STEERLOOP_LIVE_MODEL is required")

	r := newRunner()
	r.vars = map[string]string{
		"ANTHROPIC_API_KEY": os.Getenv("ANTHROPIC_API_KEY"),
		"OPENAI_API_KEY":    os.Getenv("OPENAI_API_KEY"),
	}
	res := r.run("run", "--output", "jsonl", "--model", modelSpec, "--tools", toolsYML,
		`Use the say tool to print exactly "live check", then tell me what it printed.`)
	require.Equal(t, exitOK, res.code, res.stderr)
	events := parseEvents(t, res.stdout)
	assert.NotEmpty(t, eventsOfType(events, "tool_result"))
	assert.Equal(t, "finished", doneReason(t, events))
}

// An interrupt stops the run at the next step; a second one aborts it.
func TestRun_Interrupts(t *testing.T) {
	for name, count := range map[string]int{"one interrupt stops": 1, "two interrupts abort": 2} {
		t.Run(name, func(t *testing.T) {
			signals := make(chan os.Signal, 2)
			r := newRunner()
			e := env{
				stdout:  r.stdout,
				stderr:  r.stderr,
				getenv:  func(string) string { return "" },
				signals: signals,
			}
			done := make(chan int, 1)
			go func() {
				done <- dispatch(e, []string{"run", "--model", script("nap.json"), "--tools", toolsYML, "rest"})
			}()

			for !strings.Contains(r.stderr.String(), "→ nap") {
				time.Sleep(5 * time.Millisecond)
			}
			start := time.Now()
			for i := 0; i < count; i++ {
				signals <- os.Interrupt
			}
			select {
			case code := <-done:
				assert.Equal(t, exitStopped, code)
			case <-time.After(15 * time.Second):
				t.Fatal("run did not end after the interrupt")
			}
			assert.Contains(t, r.stderr.String(), "stopping after the current step")
			if count == 2 {
				assert.Less(t, time.Since(start), 3*time.Second, "an abort does not wait for the 5 second tool")
			}
		})
	}
}
