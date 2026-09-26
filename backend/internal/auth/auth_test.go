package auth

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/denismurphy/agritelem/backend/internal/domain"
)

var (
	farm  = domain.Location{Scheme: "dk.herdnumber", ID: "123456"}
	other = domain.Location{Scheme: "dk.herdnumber", ID: "999999"}
	t0    = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
)

func keys(t *testing.T) (*Issuer, *Verifier, []byte) {
	t.Helper()
	priv, pub, err := GenerateKeyPEM()
	if err != nil {
		t.Fatal(err)
	}
	iss, err := NewIssuer(priv, "agritelem-dev", "agritelem-api")
	if err != nil {
		t.Fatal(err)
	}
	v, err := NewVerifier(pub, "agritelem-dev", "agritelem-api")
	if err != nil {
		t.Fatal(err)
	}
	v.now = func() time.Time { return t0 }
	return iss, v, pub
}

func TestVerifyAcceptsValidToken(t *testing.T) {
	iss, v, _ := keys(t)
	token, err := iss.Issue("ie.example.gateway", []string{ScopeObservationsWrite, ScopeAnimalsRead}, []domain.Location{farm}, t0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	p, err := v.Verify(token)
	if err != nil {
		t.Fatal(err)
	}
	if p.Subject != "ie.example.gateway" || p.Authorize(ScopeObservationsWrite, farm) != nil {
		t.Errorf("principal = %+v", p)
	}
	if err := p.Authorize(ScopeRemarksWrite, farm); !errors.Is(err, ErrForbidden) {
		t.Errorf("missing scope: err = %v, want ErrForbidden", err)
	}
	if err := p.Authorize(ScopeAnimalsRead, other); !errors.Is(err, ErrForbidden) {
		t.Errorf("other location: err = %v, want ErrForbidden", err)
	}
}

func TestVerifyRejects(t *testing.T) {
	iss, v, pub := keys(t)
	otherIss, _, _ := keys(t)
	valid := func() string {
		tok, err := iss.Issue("gw", []string{ScopeAnimalsRead}, []domain.Location{farm}, t0, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}

	hs256 := func() string {
		// Algorithm confusion: an HMAC token keyed with the public key bytes.
		claims := jwt.MapClaims{"iss": "agritelem-dev", "aud": "agritelem-api", "sub": "gw", "exp": t0.Add(time.Hour).Unix()}
		tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(pub)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	none := func() string {
		claims := jwt.MapClaims{"iss": "agritelem-dev", "aud": "agritelem-api", "sub": "gw", "exp": t0.Add(time.Hour).Unix()}
		tok, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).SignedString(jwt.UnsafeAllowNoneSignatureType)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	tampered := func() string {
		parts := strings.Split(valid(), ".")
		payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
		payload = []byte(strings.Replace(string(payload), `"sub":"gw"`, `"sub":"gx"`, 1))
		parts[1] = base64.RawURLEncoding.EncodeToString(payload)
		return strings.Join(parts, ".")
	}
	issue := func(i *Issuer, sub string, issuedAt time.Time, ttl time.Duration) func() string {
		return func() string {
			tok, err := i.Issue(sub, []string{ScopeAnimalsRead}, nil, issuedAt, ttl)
			if err != nil {
				t.Fatal(err)
			}
			return tok
		}
	}
	wrongAudience := func() string {
		i := *iss
		i.audience = "someone-else"
		return issue(&i, "gw", t0, time.Hour)()
	}
	wrongIssuer := func() string {
		i := *iss
		i.issuer = "evil"
		return issue(&i, "gw", t0, time.Hour)()
	}

	noExpiry := func() string {
		claims := jwt.MapClaims{"iss": "agritelem-dev", "aud": "agritelem-api", "sub": "gw"}
		tok, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(iss.key)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}

	tests := map[string]func() string{
		"no expiry":             noExpiry,
		"garbage":               func() string { return "not.a.jwt" },
		"HS256 key confusion":   hs256,
		"alg none":              none,
		"tampered payload":      tampered,
		"signed by another key": issue(otherIss, "gw", t0, time.Hour),
		"expired beyond leeway": issue(iss, "gw", t0.Add(-2*time.Hour), time.Hour-Leeway-time.Second),
		"not yet valid":         issue(iss, "gw", t0.Add(Leeway+time.Minute), time.Hour),
		"wrong audience":        wrongAudience,
		"wrong issuer":          wrongIssuer,
		"empty subject":         issue(iss, " ", t0, time.Hour),
	}
	for name, token := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := v.Verify(token()); !errors.Is(err, ErrUnauthenticated) {
				t.Errorf("err = %v, want ErrUnauthenticated", err)
			}
		})
	}

	t.Run("expired within leeway", func(t *testing.T) {
		if _, err := v.Verify(issue(iss, "gw", t0.Add(-time.Hour), time.Hour-Leeway/2)()); err != nil {
			t.Errorf("err = %v, want accepted within %v leeway", err, Leeway)
		}
	})
}

func TestNewVerifierRejectsBadKeys(t *testing.T) {
	priv, _, err := GenerateKeyPEM()
	if err != nil {
		t.Fatal(err)
	}
	for name, key := range map[string][]byte{"empty": nil, "private key": priv, "junk": []byte("-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----\n")} {
		if _, err := NewVerifier(key, "i", "a"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
