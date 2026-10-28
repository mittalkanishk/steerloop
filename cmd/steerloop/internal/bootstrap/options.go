// Package bootstrap is what `steerloop run` and `steerloop chat` share: the
// flags they both take, the loop each turn runs, its tools and the
// recording. How turns are run is each command's own business.
package bootstrap

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/mittalkanishk/steerloop/loop"
)

var stopConditions = map[string]loop.StopCondition{
	"no-tool-finish":  loop.StopOnNoToolFinish,
	"finish-tool":     loop.StopOnFinishTool,
	"external-finish": loop.StopOnExternal,
}

var reflectConditions = map[string]loop.ReflectCondition{
	"off":          loop.ReflectOff,
	"on-iteration": loop.ReflectOnIteration,
	"on-finish":    loop.ReflectOnFinish,
}

// Options are the flags of every command that runs the loop. Each command
// adds its own --user-prompt, since only `run` requires one.
type Options struct {
	// Prompt Options
	SystemPromptPath string
	UserPrompt       string
	// Model Options
	Model       string
	Temperature float64 // below 0: not sent, so the provider's default applies
	MaxTokens   int
	// Loop Options
	MaxIterations    int
	IterationTimeout time.Duration
	TotalTimeout     time.Duration
	StopCondition    string
	Reflect          string // "on-iteration" or "on-finish"
	// Tool Options
	ToolsManifestPath string
	AutoApprove       bool
	AskUser           bool
	// Output Options
	OutputRecordPath string // also write every event to a JSONL file
	Debug            bool
}

// maxTemperature is the highest temperature any supported provider accepts
// (OpenAI allows up to 2, Anthropic up to 1).
const maxTemperature = 2

// AddFlags registers the shared flags on flags, filling o.
func (o *Options) AddFlags(flags *flag.FlagSet) {
	// Prompt Flags
	flags.StringVar(&o.SystemPromptPath, "system-prompt", "", "path to a system prompt file")
	// Model Flags
	flags.StringVar(&o.Model, "model", "", "anthropic:<model>, openai:<model> or script:<path> (required)")
	flags.Float64Var(&o.Temperature, "temperature", -1, "sampling temperature; below 0 leaves it to the provider")
	flags.IntVar(&o.MaxTokens, "max-tokens", 4096, "cap on reply length, for providers that need one")
	// Loop Flags
	flags.IntVar(&o.MaxIterations, "max-iterations", 12, "cap on model calls")
	flags.DurationVar(&o.IterationTimeout, "iteration-timeout", time.Minute, "time allowed for one model call")
	flags.DurationVar(&o.TotalTimeout, "total-timeout", 10*time.Minute, "time allowed for the whole run")
	flags.StringVar(&o.StopCondition, "stop-condition", "no-tool-finish", "when the loop ends: no-tool-finish, finish-tool or external-finish")
	flags.StringVar(&o.Reflect, "reflect", "off", "when to reflect: off, on-iteration or on-finish")
	// Tool Flags
	flags.StringVar(&o.ToolsManifestPath, "tools", "", "path to a tool manifest (YAML)")
	flags.BoolVar(&o.AskUser, "ask-user", false, "give the model an ask_user tool")
	flags.BoolVar(&o.AutoApprove, "skip-confirm", false, "approve tools marked confirm without asking")
	// Output Flags
	flags.BoolVar(&o.Debug, "debug", false, "write the loop's debug log to stderr")
	flags.StringVar(&o.OutputRecordPath, "output-record-path", defaultRecordDir, `record every event as JSONL: a folder gets one new file per run, a path ending in .jsonl is used as is; a relative folder is under the repo root; "" turns recording off`)
}

// ParseFlags parses args and reports its own errors to stderr. It rejects
// leftover arguments: the flag package stops at the first one, so
// `--user-prompt fix the bug --debug` would otherwise run with the prompt
// "fix" and drop the rest without a word.
func ParseFlags(flags *flag.FlagSet, args []string, stderr io.Writer) error {
	flags.SetOutput(stderr)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		err := fmt.Errorf("unexpected argument %q: flags must come before it, and a value with spaces needs quotes", flags.Arg(0))
		fmt.Fprintf(stderr, "steerloop: %v\n", err)
		return err
	}
	return nil
}

// Validate rejects options the loop cannot run with, and flags that would
// be silently ignored. It reports every problem at once, not just the first.
// Files (--system-prompt, --tools) and the model spec are checked when the
// loops are built, still before any network call.
func (o Options) Validate() error {
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	// Model
	if strings.TrimSpace(o.Model) == "" {
		fail("--model is required")
	}
	if math.IsNaN(o.Temperature) || math.IsInf(o.Temperature, 0) {
		fail("--temperature must be a finite number")
	} else if o.Temperature > maxTemperature {
		fail("--temperature must be at most %d (below 0 leaves it to the provider)", maxTemperature)
	}
	if o.MaxTokens < 1 {
		fail("--max-tokens must be at least 1")
	}

	// Loop
	if o.MaxIterations < 1 {
		fail("--max-iterations must be at least 1")
	}
	if o.IterationTimeout < 0 {
		fail("--iteration-timeout must not be negative (0 means no limit)")
	}
	if o.TotalTimeout < 0 {
		fail("--total-timeout must not be negative (0 means no limit)")
	}
	if o.IterationTimeout > 0 && o.TotalTimeout > 0 && o.IterationTimeout > o.TotalTimeout {
		fail("--iteration-timeout (%s) must not exceed --total-timeout (%s)", o.IterationTimeout, o.TotalTimeout)
	}
	if _, ok := stopConditions[o.StopCondition]; !ok {
		fail("--stop-condition must be no-tool-finish, finish-tool or external-finish, not %q", o.StopCondition)
	}
	if _, ok := reflectConditions[o.Reflect]; !ok {
		fail("--reflect must be off, on-iteration or on-finish, not %q", o.Reflect)
	}
	if o.Reflect == "on-finish" && o.StopCondition == "external-finish" {
		fail("--reflect on-finish has no effect with --stop-condition external-finish: the model never finishes")
	}

	// Tools
	if o.AutoApprove && o.ToolsManifestPath == "" {
		fail("--skip-confirm has no effect without --tools")
	}

	return errors.Join(errs...)
}
