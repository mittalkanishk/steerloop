package exectool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// ExecuteTool runs the named tool with JSON arguments. It satisfies
// steerloop.ToolExecutor.
func (m *Manifest) ExecuteTool(ctx context.Context, name string, arguments string) (string, error) {
	t, ok := m.Tool(name)
	if !ok {
		return "", fmt.Errorf("unknown tool %q", name)
	}
	return t.Run(ctx, arguments)
}

// Run validates the arguments, builds the command line and runs it. Output is
// stdout followed by stderr, capped at MaxOutputBytes. A non-zero exit or a
// timeout is an error that still carries the output.
func (t *Tool) Run(ctx context.Context, arguments string) (string, error) {
	argv, err := t.Argv(arguments)
	if err != nil {
		return "", err
	}

	runCtx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()

	out := &cappedBuffer{limit: t.MaxOutputBytes}
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

// cappedBuffer keeps the first limit bytes written to it.
type cappedBuffer struct {
	limit     int
	buf       bytes.Buffer
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	room := c.limit - c.buf.Len()
	if room <= 0 {
		c.truncated = c.truncated || len(p) > 0
		return len(p), nil
	}
	if len(p) > room {
		c.buf.Write(p[:room])
		c.truncated = true
		return len(p), nil
	}
	c.buf.Write(p)
	return len(p), nil
}

func (c *cappedBuffer) String() string {
	if c.truncated {
		return c.buf.String() + fmt.Sprintf("\n[output truncated at %d bytes]", c.limit)
	}
	return c.buf.String()
}
