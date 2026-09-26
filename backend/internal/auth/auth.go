// Package auth verifies Ed25519-signed (EdDSA) JWT bearer tokens and carries
// the authenticated principal through request contexts.
package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/denismurphy/agritelem/backend/internal/domain"
)

// Scopes granted by tokens.
const (
	ScopeAnimalsRead       = "animals:read"
	ScopeObservationsWrite = "observations:write"
	ScopeRemarksRead       = "remarks:read"
	ScopeRemarksWrite      = "remarks:write"
)

// Leeway tolerates clock skew between the token issuer and this server.
const Leeway = 30 * time.Second

// Errors returned by Verify and Authorize.
var (
	ErrUnauthenticated = errors.New("unauthenticated")
	ErrForbidden       = errors.New("forbidden")
)

// LocationClaim is one location a token may act on.
type LocationClaim struct {
	Scheme string `json:"scheme"`
	ID     string `json:"id"`
}

// Claims is the JWT payload. `sub` identifies the source system and must
// equal meta.source on every write.
type Claims struct {
	jwt.RegisteredClaims
	Scope     string          `json:"scope"`
	Locations []LocationClaim `json:"locations"`
}

// Principal is an authenticated caller.
type Principal struct {
	Subject   string
	Scopes    []string
	Locations []domain.Location
}

// Authorize reports whether p holds scope for loc.
func (p Principal) Authorize(scope string, loc domain.Location) error {
	if !slices.Contains(p.Scopes, scope) {
		return fmt.Errorf("%w: token lacks scope %q", ErrForbidden, scope)
	}
	if !slices.Contains(p.Locations, loc) {
		return fmt.Errorf("%w: token is not valid for location %s/%s", ErrForbidden, loc.Scheme, loc.ID)
	}
	return nil
}

// Verifier validates tokens against one public key, issuer and audience.
type Verifier struct {
	key      ed25519.PublicKey
	issuer   string
	audience string
	now      func() time.Time
}

// NewVerifier parses a PEM-encoded Ed25519 public key (PKIX).
func NewVerifier(publicKeyPEM []byte, issuer, audience string) (*Verifier, error) {
	block, _ := pem.Decode(publicKeyPEM)
	if block == nil {
		return nil, errors.New("JWT public key: no PEM block found")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("JWT public key: %w", err)
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("JWT public key is %T, want an Ed25519 key", parsed)
	}
	if issuer == "" || audience == "" {
		return nil, errors.New("JWT issuer and audience must be configured")
	}
	return &Verifier{key: key, issuer: issuer, audience: audience, now: time.Now}, nil
}

// Verify parses and validates a compact JWT.
func (v *Verifier) Verify(token string) (Principal, error) {
	var claims Claims
	_, err := jwt.ParseWithClaims(token, &claims,
		func(*jwt.Token) (any, error) { return v.key, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(Leeway),
		jwt.WithTimeFunc(v.now),
	)
	if err != nil {
		return Principal{}, fmt.Errorf("%w: %w", ErrUnauthenticated, err)
	}
	if strings.TrimSpace(claims.Subject) == "" {
		return Principal{}, fmt.Errorf("%w: token has no subject", ErrUnauthenticated)
	}
	p := Principal{Subject: claims.Subject, Scopes: strings.Fields(claims.Scope)}
	for _, l := range claims.Locations {
		p.Locations = append(p.Locations, domain.Location{Scheme: l.Scheme, ID: l.ID})
	}
	return p, nil
}

type principalKey struct{}

// WithPrincipal returns a context carrying p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// FromContext returns the principal stored by WithPrincipal.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// Issuer mints tokens. It is used by cmd/devtoken and tests; production
// tokens come from an external identity provider holding the private key.
type Issuer struct {
	key      ed25519.PrivateKey
	issuer   string
	audience string
}

// NewIssuer parses a PEM-encoded Ed25519 private key (PKCS #8).
func NewIssuer(privateKeyPEM []byte, issuer, audience string) (*Issuer, error) {
	block, _ := pem.Decode(privateKeyPEM)
	if block == nil {
		return nil, errors.New("JWT private key: no PEM block found")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("JWT private key: %w", err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("JWT private key is %T, want an Ed25519 key", parsed)
	}
	return &Issuer{key: key, issuer: issuer, audience: audience}, nil
}

// Issue returns a signed token for subject valid from now for ttl.
func (i *Issuer) Issue(subject string, scopes []string, locations []domain.Location, now time.Time, ttl time.Duration) (string, error) {
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    i.issuer,
			Subject:   subject,
			Audience:  jwt.ClaimStrings{i.audience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
		Scope: strings.Join(scopes, " "),
	}
	for _, l := range locations {
		claims.Locations = append(claims.Locations, LocationClaim(l))
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(i.key)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return signed, nil
}

// GenerateKeyPEM returns a new Ed25519 key pair as PEM (PKCS #8, PKIX).
func GenerateKeyPEM() (privatePEM, publicPEM []byte, err error) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, nil, err
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, nil, err
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}), nil
}
