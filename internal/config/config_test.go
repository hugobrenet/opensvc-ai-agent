package config

import "testing"

func TestLoad(t *testing.T) {
	for _, test := range []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "default", want: DefaultListenAddress},
		{name: "ipv4 loopback", value: "127.0.0.2:9000", want: "127.0.0.2:9000"},
		{name: "ipv6 loopback", value: "[::1]:9000", want: "[::1]:9000"},
		{name: "non loopback", value: "0.0.0.0:8090", wantErr: true},
		{name: "hostname", value: "localhost:8090", wantErr: true},
		{name: "invalid", value: "127.0.0.1", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, err := load(func(key string) string {
				if key == "OPENSVC_AI_LISTEN_ADDRESS" {
					return test.value
				}
				return ""
			})
			if test.wantErr {
				if err == nil {
					t.Fatalf("load succeeded with %+v, want error", config)
				}
				return
			}
			if err != nil {
				t.Fatalf("load config: %v", err)
			}
			if config.ListenAddress != test.want {
				t.Errorf("got listen address %q, want %q", config.ListenAddress, test.want)
			}
			if config.MaxConcurrentAsks != DefaultMaxConcurrentAsks {
				t.Errorf("got max concurrent asks %d, want %d", config.MaxConcurrentAsks, DefaultMaxConcurrentAsks)
			}
		})
	}
}

func TestLoadMaxConcurrentAsks(t *testing.T) {
	config, err := load(func(key string) string {
		if key == "OPENSVC_AI_MAX_CONCURRENT_ASKS" {
			return "12"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if config.MaxConcurrentAsks != 12 {
		t.Fatalf("got max concurrent asks %d, want 12", config.MaxConcurrentAsks)
	}

	for _, value := range []string{"invalid", "0", "129"} {
		_, err := load(func(key string) string {
			if key == "OPENSVC_AI_MAX_CONCURRENT_ASKS" {
				return value
			}
			return ""
		})
		if err == nil {
			t.Fatalf("value %q succeeded", value)
		}
	}
}
