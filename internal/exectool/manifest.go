// Package exectool runs command-line tools declared in a YAML manifest.
package exectool

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/cloudwego/eino/schema"
	"gopkg.in/yaml.v3"

	"github.com/Kanishkmittal55/steerloop"
)

const (
	defaultTimeout        = 30 * time.Second
	defaultMaxOutputBytes = 64 * 1024
)

var toolNameRE = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// Manifest is a parsed tool manifest.
type Manifest struct {
	Tools []*Tool
}

// Tool is one declared command-line tool.
type Tool struct {
	Name           string
	Description    string
	Parameters     map[string]any
	Command        []string
	Timeout        time.Duration
	Workdir        string
	MaxOutputBytes int
	Confirm        bool
	// AllowFlagValues lets an argument value begin with "-". Off by default,
	// so a model cannot turn a value into a command-line option.
	AllowFlagValues bool

	templates []*template.Template
}

type rawManifest struct {
	Tools []rawTool `yaml:"tools"`
}

type rawTool struct {
	Name            string         `yaml:"name"`
	Description     string         `yaml:"description"`
	Parameters      map[string]any `yaml:"parameters"`
	Command         []string       `yaml:"command"`
	Timeout         string         `yaml:"timeout"`
	Workdir         string         `yaml:"workdir"`
	MaxOutputBytes  int            `yaml:"max_output_bytes"`
	Confirm         bool           `yaml:"confirm"`
	AllowFlagValues bool           `yaml:"allow_flag_values"`
}

// Load reads and validates a manifest file.
func Load(path string) (*Manifest, error) {
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
func Parse(data []byte) (*Manifest, error) {
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
	m := &Manifest{}
	for i, rt := range raw.Tools {
		label := fmt.Sprintf("tool %d (%s)", i+1, rt.Name)
		fail := func(format string, args ...any) {
			problems = append(problems, label+": "+fmt.Sprintf(format, args...))
		}

		t := &Tool{
			Name:            rt.Name,
			Description:     rt.Description,
			Parameters:      rt.Parameters,
			Command:         rt.Command,
			Workdir:         rt.Workdir,
			MaxOutputBytes:  rt.MaxOutputBytes,
			Confirm:         rt.Confirm,
			AllowFlagValues: rt.AllowFlagValues,
			Timeout:         defaultTimeout,
		}

		switch {
		case !toolNameRE.MatchString(rt.Name):
			fail("name must be 1 to 64 letters, digits, underscores or hyphens")
		case rt.Name == steerloop.FinishToolName:
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

// Tool returns the tool with the given name.
func (m *Manifest) Tool(name string) (*Tool, bool) {
	for _, t := range m.Tools {
		if t.Name == name {
			return t, true
		}
	}
	return nil, false
}

// ToolInfos returns the Eino definitions of every tool, for binding to a model.
func (m *Manifest) ToolInfos() ([]*schema.ToolInfo, error) {
	infos := make([]*schema.ToolInfo, 0, len(m.Tools))
	for _, t := range m.Tools {
		info, err := steerloop.NewToolInfo(t.Name, t.Description, t.Parameters)
		if err != nil {
			return nil, err
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// properties returns the declared parameter schemas by name.
func (t *Tool) properties() map[string]map[string]any {
	out := map[string]map[string]any{}
	props, _ := t.Parameters["properties"].(map[string]any)
	for name, v := range props {
		if spec, ok := v.(map[string]any); ok {
			out[name] = spec
		} else {
			out[name] = map[string]any{}
		}
	}
	return out
}

func (t *Tool) required() []string {
	switch req := t.Parameters["required"].(type) {
	case []string:
		return req
	case []any:
		out := make([]string, 0, len(req))
		for _, r := range req {
			if s, ok := r.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
