package tokens

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/karmarat/platform/packages/goshared/tenancy"
)

func testKeyPair(t *testing.T) (private, public []byte) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})
}

func newPair(t *testing.T, ttl time.Duration) (*Issuer, *Verifier) {
	t.Helper()

	privatePEM, publicPEM := testKeyPair(t)

	issuer, err := NewIssuer(IssuerConfig{
		PrivateKeyPEM: privatePEM, Issuer: "test-issuer", Audience: "test", TTL: ttl,
	})
	if err != nil {
		t.Fatalf("NewIssuer() error = %v", err)
	}

	verifier, err := NewVerifier(VerifierConfig{
		PublicKeyPEM: publicPEM, Issuer: "test-issuer", Audience: "test",
	})
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}

	return issuer, verifier
}

func TestIssueAndVerify(t *testing.T) {
	issuer, verifier := newPair(t, 15*time.Minute)

	raw, _, err := issuer.Issue(IssueInput{
		Subject:       "acc_1",
		PrincipalType: tenancy.PrincipalCompany,
		CompanyID:     "co_1",
		CompanySlug:   "acme",
		Email:         "recruiter@acme.test",
		Roles:         []string{"recruiter"},
		Permissions:   []string{"jobs.read", "jobs.create"},
		SessionID:     "ses_1",
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	claims, err := verifier.Verify(raw)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	principal := claims.Principal()
	if principal.Type != tenancy.PrincipalCompany || principal.CompanyID != "co_1" {
		t.Fatalf("principal = %+v, want a company principal scoped to co_1", principal)
	}
	if !principal.Can("jobs.create") || principal.Can("jobs.delete") {
		t.Fatal("permissions did not survive the round trip")
	}
}

// A company token with no tenant would authorize against an undefined scope.
func TestIssueRejectsCompanyTokenWithoutTenant(t *testing.T) {
	issuer, _ := newPair(t, time.Minute)

	if _, _, err := issuer.Issue(IssueInput{
		Subject: "acc_1", PrincipalType: tenancy.PrincipalCompany,
	}); err == nil {
		t.Fatal("issued a company token with no company id")
	}
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	issuer, verifier := newPair(t, -time.Hour) // already expired

	raw, _, err := issuer.Issue(IssueInput{Subject: "acc_1", PrincipalType: tenancy.PrincipalCandidate})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	if _, err := verifier.Verify(raw); !errors.Is(err, ErrExpiredToken) {
		t.Fatalf("Verify() error = %v, want ErrExpiredToken", err)
	}
}

// A token signed by a different key must never verify, or any service holding a
// keypair could mint credentials for the whole platform.
func TestVerifyRejectsForeignSignature(t *testing.T) {
	foreignIssuer, _ := newPair(t, time.Minute)
	_, verifier := newPair(t, time.Minute)

	raw, _, err := foreignIssuer.Issue(IssueInput{Subject: "acc_1", PrincipalType: tenancy.PrincipalPlatform})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	if _, err := verifier.Verify(raw); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Verify() error = %v, want ErrInvalidToken", err)
	}
}

// The classic JWT attack: strip the signature and claim the algorithm is "none".
func TestVerifyRejectsUnsignedToken(t *testing.T) {
	_, verifier := newPair(t, time.Minute)

	// {"alg":"none","typ":"JWT"}.{"sub":"acc_1","typ":"platform"}.
	unsigned := "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0." +
		"eyJzdWIiOiJhY2NfMSIsInR5cCI6InBsYXRmb3JtIn0."

	if _, err := verifier.Verify(unsigned); err == nil {
		t.Fatal("an unsigned token was accepted")
	}
}

func TestVerifyRejectsTamperedPayload(t *testing.T) {
	issuer, verifier := newPair(t, time.Minute)

	raw, _, err := issuer.Issue(IssueInput{
		Subject: "acc_1", PrincipalType: tenancy.PrincipalCompany, CompanyID: "co_1",
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	parts := strings.Split(raw, ".")
	// Swap a character in the payload; the signature no longer covers it.
	payload := []byte(parts[1])
	if payload[0] == 'a' {
		payload[0] = 'b'
	} else {
		payload[0] = 'a'
	}
	tampered := parts[0] + "." + string(payload) + "." + parts[2]

	if _, err := verifier.Verify(tampered); err == nil {
		t.Fatal("a tampered token was accepted")
	}
}

func TestRefreshTokensAreOpaqueAndHashed(t *testing.T) {
	raw, hashed, err := NewRefreshToken()
	if err != nil {
		t.Fatalf("NewRefreshToken() error = %v", err)
	}

	if raw == hashed {
		t.Fatal("the stored value equals the token; it is not hashed")
	}
	if strings.Contains(hashed, raw) {
		t.Fatal("the hash contains the raw token")
	}
	if HashRefreshToken(raw) != hashed {
		t.Fatal("hashing is not deterministic; lookup would fail")
	}

	other, _, _ := NewRefreshToken()
	if other == raw {
		t.Fatal("two refresh tokens collided")
	}
}
