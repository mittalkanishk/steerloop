package main

import (
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

var update = flag.Bool("update", false, "rewrite golden files")

const (
	testdata = "testdata/"
	toolsYML = testdata + "tools.yaml"
)

// CLISuite runs the steerloop command the way a shell would: with its own
// stdin, environment and signals, and reads back what it printed and how it
// exited. Every test starts with nothing piped in, no terminal, no variables
// and no signals, so a test states only what is particular to its own
// scenario.
type CLISuite struct {
	suite.Suite
	stdin  io.Reader   // stdin is what the command reads. Nil means nothing is piped in.
	stdout *SyncBuffer // stdout and stderr collect what the current run prints. They can be read while the run is still going.
	stderr *SyncBuffer

	terminal bool              // terminal reports stdin as a terminal, so the command prompts on it.
	vars     map[string]string // vars is the command's environment.
	signals  <-chan os.Signal  // signals is what the command receives as interrupts.
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
	s.signals = nil
	s.stdout, s.stderr = &SyncBuffer{}, &SyncBuffer{}
}

// result is what one run of the command left behind.
type result struct {
	code           int
	stdout, stderr string
}

// run invokes the command with the suite's stdin, environment and signals
// and waits for it to exit.
func (s *CLISuite) run(args ...string) result {
	return <-s.startSteerLoopCommandRun(args...)
}

// start invokes the steerloop command in the background, into fresh output buffers
// that the test can read while it runs. The result arrives on the channel
// when the command exits.
func (s *CLISuite) startSteerLoopCommandRun(args ...string) <-chan result {

	stdout, stderr := &SyncBuffer{}, &SyncBuffer{}

	s.stdout, s.stderr = stdout, stderr

	e := env{
		stdin:           s.stdin,
		stdout:          stdout,
		stderr:          stderr,
		stdinIsTerminal: s.terminal,
		getenv:          func(k string) string { return s.vars[k] },
		signals:         s.signals,
	}

	done := make(chan result, 1)

	go func() {
		code := dispatch(e, args)
		done <- result{ // result sends a value into the channel.
			code:   code,
			stdout: stdout.String(),
			stderr: stderr.String(),
		}
	}()

	return done
}

// typing runs the steerloop command as if a person were at the terminal: each reply is
// written to stdin once the given text has appeared on stderr. Closing stdin
// afterwards lets a session that waits for the next prompt see EOF and exit.
// This typing is what build the real pipe
func (s *CLISuite) typing(args []string, exchanges ...[2]string) result {

	s.T().Helper()
	pr, pw := io.Pipe()
	s.stdin, s.terminal = pr, true
	done := s.startSteerLoopCommandRun(args...)

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

// writeManifest writes a manifest that declares one tool.
func (s *CLISuite) writeManifest(toolName string) string {
	return s.writeFile("tools.yaml", "tools:\n  - name: "+toolName+"\n    description: A tool.\n    command: [\"true\"]\n")
}

var (
	timestampRE = regexp.MustCompile(`"timestamp":"[^"]+"`)

	// A tool result omits duration_ms when the tool finishes within a
	// millisecond, so the field is dropped rather than zeroed.
	durationRE = regexp.MustCompile(`,"duration_ms":\d+`)
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

// C1: recorded output of each scripted run matches its golden file.
func (s *CLISuite) TestRun_GoldenJSONL() {

	cases := map[string][]string{
		"tool_then_answer": {"--model", script("tool_then_answer.json"), "--tools", toolsYML, "--prompt", "hi"},
		"finish_tool":      {"--model", script("finish_tool.json"), "--tools", toolsYML, "--stop", "finish-tool", "--prompt", "do the task"},
		"model_error":      {"--model", script("model_error.json"), "--prompt", "hi"},
		"max_iterations":   {"--model", script("never_ends.json"), "--tools", toolsYML, "--max-iterations", "2", "--prompt", "loop"},
		"ask_unanswered":   {"--model", script("ask_user.json"), "--ask", "--prompt", "pick"},
		"confirm_no_one":   {"--model", script("confirm.json"), "--tools", toolsYML, "--prompt", "clean up"},
	}

	for name, args := range cases {
		s.Run(name, func() {
			res := s.run(append([]string{"run", "--output", "jsonl"}, args...)...)
			s.checkGolden(name+".jsonl", normalise(res.stdout))
		})
	}
}

// C2: each outcome has its own exit code.
func (s *CLISuite) TestRun_ExitCodes() {

	stop := s.writeFile("stop.jsonl", `{"type":"stop","wait_ms":100}`+"\n")

	cases := []struct {
		name string
		want int
		args []string
	}{
		{"finished", exitOK, []string{"--model", script("tool_then_answer.json"), "--tools", toolsYML, "--prompt", "hi"}},
		{"model error", exitError, []string{"--model", script("model_error.json"), "--prompt", "hi"}},
		{"max iterations", exitMaxIterations, []string{"--model", script("never_ends.json"), "--tools", toolsYML, "--max-iterations", "2", "--prompt", "x"}},
		{"timeout", exitTimeout, []string{"--model", script("nap.json"), "--tools", toolsYML, "--total-timeout", "200ms", "--prompt", "x"}},
		{"awaiting human", exitAwaitingHuman, []string{"--model", script("ask_user.json"), "--ask", "--prompt", "x"}},
		{"stopped", exitStopped, []string{"--model", script("slow_tools.json"), "--tools", toolsYML, "--control", stop, "--prompt", "x"}},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			res := s.run(append([]string{"run"}, tc.args...)...)
			s.Equal(tc.want, res.code, res.stderr)
		})
	}

}

func (s *CLISuite) TestRun_UsageErrors() {

	cases := map[string][]string{
		"--model is required":             {"run", "--prompt", "hi"},
		"--stop must be":                  {"run", "--model", "script:x", "--stop", "never", "--prompt", "hi"},
		"--output must be":                {"run", "--model", "script:x", "--output", "xml", "--prompt", "hi"},
		"--max-iterations must be":        {"run", "--model", "script:x", "--max-iterations", "0", "--prompt", "hi"},
		"--prompt is required":            {"run", "--model", script("tool_then_answer.json")},
		"give the prompt with --prompt":   {"run", "--model", script("tool_then_answer.json"), "--prompt", "clean up", "and more"},
		"unknown provider":                {"run", "--model", "mystery:m", "--prompt", "hi"},
		"no such file":                    {"run", "--model", "script:/no/such.json", "--prompt", "hi"},
		"invalid manifest":                {"run", "--model", script("tool_then_answer.json"), "--tools", testdata + "tool_then_answer.json", "--prompt", "hi"},
		"read system prompt":              {"run", "--model", script("tool_then_answer.json"), "--system", "/no/such/file", "--prompt", "hi"},
		"open control file":               {"run", "--model", script("tool_then_answer.json"), "--control", "/no/such/file", "--prompt", "hi"},
		"open record file":                {"run", "--model", script("tool_then_answer.json"), "--record", "/no/such/dir/r.jsonl", "--prompt", "hi"},
		"flag provided but not defined":   {"run", "--nope"},
		"unknown command":                 {"fly"},
		"usage: steerloop replay":         {"replay"},
		"usage: steerloop tools validate": {"tools", "check"},
		"Usage:\n  steerloop run":         {},
		"which --ask also provides":       {"run", "--model", script("ask_user.json"), "--tools", s.writeManifest("ask_user"), "--ask", "--prompt", "hi"},
		"recording is empty":              {"replay", s.writeFile("empty.jsonl", "\n")},
		"line 1:":                         {"replay", s.writeFile("bad.jsonl", "{not json\n")},
		`missing "type"`:                  {"replay", s.writeFile("untyped.jsonl", "{}\n")},
		"recording has no done event":     {"replay", s.writeFile("nodone.jsonl", `{"type":"thinking","data":{}}`+"\n")},
		"no such file or directory":       {"replay", "/no/such/recording.jsonl"},
	}

	for want, args := range cases {
		s.Run(want, func() {
			res := s.run(args...)
			s.NotEqual(exitOK, res.code)
			s.Contains(res.stderr, want)
		})
	}
}

func (s *CLISuite) TestHelpVersionAndToolsValidate() {
	res := s.run("help")
	s.Equal(exitOK, res.code)
	s.Contains(res.stdout, "steerloop replay")

	res = s.run("run", "-h")
	s.Equal(exitOK, res.code)
	s.Contains(res.stderr, "-max-iterations")
	s.Contains(res.stderr, "-prompt")

	res = s.run("version")
	s.Equal(exitOK, res.code)
	s.Contains(res.stdout, "steerloop ")

	res = s.run("tools", "validate", toolsYML)
	s.Equal(exitOK, res.code)
	s.Contains(res.stdout, "ok: 4 tool(s)")
	s.Contains(res.stdout, "danger: echo (asks before running)")

	res = s.run("tools", "validate", s.writeFile("bad.yaml", "tools:\n  - name: \"no good\"\n    command: []\n"))
	s.Equal(exitError, res.code)
	s.Contains(res.stderr, "name must be")
	s.Contains(res.stderr, "command is required")
}

func (s *CLISuite) TestRun_TextOutput() {
	system := s.writeFile("system.txt", "Be brief.")

	res := s.run("run", "--model", script("tool_then_answer.json"), "--tools", toolsYML, "--system", system, "--prompt", "hi")
	s.Require().Equal(exitOK, res.code, res.stderr)
	s.Equal("The tool printed: hello from the tool\n", res.stdout, "only the final content goes to stdout")
	s.Contains(res.stderr, "→ say")
	s.Contains(res.stderr, "← say: hello from the tool")
	s.Contains(res.stderr, "done: finished · 2 step(s) · 310 tokens")
	s.NotContains(res.stderr, "assistant:", "the final reply is not printed twice")

	res = s.run("run", "--model", script("tool_then_answer.json"), "--tools", toolsYML, "--output", "jsonl", "--system", system, "--prompt", "hi")
	turn := eventsOfType(s.parseEvents(res.stdout), "llm_turn")[0]
	s.Contains(string(turn.Data), `{"role":"system","content":"Be brief."},{"role":"user","content":"hi"}`)
}

// The prompt is only ever --prompt: not the words after the flags, not stdin.
func (s *CLISuite) TestRun_PromptFlag() {
	userTurn := func(res result) string {
		return string(eventsOfType(s.parseEvents(res.stdout), "llm_turn")[0].Data)
	}
	args := []string{"run", "--model", script("tool_then_answer.json"), "--tools", toolsYML, "--output", "jsonl"}

	s.Run("--prompt is the user message, trimmed", func() {
		res := s.run(append(args, "--prompt", "  clean up ")...)
		s.Require().Equal(exitOK, res.code, res.stderr)
		s.Contains(userTurn(res), `{"role":"user","content":"clean up"}`)
	})

	s.Run("piped stdin is never read as the prompt", func() {
		s.stdin = strings.NewReader("from stdin")
		res := s.run(args...)
		s.Equal(exitUsage, res.code)
		s.Contains(res.stderr, "--prompt is required")
		s.Empty(res.stdout, "nothing runs")
	})

	s.Run("a blank --prompt is missing", func() {
		res := s.run(append(args, "--prompt", "   ")...)
		s.Equal(exitUsage, res.code)
		s.Contains(res.stderr, "--prompt is required")
	})

	s.Run("words after the flags are rejected, with or without --prompt", func() {
		for _, extra := range [][]string{{"clean up"}, {"--prompt", "clean up", "the build"}} {
			res := s.run(append(args, extra...)...)
			s.Equal(exitUsage, res.code)
			s.Contains(res.stderr, "give the prompt with --prompt")
			s.Empty(res.stdout, "nothing runs")
		}
	})
}

func (s *CLISuite) TestRun_FinishToolTextShowsNarration() {
	res := s.run("run", "--model", script("finish_tool.json"), "--tools", toolsYML, "--stop", "finish-tool", "--verbose", "--prompt", "go")
	s.Require().Equal(exitOK, res.code, res.stderr)
	s.Equal(`{"result":"all done"}`+"\n", res.stdout)
	s.Contains(res.stderr, "assistant: Let me think about that first.")
	s.Contains(res.stderr, "level=DEBUG", "--verbose writes the loop's debug log")
}

// C3: controls from a file steer the run.
func (s *CLISuite) TestRun_ControlFileSteersTheRun() {
	control := s.writeFile("control.jsonl", strings.Join([]string{
		`{"type":"inject","message":"use metric units","wait_ms":100}`,
		`{"type":"modify_config","temperature":0.1}`,
		`{"type":"launch"}`,
		`not json`,
		``,
	}, "\n"))
	res := s.run("run", "--model", script("slow_tools.json"), "--tools", toolsYML, "--control", control, "--output", "jsonl", "--prompt", "go")
	s.Require().Equal(exitOK, res.code, res.stderr)

	turns := eventsOfType(s.parseEvents(res.stdout), "llm_turn")
	s.Require().Len(turns, 2)
	s.NotContains(string(turns[0].Data), "metric units", "the first call was already in flight")
	s.Contains(string(turns[1].Data), `{"role":"user","content":"use metric units"}`)
	s.Contains(res.stderr, `control line 3 ignored: unknown type "launch"`)
	s.Contains(res.stderr, "control line 4 ignored")

	raise := s.writeFile("raise.jsonl", `{"type":"modify_config","max_iterations":1,"wait_ms":100}`+"\n")
	res = s.run("run", "--model", script("slow_tools.json"), "--tools", toolsYML, "--control", raise, "--output", "jsonl", "--prompt", "go")
	s.Equal(exitMaxIterations, res.code)
	s.Equal("max_iterations", s.doneReason(s.parseEvents(res.stdout)))
}

// C5: ask_user and confirm prompts are answered from the terminal.
func (s *CLISuite) TestRun_AskUserOnTerminal() {
	res := s.typing([]string{"run", "--model", script("ask_user.json"), "--ask", "--prompt", "pick"},
		[2]string{"? Which colour? [red / blue]", "blue"})
	s.Require().Equal(exitOK, res.code, res.stderr)
	s.Equal("You chose blue.\n", res.stdout)
	s.Contains(res.stderr, "← ask_user: blue")
}

func (s *CLISuite) TestRun_ConfirmOnTerminal() {
	args := []string{"run", "--model", script("confirm.json"), "--tools", toolsYML, "--prompt", "clean"}

	res := s.typing(args, [2]string{`? Run danger {"target":"build/"}? [y / n]`, "y"})
	s.Require().Equal(exitOK, res.code, res.stderr)
	s.Contains(res.stderr, "← danger: removed build/")

	res = s.typing(args, [2]string{"? Run danger", "no thanks"})
	s.Require().Equal(exitOK, res.code, res.stderr)
	s.Contains(res.stderr, "← danger: The user declined to run this tool.")
	s.NotContains(res.stderr, "removed build/")

	s.reset()
	res = s.run("run", "--yes", "--model", script("confirm.json"), "--tools", toolsYML, "--prompt", "clean")
	s.Contains(res.stderr, "← danger: removed build/", "--yes approves without asking")

	res = s.run(args...)
	s.Contains(res.stderr, "nobody is available", "with no terminal the tool is not run")
	s.NotContains(res.stderr, "removed build/")
}

func (s *CLISuite) TestRun_SlashCommandsOnTerminal() {
	res := s.typing([]string{"run", "--model", script("ask_user.json"), "--ask", "--prompt", "pick"},
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
	s.Equal(exitStopped, res.code, res.stderr)
	s.Contains(res.stderr, "done: stopped")
}

// A finished answer on a terminal waits for the next prompt and keeps the history.
func (s *CLISuite) TestRun_SessionReadsTheNextPrompt() {
	scriptPath := s.writeFile("two.json", `{"steps":[{"content":"four"},{"content":"six"}]}`+"\n")
	res := s.typing([]string{"run", "--model", "script:" + scriptPath, "--prompt", "2+2"},
		[2]string{"> ", "3+3"},
	)
	s.Require().Equal(exitOK, res.code, res.stderr)
	s.Equal("four\nsix\n", res.stdout)
	s.Contains(res.stderr, "done: finished · 1 step(s)")
}

// C6: a replay prints what the live run printed, without a model or a tool.
func (s *CLISuite) TestReplay_MatchesLiveRun() {
	for _, tc := range [][]string{
		{"--model", script("tool_then_answer.json"), "--tools", toolsYML, "--prompt", "hi"},
		{"--model", script("finish_tool.json"), "--tools", toolsYML, "--stop", "finish-tool", "--prompt", "go"},
		{"--model", script("never_ends.json"), "--tools", toolsYML, "--max-iterations", "2", "--prompt", "x"},
		{"--model", script("ask_user.json"), "--ask", "--prompt", "x"},
		{"--model", script("model_error.json"), "--prompt", "x"},
	} {
		recording := filepath.Join(s.T().TempDir(), "run.jsonl")
		live := s.run(append([]string{"run", "--record", recording}, tc...)...)

		// Replay from a directory where neither the script nor the manifest
		// can be found, to show nothing is re-run.
		abs, err := filepath.Abs(recording)
		s.Require().NoError(err)
		replay := s.run("replay", abs)

		s.Equal(live.stdout, replay.stdout)
		s.Equal(live.stderr, replay.stderr)
		s.Equal(live.code, replay.code)
	}
}

// C7: a missing key fails before any call, and a key never reaches the output.
func (s *CLISuite) TestRun_APIKeys() {
	res := s.run("run", "--model", "anthropic:some-model", "--prompt", "hi")
	s.Equal(exitUsage, res.code)
	s.Contains(res.stderr, "ANTHROPIC_API_KEY is not set")
	res = s.run("run", "--model", "openai:some-model", "--prompt", "hi")
	s.Contains(res.stderr, "OPENAI_API_KEY is not set")

	const secret = "sk-test-secret-value-123"
	recording := filepath.Join(s.T().TempDir(), "run.jsonl")
	s.vars = map[string]string{"ANTHROPIC_API_KEY": secret, "OPENAI_API_KEY": secret}
	res = s.run("run", "--verbose", "--record", recording, "--model", script("tool_then_answer.json"), "--tools", toolsYML, "--prompt", "hi")
	s.Require().Equal(exitOK, res.code)
	recorded, err := os.ReadFile(recording)
	s.Require().NoError(err)
	for _, out := range []string{res.stdout, res.stderr, string(recorded)} {
		s.NotContains(out, secret)
	}
}

// C8: one real model call. Runs only when STEERLOOP_LIVE=1 and a key is set.
func (s *CLISuite) TestRun_LiveSmoke() {
	if os.Getenv("STEERLOOP_LIVE") != "1" {
		s.T().Skip("set STEERLOOP_LIVE=1 and STEERLOOP_LIVE_MODEL (for example anthropic:<model>) to run")
	}
	modelSpec := os.Getenv("STEERLOOP_LIVE_MODEL")
	s.Require().NotEmpty(modelSpec, "STEERLOOP_LIVE_MODEL is required")

	s.vars = map[string]string{
		"ANTHROPIC_API_KEY": os.Getenv("ANTHROPIC_API_KEY"),
		"OPENAI_API_KEY":    os.Getenv("OPENAI_API_KEY"),
	}
	res := s.run("run", "--output", "jsonl", "--model", modelSpec, "--tools", toolsYML, "--prompt",
		`Use the say tool to print exactly "live check", then tell me what it printed.`)
	s.Require().Equal(exitOK, res.code, res.stderr)
	events := s.parseEvents(res.stdout)
	s.NotEmpty(eventsOfType(events, "tool_result"))
	s.Equal("finished", s.doneReason(events))
}

// An interrupt stops the run at the next step; a second one aborts it.
func (s *CLISuite) TestRun_Interrupts() {
	for name, count := range map[string]int{"one interrupt stops": 1, "two interrupts abort": 2} {
		s.Run(name, func() {
			signals := make(chan os.Signal, 2)
			s.signals = signals
			done := s.startSteerLoopCommandRun("run", "--model", script("nap.json"), "--tools", toolsYML, "--prompt", "rest")

			s.waitForStderr("→ nap")
			start := time.Now()
			for i := 0; i < count; i++ {
				signals <- os.Interrupt
			}
			select {
			case res := <-done:
				s.Equal(exitStopped, res.code)
				s.Contains(res.stderr, "stopping after the current step")
			case <-time.After(15 * time.Second):
				s.Require().FailNow("run did not end after the interrupt")
			}
			if count == 2 {
				s.Less(time.Since(start), 3*time.Second, "an abort does not wait for the 5 second tool")
			}
		})
	}
}
