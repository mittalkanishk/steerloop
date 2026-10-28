package runcmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestValidateAcceptsDefaults(t *testing.T) {
	opts, err := ParseOptions([]string{"--model", "script:steps.json", "--user-prompt", "hello"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if err := opts.Validate(); err != nil {
		t.Fatalf("defaults rejected: %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"missing prompt", nil, "--user-prompt is required"},
		{"blank prompt", []string{"--user-prompt", " \n\t"}, "--user-prompt is required"},
		{"unknown output", []string{"--user-prompt", "hello", "--output", "json"}, `--output must be text or jsonl, not "json"`},
	}
	for _, tc := range cases {
		opts, err := ParseOptions(append([]string{"--model", "script:steps.json"}, tc.args...), &bytes.Buffer{})
		if err != nil {
			t.Fatal(err)
		}
		err = opts.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	err := Options{}.Validate()
	if err == nil {
		t.Fatal("empty options accepted")
	}
	for _, want := range []string{"--user-prompt", "--model", "--max-tokens", "--max-iterations", "--stop-condition", "--output"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %s in:\n%v", want, err)
		}
	}
}

func TestParseOptionsRejectsLeftoverArguments(t *testing.T) {
	for _, args := range [][]string{
		{"--model", "script:s.json", "--user-prompt", "fix", "the", "bug"}, // unquoted prompt
		{"--model", "script:s.json", "hello", "--debug"},                   // flag after a stray word
	} {
		var stderr bytes.Buffer
		if _, err := ParseOptions(args, &stderr); err == nil {
			t.Errorf("%q: accepted", args)
		} else if !strings.Contains(stderr.String(), "unexpected argument") {
			t.Errorf("%q: stderr %q", args, stderr.String())
		}
	}
}
