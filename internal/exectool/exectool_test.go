package exectool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validManifest = `
tools:
  - name: say
    description: Print a message.
    parameters:
      type: object
      properties:
        message: { type: string }
        times: { type: integer }
        loud: { type: boolean }
        mode: { type: string, enum: [plain, fancy] }
      required: [message]
    command: ["echo", "{{.message}}"]
  - name: flagged
    description: Pass a value as an embedded option.
    parameters:
      type: object
      properties:
        level: { type: string }
    command: ["echo", "--level={{.level}}"]
    timeout: 5s
    max_output_bytes: 128
    confirm: true
`

func mustParse(t *testing.T, src string) *Manifest {
	t.Helper()
	m, err := Parse([]byte(src))
	require.NoError(t, err)
	return m
}

func TestParse_Valid(t *testing.T) {
	m := mustParse(t, validManifest)
	require.Len(t, m.Tools, 2)

	say, ok := m.Tool("say")
	require.True(t, ok)
	assert.Equal(t, defaultTimeout, say.Timeout)
	assert.Equal(t, defaultMaxOutputBytes, say.MaxOutputBytes)
	assert.False(t, say.Confirm)

	flagged, _ := m.Tool("flagged")
	assert.Equal(t, 5*time.Second, flagged.Timeout)
	assert.Equal(t, 128, flagged.MaxOutputBytes)
	assert.True(t, flagged.Confirm)

	_, ok = m.Tool("missing")
	assert.False(t, ok)

	infos, err := m.ToolInfos()
	require.NoError(t, err)
	require.Len(t, infos, 2)
	assert.Equal(t, "say", infos[0].Name)
	js, err := infos[0].ToJSONSchema()
	require.NoError(t, err)
	assert.Equal(t, []string{"message"}, js.Required)
}

func TestParse_ReportsEveryProblem(t *testing.T) {
	_, err := Parse([]byte(`
tools:
  - name: "bad name!"
    description: ""
    command: []
    timeout: soon
    max_output_bytes: -1
    parameters: { type: string }
  - name: finish
    description: Reserved.
    command: ["true"]
  - name: twice
    description: First.
    command: ["true"]
  - name: twice
    description: Second.
    command: ["{{.prog}}"]
  - name: broken_template
    description: Bad template.
    command: ["echo", "{{.unclosed"]
  - name: bad_props
    description: Bad properties.
    parameters: { type: object, properties: 7 }
    command: ["true"]
`))
	require.Error(t, err)
	for _, want := range []string{
		"name must be 1 to 64",
		"description is required",
		"command is required",
		`timeout "soon" is not a positive duration`,
		"max_output_bytes must not be negative",
		`"type" must be "object"`,
		`name "finish" is reserved`,
		"name is declared twice",
		"the program name cannot be a template",
		"command element 2",
		`"properties" must be a mapping`,
	} {
		assert.Contains(t, err.Error(), want)
	}
}

func TestParse_RejectsUnknownFieldsAndEmptyManifests(t *testing.T) {
	_, err := Parse([]byte("tools:\n  - name: x\n    descripton: typo\n    command: [\"true\"]\n"))
	assert.ErrorContains(t, err, "descripton")
	_, err = Parse([]byte("tools: []\n"))
	assert.ErrorContains(t, err, "no tools")
	_, err = Parse([]byte(":\n\t- not yaml"))
	assert.ErrorContains(t, err, "parse manifest")
}

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tools.yaml")
	require.NoError(t, os.WriteFile(path, []byte(validManifest), 0o600))
	m, err := Load(path)
	require.NoError(t, err)
	assert.Len(t, m.Tools, 2)

	_, err = Load(filepath.Join(t.TempDir(), "missing.yaml"))
	assert.Error(t, err)

	require.NoError(t, os.WriteFile(path, []byte("tools: []"), 0o600))
	_, err = Load(path)
	assert.ErrorContains(t, err, "tools.yaml")
}

func TestArgv_Validation(t *testing.T) {
	say, _ := mustParse(t, validManifest).Tool("say")
	for name, tc := range map[string]struct{ args, want string }{
		"not json":         {`{oops`, "not a JSON object"},
		"missing required": {`{}`, `missing required argument "message"`},
		"unknown argument": {`{"message":"hi","extra":1}`, `unknown argument "extra"`},
		"wrong string":     {`{"message":5}`, `"message" must be of type string`},
		"wrong integer":    {`{"message":"hi","times":1.5}`, `"times" must be of type integer`},
		"wrong boolean":    {`{"message":"hi","loud":"yes"}`, `"loud" must be of type boolean`},
		"not in enum":      {`{"message":"hi","mode":"neon"}`, `"mode" is not one of the allowed values`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := say.Argv(tc.args)
			assert.ErrorContains(t, err, tc.want)
		})
	}

	argv, err := say.Argv(`{"message":"hi","times":3,"loud":true,"mode":"fancy"}`)
	require.NoError(t, err)
	assert.Equal(t, []string{"echo", "hi"}, argv)
}

func TestArgv_TypesAndOptionalParameters(t *testing.T) {
	m := mustParse(t, `
tools:
  - name: t
    description: Types.
    parameters:
      type: object
      properties:
        n: { type: number }
        list: { type: array }
        obj: { type: object }
        opt: { type: string }
    command: ["echo", "n={{.n}}", "list={{.list}}", "obj={{.obj}}", "opt={{.opt}}"]
`)
	tool, _ := m.Tool("t")
	argv, err := tool.Argv(`{"n":1.5,"list":[1,"a"],"obj":{"k":true}}`)
	require.NoError(t, err)
	assert.Equal(t, []string{"echo", "n=1.5", `list=[1,"a"]`, `obj={"k":true}`, "opt="}, argv)

	_, err = tool.Argv(`{"list":"no"}`)
	assert.ErrorContains(t, err, "must be of type array")
	_, err = tool.Argv(`{"obj":[]}`)
	assert.ErrorContains(t, err, "must be of type object")
	_, err = tool.Argv(`{"n":"1"}`)
	assert.ErrorContains(t, err, "must be of type number")

	argv, err = tool.Argv("")
	require.NoError(t, err, "empty arguments mean no arguments")
	assert.Equal(t, "n=", argv[1])
}

// A value can never add a command-line element or reach a shell.
func TestArgv_ValueStaysOneElement(t *testing.T) {
	say, _ := mustParse(t, validManifest).Tool("say")
	hostile := `a b; rm -rf / && $(id) | cat > x "q" 'q' *`
	argv, err := say.Argv(`{"message":` + quote(hostile) + `}`)
	require.NoError(t, err)
	require.Len(t, argv, 2)
	assert.Equal(t, hostile, argv[1])

	out, err := say.Run(context.Background(), `{"message":`+quote(hostile)+`}`)
	require.NoError(t, err)
	assert.Equal(t, hostile+"\n", out, "metacharacters reach the program as literal text")
}

func TestArgv_LeadingDashIsRejectedUnlessAllowed(t *testing.T) {
	m := mustParse(t, validManifest)
	say, _ := m.Tool("say")
	_, err := say.Argv(`{"message":"--pre=evil"}`)
	assert.ErrorContains(t, err, "would be read as an option")

	flagged, _ := m.Tool("flagged")
	argv, err := flagged.Argv(`{"level":"-1"}`)
	require.NoError(t, err, "a value embedded after fixed text cannot become an option")
	assert.Equal(t, []string{"echo", "--level=-1"}, argv)

	allowed := mustParse(t, `
tools:
  - name: raw
    description: Raw.
    parameters: { type: object, properties: { arg: { type: string } } }
    command: ["echo", "{{.arg}}"]
    allow_flag_values: true
`)
	raw, _ := allowed.Tool("raw")
	argv, err = raw.Argv(`{"arg":"-n"}`)
	require.NoError(t, err)
	assert.Equal(t, []string{"echo", "-n"}, argv)
}

func TestRun_NonZeroExitTimeoutAndOutputCap(t *testing.T) {
	m := mustParse(t, `
tools:
  - name: fail
    description: Exit non-zero after printing.
    command: ["sh", "-c", "echo before; echo problem >&2; exit 3"]
  - name: slow
    description: Sleep.
    command: ["sleep", "5"]
    timeout: 100ms
  - name: noisy
    description: Print a lot.
    command: ["sh", "-c", "head -c 5000 /dev/zero | tr '\\0' x"]
    max_output_bytes: 100
  - name: where
    description: Print the working directory.
    command: ["pwd"]
    workdir: /
  - name: ghost
    description: Program that does not exist.
    command: ["definitely-not-a-real-program-xyz"]
`)
	ctx := context.Background()

	out, err := m.ExecuteTool(ctx, "fail", "{}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit status 3")
	assert.Contains(t, out, "before")
	assert.Contains(t, out, "problem", "stderr is captured too")

	start := time.Now()
	_, err = m.ExecuteTool(ctx, "slow", "{}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out after 100ms")
	assert.Less(t, time.Since(start), 4*time.Second)

	out, err = m.ExecuteTool(ctx, "noisy", "{}")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(out, strings.Repeat("x", 100)))
	assert.Contains(t, out, "[output truncated at 100 bytes]")
	assert.Less(t, len(out), 200)

	out, err = m.ExecuteTool(ctx, "where", "{}")
	require.NoError(t, err)
	assert.Equal(t, "/\n", out)

	_, err = m.ExecuteTool(ctx, "ghost", "{}")
	assert.Error(t, err)
	_, err = m.ExecuteTool(ctx, "undeclared", "{}")
	assert.ErrorContains(t, err, `unknown tool "undeclared"`)
}

func TestRun_CancelledContext(t *testing.T) {
	m := mustParse(t, "tools:\n  - name: slow\n    description: Sleep.\n    command: [\"sleep\", \"5\"]\n")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := m.ExecuteTool(ctx, "slow", "{}")
	require.Error(t, err)
	assert.Less(t, time.Since(start), 4*time.Second)
}

func quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
