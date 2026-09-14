package config

import (
	"strings"
	"testing"
)

func TestLoadMCPUnixSocket(t *testing.T) {
	for _, test := range []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "default", want: DefaultMCPSocketPath},
		{name: "explicit", value: " /run/opensvc-daemon-mcp/../opensvc-daemon-mcp/custom.sock ", want: "/run/opensvc-daemon-mcp/custom.sock"},
		{name: "relative", value: "mcp.sock", wantErr: true},
		{name: "root", value: "/", wantErr: true},
		{name: "too long", value: "/" + strings.Repeat("a", maximumUnixPathBytes), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, err := loadMCP(func(key string) string {
				if key != "OPENSVC_AI_MCP_SOCKET_PATH" {
					t.Fatalf("unexpected environment key %q", key)
				}
				return test.value
			})
			if test.wantErr {
				if err == nil {
					t.Fatalf("load MCP config succeeded with %+v, want error", config)
				}
				return
			}
			if err != nil {
				t.Fatalf("load MCP config: %v", err)
			}
			if config.SocketPath != test.want {
				t.Fatalf("got socket path %q, want %q", config.SocketPath, test.want)
			}
		})
	}
}
