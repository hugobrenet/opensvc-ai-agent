package config

import "testing"

func TestLoadJWT(t *testing.T) {
	config := loadJWT(func(string) string { return "" })
	if config.VerifyKeyFile != DefaultJWTVerifyKeyFile {
		t.Fatalf("got default verification key file %q", config.VerifyKeyFile)
	}

	config = loadJWT(func(key string) string {
		if key != "OPENSVC_AI_JWT_VERIFY_KEY_FILE" {
			t.Fatalf("unexpected environment key %q", key)
		}
		return "  /tmp/cluster-ca.pem  "
	})
	if config.VerifyKeyFile != "/tmp/cluster-ca.pem" {
		t.Fatalf("got configured verification key file %q", config.VerifyKeyFile)
	}
}
