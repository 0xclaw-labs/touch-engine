package httpapi

// HUI-3553 W5：受信服务生产身份中间件（双 auth，服务路径优先；旧路径零变化）。

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/config"
)

func serviceProducerServer(tokens, roles string) (*Server, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	cfg := config.Load(func(k string) string {
		switch k {
		case "TOUCH_SERVICE_TOKENS":
			return tokens
		case "TOUCH_SERVICE_ROLES":
			return roles
		}
		return ""
	})
	return &Server{Cfg: cfg, Log: log.New(buf, "", 0)}, buf
}

func producerRequest(headers map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/content-handoffs/cho_x/queue-view", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

func TestRequireServiceProducerServicePath(t *testing.T) {
	s, logBuf := serviceProducerServer("modelxing-runtime:tok-1", "")
	handler := s.requireServiceProducer(func(w http.ResponseWriter, r *http.Request) {
		producer := serviceProducerFrom(r.Context())
		if producer == nil || producer.ServiceID != "modelxing-runtime" {
			t.Fatalf("producer context missing: %+v", producer)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, producerRequest(map[string]string{
		serviceIDHeader: "modelxing-runtime", serviceTokenHeader: "tok-1",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("valid service producer: want 200, got %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(logBuf.String(), "tok-1") {
		t.Fatalf("token leaked into logs: %s", logBuf.String())
	}
}

// 服务路径优先：同时带服务身份与（本应失败的）旧 session 凭据时，服务路径胜出。
func TestRequireServiceProducerPriorityOverSession(t *testing.T) {
	s, _ := serviceProducerServer("modelxing-runtime:tok-1", "")
	called := false
	handler := s.requireServiceProducer(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, producerRequest(map[string]string{
		serviceIDHeader:    "modelxing-runtime",
		serviceTokenHeader: "tok-1",
		"X-Internal-Token": "wrong", // legacy would 401; service path must win
		"X-Session-Token":  "sess",
		"X-Tenant-ID":      "ten-x",
	}))
	if rec.Code != http.StatusNoContent || !called {
		t.Fatalf("service path must take priority: code=%d called=%v body=%s", rec.Code, called, rec.Body.String())
	}
}

func TestRequireServiceProducerRejectsBadIdentity(t *testing.T) {
	cases := []struct {
		name     string
		tokens   string
		roles    string
		headers  map[string]string
		wantCode int
	}{
		{"missing token", "modelxing-runtime:tok-1", "", map[string]string{serviceIDHeader: "modelxing-runtime"}, http.StatusUnauthorized},
		{"wrong token", "modelxing-runtime:tok-1", "", map[string]string{serviceIDHeader: "modelxing-runtime", serviceTokenHeader: "tok-2"}, http.StatusUnauthorized},
		{"unregistered service", "modelxing-runtime:tok-1", "", map[string]string{serviceIDHeader: "goboost", serviceTokenHeader: "tok-1"}, http.StatusUnauthorized},
		{"registry empty", "", "", map[string]string{serviceIDHeader: "modelxing-runtime", serviceTokenHeader: "tok-1"}, http.StatusServiceUnavailable},
		{"role not producer", "modelxing-runtime:tok-1", "modelxing-runtime:viewer", map[string]string{serviceIDHeader: "modelxing-runtime", serviceTokenHeader: "tok-1"}, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, logBuf := serviceProducerServer(tc.tokens, tc.roles)
			called := false
			handler := s.requireServiceProducer(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			})
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, producerRequest(tc.headers))
			if rec.Code != tc.wantCode || called {
				t.Fatalf("want %d and handler not called, got %d called=%v body=%s", tc.wantCode, rec.Code, called, rec.Body.String())
			}
			if strings.Contains(logBuf.String(), "tok-1") || strings.Contains(logBuf.String(), "tok-2") {
				t.Fatalf("token leaked into logs: %s", logBuf.String())
			}
		})
	}
}

// 无服务头 → 精确委托旧 requireSession：同一请求的旧响应（状态码+body）必须
// 完全一致；旧路径不因新中间件放宽或改写。
func TestRequireServiceProducerDelegatesLegacyWithoutHeader(t *testing.T) {
	s, _ := serviceProducerServer("modelxing-runtime:tok-1", "")
	req := producerRequest(nil)

	legacy := httptest.NewRecorder()
	s.requireSession(func(w http.ResponseWriter, r *http.Request) {}).ServeHTTP(legacy, req)

	rec := httptest.NewRecorder()
	called := false
	s.requireServiceProducer(func(w http.ResponseWriter, r *http.Request) { called = true }).ServeHTTP(rec, req)

	if called {
		t.Fatal("legacy path must not reach handler without platform config")
	}
	if rec.Code != legacy.Code || rec.Body.String() != legacy.Body.String() {
		t.Fatalf("legacy delegation changed the response: legacy=%d %q new=%d %q",
			legacy.Code, legacy.Body.String(), rec.Code, rec.Body.String())
	}
}
