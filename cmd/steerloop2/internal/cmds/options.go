package runcmd

import (
	"errors"
	"flag"
	"io"
	"strings"

	"github.com/mittalkanishk/steerloop/loop2"
)

var stopConditions = map[string]loop2.StopCondition{
	"no-tool-calls": loop2.StopOnNoToolCalls,
	"finish-tool":   loop2.StopOnFinishTool,
	"explicit":      loop2.StopOnExplicit,
}

// Options is the `steerloop2 run` command line.
type Options struct {
	Prompt            string
	Model             string
	ToolsManifestPath string
	Output            string
	MaxIterations     int
	AskUser           bool
	AutoApprove       bool
	StopCondition     string
}

// ParseOptions reads flags into Options. The flag package reports its own
// errors to stderr.
func ParseOptions(args []string, stderr io.Writer) (Options, error) {
	var opts Options
	flags := flag.NewFlagSet("steerloop2 run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opts.Prompt, "prompt", "", "the prompt (required)")
	flags.StringVar(&opts.Model, "model", "", "anthropic:<model>, openai:<model> or script:<path> (required)")
	flags.StringVar(&opts.ToolsManifestPath, "tools", "", "path to a tool manifest (YAML)")
	flags.StringVar(&opts.Output, "output", "text", "text or jsonl")
	flags.IntVar(&opts.MaxIterations, "max-iterations", 12, "cap on model calls")
	flags.BoolVar(&opts.AskUser, "ask", false, "give the model an ask_user tool")
	flags.BoolVar(&opts.AutoApprove, "yes", false, "approve tools marked confirm without asking")
	flags.StringVar(&opts.StopCondition, "stop-condition", "no-tool-calls", "when the loop ends: no-tool-calls, finish-tool or explicit")
	err := flags.Parse(args)
	return opts, err
}

// Validate rejects options the loop cannot run with.
func (o Options) Validate() error {
	switch {
	case o.Model == "":
		return errors.New("--model is required")
	case strings.TrimSpace(o.Prompt) == "":
		return errors.New("--prompt is required")
	case o.Output != "text" && o.Output != "jsonl":
		return errors.New("--output must be text or jsonl")
	case o.MaxIterations < 1:
		return errors.New("--max-iterations must be at least 1")
	}

	if _, ok := stopConditions[o.StopCondition]; !ok {
		return errors.New("--stop-condition must either be no-tool-calls, finish-tool or explicit")
	}

	return nil
}
