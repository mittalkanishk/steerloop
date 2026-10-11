package provider

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestBuild_Script(t *testing.T) {
	path := filepath.Join(t.TempDir(), "demo.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"steps":[{"content":"hi"}]}`), 0o600))
	m, err := Build(context.Background(), "script:"+path, Options{Getenv: env(nil)})
	require.NoError(t, err)
	assert.Equal(t, "script", m.Provider)
	assert.Equal(t, "demo.json", m.Name)
	msg, err := m.Chat.Generate(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, "hi", msg.Content)

	_, err = Build(context.Background(), "script:/no/such/file.json", Options{Getenv: env(nil)})
	assert.Error(t, err)
}

func TestBuild_BadSpec(t *testing.T) {
	for _, spec := range []string{"", "anthropic", "anthropic:", "mystery:model"} {
		_, err := Build(context.Background(), spec, Options{Getenv: env(nil)})
		assert.Error(t, err, spec)
	}
	_, err := Build(context.Background(), "mystery:model", Options{Getenv: env(nil)})
	assert.ErrorContains(t, err, "unknown provider")
}

// A missing key fails before any model client is created.
func TestBuild_MissingKeys(t *testing.T) {
	_, err := Build(context.Background(), "anthropic:some-model", Options{Getenv: env(nil)})
	assert.EqualError(t, err, "ANTHROPIC_API_KEY is not set")
	_, err = Build(context.Background(), "openai:some-model", Options{Getenv: env(nil)})
	assert.EqualError(t, err, "OPENAI_API_KEY is not set")
}

// With a key present the client is built without contacting the provider.
func TestBuild_RealProvidersConstructOffline(t *testing.T) {
	vars := map[string]string{"ANTHROPIC_API_KEY": "test-key", "OPENAI_API_KEY": "test-key"}
	a, err := Build(context.Background(), "anthropic:some-model", Options{Getenv: env(vars)})
	require.NoError(t, err)
	assert.Equal(t, "anthropic", a.Provider)
	assert.Equal(t, "some-model", a.Name)

	o, err := Build(context.Background(), "openai:some-model", Options{Getenv: env(vars), MaxTokens: 100})
	require.NoError(t, err)
	assert.Equal(t, "openai", o.Provider)
}
