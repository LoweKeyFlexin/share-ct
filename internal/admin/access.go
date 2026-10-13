package admin

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// AccessVerifier checks the Cloudflare Access login token on a request. Cloudflare
// Access sits in front of /admin and signs every request it lets through with an RS256
// JWT (the Cf-Access-Jwt-Assertion header). The token is verified here rather than
// trusted, so a request that reaches the container some other way, or before Access is
// configured, cannot pretend to be signed in.
type AccessVerifier struct {
	TeamDomain string // e.g. "example.cloudflareaccess.com", no scheme
	Audience   string // the Access application's AUD tag
	Client     *http.Client
	Now        func() time.Time

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

const (
	keyMaxAge      = time.Hour
	keyRetryMinGap = time.Minute
	clockSkew      = time.Minute
)

var errDenied = errors.New("access denied")

// Configured reports whether both settings are present. Unconfigured means /admin is off.
func (v *AccessVerifier) Configured() bool {
	return v != nil && v.TeamDomain != "" && v.Audience != ""
}

// Verify returns nil only for a correctly signed, unexpired token issued by the team for
// this application.
func (v *AccessVerifier) Verify(ctx context.Context, r *http.Request) error {
	if !v.Configured() {
		return errDenied
	}
	tok := r.Header.Get("Cf-Access-Jwt-Assertion")
	if tok == "" {
		return errDenied
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return errDenied
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if !decodeSegment(parts[0], &hdr) || hdr.Alg != "RS256" || hdr.Kid == "" {
		return errDenied
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return errDenied
	}
	key, err := v.key(ctx, hdr.Kid)
	if err != nil {
		return errDenied
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig) != nil {
		return errDenied
	}
	var claims struct {
		Aud audience `json:"aud"`
		Iss string   `json:"iss"`
		Exp int64    `json:"exp"`
		Nbf int64    `json:"nbf"`
	}
	if !decodeSegment(parts[1], &claims) {
		return errDenied
	}
	now := v.now()
	switch {
	case claims.Iss != "https://"+v.TeamDomain:
		return errDenied
	case !claims.Aud.has(v.Audience):
		return errDenied
	case claims.Exp == 0 || now.After(time.Unix(claims.Exp, 0).Add(clockSkew)):
		return errDenied
	case claims.Nbf != 0 && now.Add(clockSkew).Before(time.Unix(claims.Nbf, 0)):
		return errDenied
	}
	return nil
}

func (v *AccessVerifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

// key returns the signing key for kid, refreshing the team's published keys when they are
// stale or the kid is unknown (at most once a minute, so bad tokens cannot hammer the fetch).
func (v *AccessVerifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.now()
	if k, ok := v.keys[kid]; ok && now.Sub(v.fetchedAt) < keyMaxAge {
		return k, nil
	}
	if !v.fetchedAt.IsZero() && now.Sub(v.fetchedAt) < keyRetryMinGap {
		if k, ok := v.keys[kid]; ok {
			return k, nil
		}
		return nil, errDenied
	}
	keys, err := v.fetchKeys(ctx)
	v.fetchedAt = now
	if err != nil {
		if k, ok := v.keys[kid]; ok {
			return k, nil
		}
		return nil, err
	}
	v.keys = keys
	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, errDenied
}

func (v *AccessVerifier) fetchKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	client := v.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+v.TeamDomain+"/cdn-cgi/access/certs", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("certs: status " + resp.Status)
	}
	var set struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&set); err != nil {
		return nil, err
	}
	out := make(map[string]*rsa.PublicKey)
	for _, k := range set.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		n, errN := base64.RawURLEncoding.DecodeString(k.N)
		e, errE := base64.RawURLEncoding.DecodeString(k.E)
		if errN != nil || errE != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		exp := 0
		for _, b := range e {
			exp = exp<<8 | int(b)
		}
		out[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exp}
	}
	if len(out) == 0 {
		return nil, errors.New("certs: no RSA keys")
	}
	return out, nil
}

func decodeSegment(seg string, v any) bool {
	b, err := base64.RawURLEncoding.DecodeString(seg)
	return err == nil && json.Unmarshal(b, v) == nil
}

// audience accepts the JWT "aud" claim as a string or an array of strings.
type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*a = audience{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

func (a audience) has(want string) bool {
	for _, s := range a {
		if s == want {
			return true
		}
	}
	return false
}
