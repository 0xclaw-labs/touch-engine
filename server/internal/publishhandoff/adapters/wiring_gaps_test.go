package adapters

import (
	"context"
	"strings"
	"testing"
)

func TestUnconfiguredSignersNeverIssueExecutablePayload(t *testing.T) {
	d := adapterTestDB(t)
	if _, err := NewDouyinAdapter(d, DouyinConfig{ClientKey: "key"}).Prepare(context.Background(), douyinRequest("h5_share")); err == nil {
		t.Fatal("nil signer issued an executable Douyin action")
	}
	if _, err := NewXiaohongshuAdapter(d, XhsPublicConfig{AppID: "key"}).Prepare(context.Background(), xhsRequest("js_media_handoff", nil)); err == nil || !strings.Contains(err.Error(), "sign") {
		t.Fatalf("missing signer should fail before issuing a media action: %v", err)
	}
}

type blockedKsPlatform struct {
	fakeKsPlatform
	entered chan struct{}
	release chan struct{}
}

func (f *blockedKsPlatform) UploadMedia(ctx context.Context, tk KsToken, kind, ctype string, b []byte) (string, error) {
	if kind == "video" {
		close(f.entered)
		<-f.release
	}
	return "upload", nil
}
func TestKuaishouConcurrentSubmitCannotCreateTwoWorks(t *testing.T) {
	d := adapterTestDB(t)
	f := &blockedKsPlatform{entered: make(chan struct{}), release: make(chan struct{})}
	f.photoID = "one-work"
	a := NewKuaishouAdapter(d, ksConfig(f))
	req := ksRequestBase("oauth_server_push", true, mediaFixtureServer(t))
	p, err := a.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.FinishOAuth(context.Background(), "code", p.ClientAction.Params["state"]); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Submit(context.Background(), req.Handoff.Handoff, p) }()
	<-f.entered
	var stage string
	_ = d.QueryRow(`SELECT stage FROM kuaishou_publish_jobs WHERE handoff_id=?`, req.Handoff.ID).Scan(&stage)
	if stage != "uploading" {
		close(f.release)
		<-done
		t.Fatalf("expected uploading, got %q", stage)
	}
	second := NewKuaishouAdapter(d, ksConfig(&fakeKsPlatform{photoID: "duplicate", uploadRef: "up"}))
	secondErr := second.Submit(context.Background(), req.Handoff.Handoff, p)
	close(f.release)
	firstErr := <-done
	if firstErr != nil {
		t.Fatal(firstErr)
	}
	if secondErr == nil {
		t.Fatal("concurrent submit started a second platform work")
	}
}

func TestKuaishouAutomaticCaptionUsesAdmissionFieldSupportOnly(t *testing.T) {
	d := adapterTestDB(t)
	a := NewKuaishouAdapter(d, ksConfig(&fakeKsPlatform{}))
	req := ksRequest("oauth_server_push", true)
	req.Text.Title = "获准标题"
	req.Text.Body = "未核实正文"
	req.Text.Topics = []string{"未获准话题"}
	p, err := a.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if p.ClientAction.Params["publish_caption"] != "" || !strings.Contains(p.ClientAction.Caption, "未核实正文") {
		t.Fatal("unknown fields were sent automatically or independent reference copy lost")
	}
	req.FieldSupport = map[string]string{"title": "supported", "body": "unknown", "topic": "unsupported"}
	p, err = a.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if p.ClientAction.Params["publish_caption"] != "获准标题" {
		t.Fatalf("automatic caption escaped field authority: %+v", p.ClientAction.Params)
	}
}
