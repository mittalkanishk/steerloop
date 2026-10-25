// Package exectool runs command-line tools declared in a YAML manifest.
package exectool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/cloudwego/eino/schema"
	"gopkg.in/yaml.v3"

	"github.com/mittalkanishk/steerloop/loop"
)

const (
	defaultTimeout        = 30 * time.Second
	defaultMaxOutputBytes = 64 * 1024
)

var toolNameRE = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// Manifest is a parsed tool manifest.
type ToolManifestExecutor struct {
	Tools []*Tool
}

// ExecuteTool runs the named tool with JSON arguments. It satisfies
// loop.ToolExecutor.
func (m *ToolManifestExecutor) ExecuteTool(ctx context.Context, name string, arguments string) (string, error) {
	t, ok := m.Tool(name)
	if !ok {
		return "", fmt.Errorf("unknown tool %q", name)
	}
	argv, err := t.Argv(arguments)
	if err != nil {
		return "", err
	}

	runCtx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()

	out := &cappedBuffer{
		limit: t.MaxOutputBytes,
	}
	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	cmd.Dir = t.Workdir
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.WaitDelay = 2 * time.Second

	runErr := cmd.Run()
	output := out.String()
	switch {
	case runErr == nil:
		return output, nil
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		return output, fmt.Errorf("timed out after %s\n%s", t.Timeout, output)
	default:
		return output, fmt.Errorf("%v\n%s", runErr, output)
	}
}

// Tool returns the tool with the given name.
func (m *ToolManifestExecutor) Tool(name string) (*Tool, bool) {
	for _, t := range m.Tools {
		if t.Name == name {
			return t, true
		}
	}
	return nil, false
}

// ToolInfos returns the Eino definitions of every tool, for binding to a model.
func (m *ToolManifestExecutor) ToolInfos() ([]*schema.ToolInfo, error) {
	infos := make([]*schema.ToolInfo, 0, len(m.Tools))
	for _, t := range m.Tools {
		info, err := loop.NewToolInfo(t.Name, t.Description, t.Parameters)
		if err != nil {
			return nil, err
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// Load reads and validates a manifest file.
func Load(path string) (*ToolManifestExecutor, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// Parse decodes and validates a manifest. Every problem found is reported, not
// only the first.
func Parse(data []byte) (*ToolManifestExecutor, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var raw rawManifest
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if len(raw.Tools) == 0 {
		return nil, errors.New("manifest declares no tools")
	}

	var problems []string
	seen := map[string]bool{}
	m := &ToolManifestExecutor{}
	for i, rt := range raw.Tools {
		label := fmt.Sprintf("tool %d (%s)", i+1, rt.Name)
		fail := func(format string, args ...any) {
			problems = append(problems, label+": "+fmt.Sprintf(format, args...))
		}

		t := &Tool{
			Name:                 rt.Name,
			Description:          rt.Description,
			Parameters:           rt.Parameters,
			Command:              rt.Command,
			Workdir:              rt.Workdir,
			MaxOutputBytes:       rt.MaxOutputBytes,
			RequiresConfirmation: rt.Confirm,
			AllowFlagValues:      rt.AllowFlagValues,
			Timeout:              defaultTimeout,
		}

		switch {
		case !toolNameRE.MatchString(rt.Name):
			fail("name must be 1 to 64 letters, digits, underscores or hyphens")
		case rt.Name == loop.FinishToolName:
			fail("name %q is reserved", rt.Name)
		case seen[rt.Name]:
			fail("name is declared twice")
		}
		seen[rt.Name] = true

		if strings.TrimSpace(rt.Description) == "" {
			fail("description is required")
		}
		if len(rt.Command) == 0 {
			fail("command is required")
		}
		if rt.Timeout != "" {
			d, err := time.ParseDuration(rt.Timeout)
			if err != nil || d <= 0 {
				fail("timeout %q is not a positive duration", rt.Timeout)
			} else {
				t.Timeout = d
			}
		}
		if rt.MaxOutputBytes < 0 {
			fail("max_output_bytes must not be negative")
		}
		if t.MaxOutputBytes == 0 {
			t.MaxOutputBytes = defaultMaxOutputBytes
		}
		if err := checkParameters(rt.Parameters); err != nil {
			fail("parameters: %v", err)
		}

		for j, arg := range rt.Command {
			tmpl, err := template.New("arg").Option("missingkey=error").Parse(arg)
			if err != nil {
				fail("command element %d: %v", j+1, err)
				continue
			}
			t.templates = append(t.templates, tmpl)
		}
		if len(rt.Command) > 0 && strings.Contains(rt.Command[0], "{{") {
			fail("the program name cannot be a template")
		}
		m.Tools = append(m.Tools, t)
	}

	if len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "\n"))
	}
	return m, nil
}

func checkParameters(params map[string]any) error {
	if params == nil {
		return nil
	}
	if typ, _ := params["type"].(string); typ != "object" {
		return errors.New(`"type" must be "object"`)
	}
	if props, ok := params["properties"]; ok {
		if _, isMap := props.(map[string]any); !isMap {
			return errors.New(`"properties" must be a mapping`)
		}
	}
	return nil
}
