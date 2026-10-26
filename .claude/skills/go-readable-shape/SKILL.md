---
name: go-readable-shape
description: Write or restructure Go code in the interface-first, nested-package shape this user can read and internalize — named types with methods instead of closures, each interface declared in the file that uses it, one composition root, nested directories without import cycles. Use whenever writing new Go code, adding a feature to a Go codebase, scaffolding a Go CLI or service, or restructuring existing Go code for readability. Triggers on "write this in Go", "add a command", "new package", "restructure", "make this readable", "go-readable-shape".
---

# Go: the readable shape

The user knows Go and event-driven services (processor/service patterns
behind interfaces, Kafka-based servers). Code that is correct but has
**delocalized control flow** — behaviour in closures built far from where
they run, goroutines started in many places, mode flags multiplying paths —
is hard for them to read. Write code where every behaviour has a name, a
type, and a file, and where the reader can follow the program from the
entry point downward.

Reference implementations in this repository: `cmd/steerloop` (the CLI) and
`loop` (the library). Match their shape.

## Rule 1 — Behaviour is a named type with a method, not a closure

- Do NOT return behaviour as a function (`func() (emit func(x), close func())`),
  build `func` factories (`askPause() PauseFunc`), or use `XxxFunc(func…)`
  adapters in application code.
- DO define an interface for the role, and a struct per implementation with
  a constructor `NewXxx(...)` and a method.
- When a library demands a `func` type, convert in exactly ONE named
  function using a method value, with a comment saying so:
  ```go
  // asHandlerFuncs is the ONE place a ToolGate becomes the HandlerFunc the
  // library asks for. gate.Handle is a method value.
  func asHandlerFuncs(gates map[string]ToolGate) map[string]lib.HandlerFunc
  ```
  This repository's own library takes interfaces instead (`loop.ToolExecutor`,
  `pause.Handler`, the hooks on `loop.Config`), so no conversion is needed.
- Small inline closures are fine only when local and obvious (a `defer`, a
  `sort.Slice` less func, a goroutine body that calls one named method).

## Rule 2 — A contract is declared in the file that uses it

- Declare each interface in the same file as the code that calls its
  methods, directly above that struct or function. Reading the consumer,
  you see the contract it relies on without opening another file. No
  `contracts.go` or `interfaces.go` collection files.
- The interface's comment ends with `→ <where it's implemented>`, so the
  reader can jump to the implementation.
- If several files in one package use the same interface, declare it in
  the file of the top-level consumer (the one closest to the entry point),
  and the others use it from there.
- Never declare an interface next to its implementation. Interfaces are
  small (1–3 methods).
- Implementations do not import the interface (Go satisfies interfaces
  implicitly), which is what keeps nested packages free of cycles.

Example — `cmd/steerloop/internal/cmds/run/run.go` declares what `Run`
needs, right above `Run`:
```go
// EventSink receives every event the loop emits.
// → cmd/steerloop/internal/render (people), steerloop/internal/eventlog (machines)
type EventSink interface {
	Write(evt event.Event)
}

// SteeringSource feeds a person's input into a running loop.
// → cmd/steerloop/internal/steering
type SteeringSource interface {
	Steer(ctx context.Context, loop *loop.Loop)
}

// Run is one fully wired run, ready to execute.
type Run struct {
	sink     EventSink
	steering SteeringSource
	// ...
}
```

## Rule 3 — One composition root

- One function picks the concrete type for every interface, with a comment
  above each choice. Nothing else calls constructors of interchangeable
  implementations. In this repository: `loop.New` in `loop/wire.go` for the
  library, and `NewRun` in `cmd/steerloop/internal/cmds/run/run.go` for
  `steerloop run`.
- `main.go` stays tiny: build the environment, wire, run, `os.Exit`.

## Rule 4 — Goroutines start in one place and say how they end

- All `go` statements for a unit of work live in one method (e.g.
  `Run.Execute`). Its doc comment lists each goroutine and how it ends
  (which channel close or context cancel stops it).
- Every goroutine takes a `ctx` and returns on `ctx.Done()` where it can;
  if it cannot (e.g. blocking stdin read), say so in a comment.
- The channel owner closes the channel; write who that is in a comment.

## Rule 5 — Modes are chosen once, not re-checked everywhere

- Don't let several booleans (`interactive`, `multiTurn`, `hasFile`…)
  branch throughout one long function. Decide the mode once in the
  composition root by choosing an implementation (`steering.None{}`,
  `steering.Terminal` or `steering.Control`), so the main flow has no mode
  branches.
- Prefer a no-op implementation over `if x != nil` checks.

## Rule 6 — Nested directories, no import cycles

Decide where each piece goes with one test:

| Touches only library/domain types | Touches stdin/stdout, signals, flags, exit codes, HTTP, env |
|---|---|
| `<module>/internal/<role>/` (reusable by any front end in the module) — or a public `<module>/<role>/` if outside users need it | `cmd/<app>/internal/<role>/` (Go enforces that only this command can import it) |

Layout stencil for a CLI:
```
<module>/
  internal/<role>/              front-end-agnostic implementations
  cmd/<app>/
    main.go                     build env + commands, app.Run, os.Exit
    internal/
      cli/                      LEAF: App + Command interface, Environment, exit codes
      cmds/<command>/           command.go, options.go, run.go (each declares the interfaces it uses)
      <role>/                   one dir per implementation family (render/, steering/, interrupt/…)
```

Cycle rules to apply every time:
1. Nothing can import `package main` → shared types go in a leaf package
   that imports nothing local.
2. Dependencies point one way: `main` → commands → `cli` (leaf); commands →
   role packages; role packages → library only.
3. If A dispatches to B and B needs A's types, inject B into A from `main`
   (e.g. `cli.NewApp(env, map[string]cli.Command{"run": runcmd.New(env)})`).
4. Package names come from the directory, so drop the stutter:
   `steering.Terminal`, not `steering.TerminalSteering`.

## Rule 7 — Files read top-down

- One role per file; file named after the role (`gates.go`, `sinks.go`).
- Keep each type and its methods in the same file. No "all types in
  types.go".
- Files under ~200 lines; functions short enough that the happy path reads
  straight through. Validation and errors return early.
- Top of `main.go` (or the package doc) states the reading order, e.g.
  "run.go → wire.go → any implementation".

## When writing code

1. Before coding, list the roles (interfaces), their implementations, and
   the directory each lands in. Show this tree in one short block.
2. Write the flow first (`run.go`, with the interfaces it needs declared
   above it), then the composition root, then implementations.
3. Build it (`go build ./...` and `go vet ./...`; use `GOWORK=off` if the
   module is not in the workspace's `go.work`) so a new package never
   breaks the module.
4. Finish with: the reading order, and a table mapping each interface to
   its implementations and files.

## When restructuring existing code

- Never edit the original in place unless asked; build the new shape
  beside it (e.g. `cmd/<app>2`) so the user can compare.
- Give a table: original closure/function → new named type and file.
- State plainly which features were left out and whether it can replace
  the original yet.
