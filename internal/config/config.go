package config

import (
	"fmt"
	"net"
	"os"
	"strings"
)

const DefaultListenAddress = "127.0.0.1:8090"

type Config struct {
	ListenAddress string
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
	return Config{ListenAddress: listenAddress}, nil
}
