package production

// production_test.go: HUI-4595 公共权威 seam 验收(quote/task)。
//
// 纪律:公共权威=派诺生态统一账单与公共任务;本包只是客户端 seam,绝不伪造
// 报价/任务号。httptest 桩只进测试入口。
//
// 覆盖:
//   - BillingQuoteAuthority: unconfirmed 报价→ErrNotConfirmed;confirmed→
//     Quote 原样返回;transport 失败→ErrUnavailable;URL/token 未配→
//     Ready()=false 且调用返回 ErrUnavailable(fail-closed);
//   - TaskSubmitter: 平台受理回 task_id/hold_id→TaskRef;未配置 fail-closed;
//   - NewQuoteAuthority/NewTaskSubmitter: 缺任一配置→nil(调用侧诚实关闭)。

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBillingQuoteAuthorityVerifiesConfirmedQuote(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		if r.Header.Get("X-PilotSeaview-Internal-Token") != "bill-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"quote": map[string]any{
				"quote_ref": "quote_confirmed_1", "amount_minor": 8900, "currency": "CNY",
				"status": "confirmed", "idempotency_key": "handoff-key-1",
			},
		})
	}))
	defer srv.Close()

	c := NewQuoteAuthority(srv.URL, "bill-token", "touch-engine")
	if !c.Ready() {
		t.Fatal("client must be ready with url+token")
	}
	q, err := c.ConfirmedQuote(context.Background(), "quote_confirmed_1")
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if q.Ref != "quote_confirmed_1" || q.AmountMinor != 8900 || q.Currency != "CNY" || q.Status != "confirmed" {
		t.Fatalf("unexpected quote: %+v", q)
	}
	if gotPath != "/internal/v1/billing/unified/quote" {
		t.Fatalf("quote endpoint drifted: %s", gotPath)
	}
	if gotBody["quote_ref"] != "quote_confirmed_1" {
		t.Fatalf("quote_ref not sent: %+v", gotBody)
	}
}

func TestBillingQuoteAuthorityRefusesUnconfirmed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"quote": map[string]any{"quote_ref": "q1", "amount_minor": 0, "status": "pending"},
		})
	}))
	defer srv.Close()
	c := NewQuoteAuthority(srv.URL, "t", "touch-engine")
	if _, err := c.ConfirmedQuote(context.Background(), "q1"); !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("want ErrNotConfirmed, got %v", err)
	}
}

func TestBillingQuoteAuthorityFailClosed(t *testing.T) {
	// 未配置(空 url/token):fail-closed,绝不把"无法核实"说成"已核实"。
	c := NewQuoteAuthority("", "", "touch-engine")
	if c.Ready() {
		t.Fatal("empty config must not be ready")
	}
	if _, err := c.ConfirmedQuote(context.Background(), "q1"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
	// transport 错误同形。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c2 := NewQuoteAuthority(srv.URL, "t", "touch-engine")
	if _, err := c2.ConfirmedQuote(context.Background(), "q1"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("transport: want ErrUnavailable, got %v", err)
	}
}

func TestTaskSubmitterReceivesTaskRef(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"task_id": "task_prod_42", "status": "PENDING", "hold_id": "hold_1",
		})
	}))
	defer srv.Close()
	s := NewTaskSubmitter(srv.URL, "task-token", "touch-engine")
	if !s.Ready() {
		t.Fatal("submitter must be ready")
	}
	ref, err := s.Submit(context.Background(), TaskSubmission{
		IdempotencyKey: "handoff-key-1", AccountID: "acct_1", Kind: "touch_content_production",
		ParamsJSON: `{"pack_id":"scpk_1"}`,
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if ref != "task_prod_42" {
		t.Fatalf("want task_prod_42, got %s", ref)
	}
	if gotBody["idempotency_key"] != "handoff-key-1" || gotBody["app_id"] != "touch-engine" {
		t.Fatalf("submission body: %+v", gotBody)
	}
}

func TestTaskSubmitterFailClosed(t *testing.T) {
	s := NewTaskSubmitter("", "", "touch-engine")
	if s.Ready() {
		t.Fatal("empty config must not be ready")
	}
	if _, err := s.Submit(context.Background(), TaskSubmission{IdempotencyKey: "k"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
}
