package adapters

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff"
)

// adapterTestDB opens a migrated database (embedded migrations apply in
// filename order; fixture data lives only here — 测试入口).
func adapterTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "adapters.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	// FK 夹具:tenant/store 最小行(adapter 表引用它们)。
	if _, err := d.Exec(`INSERT INTO tenants (id, name, created_at) VALUES ('tnt_1','seed-tenant','2026-11-11T00:00:00Z')`); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO stores (id, tenant_id, name, created_at, updated_at) VALUES ('st_1','tnt_1','seed-store','2026-11-11T00:00:00Z','2026-11-11T00:00:00Z')`); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	return d
}

func douyinRequest(method string) publishhandoff.PrepareRequest {
	h := publishhandoff.ActionHandoff{
		Handoff: publishhandoff.Handoff{
			ID: "pha_test_douyin", TenantID: "tnt_1", StoreID: "st_1",
			Platform: "douyin", Scenario: "store_promo",
			Status:         publishhandoff.StateHandoffReady,
			EvidenceSource: publishhandoff.EvidenceCustomerAction,
			ContentVersion: "fp-v1",
		},
		ActionID: "sa_1", SessionID: "sess_1", PayloadKind: publishhandoff.PayloadKindMedia,
		Method: method, Environment: "ios_safari", PlanKind: publishhandoff.PlanOfficialHandoff,
	}
	return publishhandoff.PrepareRequest{
		Handoff: h,
		Plan:    publishhandoff.ActionPlan{Kind: publishhandoff.PlanOfficialHandoff},
		Media: []publishhandoff.MediaItem{
			{Kind: publishhandoff.MediaVideo, AssetID: "mvid"},
			{Kind: publishhandoff.MediaCover, AssetID: "mcov"},
		},
		MediaRefs: map[string]string{"mvid": "https://media.example/v", "mcov": "https://media.example/c"},
		SessionID: "sess_1", Environment: "ios_safari",
	}
}

func TestDouyinPrepareRequiresClientKey(t *testing.T) {
	a := NewDouyinAdapter(nil, DouyinConfig{})
	if _, err := a.Prepare(context.Background(), douyinRequest("h5_share")); err == nil {
		t.Fatal("expected honest error without client_key")
	}
}

func TestDouyinPrepareH5ShareParams(t *testing.T) {
	d := adapterTestDB(t)
	a := NewDouyinAdapter(d, DouyinConfig{ClientKey: "ck_1", AppRef: "app_1"})
	res, err := a.Prepare(context.Background(), douyinRequest("h5_share"))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	act := res.ClientAction
	if act.Type != publishhandoff.ClientActionJSMediaHandoff {
		t.Fatalf("client action type = %q", act.Type)
	}
	if act.Params["client_key"] != "ck_1" || act.Params["share_id"] == "" {
		t.Fatalf("official params missing: %v", act.Params)
	}
	if act.Params["media_url"] != "https://media.example/v" {
		t.Fatalf("media url = %q", act.Params["media_url"])
	}
	for k := range act.Params {
		// 票面红线:不自造签名协议——无 signer 时绝不出现签名字段。
		if k == "sign" || k == "signature" || k == "open_ticket" {
			t.Fatalf("invented signature field %q present", k)
		}
	}
	found := false
	for _, u := range res.UnsupportedFields {
		if contains(u, "官方签名") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing honest signature-gap note in unsupported fields: %v", res.UnsupportedFields)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func TestDouyinShareIDIdempotentAndExpiry(t *testing.T) {
	d := adapterTestDB(t)
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	a := NewDouyinAdapter(d, DouyinConfig{ClientKey: "ck_1", ShareTTL: time.Hour, Now: func() time.Time { return now }})
	r1, err := a.Prepare(context.Background(), douyinRequest("h5_share"))
	if err != nil {
		t.Fatalf("prepare1: %v", err)
	}
	r2, err := a.Prepare(context.Background(), douyinRequest("h5_share"))
	if err != nil {
		t.Fatalf("prepare2: %v", err)
	}
	if r1.ClientAction.Params["share_id"] != r2.ClientAction.Params["share_id"] {
		t.Fatal("same handoff must replay the same share_id")
	}
	// 过期:重铸新 share_id,旧行作废保留。
	now = now.Add(2 * time.Hour)
	a.now = func() time.Time { return now }
	r3, err := a.Prepare(context.Background(), douyinRequest("h5_share"))
	if err != nil {
		t.Fatalf("prepare3: %v", err)
	}
	if r3.ClientAction.Params["share_id"] == r1.ClientAction.Params["share_id"] {
		t.Fatal("expired share_id must not be reused")
	}
	var revoked int
	if err := d.QueryRow(`SELECT COUNT(1) FROM douyin_share_ids WHERE revoked_at!=''`).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	if revoked != 1 {
		t.Fatalf("revoked rows = %d", revoked)
	}
}

func TestDouyinShareIDRevokedOnContentChange(t *testing.T) {
	d := adapterTestDB(t)
	a := NewDouyinAdapter(d, DouyinConfig{ClientKey: "ck_1"})
	r1, err := a.Prepare(context.Background(), douyinRequest("h5_share"))
	if err != nil {
		t.Fatalf("prepare1: %v", err)
	}
	req := douyinRequest("h5_share")
	req.Handoff.ContentVersion = "fp-v2"
	r2, err := a.Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("prepare2: %v", err)
	}
	if r1.ClientAction.Params["share_id"] == r2.ClientAction.Params["share_id"] {
		t.Fatal("content change must mint a fresh share_id")
	}
}

func TestDouyinSubmitAndCallbackUnsupported(t *testing.T) {
	a := NewDouyinAdapter(nil, DouyinConfig{ClientKey: "ck_1"})
	if err := a.Submit(context.Background(), publishhandoff.Handoff{}, publishhandoff.PrepareResult{}); !isUnsupported(err) {
		t.Fatalf("submit err = %v", err)
	}
	if _, err := a.VerifyCallback(context.Background(), publishhandoff.CallbackPayload{Body: []byte("{}")}); !isUnsupported(err) {
		t.Fatalf("verify callback err = %v", err)
	}
}

func isUnsupported(err error) bool {
	return err != nil && contains(err.Error(), "capability not supported")
}

type fakeDouyinQuery struct {
	state     string
	published bool
	item      string
	err       error
}

func (f fakeDouyinQuery) QueryShare(ctx context.Context, shareID string) (string, bool, string, map[string]string, error) {
	if f.err != nil {
		return "", false, "", nil, f.err
	}
	return f.state, f.published, f.item, map[string]string{"official": "1"}, nil
}

func TestDouyinQueryUnknownWithoutSource(t *testing.T) {
	d := adapterTestDB(t)
	a := NewDouyinAdapter(d, DouyinConfig{ClientKey: "ck_1"})
	out, err := a.Query(context.Background(), douyinRequest("h5_share").Handoff.Handoff)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if out.State != "unknown" || out.Receipt != nil {
		t.Fatalf("state=%q receipt=%v", out.State, out.Receipt)
	}
}

func TestDouyinQueryPublishedMintsBoundReceipt(t *testing.T) {
	d := adapterTestDB(t)
	a := NewDouyinAdapter(d, DouyinConfig{ClientKey: "ck_1", AppRef: "app_1", Query: fakeDouyinQuery{state: "published", published: true, item: "item_9"}})
	h := douyinRequest("h5_share").Handoff.Handoff
	// Prepare 先铸 share_id 映射(StartAction 真实时序)。
	if _, err := a.Prepare(context.Background(), douyinRequest("h5_share")); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	out, err := a.Query(context.Background(), h)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if out.Receipt == nil {
		t.Fatal("expected receipt for explicit published result")
	}
	r := out.Receipt
	if r.HandoffID != h.ID || r.TenantID != h.TenantID || r.PostID != "item_9" {
		t.Fatalf("receipt bindings wrong: %+v", r)
	}
	if r.Outcome != publishhandoff.ReceiptOutcomePublished {
		t.Fatalf("outcome = %q", r.Outcome)
	}
	var item string
	if err := d.QueryRow(`SELECT provider_item FROM douyin_provider_refs WHERE handoff_id=?`, h.ID).Scan(&item); err != nil {
		t.Fatalf("provider ref not saved: %v", err)
	}
	if item != "item_9" {
		t.Fatalf("provider item = %q", item)
	}
}

func TestDouyinQueryFailureStaysUnknown(t *testing.T) {
	d := adapterTestDB(t)
	a := NewDouyinAdapter(d, DouyinConfig{ClientKey: "ck_1", Query: fakeDouyinQuery{err: errFake{}}})
	out, err := a.Query(context.Background(), douyinRequest("h5_share").Handoff.Handoff)
	if err != nil {
		t.Fatalf("query must not surface transport errors as state changes: %v", err)
	}
	if out.State != "unknown" || out.Receipt != nil {
		t.Fatalf("state=%q receipt=%v", out.State, out.Receipt)
	}
}

type errFake struct{}

func (errFake) Error() string { return "network down" }
