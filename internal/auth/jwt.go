package auth

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var ErrInvalidToken = errors.New("invalid OpenSVC access token")

type Identity struct {
	Subject   string
	Issuer    string
	Grants    []string
	ExpiresAt time.Time
}

type TokenVerifier interface {
	Verify(context.Context, string) (Identity, error)
}

type jwtClaims struct {
	Grant    []string `json:"grant"`
	TokenUse string   `json:"token_use"`
	jwt.RegisteredClaims
}

// JWTVerifier validates OpenSVC access JWTs signed by the cluster CA.
type JWTVerifier struct {
	publicKey *rsa.PublicKey
}

// NewJWTVerifier loads an RSA public key from an OpenSVC cluster CA
// certificate or public-key file.
func NewJWTVerifier(verifyKeyFile string) (*JWTVerifier, error) {
	if strings.TrimSpace(verifyKeyFile) == "" {
		return nil, fmt.Errorf("OpenSVC JWT verification key file path is empty")
	}
	keyPEM, err := os.ReadFile(verifyKeyFile)
	if err != nil {
		return nil, fmt.Errorf("read OpenSVC JWT verification key file %q: %w", verifyKeyFile, err)
	}
	publicKey, err := jwt.ParseRSAPublicKeyFromPEM(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse OpenSVC JWT RSA verification key file %q: %w", verifyKeyFile, err)
	}
	return &JWTVerifier{publicKey: publicKey}, nil
}

func (v *JWTVerifier) Verify(_ context.Context, rawToken string) (Identity, error) {
	claims := &jwtClaims{}
	token, err := jwt.ParseWithClaims(
		rawToken,
		claims,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodRS256 {
				return nil, fmt.Errorf("unexpected signing method %q", token.Method.Alg())
			}
			return v.publicKey, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithExpirationRequired(),
	)
	if err != nil || token == nil || !token.Valid {
		return Identity{}, invalidToken("signature or registered claims validation failed")
	}
	if claims.Subject == "" {
		return Identity{}, invalidToken("subject claim is missing")
	}
	if claims.Issuer == "" {
		return Identity{}, invalidToken("issuer claim is missing")
	}
	if claims.TokenUse != "access" {
		return Identity{}, invalidToken("token_use claim is not access")
	}
	if claims.ExpiresAt == nil {
		return Identity{}, invalidToken("expiration claim is missing")
	}
	return Identity{
		Subject:   claims.Subject,
		Issuer:    claims.Issuer,
		Grants:    append([]string(nil), claims.Grant...),
		ExpiresAt: claims.ExpiresAt.Time,
	}, nil
}

func invalidToken(reason string) error {
	return errors.Join(ErrInvalidToken, errors.New(reason))
}
