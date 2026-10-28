package bootstrap

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

func validOptions() Options {
	return Options{
		Model:            "script:steps.json",
		Temperature:      -1,
		MaxTokens:        4096,
		MaxIterations:    12,
		IterationTimeout: time.Minute,
		TotalTimeout:     10 * time.Minute,
		StopCondition:    "no-tool-finish",
		Reflect:          "off",
	}
}

func TestValidateAcceptsEdgeValues(t *testing.T) {
	for name, change := range map[string]func(*Options){
		"temperature 0":           func(o *Options) { o.Temperature = 0 },
		"temperature 2":           func(o *Options) { o.Temperature = 2 },
		"no timeouts":             func(o *Options) { o.IterationTimeout, o.TotalTimeout = 0, 0 },
		"iteration timeout only":  func(o *Options) { o.TotalTimeout = 0 },
		"skip-confirm with tools": func(o *Options) { o.AutoApprove, o.ToolsManifestPath = true, "tools.yaml" },
	} {
		opts := validOptions()
		change(&opts)
		if err := opts.Validate(); err != nil {
			t.Errorf("%s: rejected: %v", name, err)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Options)
		want   string
	}{
		{"missing model", func(o *Options) { o.Model = "" }, "--model is required"},
		{"blank model", func(o *Options) { o.Model = "  " }, "--model is required"},
		{"NaN temperature", func(o *Options) { o.Temperature = math.NaN() }, "--temperature must be a finite number"},
		{"infinite temperature", func(o *Options) { o.Temperature = math.Inf(-1) }, "--temperature must be a finite number"},
		{"temperature too high", func(o *Options) { o.Temperature = 2.5 }, "--temperature must be at most 2"},
		{"zero max tokens", func(o *Options) { o.MaxTokens = 0 }, "--max-tokens must be at least 1"},
		{"zero iterations", func(o *Options) { o.MaxIterations = 0 }, "--max-iterations must be at least 1"},
		{"negative iteration timeout", func(o *Options) { o.IterationTimeout = -time.Second }, "--iteration-timeout must not be negative"},
		{"negative total timeout", func(o *Options) { o.TotalTimeout = -time.Second }, "--total-timeout must not be negative"},
		{"iteration longer than total", func(o *Options) { o.IterationTimeout = time.Hour }, "--iteration-timeout (1h0m0s) must not exceed --total-timeout (10m0s)"},
		{"unknown stop condition", func(o *Options) { o.StopCondition = "never" }, `--stop-condition must be no-tool-finish, finish-tool or external-finish, not "never"`},
		{"unknown reflect", func(o *Options) { o.Reflect = "always" }, `--reflect must be off, on-iteration or on-finish, not "always"`},
		{"on-finish never runs", func(o *Options) { o.Reflect, o.StopCondition = "on-finish", "external-finish" }, "--reflect on-finish has no effect"},
		{"skip-confirm without tools", func(o *Options) { o.AutoApprove = true }, "--skip-confirm has no effect without --tools"},
	}
	for _, tc := range cases {
		opts := validOptions()
		tc.change(&opts)
		err := opts.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestInitialMessagesRejectsEmptySystemPrompt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "system.md")
	if err := os.WriteFile(path, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := validOptions()
	opts.SystemPromptPath = path
	if _, err := InitialMessages(opts); err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("got %v", err)
	}
}

// chat without --user-prompt starts from the system prompt alone.
func TestInitialMessagesWithoutAPrompt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "system.md")
	if err := os.WriteFile(path, []byte("be brief"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := validOptions()
	opts.SystemPromptPath = path
	messages, err := InitialMessages(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Role != schema.System {
		t.Fatalf("got %v", messages)
	}
}
