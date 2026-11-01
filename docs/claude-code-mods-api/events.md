# Claude Code mod events

Extracted from [`mods/types/claude-code.d.ts`](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts) in [anthropics/claude-code](https://github.com/anthropics/claude-code), commit `bf2323b` (2026-10-13). The GitHub copy can lag your installed Claude Code; the types Claude Code writes into a `--plugin-dir` mod (`.claude-plugin/types/claude-code/index.d.ts`) match your version.

Docs: [Mods events](https://code.claude.com/docs/en/plugins/mods/events) · [Mods reference](https://code.claude.com/docs/en/plugins/mods/reference)

## Groups

| Group | Count | What they are | Declared in |
|---|---|---|---|
| [Engine events](#engine-events) | 40 | Claude Code's own events | [`EngineEventOf`](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3255) |
| [API-call events](#api-call-events) | 55 | Every `$.<noun>.<method>()` call is also an event | [`OpEventOf`](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5537) |
| [Classic settings-hook events](#classic-settings-hook-events) | 33 | Each settings hook, as `classic.<Name>` | [`ClassicEventOf`](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L951) |
| **Total** | **128** | | |

All three are joined in [`EventOf`](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3962), with [`NounEventOf`](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5436) adding the methods of any API another mod declares. The wildcard `'*'` matches every event except telemetry; `'classic.*'` matches every classic event. This copy declares no `telemetry.*` events.

## Engine events

| Event | Line | Description |
|---|---|---|
| `tool.call` | [L3264](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3264) | Fires when Claude Code is about to run a tool. |
| `tool.check` | [L3276](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3276) | Fires when Claude Code decides whether a tool call may run, after `tool.call` and PreToolUse hooks. |
| `ui.render` | [L3285](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3285) | Fires when Claude Code is about to draw a component. |
| `ui.resolve` | [L3294](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3294) | Fires once per surface, component and plugin when plugins load (not per draw). |
| `ui.press` | [L3303](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3303) | Fires when a `Button` drawn by a render hook is pressed. |
| `ui.input` | [L3312](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3312) | Fires when an `Input` drawn by a render hook changes or is submitted. |
| `ui.select` | [L3321](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3321) | Fires when an option is picked from a `Select` drawn by a render hook. |
| `ui.message` | [L3330](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3330) | Fires when a `Client` this plugin drew posts data from its surface module. |
| `ui.scroll` | [L3342](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3342) | Fires before a pane or the band above the prompt scrolls. |
| `ui.focus` | [L3354](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3354) | Fires before focus moves within a pane or the band above the prompt. |
| `agent.offer` | [L3365](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3365) | Fires when Claude Code offers an agent type to the model, in the listing and at dispatch. |
| `agent.spawn` | [L3374](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3374) | Fires when the Agent tool is about to start a subagent. |
| `prompt.submit` | [L3383](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3383) | Fires when a prompt is submitted, before the turn starts. |
| `prompt.fill` | [L3395](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3395) | Fires when text is about to be put in the prompt box as the draft. |
| `prompt.suggest` | [L3407](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3407) | Fires when text is proposed as the prompt box's dim Tab-to-accept suggestion. |
| `prompt.edit` | [L3419](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3419) | Fires when the person edits the main prompt box (a keystroke or a paste). |
| `prompt.section` | [L3431](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3431) | Fires once per named section of the system prompt as it is assembled. |
| `prompt.context` | [L3443](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3443) | Fires once per conversation, when the context blocks of the first user message are computed. |
| `prompt.compose` | [L3455](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3455) | Fires when Claude Code renders a system prompt. |
| `prompt.attachment` | [L3467](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3467) | Fires once per message Claude Code injects for the model by itself (a reminder, a mode change, a mentioned file). |
| `tool.describe` | [L3481](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3481) | Fires once per tool, the first time its schema is rendered in a session. |
| `command.run` | [L3493](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3493) | Fires when a slash command is about to run. |
| `command.describe` | [L3505](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3505) | Fires once per command, when it is listed for the typeahead and `/help`. |
| `config.set` | [L3517](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3517) | Fires when a `/config` row is about to change. |
| `config.describe` | [L3529](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3529) | Fires once per `/config` row, when the menu lists it. |
| `skill.prompt` | [L3540](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3540) | Fires when a skill's prompt is expanded for the model. |
| `attribution.text` | [L3551](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3551) | Fires when Claude Code composes git text for the model to write (e.g. commit attribution). |
| `session.start` | [L3563](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3563) | Fires once per loaded plugin before the first prompt, and on each fresh load. |
| `session.receive` | [L3575](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3575) | Fires when a delivery reaches the session (a relay event, a peer's message, a Remote Control prompt). |
| `session.append` | [L3587](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3587) | Fires once per row the conversation keeps (prompt, response block, tool result, notice). |
| `session.compact` | [L3599](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3599) | Fires when the conversation is about to be compacted. |
| `session.attach` | [L3611](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3611) | Fires when a remote client joins the session's attached surfaces. |
| `session.detach` | [L3616](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3616) | Fires when a client leaves the session's attached surfaces. |
| `session.measure` | [L3628](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3628) | Fires when Claude Code measures the session and a unit (context, rate limit, cost) moved. |
| `session.end` | [L3640](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3640) | Fires once when the session ends (exit, /clear, resume, logout, signal, `-p` run done). |
| `plugin.register` | [L3652](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3652) | Fires once per hooks module about to join the chain, at load and reload. |
| `turn.start` | [L3657](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3657) | Fires when a model turn begins, before its first model call. |
| `turn.step` | [L3666](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3666) | Fires when Claude Code is about to send a model request within a turn (main thread or subagent). |
| `turn.complete` | [L3675](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3675) | Fires when a model turn has ended. |
| `engine.create` | [L3684](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3684) | Runs while `$` is built, once per load or reload of the plugin, before any of its other hooks. |

## API-call events

Line points to the event's argument type in `OpEventOf`.

| Event | Line | Description |
|---|---|---|
| `model.complete` | [L5541](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5541) | `$.model.complete`: runs one text completion through the session's API client. |
| `model.classify` | [L5545](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5545) | `$.model.classify`: picks one of the given labels for a text with one completion. |
| `model.fork` | [L5553](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5553) | `$.model.fork`: runs one tool-less completion over the session's own transcript. |
| `audio.play` | [L5558](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5558) | `$.audio.play`: plays one audio clip, starting now. |
| `audio.speak` | [L5566](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5566) | `$.audio.speak`: speaks text with the platform's synthesizer (`say` on macOS). |
| `mcp.call` | [L5570](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5570) | `$.mcp.call`: calls a tool on one of the connected MCP servers. |
| `session.cwd` | [L5578](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5578) | `$.session.cwd`: returns the directory the session runs in. |
| `session.root` | [L5582](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5582) | `$.session.root`: returns the session's project root. |
| `session.model` | [L5586](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5586) | `$.session.model`: returns the main loop's model. |
| `session.turns` | [L5590](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5590) | `$.session.turns`: returns how many prompts the user has sent this session. |
| `session.id` | [L5594](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5594) | `$.session.id`: returns the session's id. |
| `session.messages` | [L5598](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5598) | `$.session.messages`: returns the transcript so far, one entry per message. |
| `session.repo` | [L5602](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5602) | `$.session.repo`: returns the git repository the session runs in, or null. |
| `session.surface` | [L5608](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5608) | `$.session.surface`: returns the first surface the session draws on, or null. |
| `session.surfaces` | [L5612](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5612) | `$.session.surfaces`: returns every surface the session draws on. |
| `session.authorize` | [L5616](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5616) | `$.session.authorize`: returns an opaque handle to the session's Anthropic credential. |
| `session.usage` | [L5620](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5620) | `$.session.usage`: returns context fill, rate-limit windows and cost. |
| `session.version` | [L5624](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5624) | `$.session.version`: returns the Claude Code version, release and build time. |
| `turn.abort` | [L5628](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5628) | `$.turn.abort`: cancels the running model turn. |
| `prompt.read` | [L5634](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5634) | `$.prompt.read`: returns the prompt box draft and cursor position. |
| `tool.list` | [L5638](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5638) | `$.tool.list`: returns the tools the model can call now. |
| `tool.register` | [L5642](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5642) | `$.tool.register`: declares a new tool the model can call. |
| `command.list` | [L5646](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5646) | `$.command.list`: returns the slash commands the person can run now. |
| `command.register` | [L5650](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5650) | `$.command.register`: declares a new slash command for this session. |
| `config.list` | [L5654](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5654) | `$.config.list`: returns the rows the `/config` menu would show. |
| `agent.list` | [L5658](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5658) | `$.agent.list`: returns the session's subagents so far. |
| `agent.register` | [L5663](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5663) | `$.agent.register`: defines a new agent type the Agent tool can dispatch. |
| `ui.toast` | [L5667](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5667) | `$.ui.toast`: shows text under the prompt for a few seconds. |
| `ui.status` | [L5674](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5674) | `$.ui.status`: pins a status line under the prompt. |
| `ui.log` | [L5681](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5681) | `$.ui.log`: adds a dim line to the transcript, or to the debug log with `{ to: "debug" }`. |
| `ui.notice` | [L5688](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5688) | `$.ui.notice`: shows a line under an open tool dialog. |
| `ui.invalidate` | [L5695](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5695) | `$.ui.invalidate`: re-runs an event whose result is cached (e.g. a redraw). |
| `ui.open` | [L5702](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5702) | `$.ui.open`: opens a pane the plugin draws. |
| `ui.close` | [L5707](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5707) | `$.ui.close`: closes one of the plugin's open panes. |
| `ui.panes` | [L5711](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5711) | `$.ui.panes`: lists the plugin's open panes. |
| `ui.blit` | [L5716](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5716) | `$.ui.blit`: repaints a `Raster` or swaps an `Image` the plugin drew. |
| `fs.read` | [L5721](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5721) | `$.fs.read`: reads a file as text or bytes. |
| `fs.write` | [L5728](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5728) | `$.fs.write`: writes a file, creating directories as needed. |
| `fs.list` | [L5735](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5735) | `$.fs.list`: lists a directory. |
| `fs.exists` | [L5741](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5741) | `$.fs.exists`: returns whether a path exists. |
| `fs.stat` | [L5748](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5748) | `$.fs.stat`: returns a path's kind, size, modified time and link status. |
| `fs.ancestors` | [L5755](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5755) | `$.fs.ancestors`: reads named instruction files in every directory above the working directory. |
| `store.get` | [L5759](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5759) | `$.store.get`: returns the stored value for a key. |
| `store.set` | [L5765](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5765) | `$.store.set`: stores a JSON value under a key. |
| `store.delete` | [L5772](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5772) | `$.store.delete`: removes a key from the store. |
| `store.keys` | [L5778](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5778) | `$.store.keys`: returns every stored key. |
| `clock.now` | [L5782](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5782) | `$.clock.now`: returns the current time in milliseconds. |
| `clock.sleep` | [L5787](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5787) | `$.clock.sleep`: waits a number of milliseconds. |
| `clock.after` | [L5792](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5792) | `$.clock.after`: calls a function once after a delay. |
| `clock.every` | [L5797](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5797) | `$.clock.every`: calls a function on a repeating interval. |
| `http.fetch` | [L5801](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5801) | `$.http.fetch`: fetches a URL through the host. |
| `process.run` | [L5808](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5808) | `$.process.run`: runs a command on the host (no shell). |
| `settings.read` | [L5815](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5815) | `$.settings.read`: returns merged settings, or one source's settings. |
| `env.get` | [L5819](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5819) | `$.env.get`: returns an environment variable's value. |
| `env.set` | [L5826](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5826) | `$.env.set`: sets or unsets an environment variable. |

## Classic settings-hook events

Hooked as `classic.<Name>`; `e` is the hook's stdin JSON. Line points to `hook_event_name` in the hook's input type. The types file has no descriptions for these; the ones below are written from each type's fields.

| Event | Line | Description |
|---|---|---|
| `classic.ConfigChange` | [L1591](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L1591) | A settings file (user, project, local, policy) or skills changed. |
| `classic.CwdChanged` | [L3062](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3062) | The working directory changed. |
| `classic.DirectoryAdded` | [L3068](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3068) | A directory was added to the session. |
| `classic.Elicitation` | [L3195](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3195) | An MCP server asks the user for input. |
| `classic.ElicitationResult` | [L3208](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3208) | The user answered an MCP elicitation. |
| `classic.FileChanged` | [L3992](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L3992) | A watched file was changed, added or deleted. |
| `classic.InstructionsLoaded` | [L4682](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L4682) | An instruction file (CLAUDE.md) was loaded. |
| `classic.MessageDisplay` | [L5083](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5083) | An assistant message is displayed. |
| `classic.Notification` | [L5408](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L5408) | Claude Code sends a notification. |
| `classic.PermissionDenied` | [L6094](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L6094) | A tool call was denied permission. |
| `classic.PermissionRequest` | [L6122](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L6122) | A permission dialog is about to be shown. |
| `classic.PostCompact` | [L6326](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L6326) | The conversation was compacted. |
| `classic.PostModelSwitch` | [L6335](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L6335) | The model was switched. |
| `classic.PostToolBatch` | [L6376](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L6376) | A batch of tool calls finished. |
| `classic.PostToolUse` | [L6402](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L6402) | A tool call succeeded. |
| `classic.PostToolUseFailure` | [L6388](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L6388) | A tool call failed. |
| `classic.PreCompact` | [L6415](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L6415) | The conversation is about to be compacted. |
| `classic.PreModelSwitch` | [L6421](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L6421) | The model is about to be switched. |
| `classic.PreToolUse` | [L6506](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L6506) | A tool is about to run. |
| `classic.SessionEnd` | [L9087](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L9087) | The session is ending. |
| `classic.SessionStart` | [L9369](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L9369) | The session is starting or resuming. |
| `classic.Setup` | [L9529](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L9529) | Claude Code runs setup (init or maintenance). |
| `classic.Stop` | [L9664](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L9664) | Claude finished responding. |
| `classic.StopFailure` | [L9657](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L9657) | Claude stopped because of an error. |
| `classic.SubagentStart` | [L9783](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L9783) | A subagent started. |
| `classic.SubagentStop` | [L9789](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L9789) | A subagent finished. |
| `classic.TaskCompleted` | [L9860](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L9860) | A task was completed. |
| `classic.TaskCreated` | [L9872](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L9872) | A task was created. |
| `classic.TeammateIdle` | [L9884](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L9884) | A teammate in an agent team went idle. |
| `classic.UserPromptExpansion` | [L11530](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L11530) | A slash command or MCP prompt is expanded into a prompt. |
| `classic.UserPromptSubmit` | [L11539](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L11539) | The user submitted a prompt. |
| `classic.WorktreeCreate` | [L11561](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L11561) | A git worktree is being created. |
| `classic.WorktreeRemove` | [L11566](https://github.com/anthropics/claude-code/blob/bf2323b/mods/types/claude-code.d.ts#L11566) | A git worktree is being removed. |
