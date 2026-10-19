// Package tokens issues and verifies the platform's access tokens.
//
// Access tokens are short-lived RS256 JWTs. Asymmetric signing means only the
// identity service holds the private key: every other service — and the gateway —
// verifies with the public key and can never mint a token of its own.
//
// Refresh tokens are deliberately *not* JWTs. They are opaque random strings
// stored hashed in Redis, so a session can be revoked the moment a password is
// reset or a user is deactivated, which a self-contained JWT cannot offer.
package tokens

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/karmarat/platform/packages/goshared/tenancy"
)

var (
	ErrInvalidToken = errors.New("tokens: invalid or malformed token")
	ErrExpiredToken = errors.New("tokens: token has expired")
)

// Claims is the platform's access-token payload.
type Claims struct {
	jwt.RegisteredClaims

	// PrincipalType tells a service which kind of actor it is serving.
	PrincipalType tenancy.PrincipalType `json:"typ"`
	// CompanyID is present only for company principals and is the tenant boundary.
	CompanyID string `json:"cid,omitempty"`
	// CompanySlug is carried for convenience when routing and logging.
	CompanySlug string   `json:"slug,omitempty"`
	Email       string   `json:"email,omitempty"`
	Name        string   `json:"name,omitempty"`
	Roles       []string `json:"roles,omitempty"`
	// Permissions are resolved at issue time so services need no lookup to authorize.
	Permissions []string `json:"perms,omitempty"`
	// SessionID links the access token to its refresh session for revocation.
	SessionID string `json:"sid,omitempty"`
}

// Principal converts the claims into the request principal.
func (c *Claims) Principal() tenancy.Principal {
	return tenancy.Principal{
		Type:        c.PrincipalType,
		Subject:     c.Subject,
		CompanyID:   c.CompanyID,
		Roles:       c.Roles,
		Permissions: c.Permissions,
		SessionID:   c.SessionID,
		Email:       c.Email,
	}
}

// Issuer signs access tokens. Only the identity service constructs one.
type Issuer struct {
	privateKey *rsa.PrivateKey
	keyID      string
	issuer     string
	audience   string
	ttl        time.Duration
}

// IssuerConfig configures token signing.
type IssuerConfig struct {
	PrivateKeyPEM []byte
	Issuer        string
	Audience      string
	TTL           time.Duration
}

// NewIssuer builds a signer from a PEM-encoded RSA private key.
func NewIssuer(cfg IssuerConfig) (*Issuer, error) {
	key, err := parsePrivateKey(cfg.PrivateKeyPEM)
	if err != nil {
		return nil, err
	}
	if cfg.TTL == 0 {
		cfg.TTL = 15 * time.Minute
	}
	if cfg.Issuer == "" {
		cfg.Issuer = "karmarat-identity"
	}

	return &Issuer{
		privateKey: key,
		keyID:      fingerprint(&key.PublicKey),
		issuer:     cfg.Issuer,
		audience:   cfg.Audience,
		ttl:        cfg.TTL,
	}, nil
}

// IssueInput describes the token to mint.
type IssueInput struct {
	Subject       string
	PrincipalType tenancy.PrincipalType
	CompanyID     string
	CompanySlug   string
	Email         string
	Name          string
	Roles         []string
	Permissions   []string
	SessionID     string
}

// Issue mints a signed access token.
func (i *Issuer) Issue(in IssueInput) (string, *Claims, error) {
	if in.Subject == "" {
		return "", nil, errors.New("tokens: subject is required")
	}
	if !in.PrincipalType.Valid() || in.PrincipalType == tenancy.PrincipalAnonymous {
		return "", nil, fmt.Errorf("tokens: invalid principal type %q", in.PrincipalType)
	}
	// A company token without a tenant would authorize nothing safely.
	if in.PrincipalType == tenancy.PrincipalCompany && in.CompanyID == "" {
		return "", nil, errors.New("tokens: company principals require a company id")
	}

	now := time.Now()
	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   in.Subject,
			Issuer:    i.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-30 * time.Second)),
			ExpiresAt: jwt.NewNumericDate(now.Add(i.ttl)),
			ID:        randomHex(16),
		},
		PrincipalType: in.PrincipalType,
		CompanyID:     in.CompanyID,
		CompanySlug:   in.CompanySlug,
		Email:         in.Email,
		Name:          in.Name,
		Roles:         in.Roles,
		Permissions:   in.Permissions,
		SessionID:     in.SessionID,
	}
	if i.audience != "" {
		claims.Audience = jwt.ClaimStrings{i.audience}
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = i.keyID

	signed, err := token.SignedString(i.privateKey)
	if err != nil {
		return "", nil, fmt.Errorf("tokens: sign: %w", err)
	}

	return signed, claims, nil
}

// TTL reports the access-token lifetime.
func (i *Issuer) TTL() time.Duration { return i.ttl }

// PublicKeyPEM returns the PEM-encoded public key, for the JWKS endpoint.
func (i *Issuer) PublicKeyPEM() ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(&i.privateKey.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("tokens: marshal public key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
}

// KeyID returns the signing key's identifier.
func (i *Issuer) KeyID() string { return i.keyID }

// Verifier validates access tokens. Every service holds one.
type Verifier struct {
	publicKey *rsa.PublicKey
	issuer    string
	audience  string
}

// VerifierConfig configures verification.
type VerifierConfig struct {
	PublicKeyPEM []byte
	Issuer       string
	Audience     string
}

// NewVerifier builds a verifier from a PEM-encoded RSA public key.
func NewVerifier(cfg VerifierConfig) (*Verifier, error) {
	key, err := parsePublicKey(cfg.PublicKeyPEM)
	if err != nil {
		return nil, err
	}
	if cfg.Issuer == "" {
		cfg.Issuer = "karmarat-identity"
	}
	return &Verifier{publicKey: key, issuer: cfg.Issuer, audience: cfg.Audience}, nil
}

// Verify parses and validates a signed access token.
func (v *Verifier) Verify(raw string) (*Claims, error) {
	options := []jwt.ParserOption{
		// Pinning the algorithm blocks the "alg: none" and HMAC-confusion attacks.
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithIssuer(v.issuer),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(30 * time.Second),
	}
	if v.audience != "" {
		options = append(options, jwt.WithAudience(v.audience))
	}

	claims := &Claims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(*jwt.Token) (any, error) {
		return v.publicKey, nil
	}, options...)

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpiredToken
		}
		return nil, fmt.Errorf("%w: %s", ErrInvalidToken, err.Error())
	}
	if !token.Valid {
		return nil, ErrInvalidToken
	}
	if !claims.PrincipalType.Valid() {
		return nil, fmt.Errorf("%w: unknown principal type", ErrInvalidToken)
	}

	return claims, nil
}

// LoadKeyPair reads the signing keypair from disk.
func LoadKeyPair(privatePath, publicPath string) (private, public []byte, err error) {
	private, err = os.ReadFile(privatePath)
	if err != nil {
		return nil, nil, fmt.Errorf("tokens: read private key %s: %w", privatePath, err)
	}
	public, err = os.ReadFile(publicPath)
	if err != nil {
		return nil, nil, fmt.Errorf("tokens: read public key %s: %w", publicPath, err)
	}
	return private, public, nil
}

func parsePrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("tokens: private key is not valid PEM")
	}

	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}

	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("tokens: parse private key: %w", err)
	}

	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("tokens: private key is not RSA")
	}
	if key.N.BitLen() < 2048 {
		return nil, errors.New("tokens: RSA key must be at least 2048 bits")
	}
	return key, nil
}

func parsePublicKey(pemBytes []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("tokens: public key is not valid PEM")
	}

	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		if key, pkcs1Err := x509.ParsePKCS1PublicKey(block.Bytes); pkcs1Err == nil {
			return key, nil
		}
		return nil, fmt.Errorf("tokens: parse public key: %w", err)
	}

	key, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("tokens: public key is not RSA")
	}
	return key, nil
}

func fingerprint(key *rsa.PublicKey) string {
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return "default"
	}
	sum := sha256.Sum256(der)
	return base64.RawURLEncoding.EncodeToString(sum[:8])
}

// NewRefreshToken returns an opaque refresh token and the hash to store.
//
// Only the hash is persisted, so a leaked datastore does not hand an attacker
// usable sessions.
func NewRefreshToken() (raw, hashed string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("tokens: generate refresh token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	return raw, HashRefreshToken(raw), nil
}

// HashRefreshToken hashes a refresh token for storage and lookup.
func HashRefreshToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}
