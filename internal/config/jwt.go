package config

import (
	"os"
	"strings"
)

const DefaultJWTVerifyKeyFile = "/var/lib/opensvc/certs/ca_certificates"

type JWTConfig struct {
	VerifyKeyFile string
}

func LoadJWT() JWTConfig {
	return loadJWT(os.Getenv)
}

func loadJWT(getenv func(string) string) JWTConfig {
	verifyKeyFile := strings.TrimSpace(getenv("OPENSVC_AI_JWT_VERIFY_KEY_FILE"))
	if verifyKeyFile == "" {
		verifyKeyFile = DefaultJWTVerifyKeyFile
	}
	return JWTConfig{VerifyKeyFile: verifyKeyFile}
}
