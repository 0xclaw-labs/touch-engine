// Package identity is the platform-identity client.
//
// 契约对齐 public-ai docs/integration-guide.md:
// POST /internal/v1/identity/{login,refresh,revocations,session/resolve},
// 头部 X-PilotSeaView-Internal-Token + X-App-ID,refresh 必须带 app_id。
// 本服务只认 platform 一条身份路径;resolve 失败一律 fail-closed。
package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const internalTokenHeader = "X-PilotSeaView-Internal-Token"
const appIDHeader = "X-App-ID"

// Sentinel errors, safe to test with errors.Is.
var (
	// ErrUnauthenticated means identity answered that the session is not valid.
	ErrUnauthenticated = errors.New("identity: session not authenticated")
	// ErrNoPrincipal means the session resolved but carried no principal id;
	// we refuse to fabricate an identity (fail-closed).
	ErrNoPrincipal = errors.New("identity: session resolved without principal_id")
	// ErrShellInvalidResponse means the shell launch answered without a usable
	// https launch_url. A URL is never invented locally.
	ErrShellInvalidResponse = errors.New("identity: shell launch returned no usable https launch_url")
)

// Principal is the resolved platform identity of a session.
type Principal struct {
	ID    string
	Email string
}

// TokenPair mirrors the identity TokenPair contract.
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
}

// Client calls platform-identity internal endpoints.
type Client struct {
	BaseURL string
	Token   string
	AppID   string
	HTTP    *http.Client
}

type resolveResult struct {
	Authenticated bool           `json:"authenticated"`
	AppID         string         `json:"app_id,omitempty"`
	Session       map[string]any `json:"session,omitempty"`
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// Login exchanges email+password for a token pair (aud = app id).
func (c *Client) Login(ctx context.Context, email, password string) (TokenPair, error) {
	var out TokenPair
	err := c.postJSON(ctx, "/internal/v1/identity/login", map[string]string{
		"email": email, "password": password, "app_id": c.AppID,
	}, &out)
	return out, err
}

// Refresh must pass the app id (JWT aud).
func (c *Client) Refresh(ctx context.Context, refreshToken string) (TokenPair, error) {
	var out TokenPair
	err := c.postJSON(ctx, "/internal/v1/identity/refresh", map[string]string{
		"refresh_token": refreshToken,
		"app_id":        c.AppID,
	}, &out)
	return out, err
}

// Logout revokes tokens via the internal revocations endpoint.
func (c *Client) Logout(ctx context.Context, refreshToken, userID string) error {
	return c.postJSON(ctx, "/internal/v1/identity/revocations", map[string]string{
		"refresh_token": refreshToken,
		"user_id":       userID,
	}, nil)
}

// ResolveSession resolves a session token into a Principal. It fails closed:
// authenticated=false -> ErrUnauthenticated; no principal id -> ErrNoPrincipal.
func (c *Client) ResolveSession(ctx context.Context, sessionToken string) (Principal, error) {
	var rr resolveResult
	if err := c.postJSON(ctx, "/internal/v1/identity/session/resolve", map[string]string{
		"session_token": sessionToken,
	}, &rr); err != nil {
		// Distinguish a definitive identity-side rejection (401/403) from
		// transport failure so callers can map to 401 vs 503.
		var he *HTTPError
		if errors.As(err, &he) && (he.Status == http.StatusUnauthorized || he.Status == http.StatusForbidden) {
			return Principal{}, ErrUnauthenticated
		}
		return Principal{}, err
	}
	if !rr.Authenticated {
		return Principal{}, ErrUnauthenticated
	}
	p := Principal{}
	if v, ok := sessionString(rr.Session, "principal_id", "user_id"); ok {
		p.ID = v
	}
	if v, ok := sessionString(rr.Session, "email"); ok {
		p.Email = v
	}
	if strings.TrimSpace(p.ID) == "" {
		return Principal{}, ErrNoPrincipal
	}
	return p, nil
}

// LaunchShell asks the platform shell to mint a one-time SSO code and returns
// the target app's launch_url. The caller passes only the two contract ids;
// no campaign, asset, payer or project data enters this request.
func (c *Client) LaunchShell(ctx context.Context, principalID, tenantID, appID, targetID string) (string, error) {
	var out struct {
		OK        bool   `json:"ok"`
		AppID     string `json:"app_id"`
		TargetID  string `json:"target_id"`
		LaunchURL string `json:"launch_url"`
	}
	err := c.postJSON(ctx, "/internal/v1/identity/shell/launch", map[string]string{
		"principal_id": principalID,
		"tenant_id":    tenantID,
		"app_id":       appID,
		"target_id":    targetID,
	}, &out)
	if err != nil {
		return "", err
	}
	raw := strings.TrimSpace(out.LaunchURL)
	parsed, perr := url.Parse(raw)
	if raw == "" || perr != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", ErrShellInvalidResponse
	}
	return raw, nil
}

func sessionString(m map[string]any, keys ...string) (string, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return s, true
			}
		}
	}
	return "", false
}

// HTTPError is a non-2xx answer from identity.
type HTTPError struct {
	Path   string
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("identity %s: status %d", e.Path, e.Status)
}

func (c *Client) postJSON(ctx context.Context, path string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("identity %s: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+path, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("identity %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(internalTokenHeader, c.Token)
	req.Header.Set(appIDHeader, c.AppID)
	res, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("identity %s: %w", path, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &HTTPError{Path: path, Status: res.StatusCode, Body: string(b)}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("identity %s: decode: %w", path, err)
	}
	return nil
}
