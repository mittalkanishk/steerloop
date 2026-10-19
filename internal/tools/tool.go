package exectool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"text/template"
	"time"
)

// Tool is one declared command-line tool.
type Tool struct {
	Name                 string
	Description          string
	Parameters           map[string]any
	Command              []string
	Timeout              time.Duration
	Workdir              string
	MaxOutputBytes       int
	RequiresConfirmation bool
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

// Argv validates JSON arguments against the tool's parameters and expands the
// command templates. Each template expands inside its own element, so an
// argument value can never add an element or reach a shell.
func (t *Tool) Argv(arguments string) ([]string, error) {
	args := map[string]any{}
	if strings.TrimSpace(arguments) != "" {
		dec := json.NewDecoder(strings.NewReader(arguments))
		dec.UseNumber()
		if err := dec.Decode(&args); err != nil {
			return nil, fmt.Errorf("arguments are not a JSON object: %w", err)
		}
	}
	if err := t.validate(args); err != nil {
		return nil, err
	}

	// Every declared parameter is defined for the templates; an optional one
	// that was not supplied expands to the empty string.
	values := map[string]string{}
	for name := range t.properties() {
		values[name] = ""
	}
	for name, v := range args {
		values[name] = stringify(v)
	}

	argv := make([]string, 0, len(t.templates))
	for i, tmpl := range t.templates {
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, values); err != nil {
			return nil, fmt.Errorf("command element %d: %w", i+1, err)
		}
		rendered := buf.String()
		templated := strings.Contains(t.Command[i], "{{")
		if templated && !t.AllowFlagValues && strings.HasPrefix(strings.TrimSpace(t.Command[i]), "{{") && strings.HasPrefix(rendered, "-") {
			return nil, fmt.Errorf("argument %q starts with \"-\" and would be read as an option", rendered)
		}
		argv = append(argv, rendered)
	}
	return argv, nil
}

func (t *Tool) validate(args map[string]any) error {
	props := t.properties()
	for _, name := range t.required() {
		if _, ok := args[name]; !ok {
			return fmt.Errorf("missing required argument %q", name)
		}
	}

	names := make([]string, 0, len(args))
	for name := range args {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		spec, declared := props[name]
		if !declared {
			return fmt.Errorf("unknown argument %q", name)
		}
		if err := checkType(name, args[name], spec); err != nil {
			return err
		}
	}
	return nil
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

func checkType(name string, value any, spec map[string]any) error {
	want, _ := spec["type"].(string)
	ok := true
	switch want {
	case "string":
		_, ok = value.(string)
	case "boolean":
		_, ok = value.(bool)
	case "number":
		_, ok = value.(json.Number)
	case "integer":
		n, isNum := value.(json.Number)
		ok = isNum
		if isNum {
			f, err := n.Float64()
			ok = err == nil && f == math.Trunc(f)
		}
	case "array":
		_, ok = value.([]any)
	case "object":
		_, ok = value.(map[string]any)
	}
	if !ok {
		return fmt.Errorf("argument %q must be of type %s", name, want)
	}

	if enum, hasEnum := spec["enum"].([]any); hasEnum {
		for _, allowed := range enum {
			if stringify(allowed) == stringify(value) {
				return nil
			}
		}
		return fmt.Errorf("argument %q is not one of the allowed values", name)
	}
	return nil
}

func stringify(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case nil:
		return ""
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x)
		}
		return string(b)
	}
}
