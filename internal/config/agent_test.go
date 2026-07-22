package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadAgent(t *testing.T) {
	config, err := loadAgent(func(string) string { return "" })
	if err != nil {
		t.Fatalf("load default agent config: %v", err)
	}
	if config.MaxIterations != DefaultAgentMaxIterations || config.Timeout != DefaultAgentTimeout {
		t.Fatalf("got config %+v, want max iterations %d and timeout %s", config, DefaultAgentMaxIterations, DefaultAgentTimeout)
	}

	config, err = loadAgent(func(key string) string {
		switch key {
		case "OPENSVC_AI_AGENT_MAX_ITERATIONS":
			return "12"
		case "OPENSVC_AI_AGENT_TIMEOUT":
			return "10m"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("load custom agent config: %v", err)
	}
	if config.MaxIterations != 12 || config.Timeout != 10*time.Minute {
		t.Fatalf("got custom config %+v", config)
	}
}

func TestLoadAgentRejectsInvalidTimeout(t *testing.T) {
	for _, value := range []string{"invalid", "0s", "500ms", "30m1s"} {
		_, err := loadAgent(func(key string) string {
			if key == "OPENSVC_AI_AGENT_TIMEOUT" {
				return value
			}
			return ""
		})
		if err == nil || !strings.Contains(err.Error(), "between 1s and 30m0s") {
			t.Fatalf("value %q error = %v", value, err)
		}
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
