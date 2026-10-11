package matrixconsume

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/matrixhandoff"
)

func TestEmptySupplyURLDoesNotDial(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("empty supply dialed")
		return nil, nil
	})}
	perm, err := NewHTTPPermission("", client).Decide(context.Background(), "usr", "ten", "brd")
	if err != nil || perm.Supply != SupplyMissing || perm.Allowed {
		t.Fatalf("permission = %+v %v", perm, err)
	}
	sink := NewHTTPDraftSink("  ", client)
	if sink.Available() {
		t.Fatal("blank url is available")
	}
}

func TestHTTPSupplyAcceptsDraftAndRejectsPublished(t *testing.T) {
	var sawPerm, sawDraft bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/permissions":
			sawPerm = true
			if r.URL.Query().Get("action") != "brand_matrix_draft" || r.URL.Query().Get("tenant_id") != "ten" {
				t.Errorf("query %s", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"allowed":true,"supply":"ready","reason":"ok","payer_id":"pay","license_ref":"lic","license_expires_at":"2027-01-01T00:00:00Z"}`))
		case "/v1/matrix-drafts":
			sawDraft = true
			if r.Header.Get("X-Matrix-Tenant") != "ten" {
				t.Errorf("tenant header %q", r.Header.Get("X-Matrix-Tenant"))
			}
			raw, _ := io.ReadAll(r.Body)
			var posted map[string]any
			if err := json.Unmarshal(raw, &posted); err != nil {
				t.Errorf("draft json: %v", err)
			}
			if posted["schema_version"] != "matrix-draft-request/v1" || posted["source_app"] != "touch" || posted["content_type"] != "activity" || posted["campaign_id"] != "cmp" || posted["revision"] != float64(3) || posted["grant_ref"] == "" || posted["payer_id"] != "pay" {
				t.Errorf("draft body = %s", raw)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"draft_id":"md_1","status":"draft","published":false,"charged":false,"idempotent_replay":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	gate := NewHTTPPermission(srv.URL, srv.Client())
	sink := NewHTTPDraftSink(srv.URL, srv.Client())
	out, err := New(gate, sink, nil).Schedule(context.Background(), time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC), Actor{
		PrincipalID: "usr", TenantID: "ten", BrandID: "brd", Role: "org_owner",
	}, matrixhandoff.Request{
		TenantID: "ten", BrandID: "brd", StoreID: "sto", ActivityID: "cmp", ActivityVersion: 3,
		Assets:              []matrixhandoff.Asset{{ID: "ast", Hash: strings.Repeat("ab", 32), Kind: "video", GrantRef: "grant_x"}},
		OfferText:           "到店有礼",
		OfferExpiry:         time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		Disclosure:          "这是商家活动草稿，不是已经发出的内容。",
		ReturnLocationToken: "return:cmp",
	}, Surfaces{LeadEnabled: true, UGCEnabled: true})
	if err != nil || !sawPerm || !sawDraft || out.OutboundComplete || out.Draft.Status != matrixhandoff.StatusDraft {
		t.Fatalf("schedule err=%v perm=%v draft=%v out=%+v", err, sawPerm, sawDraft, out)
	}
	if strings.Contains(out.Copy, "发布成功") || strings.Contains(out.Copy, "外发完成") {
		t.Fatalf("copy %q", out.Copy)
	}

	published := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/permissions" {
			_, _ = w.Write([]byte(`{"allowed":true,"supply":"ready","payer_id":"pay","license_ref":"lic","license_expires_at":"2027-01-01T00:00:00Z"}`))
			return
		}
		_, _ = w.Write([]byte(`{"draft_id":"md_2","status":"draft","published":true,"charged":false,"idempotent_replay":false}`))
	}))
	defer published.Close()
	_, err = New(NewHTTPPermission(published.URL, published.Client()), NewHTTPDraftSink(published.URL, published.Client()), nil).Schedule(
		context.Background(), time.Now().UTC(), Actor{PrincipalID: "usr", TenantID: "ten", BrandID: "brd", Role: "org_owner"},
		matrixhandoff.Request{
			TenantID: "ten", BrandID: "brd", StoreID: "sto", ActivityID: "cmp2", ActivityVersion: 1,
			Assets:              []matrixhandoff.Asset{{ID: "ast", Hash: strings.Repeat("cd", 32), Kind: "video", GrantRef: "grant_y"}},
			OfferText:           "到店有礼",
			OfferExpiry:         time.Now().Add(24 * time.Hour),
			Disclosure:          "这是商家活动草稿，不是已经发出的内容。",
			ReturnLocationToken: "return:cmp",
		}, Surfaces{LeadEnabled: true, UGCEnabled: false})
	if err != ErrNotDraft {
		t.Fatalf("published ack err = %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
