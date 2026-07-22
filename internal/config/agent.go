package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	DefaultAgentMaxIterations = 8
	maximumAgentMaxIterations = 32
)

type AgentConfig struct {
	MaxIterations int
}

func LoadAgent() (AgentConfig, error) {
	return loadAgent(os.Getenv)
}

func loadAgent(getenv func(string) string) (AgentConfig, error) {
	config := AgentConfig{MaxIterations: DefaultAgentMaxIterations}
	if value := strings.TrimSpace(getenv("OPENSVC_AI_AGENT_MAX_ITERATIONS")); value != "" {
		maxIterations, err := strconv.Atoi(value)
		if err != nil || maxIterations <= 0 || maxIterations > maximumAgentMaxIterations {
			return AgentConfig{}, fmt.Errorf("parse OPENSVC_AI_AGENT_MAX_ITERATIONS %q: expected an integer between 1 and %d", value, maximumAgentMaxIterations)
		}
		config.MaxIterations = maxIterations
	}
	return config, nil
}
