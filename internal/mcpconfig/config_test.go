package mcpconfig

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// cfg builds a normalised Config from servers keyed by their Name field, so
// tests can write a flat list instead of repeating each name as a map key.
func cfg(servers ...Server) Config {
	m := make(map[string]Server, len(servers))
	for _, s := range servers {
		m[s.Name] = s
	}
	return Config{Servers: m}.Normalize()
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		name  string
		input Config
		want  Config
	}{
		{
			name:  "nil servers",
			input: Config{},
			want:  Config{},
		},
		{
			name:  "empty server map collapses to nil",
			input: Config{Servers: map[string]Server{}},
			want:  Config{},
		},
		{
			name:  "missing type defaults to stdio and name is back-filled",
			input: Config{Servers: map[string]Server{"github": {Command: "npx"}}},
			want:  Config{Servers: map[string]Server{"github": {Name: "github", Type: TypeStdio, Command: "npx"}}},
		},
		{
			name: "empty collections collapse to nil",
			input: Config{Servers: map[string]Server{"github": {
				Name: "github", Type: TypeStdio, Command: "npx",
				Args: []string{}, Env: map[string]string{}, Headers: map[string]string{},
			}}},
			want: Config{Servers: map[string]Server{"github": {Name: "github", Type: TypeStdio, Command: "npx"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.input.Normalize(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Normalize() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{name: "empty", config: Config{}},
		{
			name: "stdio with args and env",
			config: cfg(Server{
				Name: "github", Type: TypeStdio, Command: "npx",
				Args: []string{"-y", "@modelcontextprotocol/server-github"},
				Env:  map[string]string{"GITHUB_TOKEN": "${env:GITHUB_TOKEN}"},
			}),
		},
		{
			name: "http with headers",
			config: cfg(Server{
				Name: "remote", Type: TypeHTTP, URL: "https://example.test/mcp",
				Headers: map[string]string{"Authorization": "${env:MCP_AUTH}"},
			}),
		},
		{
			name: "multiple servers",
			config: cfg(
				Server{Name: "github", Type: TypeStdio, Command: "npx", Args: []string{"-y", "server-github"}},
				Server{Name: "sse-feed", Type: TypeSSE, URL: "https://example.test/sse"},
				Server{Name: "files", Type: TypeStdio, Command: "mcp-filesystem", Env: map[string]string{"ROOT": "$HOME"}},
			),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.yaml")
			if err := Save(path, tt.config); err != nil {
				t.Fatalf("Save() = %v", err)
			}
			got, err := Load(path)
			if err != nil {
				t.Fatalf("Load() = %v", err)
			}
			if !reflect.DeepEqual(got, tt.config) {
				t.Errorf("Load(Save(c)) = %#v, want %#v", got, tt.config)
			}
		})
	}
}

// A phantom diff on every apply would make the watcher rewrite files in a loop,
// so byte-stability is part of the contract rather than a cosmetic concern.
func TestSaveIsDeterministic(t *testing.T) {
	c := cfg(
		Server{Name: "zeta", Type: TypeStdio, Command: "npx", Env: map[string]string{"ZZ": "$Z", "AA": "$A", "MM": "$M"}},
		Server{Name: "alpha", Type: TypeStdio, Command: "npx"},
		Server{Name: "middle", Type: TypeStdio, Command: "npx", Env: map[string]string{"B": "$B", "A": "$A"}},
	)
	dir := t.TempDir()

	var first []byte
	const runs = 8 // enough to shake out Go's randomised map iteration order
	for i := range runs {
		path := filepath.Join(dir, "mcp.yaml")
		if err := Save(path, c); err != nil {
			t.Fatalf("Save() = %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile() = %v", err)
		}
		if i == 0 {
			first = data
			continue
		}
		if !bytes.Equal(first, data) {
			t.Fatalf("Save() output differs between runs:\nrun 0:\n%s\nrun %d:\n%s", first, i, data)
		}
	}

	// Sorted order, not merely stable order — the file should read predictably.
	want := "servers:\n    alpha:\n"
	if !strings.HasPrefix(string(first), want) {
		t.Errorf("Save() output = %q, want it to start with %q", first, want)
	}
	if a, z := strings.Index(string(first), "AA:"), strings.Index(string(first), "ZZ:"); a > z {
		t.Errorf("env keys are not sorted:\n%s", first)
	}
}

func TestSaveRejectsLiteralSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.yaml")
	c := cfg(Server{
		Name: "github", Type: TypeStdio, Command: "npx",
		Env: map[string]string{"GITHUB_TOKEN": "ghp_0123456789abcdefghijklmnopqrstuvwxyz"},
	})
	err := Save(path, c)
	if err == nil {
		t.Fatal("Save() = nil, want a literal-credential error")
	}
	if !strings.Contains(err.Error(), "literal credential") {
		t.Errorf("Save() = %q, want it to mention a literal credential", err)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("Save() wrote the file despite refusing the config")
	}
}

func TestLoadRejectsLiteralSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.yaml")
	body := "servers:\n  github:\n    command: npx\n    env:\n      GITHUB_TOKEN: ghp_0123456789abcdefghijklmnopqrstuvwxyz\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile() = %v", err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "literal credential") {
		t.Errorf("Load() = %v, want a literal-credential error", err)
	}
}

func TestLoadErrors(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing file", func(t *testing.T) {
		if _, err := Load(filepath.Join(dir, "absent.yaml")); err == nil {
			t.Fatal("Load() = nil, want an error for a missing file")
		}
	})

	t.Run("malformed yaml", func(t *testing.T) {
		path := filepath.Join(dir, "bad.yaml")
		if err := os.WriteFile(path, []byte("servers: [oops\n"), 0o644); err != nil {
			t.Fatalf("WriteFile() = %v", err)
		}
		if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "parsing mcp config") {
			t.Errorf("Load() = %v, want a parse error", err)
		}
	})
}
