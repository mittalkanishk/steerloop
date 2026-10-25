### Stop Conditions and stopping policy for agentic harness - 

Stop condition is a policy you choose before a run begins: when is the model/llm allowed to end a run ( a group of iterations ). There are 3 cases: 

- `no-tool-finish` - a text reply from the model can end the run.

- `finish-tool` - model calling `finish-tool` recognized by the harness ends the run.

- `external_finish` - model `cannot` end the run, because in this mode, **llm has no way to end the run**, it has no access to the finish-tool and even the plain text answers of the model are ignored, so plain text doesn't end the run, and it is told explicitly in the prompt to keep working and never stop until obviously base conditions like **maximum cap** or **timeout** are hit, that is the only way model stops and yes it is  abruptly in the middle, hence the name external_finish.

### Reflection policy for agentic harness - 

Reflection policy is a decision the harness applies during the a run or after a run begins: when is the model allowed to reflect on its answers and responses ( between iterations or after the final answer). There are two types of reflection policy supported right now: 

1. `on_finish` - runs only when the model is allowed to finish a run. This mode does depend on the stop policy: 
> **no-tool-finish**: the judge checks the final text reply.
> **finish-tool**: the judge checks the finish call's arguments.
> **external-finish**: the model never finishes (because it doenot know how to), so on-finish never runs, and this is why a pair of --reflect on_finish --stop-condition external_finish is not allowed because in that mode doesnot know how to finish.

### Stop policy, reflection policy and the verdict - 

```text
┌────────────────────────────────────────────────────────────────────────────────┐
|                                                                                |   
│      Stop policy          Reflection policy (on_finish)                        │
│   ┌─────────────────┐     ┌───────────────────────┐                            │
├──▶│ no-tool-finish  │────▶│ Judge                 │───┐                        │
│   └─────────────────┘     │                       │   │                        │
│   ┌─────────────────┐     │                       │   │                        │
├──▶│ finish-tool     │────▶│ Judge                 │───┤                        │
│   └─────────────────┘     │                       │   │                 ┌──────┴───────┐
│   ┌─────────────────┐     │                       │   │   ┌─────────┐   │ New run      │
└──▶│ external-finish │─────┼ (no judge needed)     |───┴──▶│ Verdict │──▶│ (new params) │
    └─────────────────┘     └───────────────────────┘       └─────────┘   └──────────────┘
```

- `no-tool-finish` and `finish-tool` go through the on_finish judge, which returns a verdict.
- `external-finish` needs no judge: the harness already knows which limit or stop ended the run, and reports it.
- A drift verdict starts the loop again with the judge's notes; no drift returns the answer.

**Design decision: re-open.** On verdict result being a drift the run re-opens: it continues the same conversation with the judge's notes added, rather than starting a fresh run from the task. Tool results and work already done are kept, so it is cheaper, and `MaxReopens` caps how many times it can happen.

2. `on-iteration` - reflecting on every step of the run i.e. every iteration of the run, this is correcting forward. Retry here is not an option because retrying an iteration means undoing it, and tool calls have side effects ( file written, commands run ) that can't be undone **easily**. So the only safe move during a run is to `steer` the loop's next iteration, which is what this reflection policy is for.

### On-iteration reflection and its verdict - 

```text
┌─────────────┐     ┌───────┐     ┌────────────────┐
│ iteration N │────▶│ Judge │────▶│ Verdict        │──┬── on track  → nothing injected ─────────────┐
└─────────────┘     └───────┘     │ on_track: bool │  │                                             │
                                  │ issue          │  ├── off track → inject one note: issue + hint ┤
                                  │ hint           │  │                                             │
                                  └────────────────┘  └── failed    → emit error, nothing injected ─┤
                                                                                                    ▼
                                                                                           ┌───────────────┐
                                                                                           │ iteration N+1 │
                                                                                           └───────────────┘
```

- After every iteration, a judge reviews that iteration against the task and returns a verdict.
- `issue` and `hint` are two fields of one off-track verdict, not separate branches.
- Every branch continues to the next iteration: the verdict only decides what is injected before it, never whether the run goes on. Ending the run stays the stop policy's job.
