package adapters

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff"
)

func xhsRequest(method string, media []publishhandoff.MediaItem) publishhandoff.PrepareRequest {
	h := publishhandoff.ActionHandoff{
		Handoff: publishhandoff.Handoff{
			ID: "pha_test_xhs", TenantID: "tnt_1", StoreID: "st_1",
			Platform: "xiaohongshu", Scenario: "store_promo",
			Status:         publishhandoff.StateHandoffReady,
			EvidenceSource: publishhandoff.EvidenceCustomerAction,
			ContentVersion: "fp-xhs-1",
		},
		ActionID: "sa_x", SessionID: "sess_x", PayloadKind: publishhandoff.PayloadKindMedia,
		Method: method, Environment: "ios_safari", PlanKind: publishhandoff.PlanOfficialHandoff,
	}
	return publishhandoff.PrepareRequest{
		Handoff: h,
		Plan:    publishhandoff.ActionPlan{Kind: publishhandoff.PlanOfficialHandoff},
		Text:    publishhandoff.TextInput{Title: "店庆", Body: "来店里玩", Topics: []string{"探店", "咖啡"}},
		Media:   media,
		MediaRefs: map[string]string{
			"mvid": "https://media.example/xv", "mcov": "https://media.example/xc",
			"mi1": "https://media.example/i1", "mi2": "https://media.example/i2",
		},
		SessionID: "sess_x", Environment: "ios_safari",
	}
}

func TestXhsPrepareVideoPayloadHasNoTextFields(t *testing.T) {
	a := NewXiaohongshuAdapter(adapterTestDB(t), XhsPublicConfig{AppID: "xhs_app"})
	res, err := a.Prepare(context.Background(), xhsRequest("js_media_handoff", []publishhandoff.MediaItem{
		{Kind: publishhandoff.MediaVideo, AssetID: "mvid"},
		{Kind: publishhandoff.MediaCover, AssetID: "mcov"},
	}))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	act := res.ClientAction
	if act.Type != publishhandoff.ClientActionJSMediaHandoff || act.Params["sdk"] != "xhs_js" {
		t.Fatalf("client action = %+v", act)
	}
	// 红线:载荷与 params 任何位置不得出现标题/正文/话题字段。
	var payload map[string]any
	if err := json.Unmarshal([]byte(act.Params["payload"]), &payload); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	for _, banned := range []string{"title", "content", "topics", "desc"} {
		if _, ok := payload[banned]; ok {
			t.Fatalf("payload carries banned text field %q", banned)
		}
	}
	raw, _ := json.Marshal(act.Params)
	if strings.Contains(string(raw), "店庆") || strings.Contains(string(raw), "探店") {
		t.Fatal("SDK params must not leak reference copy text")
	}
	if payload["type"] != "video" || payload["video"] != "https://media.example/xv" || payload["cover"] != "https://media.example/xc" {
		t.Fatalf("video payload wrong: %v", payload)
	}
	// copy_view:参考文案只走 Caption(独立复制区)。
	if !strings.Contains(act.Caption, "店庆") || !strings.Contains(act.Caption, "#探店") {
		t.Fatalf("caption copy view wrong: %q", act.Caption)
	}
}

func TestXhsPrepareOrderedImagesKeepsOrder(t *testing.T) {
	a := NewXiaohongshuAdapter(adapterTestDB(t), XhsPublicConfig{AppID: "xhs_app"})
	res, err := a.Prepare(context.Background(), xhsRequest("js_media_handoff", []publishhandoff.MediaItem{
		{Kind: publishhandoff.MediaImage, AssetID: "mi1"},
		{Kind: publishhandoff.MediaImage, AssetID: "mi2"},
	}))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	var payload struct {
		Type   string   `json:"type"`
		Images []string `json:"images"`
	}
	if err := json.Unmarshal([]byte(res.ClientAction.Params["payload"]), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Type != "normal" {
		t.Fatalf("type = %q", payload.Type)
	}
	if len(payload.Images) != 2 || payload.Images[0] != "https://media.example/i1" || payload.Images[1] != "https://media.example/i2" {
		t.Fatalf("image order wrong: %v", payload.Images)
	}
}

func TestXhsIssuedPayloadRecorded(t *testing.T) {
	d := adapterTestDB(t)
	a := NewXiaohongshuAdapter(d, XhsPublicConfig{AppID: "xhs_app"})
	req := xhsRequest("js_media_handoff", []publishhandoff.MediaItem{{Kind: publishhandoff.MediaImage, AssetID: "mi1"}})
	if _, err := a.Prepare(context.Background(), req); err != nil {
		t.Fatalf("prepare1: %v", err)
	}
	if _, err := a.Prepare(context.Background(), req); err != nil {
		t.Fatalf("prepare2 (retry must reuse row): %v", err)
	}
	var n int
	if err := d.QueryRow(`SELECT COUNT(1) FROM xiaohongshu_handoff_refs WHERE handoff_id=?`, req.Handoff.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("issued rows = %d (want single row per handoff)", n)
	}
	var sha string
	if err := d.QueryRow(`SELECT payload_sha256 FROM xiaohongshu_handoff_refs WHERE handoff_id=?`, req.Handoff.ID).Scan(&sha); err != nil {
		t.Fatal(err)
	}
	if sha == "" {
		t.Fatal("payload fingerprint missing (审计可核无文字字段)")
	}
}

func TestXhsMissingMediaRefusedHonestly(t *testing.T) {
	a := NewXiaohongshuAdapter(adapterTestDB(t), XhsPublicConfig{AppID: "xhs_app"})
	if _, err := a.Prepare(context.Background(), xhsRequest("js_media_handoff", nil)); err == nil {
		t.Fatal("no media = honest error, not a fake JS handoff")
	}
}

func TestXhsUnsupportedMethods(t *testing.T) {
	a := NewXiaohongshuAdapter(nil, XhsPublicConfig{AppID: "xhs_app"})
	if _, err := a.Prepare(context.Background(), xhsRequest("manual_material", nil)); !isUnsupported(err) {
		t.Fatalf("prepare other method err = %v", err)
	}
	if err := a.Submit(context.Background(), publishhandoff.Handoff{}, publishhandoff.PrepareResult{}); !isUnsupported(err) {
		t.Fatalf("submit err = %v", err)
	}
	out, err := a.Query(context.Background(), publishhandoff.Handoff{})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if out.State != "unknown" || out.Receipt != nil {
		t.Fatalf("JS 无查询契约必须保持 unknown: state=%q receipt=%v", out.State, out.Receipt)
	}
	if _, err := a.VerifyCallback(context.Background(), publishhandoff.CallbackPayload{Body: []byte(`{"success":true}`)}); !isUnsupported(err) {
		t.Fatalf("callback err = %v (本地伪 success 不得入库)", err)
	}
}

func TestXhsMissingAppIDHonest(t *testing.T) {
	a := NewXiaohongshuAdapter(nil, XhsPublicConfig{})
	if _, err := a.Prepare(context.Background(), xhsRequest("js_media_handoff", []publishhandoff.MediaItem{{Kind: publishhandoff.MediaImage, AssetID: "mi1"}})); err == nil {
		t.Fatal("missing app id must fail honestly")
	}
}
