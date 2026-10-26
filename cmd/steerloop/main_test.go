package main

import (
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mittalkanishk/steerloop/cmd/steerloop/internal/cli"
)

// C1: the JSONL output of each scripted run matches its golden file.
func (s *CLISuite) TestRun_GoldenJSONL() {
	cases := map[string][]string{
		"tool_then_answer": {"--model", script("tool_then_answer.json"), "--tools", toolsYML, "--user-prompt", "hi"},
		"finish_tool":      {"--model", script("finish_tool.json"), "--tools", toolsYML, "--stop-condition", "finish-tool", "--user-prompt", "do the task"},
		"model_error":      {"--model", script("model_error.json"), "--user-prompt", "hi"},
		"max_iterations":   {"--model", script("never_ends.json"), "--tools", toolsYML, "--max-iterations", "2", "--user-prompt", "loop"},
		"ask_unanswered":   {"--model", script("ask_user.json"), "--ask-user", "--user-prompt", "pick"},
		"confirm_no_one":   {"--model", script("confirm.json"), "--tools", toolsYML, "--user-prompt", "clean up"},
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
		{"finished", cli.ExitOK, []string{"--model", script("tool_then_answer.json"), "--tools", toolsYML, "--user-prompt", "hi"}},
		{"model error", cli.ExitError, []string{"--model", script("model_error.json"), "--user-prompt", "hi"}},
		{"max iterations", cli.ExitMaxIterations, []string{"--model", script("never_ends.json"), "--tools", toolsYML, "--max-iterations", "2", "--user-prompt", "x"}},
		{"timeout", cli.ExitTimeout, []string{"--model", script("nap.json"), "--tools", toolsYML, "--iteration-timeout", "100ms", "--total-timeout", "200ms", "--user-prompt", "x"}},
		{"awaiting human", cli.ExitAwaitingHuman, []string{"--model", script("ask_user.json"), "--ask-user", "--user-prompt", "x"}},
		{"stopped", cli.ExitStopped, []string{"--model", script("slow_tools.json"), "--tools", toolsYML, "--control-file", stop, "--user-prompt", "x"}},
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
		"--model is required":               {"run", "--user-prompt", "hi"},
		"--reflect must be":                 {"run", "--model", "script:x", "--reflect", "always", "--user-prompt", "hi"},
		"--output must be":                  {"run", "--model", "script:x", "--output", "xml", "--user-prompt", "hi"},
		"--max-iterations must be":          {"run", "--model", "script:x", "--max-iterations", "0", "--user-prompt", "hi"},
		"--user-prompt is required":         {"run", "--model", script("tool_then_answer.json")},
		"unexpected argument":               {"run", "--model", script("tool_then_answer.json"), "--user-prompt", "clean up", "and more"},
		"unknown provider":                  {"run", "--model", "mystery:m", "--user-prompt", "hi"},
		"no such file":                      {"run", "--model", "script:/no/such.json", "--user-prompt", "hi"},
		"invalid manifest":                  {"run", "--model", script("tool_then_answer.json"), "--tools", testdata + "tool_then_answer.json", "--user-prompt", "hi"},
		"read system prompt":                {"run", "--model", script("tool_then_answer.json"), "--system-prompt", "/no/such/file", "--user-prompt", "hi"},
		"control file":                      {"run", "--model", script("tool_then_answer.json"), "--control-file", "/no/such/file", "--user-prompt", "hi"},
		"record:":                           {"run", "--model", script("tool_then_answer.json"), "--output-record-path", "/no/such/dir/r.jsonl", "--user-prompt", "hi"},
		"flag provided but not defined":     {"run", "--nope"},
		"unknown command":                   {"fly"},
		"--path is required":                {"replay"},
		`tools: unknown subcommand "check"`: {"tools", "check"},
		"Usage:\n  steerloop run":           {},
		"recording is empty":                {"replay", "--path", s.writeFile("empty.jsonl", "\n")},
		"line 1:":                           {"replay", "--path", s.writeFile("bad.jsonl", "{not json\n")},
		`missing "type"`:                    {"replay", "--path", s.writeFile("untyped.jsonl", "{}\n")},
		"recording has no done event":       {"replay", "--path", s.writeFile("nodone.jsonl", `{"type":"thinking","data":{}}`+"\n")},
		"no such file or directory":         {"replay", "--path", "/no/such/recording.jsonl"},
		"--max-wait has no effect":          {"replay", "--path", "x.jsonl", "--speed", "0", "--max-wait", "1s"},
	}
	cases[`--stop-condition must be no-tool-finish, finish-tool or external-finish, not "never"`] =
		[]string{"run", "--model", "script:x", "--stop-condition", "never", "--user-prompt", "hi"}
	for want, args := range cases {
		s.Run(want, func() {
			res := s.run(args...)
			s.NotEqual(cli.ExitOK, res.code)
			s.Contains(res.stderr, want)
		})
	}
}

func (s *CLISuite) TestHelpVersionAndToolsValidate() {
	res := s.run("help")
	s.Equal(cli.ExitOK, res.code)
	s.Contains(res.stdout, "steerloop replay")

	res = s.run("run", "-h")
	s.Equal(cli.ExitOK, res.code)
	s.Contains(res.stderr, "-max-iterations")
	s.Contains(res.stderr, "-user-prompt")
	s.Contains(res.stderr, "when the loop ends: no-tool-finish, finish-tool or external-finish")

	res = s.run("version")
	s.Equal(cli.ExitOK, res.code)
	s.Contains(res.stdout, "steerloop ")

	res = s.run("tools", "validate", "--path", toolsYML)
	s.Equal(cli.ExitOK, res.code)
	s.Contains(res.stdout, "ok: 4 tool(s)")
	s.Contains(res.stdout, "danger: echo (asks before running)")

	res = s.run("tools", "validate", "--path", s.writeFile("bad.yaml", "tools:\n  - name: \"no good\"\n    command: []\n"))
	s.Equal(cli.ExitError, res.code)
	s.Contains(res.stderr, "name must be")
	s.Contains(res.stderr, "command is required")
}

func (s *CLISuite) TestRun_TextOutput() {
	system := s.writeFile("system.txt", "Be brief.")

	res := s.run("run", "--model", script("tool_then_answer.json"), "--tools", toolsYML, "--system-prompt", system, "--user-prompt", "hi")
	s.Require().Equal(cli.ExitOK, res.code, res.stderr)
	s.Equal("The tool printed: hello from the tool\n", res.stdout, "only the final content goes to stdout")
	s.Contains(res.stderr, "▸ say")
	s.Contains(res.stderr, "✓ say hello from the tool")
	s.Contains(res.stderr, "● finished · 2 iteration(s) · 310 tokens")
	s.NotContains(res.stderr, "│ The tool printed", "the final reply is not printed twice")

	res = s.run("run", "--model", script("tool_then_answer.json"), "--tools", toolsYML, "--output", "jsonl", "--system-prompt", system, "--user-prompt", "hi")
	request := s.requestOf(eventsOfType(s.parseEvents(res.stdout), "llm_turn")[0])
	s.Contains(request, `{"role":"system","content":"Be brief."}`)
	s.Contains(request, `{"role":"user","content":"hi"}`)
}

// The prompt is only ever --user-prompt: not the words after the flags, not stdin.
func (s *CLISuite) TestRun_UserPromptFlag() {
	args := []string{"run", "--model", script("tool_then_answer.json"), "--tools", toolsYML, "--output", "jsonl"}

	s.Run("--user-prompt is the user message, trimmed", func() {
		res := s.run(append(args, "--user-prompt", "  clean up ")...)
		s.Require().Equal(cli.ExitOK, res.code, res.stderr)
		s.Contains(s.requestOf(eventsOfType(s.parseEvents(res.stdout), "llm_turn")[0]), `{"role":"user","content":"clean up"}`)
	})

	s.Run("piped stdin is never read as the prompt", func() {
		s.stdin = strings.NewReader("from stdin")
		res := s.run(args...)
		s.Equal(cli.ExitUsage, res.code)
		s.Contains(res.stderr, "--user-prompt is required")
		s.Empty(res.stdout, "nothing runs")
	})

	s.Run("a blank --user-prompt is missing", func() {
		res := s.run(append(args, "--user-prompt", "   ")...)
		s.Equal(cli.ExitUsage, res.code)
		s.Contains(res.stderr, "--user-prompt is required")
	})

	s.Run("words after the flags are rejected, with or without --user-prompt", func() {
		for _, extra := range [][]string{{"clean up"}, {"--user-prompt", "clean up", "the build"}} {
			res := s.run(append(args, extra...)...)
			s.Equal(cli.ExitUsage, res.code)
			s.Contains(res.stderr, "unexpected argument")
			s.Empty(res.stdout, "nothing runs")
		}
	})
}

func (s *CLISuite) TestRun_FinishToolTextShowsNarration() {
	res := s.run("run", "--model", script("finish_tool.json"), "--tools", toolsYML, "--stop-condition", "finish-tool", "--debug", "--user-prompt", "go")
	s.Require().Equal(cli.ExitOK, res.code, res.stderr)
	s.Equal(`{"result":"all done"}`+"\n", res.stdout)
	s.Contains(res.stderr, "│ Let me think about that first.")
	s.Contains(res.stderr, "level=DEBUG", "--debug writes the loop's debug log")
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
	res := s.run("run", "--model", script("slow_tools.json"), "--tools", toolsYML, "--control-file", control, "--output", "jsonl", "--user-prompt", "go")
	s.Require().Equal(cli.ExitOK, res.code, res.stderr)

	turns := eventsOfType(s.parseEvents(res.stdout), "llm_turn")
	s.Require().Len(turns, 2)
	s.NotContains(s.requestOf(turns[0]), "metric units", "the first call was already in flight")
	s.Contains(s.requestOf(turns[1]), `{"role":"user","content":"use metric units"}`)
	s.Contains(res.stderr, `control line 3 ignored: unknown type "launch"`)
	s.Contains(res.stderr, "control line 4 ignored")

	raise := s.writeFile("raise.jsonl", `{"type":"modify_config","max_iterations":1,"wait_ms":100}`+"\n")
	res = s.run("run", "--model", script("slow_tools.json"), "--tools", toolsYML, "--control-file", raise, "--output", "jsonl", "--user-prompt", "go")
	s.Equal(cli.ExitMaxIterations, res.code)
	s.Equal("max_iterations", s.doneReason(s.parseEvents(res.stdout)))
}

// A control file is someone to ask: it answers ask_user without a terminal.
func (s *CLISuite) TestRun_ControlFileAnswersAskUser() {
	answer := s.writeFile("answer.jsonl", `{"type":"inject","message":"blue","wait_ms":200}`+"\n")
	res := s.run("run", "--model", script("ask_user.json"), "--ask-user", "--control-file", answer, "--user-prompt", "pick")
	s.Require().Equal(cli.ExitOK, res.code, res.stderr)
	s.Equal("You chose blue.\n", res.stdout)
}

// C5: ask_user and confirm prompts are answered from the terminal.
func (s *CLISuite) TestRun_AskUserOnTerminal() {
	res := s.typing([]string{"run", "--model", script("ask_user.json"), "--ask-user", "--user-prompt", "pick"},
		[2]string{"? Which colour? [red / blue]", "blue"})
	s.Require().Equal(cli.ExitOK, res.code, res.stderr)
	s.Equal("You chose blue.\n", res.stdout)
	s.Contains(res.stderr, "✓ ask_user blue")
}

func (s *CLISuite) TestRun_ConfirmOnTerminal() {
	args := []string{"run", "--model", script("confirm.json"), "--tools", toolsYML, "--user-prompt", "clean"}

	res := s.typing(args, [2]string{`? Run danger {"target":"build/"}? [y / n]`, "y"})
	s.Require().Equal(cli.ExitOK, res.code, res.stderr)
	s.Contains(res.stderr, "✓ danger removed build/")

	res = s.typing(args, [2]string{"? Run danger", "no thanks"})
	s.Require().Equal(cli.ExitOK, res.code, res.stderr)
	s.Contains(res.stderr, "✓ danger The user declined to run this tool.")
	s.NotContains(res.stderr, "removed build/")

	s.reset()
	res = s.run("run", "--skip-confirm", "--model", script("confirm.json"), "--tools", toolsYML, "--user-prompt", "clean")
	s.Contains(res.stderr, "✓ danger removed build/", "--skip-confirm approves without asking")

	res = s.run(args...)
	s.Contains(res.stderr, "nobody is available", "with no terminal the tool is not run")
	s.NotContains(res.stderr, "removed build/")
}

// "/stop" typed at the terminal stops the run, even while a question waits.
func (s *CLISuite) TestRun_StopOnTerminal() {
	res := s.typing([]string{"run", "--model", script("ask_user.json"), "--ask-user", "--user-prompt", "pick"},
		[2]string{"? Which colour?", "/stop"})
	s.Equal(cli.ExitStopped, res.code, res.stderr)
	s.Contains(res.stderr, "● stopped")
}

// C6: a replay prints what the live run printed, without a model or a tool.
func (s *CLISuite) TestReplay_MatchesLiveRun() {
	for _, tc := range [][]string{
		{"--model", script("tool_then_answer.json"), "--tools", toolsYML, "--user-prompt", "hi"},
		{"--model", script("finish_tool.json"), "--tools", toolsYML, "--stop-condition", "finish-tool", "--user-prompt", "go"},
		{"--model", script("never_ends.json"), "--tools", toolsYML, "--max-iterations", "2", "--user-prompt", "x"},
		{"--model", script("ask_user.json"), "--ask-user", "--user-prompt", "x"},
		{"--model", script("model_error.json"), "--user-prompt", "x"},
	} {
		recording := filepath.Join(s.T().TempDir(), "run.jsonl")
		live := s.run(append([]string{"run", "--output-record-path", recording}, tc...)...)
		s.Contains(live.stderr, "recorded to "+recording)

		replay := s.run("replay", "--path", recording, "--speed", "0")

		s.Equal(live.stdout, replay.stdout)
		s.Equal(recordedRE.ReplaceAllString(live.stderr, ""), replay.stderr, "only the live run says where it recorded")
		s.Equal(live.code, replay.code)
	}
}

// C7: a missing key fails before any call, and a key never reaches the output.
func (s *CLISuite) TestRun_APIKeys() {
	res := s.run("run", "--model", "anthropic:some-model", "--user-prompt", "hi")
	s.Equal(cli.ExitUsage, res.code)
	s.Contains(res.stderr, "ANTHROPIC_API_KEY is not set")
	res = s.run("run", "--model", "openai:some-model", "--user-prompt", "hi")
	s.Contains(res.stderr, "OPENAI_API_KEY is not set")

	const secret = "sk-test-secret-value-123"
	recording := filepath.Join(s.T().TempDir(), "run.jsonl")
	s.vars = map[string]string{"ANTHROPIC_API_KEY": secret, "OPENAI_API_KEY": secret}
	res = s.run("run", "--debug", "--output-record-path", recording, "--model", script("tool_then_answer.json"), "--tools", toolsYML, "--user-prompt", "hi")
	s.Require().Equal(cli.ExitOK, res.code)
	recorded, err := os.ReadFile(recording)
	s.Require().NoError(err)
	for _, out := range []string{res.stdout, res.stderr, string(recorded)} {
		s.NotContains(out, secret)
	}
}

// C8: one real model call. Runs only when STEERLOOP_LIVE=1 and a key is set.
func (s *CLISuite) TestRun_LiveSmoke() {
	if os.Getenv("STEERLOOP_LIVE") != "1" {
		s.T().Skip("set STEERLOOP_LIVE=1 and STEERLOOP_LIVE_MODEL (for example openai:<model>) to run")
	}
	modelSpec := os.Getenv("STEERLOOP_LIVE_MODEL")
	s.Require().NotEmpty(modelSpec, "STEERLOOP_LIVE_MODEL is required")

	s.vars = map[string]string{
		"ANTHROPIC_API_KEY": os.Getenv("ANTHROPIC_API_KEY"),
		"OPENAI_API_KEY":    os.Getenv("OPENAI_API_KEY"),
	}
	res := s.run("run", "--output", "jsonl", "--model", modelSpec, "--tools", toolsYML, "--user-prompt",
		`Use the say tool to print exactly "live check", then tell me what it printed.`)
	s.Require().Equal(cli.ExitOK, res.code, res.stderr)
	events := s.parseEvents(res.stdout)
	s.NotEmpty(eventsOfType(events, "tool_result"))
	s.Equal("finished", s.doneReason(events))
}

// An interrupt stops the run after the current iteration; a second one
// aborts it. The watcher listens for real signals, so the test sends SIGINT
// to its own process. The test's own Notify keeps that signal from killing
// the test binary even before the watcher has registered.
func (s *CLISuite) TestRun_Interrupts() {
	guard := make(chan os.Signal, 4)
	signal.Notify(guard, os.Interrupt)
	defer signal.Stop(guard)

	for name, count := range map[string]int{"one interrupt stops": 1, "two interrupts abort": 2} {
		s.Run(name, func() {
			done := s.start("run", "--model", script("nap.json"), "--tools", toolsYML, "--user-prompt", "rest")

			s.waitForStderr("▸ nap")
			start := time.Now()
			for range count {
				s.Require().NoError(syscall.Kill(os.Getpid(), syscall.SIGINT))
				time.Sleep(50 * time.Millisecond)
			}
			select {
			case res := <-done:
				s.Equal(cli.ExitStopped, res.code)
				s.Contains(res.stderr, "stopping after the current iteration")
			case <-time.After(15 * time.Second):
				s.Require().FailNow("run did not end after the interrupt")
			}
			if count == 2 {
				s.Less(time.Since(start), 3*time.Second, "an abort does not wait for the 5 second tool")
			}
		})
	}
}
