package agent

import (
	"fmt"

	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/internal/gates"
	executor "github.com/mittalkanishk/steerloop/internal/tools"
	"github.com/mittalkanishk/steerloop/loop"
	"github.com/mittalkanishk/steerloop/loop/pause"
)

// Toolset is what the model can call, and the gates in front of some of it.
// Gates are loop pause.Handlers, so they go straight into loop.Config with
// no conversion.
type Toolset struct {
	Infos    []*schema.ToolInfo
	Executor loop.ToolExecutor
	Gates    map[string]pause.Handler // tool name → the gate that handles it
}

func NewToolset(opts Options, humanAvailable bool) (Toolset, error) {

	ts := Toolset{
		Executor: executor.Unknown{},
		Gates:    map[string]pause.Handler{},
	}

	if opts.ToolsManifestPath != "" {

		manifestExecutor, err := executor.Load(opts.ToolsManifestPath)
		if err != nil {
			return Toolset{}, fmt.Errorf("invalid manifest:\n%w", err)
		}
		if ts.Infos, err = manifestExecutor.ToolInfos(); err != nil {
			return Toolset{}, err
		}

		// *exectool.Manifest has an ExecuteTool method, so it is a
		// loop.ToolExecutor without saying so.
		ts.Executor = manifestExecutor

		for _, tool := range manifestExecutor.Tools {
			if tool.RequiresConfirmation {
				ts.Gates[tool.Name] = gates.NewConfirm(opts.AutoApprove, humanAvailable)
			}
		}
	}

	if opts.AskUser {
		info, err := gates.AskUserToolInfo()
		if err != nil {
			return Toolset{}, err
		}
		ts.Infos = append(ts.Infos, info)
		ts.Gates[gates.AskUserToolName] = gates.NewAskUser(humanAvailable)
	}

	stopToolParams := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"result": map[string]any{"type": "string", "description": "The final result"},
		},
		"required": []string{"result"},
	}

	// finish is not a gate, in toolset its a toolinfo appended when the stop mode is finish-tool;
	// the loop recognizes it by name, so it needs no executor or gate entry.
	if opts.StopCondition == "finish-tool" {
		info, err := loop.NewToolInfo(loop.FinishToolName, "Call this once the task is complete with the final result", stopToolParams)
		if err != nil {
			return Toolset{}, err
		}

		ts.Infos = append(ts.Infos, info)
	}

	return ts, nil
}
