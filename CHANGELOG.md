# Changelog

## Unreleased

- Breaking: `steerlooptest` is removed. The scripted model moved to `internal/mockllm/scripts`, where it still backs `--model script:<file>` and the examples. The loop tests use a mockery mock of `loop.ToolExecutor` in `loop/_mocks` (`mise run mocks`). To test code built on steerloop, mock `model.ToolCallingChatModel` and `loop.ToolExecutor`.

## v0.2.7 (2026-10-28)

- New `steerloop chat`: a multi-turn session at the terminal. After each turn a `❯ ` prompt continues the conversation. `--user-prompt` is optional. Ctrl-D, or Ctrl-C at the prompt, exits. `steerloop run` stays one turn.
- The setup `run` and `chat` share (flags, loop builder, tools, recording) is in `cmd/steerloop/internal/bootstrap`.
- The loop tests are one suite, and manual CLI checks are in `docs/cli-testing.md`.
- `loop/wire.go` is folded into `loop/loop.go`.

## v0.2.6 (2026-10-26)

- Breaking: `--stop-condition explicit` is now `external-finish`.
- A new README, with demo recordings in `docs/demo`.

## v0.2.5 (2026-10-25)

- Breaking: `loop2` is now `loop` (`github.com/mittalkanishk/steerloop/loop`), and the CLI is `steerloop` again, not `steerloop2`.

## v0.2.4 (2026-10-25)

A rewrite of the library and the CLI.

- Breaking: the root package `github.com/mittalkanishk/steerloop` is removed. The library is `loop2`, with `loop2/pause` for pause handlers and `loop2/registry`. Its internals are split into packages under `loop2/internal`.
- Stop policies (`no-tool-finish`, `finish-tool`, `explicit`) and reflection policies (`off`, `on-iteration`, `on-finish`).
- Breaking: the CLI is `steerloop2`, with `run`, `replay`, `tools validate`, `version` and `help`. `run` is one turn and takes the prompt from `--user-prompt`. New flags: `--system-prompt`, `--stop-condition`, `--reflect`. Control-file steering is carried over. Runs are recorded to `run-recordings/` by default (`--output-record-path`). The multi-turn session and the `/max`, `/temp` and `/reflect` commands are gone.
- The trace is coloured (lipgloss).

## v0.2.3 (2026-10-17)

- Breaking: `steerloop run` takes the prompt only from `--prompt <text>`, which is required. Words after the flags are a usage error, and stdin is no longer read as the prompt.
- The CLI's internals are restructured.

## v0.2.2 (2026-10-14)

- `steerloop run` on a terminal is a session: after an answer it prints `> `, and the next line continues the conversation.
- The loop tests are rewritten as one end-to-end suite.
- CI retries module downloads before linting. GitHub Actions and testify are updated.

## v0.2.0 (2026-10-13)

The library and the CLI are unchanged. This release is about how the repo is kept up.

- Dependabot checks Go modules and GitHub Actions every day. Smaller Go updates are gathered into one pull request, so you are not reviewing a pile of them.
- CI lints with golangci-lint. That takes the place of the separate format check and the separate staticcheck run.
- Locally, `mise run test`, `mise run lint`, and `mise run git-tag-release`. The scripts live in `.mise/scripts`.
- A new pull request starts from a template keyed `STLOOP-xxx`.

## v0.1.1 (2026-10-11)

- The module path is now `github.com/mittalkanishk/steerloop`. v0.1.0 was published as `github.com/Kanishkmittal55/steerloop`; update imports when upgrading.

## v0.1.0 (2026-10-11)

First public release.

### Library

- `Loop` with stop, inject and modify-config controls, a typed event stream, pause tools, retry on transient model failure, a stream idle watchdog and per-turn audit events.
- Active Loop Registry - `Registry[K]` for finding running loops by key.
- `Loop.Result` returns the outcome without reading the event channel.
- `steerlooptest`: a scripted chat model, a JSON script format and a recording tool executor.

### CLI

- `steerloop run` with manifest-declared command-line tools, live steering from the terminal or a control file, `ask_user`, tool confirmation, text and JSONL output, and recording.
- `steerloop replay` re-renders a recording.
- `steerloop tools validate` checks a manifest.
