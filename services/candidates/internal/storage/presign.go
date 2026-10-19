// Package storage signs short-lived URLs against S3-compatible object storage.
//
// Resumes never pass through this service: a browser PUTs straight to the
// bucket with a URL signed here, and a recruiter downloads with a signed GET.
// That keeps megabytes of document traffic off the API, and it means a bug in a
// handler cannot serve one candidate's CV to another company — the URL is scoped
// to one key and expires.
//
// The signing is AWS Signature Version 4 in query-string form, written against
// the specification rather than pulled from an SDK, because the platform's only
// use for an object-storage client is this file.
package storage

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Config describes the bucket this service writes to.
type Config struct {
	// Endpoint is the service URL, e.g. https://s3.eu-west-1.amazonaws.com or
	// http://localhost:9000 for MinIO.
	Endpoint  string
	Region    string
	AccessKey string
	SecretKey string
	Bucket    string
	// PathStyle addresses the bucket as /<bucket>/<key>. MinIO needs it; AWS
	// prefers the virtual-host form.
	PathStyle bool
}

// Presigner issues signed URLs for one bucket.
type Presigner struct {
	cfg    Config
	scheme string
	host   string
	// now is injectable so the signature can be tested against a fixed clock.
	now func() time.Time
}

// New validates the configuration and builds a presigner.
func New(cfg Config) (*Presigner, error) {
	var missing []string
	if strings.TrimSpace(cfg.Endpoint) == "" {
		missing = append(missing, "S3_ENDPOINT")
	}
	if strings.TrimSpace(cfg.AccessKey) == "" {
		missing = append(missing, "S3_ACCESS_KEY")
	}
	if strings.TrimSpace(cfg.SecretKey) == "" {
		missing = append(missing, "S3_SECRET_KEY")
	}
	if strings.TrimSpace(cfg.Bucket) == "" {
		missing = append(missing, "CANDIDATES_RESUME_BUCKET")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("storage: missing configuration: %s", strings.Join(missing, ", "))
	}

	parsed, err := url.Parse(cfg.Endpoint)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("storage: endpoint %q is not a valid URL", cfg.Endpoint)
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}

	return &Presigner{cfg: cfg, scheme: parsed.Scheme, host: parsed.Host, now: time.Now}, nil
}

// ErrEmptyKey guards against signing a URL for the bucket root.
var ErrEmptyKey = errors.New("storage: an object key is required")

// PresignPut returns a URL a browser may upload one object to.
//
// content-type is a signed header, so the upload must present exactly the type
// that was validated against the allow-list; a client cannot get a URL for a
// PDF and then push an executable through it.
func (p *Presigner) PresignPut(key, contentType string, expiry time.Duration) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", ErrEmptyKey
	}
	return p.sign("PUT", key, expiry, map[string]string{"content-type": contentType}, nil)
}

// PresignGet returns a URL that downloads one object.
//
// downloadName is echoed through Content-Disposition so the recruiter's browser
// saves "jane-doe.pdf" rather than the opaque key.
func (p *Presigner) PresignGet(key, downloadName string, expiry time.Duration) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", ErrEmptyKey
	}

	extra := map[string]string{}
	if name := sanitizeFilename(downloadName); name != "" {
		extra["response-content-disposition"] = `attachment; filename="` + name + `"`
	}
	return p.sign("GET", key, expiry, nil, extra)
}

// sign builds a SigV4 query-signed URL.
func (p *Presigner) sign(
	method, key string,
	expiry time.Duration,
	signedHeaders map[string]string,
	extraQuery map[string]string,
) (string, error) {
	if expiry <= 0 || expiry > 7*24*time.Hour {
		// Seven days is the protocol's own ceiling for query signing.
		return "", fmt.Errorf("storage: expiry must be between 1s and 7 days, got %s", expiry)
	}

	now := p.now().UTC()
	amzDate := now.Format("20060102T150405Z")
	scopeDate := now.Format("20060102")
	scope := strings.Join([]string{scopeDate, p.cfg.Region, "s3", "aws4_request"}, "/")

	host := p.host
	path := "/" + strings.TrimPrefix(key, "/")
	if p.cfg.PathStyle {
		path = "/" + p.cfg.Bucket + path
	} else {
		host = p.cfg.Bucket + "." + p.host
	}

	// The host header is always signed; anything else the caller named joins it.
	headers := map[string]string{"host": host}
	for name, value := range signedHeaders {
		if strings.TrimSpace(value) == "" {
			continue
		}
		headers[strings.ToLower(name)] = strings.TrimSpace(value)
	}

	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)

	var canonicalHeaders strings.Builder
	for _, name := range names {
		canonicalHeaders.WriteString(name)
		canonicalHeaders.WriteByte(':')
		canonicalHeaders.WriteString(headers[name])
		canonicalHeaders.WriteByte('\n')
	}
	signedHeaderList := strings.Join(names, ";")

	query := map[string]string{
		"X-Amz-Algorithm":     "AWS4-HMAC-SHA256",
		"X-Amz-Credential":    p.cfg.AccessKey + "/" + scope,
		"X-Amz-Date":          amzDate,
		"X-Amz-Expires":       strconv.Itoa(int(expiry.Seconds())),
		"X-Amz-SignedHeaders": signedHeaderList,
	}
	for name, value := range extraQuery {
		query[name] = value
	}

	canonicalRequest := strings.Join([]string{
		method,
		encodePath(path),
		canonicalQuery(query),
		canonicalHeaders.String(),
		signedHeaderList,
		// The body is not known at signing time for a browser upload.
		"UNSIGNED-PAYLOAD",
	}, "\n")

	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hashHex(canonicalRequest),
	}, "\n")

	signingKey := hmacBytes(
		hmacBytes(
			hmacBytes(
				hmacBytes([]byte("AWS4"+p.cfg.SecretKey), []byte(scopeDate)),
				[]byte(p.cfg.Region)),
			[]byte("s3")),
		[]byte("aws4_request"))

	query["X-Amz-Signature"] = hex.EncodeToString(hmacBytes(signingKey, []byte(stringToSign)))

	return p.scheme + "://" + host + encodePath(path) + "?" + canonicalQuery(query), nil
}

// canonicalQuery renders parameters sorted by name, encoded per RFC 3986.
//
// net/url is not usable here: it encodes a space as "+", which produces a
// signature the service will reject.
func canonicalQuery(params map[string]string) string {
	names := make([]string, 0, len(params))
	for name := range params {
		names = append(names, name)
	}
	sort.Strings(names)

	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, encode(name)+"="+encode(params[name]))
	}
	return strings.Join(parts, "&")
}

// encodePath encodes each path segment but leaves the separators alone.
func encodePath(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		segments[i] = encode(segment)
	}
	return strings.Join(segments, "/")
}

func encode(value string) string {
	var out strings.Builder
	out.Grow(len(value))

	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			out.WriteByte(c)
		default:
			fmt.Fprintf(&out, "%%%02X", c)
		}
	}
	return out.String()
}

func hashHex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func hmacBytes(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// sanitizeFilename strips what would break a Content-Disposition header, so the
// filename a candidate chose cannot inject a second header field.
func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}

	var out strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20, r == '"', r == '\\', r == '/', r > 0x7e:
			out.WriteByte('_')
		default:
			out.WriteRune(r)
		}
	}
	trimmed := out.String()
	if len(trimmed) > 120 {
		trimmed = trimmed[:120]
	}
	return trimmed
}
