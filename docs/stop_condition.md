### Stop Conditions for harness - 

Stop condition is a policy you choose before a run begins: when is the model/llm allowed to end a run ( a group of steps/iterations ). There are 3 cases: 

- `no-tool-calls` - a text reply from the model can end the run.

- `finish-tool` - model calling `finish-tool` recognized by the harness ends the run.

- `external_finish` - model `cannot` end the run, because in this mode, llm has not way to end the run, it has no access to the finish-tool, and even the text answers of the model are ignored so plain text doesn't end the run, and it is told explicit in the prompt to keep working and never stopped until obviously base conditions like maximum cap or timeout are hit, that is only way model stops abruptly in the middle, hence external_finish.