package runcmd

import (
	"fmt"

	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/internal/exectool"
	"github.com/mittalkanishk/steerloop/internal/gates"
	"github.com/mittalkanishk/steerloop/loop2"
	"github.com/mittalkanishk/steerloop/loop2/pause"
)

// Toolset is what the model can call, and the gates in front of some of it.
// Gates are loop2 pause.Handlers, so they go straight into loop2.Config with
// no conversion.
type Toolset struct {
	Infos    []*schema.ToolInfo
	Executor loop2.ToolExecutor
	Gates    map[string]pause.Handler // tool name → the gate that handles it
}

func NewToolset(opts Options, humanAvailable bool) (Toolset, error) {
	ts := Toolset{
		Executor: exectool.Unknown{},
		Gates:    map[string]pause.Handler{},
	}

	if opts.ManifestPath != "" {
		manifest, err := exectool.Load(opts.ManifestPath)
		if err != nil {
			return Toolset{}, fmt.Errorf("invalid manifest:\n%w", err)
		}
		if ts.Infos, err = manifest.ToolInfos(); err != nil {
			return Toolset{}, err
		}
		// *exectool.Manifest has an ExecuteTool method, so it is a
		// loop2.ToolExecutor without saying so.
		ts.Executor = manifest
		for _, t := range manifest.Tools {
			if t.Confirm {
				ts.Gates[t.Name] = gates.NewConfirm(opts.AutoApprove, humanAvailable)
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

	return ts, nil
}
