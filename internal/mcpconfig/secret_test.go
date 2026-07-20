package mcpconfig

import (
	"strings"
	"testing"
)

func TestIsIndirection(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"${env:GITHUB_TOKEN}", true},
		{"${GITHUB_TOKEN}", true},
		{"$GITHUB_TOKEN", true},
		{"$_private", true},
		{"", false},
		{"$", false},
		{"$1TOKEN", false},                   // shell names cannot start with a digit
		{"Bearer ${env:TOKEN}", false},       // must be the whole value, not embedded
		{"${env:GITHUB_TOKEN} ", false},      // trailing space would be passed through literally
		{"${env:GITHUB TOKEN}", false},       //nolint:misspell // space is not legal in a variable name
		{"ghp_16CharsOfNonsense0123", false}, // a literal, not a reference
		{"npx", false},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			if got := IsIndirection(tt.value); got != tt.want {
				t.Errorf("IsIndirection(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestLooksSecretDetectsLiterals(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"anthropic key", "sk-ant-api03-abcdef0123456789"},
		{"openai key", "sk-proj-0123456789abcdef"},
		{"github classic pat", "ghp_0123456789abcdefghijklmnopqrstuvwxyz"},
		{"github oauth token", "gho_0123456789abcdefghij"},
		{"github fine grained pat", "github_pat_11ABCDE0123456789"},
		{"aws access key id", "AKIAIOSFODNN7EXAMPLE"},
		{"slack bot token", "xoxb-1234-5678-abcdefg"},
		{"slack user token", "xoxp-1234-5678-abcdefg"},
		{"google api key", "AIzaSyD-0123456789abcdefghij"},
		{"short vendor prefix still caught", "sk-abc"},
		{"generic base64 blob", "aB3xK9mQ2pL7wR4tY8vZ"},
		{"generic hex digest", "9f4a1c7e2b8d3506af71c9e4d2b60837"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !LooksSecret(tt.value) {
				t.Errorf("LooksSecret(%q) = false, want true", tt.value)
			}
		})
	}
}

// Guarding against false positives matters as much as detection: a guard that
// fires on ordinary values gets routed around, and then it protects nothing.
func TestLooksSecretIgnoresOrdinaryValues(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"command", "npx"},
		{"flag", "-y"},
		{"npm package", "@modelcontextprotocol/server-github"},
		{"long package name", "modelcontextprotocol"},
		{"plain words", "enable verbose logging please"},
		{"boolean", "true"},
		{"number", "8080"},
		{"url", "https://api.githubcopilot.com/mcp/"},
		{"absolute path", "/home/philip/workspace/projects/weft"},
		{"home path", "~/workspace/projects/notes-and-drafts"},
		{"relative path", "./scripts/run-the-mcp-server.sh"},
		{"windows path", `C:\Users\philip\Documents\projects`},
		{"indirection", "${env:GITHUB_TOKEN}"},
		{"log level", "debug"},
		{"comma separated list", "read,write,execute,delete"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if LooksSecret(tt.value) {
				t.Errorf("LooksSecret(%q) = true, want false (entropy %.2f)", tt.value, shannonEntropy(tt.value))
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr string // substring; empty means the config must validate
	}{
		{
			name:   "empty config",
			config: Config{},
		},
		{
			name: "stdio with indirection env",
			config: cfg(Server{
				Name: "github", Type: TypeStdio, Command: "npx",
				Args: []string{"-y", "@modelcontextprotocol/server-github"},
				Env:  map[string]string{"GITHUB_TOKEN": "${env:GITHUB_TOKEN}"},
			}),
		},
		{
			name: "literal credential in env",
			config: cfg(Server{
				Name: "github", Type: TypeStdio, Command: "npx",
				Env: map[string]string{"GITHUB_TOKEN": "ghp_0123456789abcdefghijklmnopqrstuvwxyz"},
			}),
			wantErr: `mcp server "github": env GITHUB_TOKEN holds a literal credential — replace it with "${env:GITHUB_TOKEN}" and export the value in your shell`,
		},
		{
			name: "literal credential in header",
			config: cfg(Server{
				Name: "remote", Type: TypeHTTP, URL: "https://example.test/mcp",
				Headers: map[string]string{"X-Api-Key": "sk-ant-api03-abcdef0123456789"},
			}),
			wantErr: `header X-Api-Key holds a literal credential — replace it with "${env:X_API_KEY}"`,
		},
		{
			name:    "stdio without command",
			config:  cfg(Server{Name: "broken", Type: TypeStdio}),
			wantErr: "stdio servers need a command",
		},
		{
			name:    "http without url",
			config:  cfg(Server{Name: "broken", Type: TypeHTTP}),
			wantErr: "http servers need a url",
		},
		{
			name:    "unknown transport",
			config:  cfg(Server{Name: "broken", Type: "grpc", Command: "x"}),
			wantErr: `unknown type "grpc"`,
		},
		{
			name:    "stdio carrying remote fields",
			config:  cfg(Server{Name: "mixed", Type: TypeStdio, Command: "npx", URL: "https://example.test"}),
			wantErr: "stdio servers cannot set url or headers",
		},
		{
			name:    "http carrying stdio fields",
			config:  cfg(Server{Name: "mixed", Type: TypeHTTP, URL: "https://example.test", Command: "npx"}),
			wantErr: "http servers cannot set command, args or env",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestEnvVarName(t *testing.T) {
	tests := []struct{ key, want string }{
		{"GITHUB_TOKEN", "GITHUB_TOKEN"},
		{"Authorization", "AUTHORIZATION"},
		{"X-Api-Key", "X_API_KEY"},
		{"api.key", "API_KEY"},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if got := envVarName(tt.key); got != tt.want {
				t.Errorf("envVarName(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}
