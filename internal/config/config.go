package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultSocketPath        = "/run/opensvc-ai-agent/agent.sock"
	DefaultMaxConcurrentAsks = 4
	DefaultShutdownTimeout   = 30 * time.Second
	maximumUnixPathBytes     = 107
	maximumMaxConcurrentAsks = 128
	minimumShutdownTimeout   = time.Second
	maximumShutdownTimeout   = 5 * time.Minute
)

type Config struct {
	SocketPath        string
	MaxConcurrentAsks int
	ShutdownTimeout   time.Duration
}

func Load() (Config, error) {
	return load(os.Getenv)
}

func load(getenv func(string) string) (Config, error) {
	socketPath := strings.TrimSpace(getenv("OPENSVC_AI_SOCKET_PATH"))
	if socketPath == "" {
		socketPath = DefaultSocketPath
	}
	var err error
	socketPath, err = cleanUnixSocketPath(socketPath)
	if err != nil {
		return Config{}, fmt.Errorf("parse OPENSVC_AI_SOCKET_PATH: %w", err)
	}
	maxConcurrentAsks := DefaultMaxConcurrentAsks
	if value := strings.TrimSpace(getenv("OPENSVC_AI_MAX_CONCURRENT_ASKS")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > maximumMaxConcurrentAsks {
			return Config{}, fmt.Errorf(
				"parse OPENSVC_AI_MAX_CONCURRENT_ASKS %q: expected an integer between 1 and %d",
				value,
				maximumMaxConcurrentAsks,
			)
		}
		maxConcurrentAsks = parsed
	}
	shutdownTimeout := DefaultShutdownTimeout
	if value := strings.TrimSpace(getenv("OPENSVC_AI_SHUTDOWN_TIMEOUT")); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed < minimumShutdownTimeout || parsed > maximumShutdownTimeout {
			return Config{}, fmt.Errorf(
				"parse OPENSVC_AI_SHUTDOWN_TIMEOUT %q: expected a duration between %s and %s",
				value,
				minimumShutdownTimeout,
				maximumShutdownTimeout,
			)
		}
		shutdownTimeout = parsed
	}
	return Config{
		SocketPath:        socketPath,
		MaxConcurrentAsks: maxConcurrentAsks,
		ShutdownTimeout:   shutdownTimeout,
	}, nil
}

func cleanUnixSocketPath(value string) (string, error) {
	path := filepath.Clean(value)
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path must be absolute")
	}
	if path == string(filepath.Separator) {
		return "", fmt.Errorf("path must name a socket")
	}
	if len([]byte(path)) > maximumUnixPathBytes {
		return "", fmt.Errorf("path exceeds the Linux Unix socket limit of %d bytes", maximumUnixPathBytes)
	}
	return path, nil
}
