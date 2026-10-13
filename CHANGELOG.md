# Changelog

## v0.2.0

The library and the CLI are unchanged. This release is about how the repo is kept up.

- Dependabot checks Go modules and GitHub Actions every day. Smaller Go updates are gathered into one pull request, so you are not reviewing a pile of them.
- CI lints with golangci-lint. That takes the place of the separate format check and the separate staticcheck run.
- Locally, `mise run test`, `mise run lint`, and `mise run git-tag-release`. The scripts live in `.mise/scripts`.
- A new pull request starts from a template keyed `STLOOP-xxx`.

## v0.1.1

- The module path is now `github.com/mittalkanishk/steerloop`. v0.1.0 was published as `github.com/Kanishkmittal55/steerloop`; update imports when upgrading.

## v0.1.0

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
