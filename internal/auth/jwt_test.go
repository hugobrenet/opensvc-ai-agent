package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestJWTVerifierAcceptsOpenSVCAccessToken(t *testing.T) {
	privateKey, certificateFile := writeJWTTestCertificate(t)
	verifier, err := NewJWTVerifier(certificateFile)
	if err != nil {
		t.Fatalf("create JWT verifier: %v", err)
	}
	token := signTestJWT(t, privateKey, jwt.MapClaims{
		"exp":       time.Now().Add(time.Hour).Unix(),
		"grant":     []string{"guest", "operator:lab"},
		"iss":       "node-a",
		"sub":       "alice",
		"token_use": "access",
	})

	identity, err := verifier.Verify(t.Context(), token)
	if err != nil {
		t.Fatalf("verify JWT: %v", err)
	}
	if identity.Subject != "alice" || identity.Issuer != "node-a" {
		t.Fatalf("unexpected identity %+v", identity)
	}
	if len(identity.Grants) != 2 || identity.Grants[0] != "guest" || identity.Grants[1] != "operator:lab" {
		t.Fatalf("unexpected grants %#v", identity.Grants)
	}
	if identity.ExpiresAt.IsZero() {
		t.Fatal("verified identity has no expiration")
	}
}

func TestJWTVerifierRejectsInvalidClaims(t *testing.T) {
	privateKey, certificateFile := writeJWTTestCertificate(t)
	verifier, err := NewJWTVerifier(certificateFile)
	if err != nil {
		t.Fatalf("create JWT verifier: %v", err)
	}
	valid := jwt.MapClaims{
		"exp":       time.Now().Add(time.Hour).Unix(),
		"grant":     []string{"guest"},
		"iss":       "node-a",
		"sub":       "alice",
		"token_use": "access",
	}
	for _, test := range []struct {
		name   string
		change func(jwt.MapClaims)
	}{
		{name: "expired", change: func(claims jwt.MapClaims) { claims["exp"] = time.Now().Add(-time.Minute).Unix() }},
		{name: "not active", change: func(claims jwt.MapClaims) { claims["nbf"] = time.Now().Add(time.Hour).Unix() }},
		{name: "missing expiration", change: func(claims jwt.MapClaims) { delete(claims, "exp") }},
		{name: "missing subject", change: func(claims jwt.MapClaims) { delete(claims, "sub") }},
		{name: "missing issuer", change: func(claims jwt.MapClaims) { delete(claims, "iss") }},
		{name: "refresh token", change: func(claims jwt.MapClaims) { claims["token_use"] = "refresh" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			claims := jwt.MapClaims{}
			for key, value := range valid {
				claims[key] = value
			}
			test.change(claims)
			_, err := verifier.Verify(t.Context(), signTestJWT(t, privateKey, claims))
			if !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("Verify() error = %v, want ErrInvalidToken", err)
			}
		})
	}
}

func TestJWTVerifierRejectsInvalidSignatureAndAlgorithm(t *testing.T) {
	_, certificateFile := writeJWTTestCertificate(t)
	verifier, err := NewJWTVerifier(certificateFile)
	if err != nil {
		t.Fatalf("create JWT verifier: %v", err)
	}
	claims := jwt.MapClaims{
		"exp":       time.Now().Add(time.Hour).Unix(),
		"iss":       "node-a",
		"sub":       "alice",
		"token_use": "access",
	}
	untrustedKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate untrusted key: %v", err)
	}
	wrongSignature := signTestJWT(t, untrustedKey, claims)
	_, verifyErr := verifier.Verify(t.Context(), wrongSignature)
	if !errors.Is(verifyErr, ErrInvalidToken) {
		t.Fatalf("wrong signature error = %v", verifyErr)
	}
	if strings.Contains(verifyErr.Error(), wrongSignature) {
		t.Fatal("verification error exposes raw JWT")
	}

	wrongAlgorithm, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("shared-secret"))
	if err != nil {
		t.Fatalf("sign HMAC test JWT: %v", err)
	}
	if _, err := verifier.Verify(t.Context(), wrongAlgorithm); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("wrong algorithm error = %v", err)
	}
}

func TestNewJWTVerifierRejectsInvalidKeyFiles(t *testing.T) {
	if _, err := NewJWTVerifier(filepath.Join(t.TempDir(), "missing.pem")); err == nil {
		t.Fatal("missing verification key accepted")
	}
	invalidFile := filepath.Join(t.TempDir(), "invalid.pem")
	if err := os.WriteFile(invalidFile, []byte("not a key"), 0o600); err != nil {
		t.Fatalf("write invalid key: %v", err)
	}
	if _, err := NewJWTVerifier(invalidFile); err == nil {
		t.Fatal("invalid verification key accepted")
	}
}

func writeJWTTestCertificate(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "OpenSVC test CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("create test certificate: %v", err)
	}
	certificateFile := filepath.Join(t.TempDir(), "ca.pem")
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	if err := os.WriteFile(certificateFile, certificatePEM, 0o600); err != nil {
		t.Fatalf("write test certificate: %v", err)
	}
	return privateKey, certificateFile
}

func signTestJWT(t *testing.T, privateKey *rsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(privateKey)
	if err != nil {
		t.Fatalf("sign test JWT: %v", err)
	}
	return token
}
