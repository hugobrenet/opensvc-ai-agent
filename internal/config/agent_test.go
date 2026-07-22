package config

import (
	"strings"
	"testing"
)

func TestLoadAgent(t *testing.T) {
	config, err := loadAgent(func(string) string { return "" })
	if err != nil {
		t.Fatalf("load default agent config: %v", err)
	}
	if config.MaxIterations != DefaultAgentMaxIterations {
		t.Fatalf("got max iterations %d, want %d", config.MaxIterations, DefaultAgentMaxIterations)
	}

	config, err = loadAgent(func(key string) string {
		if key == "OPENSVC_AI_AGENT_MAX_ITERATIONS" {
			return "12"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("load custom agent config: %v", err)
	}
	if config.MaxIterations != 12 {
		t.Fatalf("got max iterations %d, want 12", config.MaxIterations)
	}
}

func TestLoadAgentRejectsInvalidMaxIterations(t *testing.T) {
	for _, value := range []string{"invalid", "0", "33"} {
		_, err := loadAgent(func(string) string { return value })
		if err == nil || !strings.Contains(err.Error(), "between 1") {
			t.Fatalf("value %q error = %v", value, err)
		}
	}
}
