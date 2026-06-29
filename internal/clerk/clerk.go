// Package clerk mints short-lived RunPod session JWTs.
//
// RunPod's console authenticates against Clerk (clerk.runpod.io). A logged-in
// browser holds a long-lived "__client" cookie and a session id, and exchanges
// them for a fresh ~60-second JWT by POSTing to:
//
//	POST https://clerk.runpod.io/v1/client/sessions/<sessionID>/tokens
//	Cookie: __client=<clientCookie>
//	Content-Type: application/x-www-form-urlencoded
//
//	organization_id=
//
// The response is {"object":"token","jwt":"<jwt>"}. That JWT is then used as a
// Bearer token against api.runpod.io. This package replicates that exchange and
// caches the minted token until shortly before it expires.
package clerk

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	defaultBaseURL = "https://clerk.runpod.io"
	userAgent      = "runpod-orchestrator/0.1"
	origin         = "https://console.runpod.io"
	referer        = "https://console.runpod.io/"

	// expiryBuffer mints a new token this long before the cached one expires,
	// so a token never goes stale mid-request.
	expiryBuffer = 10 * time.Second
)

// Client mints and caches RunPod session JWTs. It is safe for concurrent use.
type Client struct {
	httpClient   *http.Client
	baseURL      string
	sessionID    string
	clientCookie string

	mu     sync.Mutex
	token  string
	expiry time.Time
}

// New returns a Client that mints tokens for the given session.
func New(sessionID, clientCookie string) *Client {
	return &Client{
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		baseURL:      defaultBaseURL,
		sessionID:    sessionID,
		clientCookie: clientCookie,
	}
}

// Token returns a valid JWT, minting a fresh one if the cache is empty or near
// expiry. It satisfies the token-source contract expected by the runpod client.
func (c *Client) Token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && time.Now().Before(c.expiry.Add(-expiryBuffer)) {
		return c.token, nil
	}

	token, err := c.mint(ctx)
	if err != nil {
		return "", err
	}

	exp, err := jwtExpiry(token)
	if err != nil {
		return "", fmt.Errorf("clerk: reading token expiry: %w", err)
	}

	c.token = token
	c.expiry = exp
	return token, nil
}

// mint performs the token exchange against Clerk.
func (c *Client) mint(ctx context.Context) (string, error) {
	endpoint := fmt.Sprintf("%s/v1/client/sessions/%s/tokens", c.baseURL, url.PathEscape(c.sessionID))
	body := strings.NewReader(url.Values{"organization_id": {""}}.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", "__client="+c.clientCookie)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", referer)
	req.Header.Set("Accept", "*/*")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// A transport error is a *url.Error whose message embeds the request URL,
		// which carries the session id. Report only the underlying cause so the
		// session id never reaches the terminal or logs.
		if ue := (*url.Error)(nil); errors.As(err, &ue) {
			err = ue.Err
		}
		return "", fmt.Errorf("clerk: minting token: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("clerk: reading response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("clerk: session rejected (HTTP 401) — your session_id/client_cookie are stale; "+
			"refresh them from a logged-in console.runpod.io browser session: %s",
			strings.TrimSpace(string(data)))
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("clerk: token request failed: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}

	var out struct {
		JWT string `json:"jwt"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("clerk: decoding response: %w", err)
	}
	if out.JWT == "" {
		return "", fmt.Errorf("clerk: response contained no jwt")
	}
	return out.JWT, nil
}

// jwtExpiry extracts the "exp" claim from a JWT without verifying its signature.
func jwtExpiry(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, fmt.Errorf("malformed jwt")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("decoding jwt payload: %w", err)
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, fmt.Errorf("parsing jwt claims: %w", err)
	}
	if claims.Exp == 0 {
		return time.Time{}, fmt.Errorf("jwt has no exp claim")
	}
	return time.Unix(claims.Exp, 0), nil
}
