package identity

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func stubIdentity(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, Token: "app-token", AppID: "touch-engine", HTTP: srv.Client()}, srv
}

func TestResolveSessionOK(t *testing.T) {
	var gotPath, gotToken, gotAppID, gotBody string
	c, _ := stubIdentity(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotToken = r.Header.Get("X-PilotSeaView-Internal-Token")
		gotAppID = r.Header.Get("X-App-ID")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"authenticated":true,"app_id":"touch-engine","session":{"principal_id":"usr_01","email":"m@example.com"}}`))
	})
	p, err := c.ResolveSession(context.Background(), "sess-token")
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}
	if p.ID != "usr_01" || p.Email != "m@example.com" {
		t.Fatalf("principal = %+v", p)
	}
	if gotPath != "/internal/v1/identity/session/resolve" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotToken != "app-token" || gotAppID != "touch-engine" {
		t.Fatalf("headers = %q %q", gotToken, gotAppID)
	}
	if gotBody == "" || !strings.Contains(gotBody, "sess-token") {
		t.Fatalf("body = %q", gotBody)
	}
}

func TestResolveSessionRejected(t *testing.T) {
	c, _ := stubIdentity(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthenticated"}`))
	})
	if _, err := c.ResolveSession(context.Background(), "bad"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("want ErrUnauthenticated, got %v", err)
	}
}

func TestResolveSessionUnreachableIsNotUnauthenticated(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1", Token: "t", AppID: "touch-engine", HTTP: &http.Client{Timeout: 1}}
	// transport failure must NOT be classified as unauthenticated (503, not 401)
	_, err := c.ResolveSession(context.Background(), "sess")
	if err == nil || errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("transport failure misclassified: %v", err)
	}
}

func TestResolveSessionNoPrincipalFailsClosed(t *testing.T) {
	c, _ := stubIdentity(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"authenticated":true,"session":{"email":"x@example.com"}}`))
	})
	if _, err := c.ResolveSession(context.Background(), "sess"); !errors.Is(err, ErrNoPrincipal) {
		t.Fatalf("want ErrNoPrincipal, got %v", err)
	}
}

func TestLoginSendsAppID(t *testing.T) {
	var body string
	c, _ := stubIdentity(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","token_type":"bearer","expires_in":3600}`))
	})
	pair, err := c.Login(context.Background(), "m@example.com", "pw")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if pair.AccessToken != "at" || pair.RefreshToken != "rt" {
		t.Fatalf("pair = %+v", pair)
	}
	if !strings.Contains(body, `"app_id":"touch-engine"`) {
		t.Fatalf("login body missing app_id: %q", body)
	}
	if !strings.Contains(body, `"email":"m@example.com"`) {
		t.Fatalf("login body missing email: %q", body)
	}
}

func TestRefreshSendsAppID(t *testing.T) {
	var body string
	c, _ := stubIdentity(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_, _ = w.Write([]byte(`{"access_token":"at2","refresh_token":"rt2","expires_in":3600}`))
	})
	pair, err := c.Refresh(context.Background(), "rt-old")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if pair.AccessToken != "at2" {
		t.Fatalf("pair = %+v", pair)
	}
	if !strings.Contains(body, `"app_id":"touch-engine"`) || !strings.Contains(body, `"refresh_token":"rt-old"`) {
		t.Fatalf("refresh body = %q", body)
	}
}

// HUI-2227/2628: app switching mints a one-time shell code through the
// platform identity. The client sends the fixed shell path and the exact
// identity body; it never accepts a caller-supplied URL.
func TestLaunchShellContract(t *testing.T) {
	var path, token, appID, body string
	c, _ := stubIdentity(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		token = r.Header.Get("X-PilotSeaView-Internal-Token")
		appID = r.Header.Get("X-App-ID")
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"app_id":"goboost","target_id":"ti-goboost-web","launch_url":"https://goboost.example.com/projects?code=one"}`))
	})
	url, err := c.LaunchShell(context.Background(), "usr_01", "tnt_01", "goboost", "ti-goboost-web")
	if err != nil {
		t.Fatalf("LaunchShell: %v", err)
	}
	if url != "https://goboost.example.com/projects?code=one" {
		t.Fatalf("url = %q", url)
	}
	if path != "/internal/v1/identity/shell/launch" {
		t.Fatalf("path = %q", path)
	}
	if token != "app-token" || appID != "touch-engine" {
		t.Fatalf("headers = %q %q", token, appID)
	}
	for _, want := range []string{`"principal_id":"usr_01"`, `"tenant_id":"tnt_01"`, `"app_id":"goboost"`, `"target_id":"ti-goboost-web"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body %q missing %s", body, want)
		}
	}
}

func TestLaunchShellRefusesNonHTTPSAndEmptyURL(t *testing.T) {
	for _, payload := range []string{
		`{"ok":true,"launch_url":""}`,
		`{"ok":true,"launch_url":"http://goboost.example.com/projects"}`,
		`{"ok":true,"launch_url":"javascript:alert(1)"}`,
	} {
		c, _ := stubIdentity(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(payload))
		})
		if _, err := c.LaunchShell(context.Background(), "usr_01", "tnt_01", "goboost", "ti-goboost-web"); err == nil {
			t.Fatalf("accepted unsafe launch_url payload %s", payload)
		}
	}
}
