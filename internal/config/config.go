package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

const (
	DefaultListenAddress     = "127.0.0.1:8090"
	DefaultMaxConcurrentAsks = 4
	maximumMaxConcurrentAsks = 128
)

type Config struct {
	ListenAddress     string
	MaxConcurrentAsks int
}

func Load() (Config, error) {
	return load(os.Getenv)
}

func load(getenv func(string) string) (Config, error) {
	listenAddress := strings.TrimSpace(getenv("OPENSVC_AI_LISTEN_ADDRESS"))
	if listenAddress == "" {
		listenAddress = DefaultListenAddress
	}
	host, _, err := net.SplitHostPort(listenAddress)
	if err != nil {
		return Config{}, fmt.Errorf("parse OPENSVC_AI_LISTEN_ADDRESS: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return Config{}, fmt.Errorf("OPENSVC_AI_LISTEN_ADDRESS must use a loopback IP")
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
	return Config{ListenAddress: listenAddress, MaxConcurrentAsks: maxConcurrentAsks}, nil
}
