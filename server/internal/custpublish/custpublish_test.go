package custpublish

// HUI-1670 FEAT-0171: 顾客预览与发布。发布主体是参与活动的顾客。
// 未授权的平台能力必须关闭;按钮、自报、客户端自带 post_id 都不是发布成功。

import (
	"strings"
	"testing"
	"time"
)

func TestMatrixClosesUnauthorizedPublish(t *testing.T) {
	rows := Matrix(nil)
	if len(rows) != 4 {
		t.Fatalf("platforms = %d, want 4", len(rows))
	}
	want := []Platform{PlatformDouyin, PlatformKuaishou, PlatformXiaohongshu, PlatformChannels}
	for i, row := range rows {
		if row.Platform != want[i] {
			t.Fatalf("row[%d] = %s, want %s", i, row.Platform, want[i])
		}
		if row.Publisher != PublisherActivityCustomer {
			t.Fatalf("%s publisher = %s", row.Platform, row.Publisher)
		}
		preview := row.Capability(CapPreview)
		export := row.Capability(CapExport)
		if !preview.Enabled || !export.Enabled {
			t.Fatalf("%s preview/export must stay available for local compliant flow: %+v %+v", row.Platform, preview, export)
		}
		if strings.TrimSpace(preview.Reason) == "" || strings.TrimSpace(export.Reason) == "" {
			t.Fatalf("%s enabled caps must say why they are local-only", row.Platform)
		}
		for _, kind := range []Capability{CapOpenEditor, CapAuthorizedPublish, CapConfirmPublish} {
			cap := row.Capability(kind)
			if cap.Enabled {
				t.Fatalf("%s %s enabled without an authorized app", row.Platform, kind)
			}
			if strings.TrimSpace(cap.Reason) == "" || strings.TrimSpace(cap.EvidenceURL) == "" {
				t.Fatalf("%s %s closed without reason and evidence: %+v", row.Platform, kind, cap)
			}
		}
		if !strings.Contains(row.ManualGuide, "手动") {
			t.Fatalf("%s manual guide missing: %q", row.Platform, row.ManualGuide)
		}
	}
}

func TestNotedAdapterDoesNotEnablePlatforms(t *testing.T) {
	noted := NoteAdapter(nil, "douyin", "douyin-openapi")
	noted = NoteAdapter(noted, "kuaishou", "kuaishou-openapi")
	noted = NoteAdapter(noted, "xiaohongshu", "xhs-write-notes")
	noted = NoteAdapter(noted, "channels", "wechat-channels")
	for _, row := range Matrix(noted) {
		if row.Capability(CapAuthorizedPublish).Enabled || row.Capability(CapConfirmPublish).Enabled || row.Capability(CapOpenEditor).Enabled {
			t.Fatalf("adapter notes enabled %s: %+v", row.Platform, row)
		}
	}
	if len(noted) != 4 {
		t.Fatalf("notes = %d", len(noted))
	}
}

func TestPreviewAndExportAreNotPublish(t *testing.T) {
	a, err := Preview(PreviewInput{
		Platform: PlatformDouyin, Publisher: PublisherActivityCustomer,
		Copy: "  到店打卡  ", AccountLabel: "顾客自己的抖音",
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if a.Status != StatusPreviewed || a.PlatformPostID != "" || a.OutboundCalls != 0 || a.CountsAsPublished() {
		t.Fatalf("preview looked like a publish: %+v", a)
	}
	if a.ContentVersion == "" || a.Copy != "到店打卡" {
		t.Fatalf("copy not normalized into a version: %+v", a)
	}
	exported, pkg, err := Export(a)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if exported.Status != StatusExported || pkg.PostID != "" || exported.CountsAsPublished() {
		t.Fatalf("export counted as publish: %+v pkg=%+v", exported, pkg)
	}
	if !strings.Contains(strings.Join(pkg.Steps, "\n"), "打开抖音") {
		t.Fatalf("manual steps = %v", pkg.Steps)
	}
	if exported.RewardTriggered || RewardDecision(exported).Trigger {
		t.Fatalf("export triggered a reward")
	}
}

func TestMerchantPublisherRejected(t *testing.T) {
	_, err := Preview(PreviewInput{
		Platform: PlatformKuaishou, Publisher: "merchant",
		Copy: "文案", AccountLabel: "商家官方号",
	})
	if err == nil || !strings.Contains(err.Error(), ReasonPublisherMustBeCustomer) {
		t.Fatalf("merchant preview err = %v", err)
	}
}

func TestExplicitConfirmIsNotPublishSuccess(t *testing.T) {
	a := mustPreview(t)
	confirmed, err := Confirm(a, Confirmation{
		ContentVersion: a.ContentVersion, AccountLabel: a.AccountLabel,
		Platform: a.Platform, Publisher: PublisherActivityCustomer, AssetUseAccepted: true,
	})
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if confirmed.Status == StatusPublishConfirmed || confirmed.CountsAsPublished() || confirmed.PlatformPostID != "" {
		t.Fatalf("confirm became publish success: %+v", confirmed)
	}
	if !confirmed.ConfirmationCurrent() {
		t.Fatalf("confirmation not stored: %+v", confirmed)
	}
	if confirmed.RewardTriggered {
		t.Fatalf("confirm triggered reward")
	}
}

func TestCopyOrAccountChangeClearsConfirmation(t *testing.T) {
	a := mustPreview(t)
	a, err := Confirm(a, Confirmation{
		ContentVersion: a.ContentVersion, AccountLabel: a.AccountLabel,
		Platform: a.Platform, Publisher: PublisherActivityCustomer, AssetUseAccepted: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	changed, err := ChangeCopy(a, "换了一条文案")
	if err != nil {
		t.Fatal(err)
	}
	if changed.ConfirmationCurrent() || changed.Status != StatusPreviewed {
		t.Fatalf("copy change kept confirmation: %+v", changed)
	}
	if changed.ContentVersion == a.ContentVersion {
		t.Fatal("content version did not change")
	}
	reconfirmed, err := Confirm(changed, Confirmation{
		ContentVersion: changed.ContentVersion, AccountLabel: changed.AccountLabel,
		Platform: changed.Platform, Publisher: PublisherActivityCustomer, AssetUseAccepted: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	swapped := ChangeAccount(reconfirmed, "另一个顾客账号")
	if swapped.ConfirmationCurrent() || swapped.Status != StatusPreviewed {
		t.Fatalf("account change kept confirmation or the old export: %+v", swapped)
	}
}

func TestChangeCopyRejectsEmpty(t *testing.T) {
	a := mustPreview(t)
	next, err := ChangeCopy(a, "   ")
	if err == nil || next.Copy != a.Copy || next.ContentVersion != a.ContentVersion {
		t.Fatalf("empty copy was stored: %v %+v", err, next)
	}
}

func TestAccountChangeAfterExportDropsStalePackage(t *testing.T) {
	a, _, err := Export(mustPreview(t))
	if err != nil {
		t.Fatal(err)
	}
	a, err = Confirm(a, Confirmation{
		ContentVersion: a.ContentVersion, AccountLabel: a.AccountLabel,
		Platform: a.Platform, Publisher: PublisherActivityCustomer, AssetUseAccepted: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != StatusExported {
		t.Fatalf("confirm changed status: %s", a.Status)
	}
	swapped := ChangeAccount(a, "另一个顾客账号")
	if swapped.Status != StatusPreviewed || swapped.ConfirmationCurrent() {
		t.Fatalf("stale export survived an account change: %+v", swapped)
	}
}

func TestSelfReportAndClientPostIDAreNotSuccess(t *testing.T) {
	a, _, err := Export(mustPreview(t))
	if err != nil {
		t.Fatal(err)
	}
	reported := SelfReport(a, "dy_fake_post")
	if reported.Status == StatusPublishConfirmed || reported.CountsAsPublished() || reported.PlatformPostID != "" {
		t.Fatalf("self-report stored a post id: %+v", reported)
	}
	if !reported.SelfReported {
		t.Fatal("self-report was dropped")
	}
	if RewardDecision(reported).Trigger || RewardDecision(reported).Reason != ReasonNoOfficialEvidence {
		t.Fatalf("reward = %+v", RewardDecision(reported))
	}
}

func TestClosedPublishDoesNotSendOrConfirm(t *testing.T) {
	a, _, err := Export(mustPreview(t))
	if err != nil {
		t.Fatal(err)
	}
	a, err = Confirm(a, Confirmation{
		ContentVersion: a.ContentVersion, AccountLabel: a.AccountLabel,
		Platform: a.Platform, Publisher: PublisherActivityCustomer, AssetUseAccepted: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	next, err := RequestPublish(a)
	if err == nil || !strings.Contains(err.Error(), ReasonCapabilityClosed) {
		t.Fatalf("request publish err = %v", err)
	}
	if next.OutboundCalls != 0 || next.PlatformPostID != "" || next.Status == StatusPublishConfirmed || next.Status == StatusPublishRequested {
		t.Fatalf("closed publish still sent or requested: %+v", next)
	}
	if next.CountsAsPublished() {
		t.Fatal("closed publish counted")
	}
	opened, err := OpenEditor(a)
	if err == nil || opened.Status == StatusEditorOpened || opened.OutboundCalls != 0 {
		t.Fatalf("editor open = %v %+v", err, opened)
	}
}

func TestTimeoutQueriesInsteadOfResending(t *testing.T) {
	pending := Attempt{
		Platform: PlatformXiaohongshu, Publisher: PublisherActivityCustomer,
		Status: StatusPublishRequested, Copy: "文案", AccountLabel: "顾客小红书",
		ContentVersion: "abc", OutboundCalls: 1,
	}
	timed, err := MarkTimedOut(pending, time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if timed.Status != StatusUnknown || timed.OutboundCalls != 1 {
		t.Fatalf("timeout resend or wrong status: %+v", timed)
	}
	resent, err := Resend(timed)
	if err == nil || !strings.Contains(err.Error(), ReasonQueryRequired) || resent.OutboundCalls != 1 {
		t.Fatalf("resend = %v %+v", err, resent)
	}
	queried, err := Query(timed)
	if err != nil {
		t.Fatal(err)
	}
	if queried.OutboundCalls != 1 || queried.QueryCount != 1 || queried.Status != StatusUnknown || queried.PlatformPostID != "" || queried.CountsAsPublished() {
		t.Fatalf("query confirmed or resent: %+v", queried)
	}
}

func TestOfficialLookingEvidenceCannotConfirmWhileUnauthorized(t *testing.T) {
	pending := Attempt{
		Platform: PlatformDouyin, Publisher: PublisherActivityCustomer,
		Status: StatusUnknown, Copy: "文案", AccountLabel: "顾客抖音", ContentVersion: "v1",
	}
	plays := int64(10)
	next, err := ApplyOfficialEvidence(pending, Evidence{
		Source: "official_query", PostID: "dy_real", Platform: PlatformDouyin,
		ContentVersion: "v1", AccountLabel: "顾客抖音", Plays: &plays,
	})
	if err == nil || !strings.Contains(err.Error(), ReasonCapabilityClosed) {
		t.Fatalf("evidence err = %v", err)
	}
	if next.PlatformPostID != "" || next.Status == StatusPublishConfirmed || next.CountsAsPublished() || next.ReceiptSource != "" {
		t.Fatalf("unauthorized evidence stored: %+v", next)
	}
	playsMetric := InterpretMetric(false, &plays)
	if playsMetric.Available || playsMetric.Value != nil {
		t.Fatalf("unauthorized plays became a number: %+v", playsMetric)
	}
	missing := InterpretMetric(true, nil)
	if missing.Available || missing.Value != nil {
		t.Fatalf("missing metric became zero: %+v", missing)
	}
}

func TestOnlyOfficialReceiptCountsAndRewardStaysUnissued(t *testing.T) {
	cases := []Attempt{
		{Status: StatusPreviewed},
		{Status: StatusExported},
		{Status: StatusEditorOpened},
		{Status: StatusPublishRequested},
		{Status: StatusUnknown, PlatformPostID: "client"},
		{Status: StatusPublishConfirmed, PlatformPostID: "dy_x", ReceiptSource: "client_self_report", Publisher: PublisherActivityCustomer},
		{Status: StatusPublishConfirmed, PlatformPostID: "", ReceiptSource: ReceiptOfficialQuery, Publisher: PublisherActivityCustomer},
	}
	for _, a := range cases {
		if a.CountsAsPublished() {
			t.Fatalf("counted without official receipt: %+v", a)
		}
		if RewardDecision(a).Trigger {
			t.Fatalf("reward triggered: %+v", a)
		}
	}
	ok := Attempt{
		Status: StatusPublishConfirmed, PlatformPostID: "dy_verified",
		ReceiptSource: ReceiptOfficialQuery, Publisher: PublisherActivityCustomer,
	}
	if !ok.CountsAsPublished() {
		t.Fatal("official receipt did not count")
	}
	decision := RewardDecision(ok)
	if decision.Trigger || decision.Reason != ReasonRewardOutOfScope {
		t.Fatalf("confirmed attempt issued a reward: %+v", decision)
	}
	likes := InterpretMetric(false, nil)
	if likes.Available || likes.Value != nil || strings.TrimSpace(likes.Reason) == "" {
		t.Fatalf("likes = %+v", likes)
	}
}

func mustPreview(t *testing.T) Attempt {
	t.Helper()
	a, err := Preview(PreviewInput{
		Platform: PlatformDouyin, Publisher: PublisherActivityCustomer,
		Copy: "到店打卡", AccountLabel: "顾客自己的抖音",
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestManualPreviewDoesNotRequireUntrustedAccountLabel(t *testing.T) {
	a, err := Preview(PreviewInput{Platform: PlatformDouyin, Publisher: PublisherActivityCustomer, Copy: "本人选择的内容"})
	if err != nil {
		t.Fatalf("untrusted account label should be optional: %v", err)
	}
	got, err := Confirm(a, Confirmation{Platform: a.Platform, Publisher: PublisherActivityCustomer, ContentVersion: a.ContentVersion, AssetUseAccepted: true})
	if err != nil || !got.ConfirmationCurrent() {
		t.Fatalf("content confirmation: %v", err)
	}
	if got.PlatformPostID != "" {
		t.Fatal("manual confirmation became official account/publish fact")
	}
}
