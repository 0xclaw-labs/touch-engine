package httpapi

import (
	"net/http"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/custpublish"
)

func TestConfiguredCampaignDoesNotOfferLegacyManualBypass(t *testing.T) {
	x := newCampaignLiveFixture(t)
	base := "/api/v1/public/links/" + x.public.code
	for _, tc := range []struct{ method, suffix, body string }{
		{http.MethodGet, "/publish-capabilities", ""},
		{http.MethodPost, "/publish-attempts", `{"platform":"douyin","publisher":"activity_customer","copy":"原活动参考内容"}`},
	} {
		status, out := x.public.public(t, tc.method, base+tc.suffix, visitorCookie, tc.body)
		if status != http.StatusConflict || out["error"] != "original_participation_required" {
			t.Fatalf("configured campaign exposed old manual workflow: %s %d %v", tc.suffix, status, out)
		}
	}
	var n int
	if err := x.f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM customer_publish_attempts WHERE campaign_id=?`, x.f.camp1ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rejected preview wrote history: count=%d err=%v", n, err)
	}
	preview, err := custpublish.Preview(custpublish.PreviewInput{Platform: custpublish.Platform("douyin"), Publisher: custpublish.PublisherActivityCustomer, Copy: "已有参考内容"})
	if err != nil {
		t.Fatal(err)
	}
	old, err := x.f.s.St.CreateCustomerPublishPreview(x.f.tenA, x.f.camp1ID, x.public.code, preview)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ method, suffix, body string }{
		{http.MethodPost, "/export", `{}`},
		{http.MethodPost, "/self-report", `{}`},
		{http.MethodPost, "/confirm", `{}`},
		{http.MethodPatch, "", `{"copy":"新内容"}`},
	} {
		status, out := x.public.public(t, tc.method, base+"/publish-attempts/"+old.ID+tc.suffix, visitorCookie, tc.body)
		if status != http.StatusConflict || out["error"] != "original_participation_required" {
			t.Fatalf("old attempt bypassed current workflow: %s %d %v", tc.suffix, status, out)
		}
	}
	kept, err := x.f.s.St.GetCustomerPublish(old.ID, x.f.tenA)
	if err != nil || kept.Status != old.Status || kept.SelfReported || kept.Copy != old.Copy {
		t.Fatalf("old fact was changed: %+v %v", kept, err)
	}
	status, out := x.public.public(t, http.MethodGet, base+"/reward-summary", visitorCookie, "")
	if status != http.StatusOK || out["promise"] == nil {
		t.Fatalf("legacy-only gate blocked original reward read: %d %v", status, out)
	}
}
