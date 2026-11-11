package adapters

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff"
)

func manualRequest(platform, method string) publishhandoff.PrepareRequest {
	req := douyinRequest(method)
	req.Handoff.ID = "pha_test_manual"
	req.Handoff.Platform = platform
	req.Handoff.SessionID = "sess_manual"
	req.Handoff.Environment = "ios_wechat"
	req.Environment = "ios_wechat"
	req.Media = []publishhandoff.MediaItem{
		{Kind: publishhandoff.MediaImage, AssetID: "mi1"},
		{Kind: publishhandoff.MediaImage, AssetID: "mi2"},
	}
	req.MediaRefs = map[string]string{"mi1": "https://media.example/i1", "mi2": "https://media.example/i2"}
	return req
}

func TestManualFamilyAllPlatformsRegistered(t *testing.T) {
	if len(ManualFamilyPlatforms) != 6 {
		t.Fatalf("family platforms = %v", ManualFamilyPlatforms)
	}
	for _, p := range ManualFamilyPlatforms {
		a := NewManualAdapter(p)
		if a.Platform() != p {
			t.Fatalf("platform mismatch %q", a.Platform())
		}
	}
}

func TestManualPrepareManualMaterialOrderedMedia(t *testing.T) {
	a := NewManualAdapter("wechat")
	res, err := a.Prepare(context.Background(), manualRequest("wechat", "manual_material"))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	act := res.ClientAction
	if act.Type != publishhandoff.ClientActionManualExport {
		t.Fatalf("type = %q", act.Type)
	}
	order := act.Params["media_order"]
	if order != "media_00,media_01" {
		t.Fatalf("media order = %q (A05 多图顺序)", order)
	}
	if act.Params["media_00"] != "image:https://media.example/i1" || act.Params["media_01"] != "image:https://media.example/i2" {
		t.Fatalf("ordered media wrong: %v", act.Params)
	}
	// 微信内环境步骤:诚实说明受限与浏览器打开恢复路径。
	joined := strings.Join(act.Steps, "\n")
	if !strings.Contains(joined, "在浏览器打开") {
		t.Fatal("ios_wechat environment steps must include the honest browser-open recovery")
	}
	// 系统分享成功只记录素材交接:「已完成」只存声明。
	if !strings.Contains(act.Note, "不生成平台确认") {
		t.Fatalf("honest note missing: %q", act.Note)
	}
}

func TestManualPrepareSuggestedPlatformsLabeled(t *testing.T) {
	for _, p := range []string{"bilibili", "weibo", "qq", "qzone"} {
		a := NewManualAdapter(p)
		res, err := a.Prepare(context.Background(), manualRequest(p, "manual_material"))
		if err != nil {
			t.Fatalf("%s prepare: %v", p, err)
		}
		if !strings.Contains(res.ClientAction.Note, "建议补充") {
			t.Fatalf("%s missing 建议补充 label: %q", p, res.ClientAction.Note)
		}
	}
	// A04/A05/A06 必需平台不得带建议标签。
	for _, p := range []string{"wechat", "wechat_channels"} {
		a := NewManualAdapter(p)
		res, err := a.Prepare(context.Background(), manualRequest(p, "manual_material"))
		if err != nil {
			t.Fatalf("%s prepare: %v", p, err)
		}
		if strings.Contains(res.ClientAction.Note, "建议补充") {
			t.Fatalf("%s must not carry the suggested label", p)
		}
	}
}

func TestManualLinkShareRequiresVerifiedURL(t *testing.T) {
	a := NewManualAdapter("qq")
	if _, err := a.Prepare(context.Background(), manualRequest("qq", "link_share")); err == nil {
		t.Fatal("link_share without server-verified url must fail")
	}
	req := manualRequest("qq", "link_share")
	req.ShareURL = "https://pub.example.com/s/store"
	req.Media = nil
	req.MediaRefs = nil
	res, err := a.Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("link share prepare: %v", err)
	}
	if res.ClientAction.Type != publishhandoff.ClientActionOpenURL || res.ClientAction.URL != req.ShareURL {
		t.Fatalf("link share action = %+v", res.ClientAction)
	}
	joined := strings.Join(res.ClientAction.Steps, "\n")
	if !strings.Contains(joined, "不会代发消息") && !strings.Contains(res.ClientAction.Note, "不会自动发消息") {
		t.Fatal("A27 must state that the system never messages contacts on its own")
	}
}

func TestManualUnsupportedMethods(t *testing.T) {
	for _, p := range ManualFamilyPlatforms {
		a := NewManualAdapter(p)
		if err := a.Submit(context.Background(), publishhandoff.Handoff{}, publishhandoff.PrepareResult{}); !isUnsupported(err) {
			t.Fatalf("%s submit err = %v", p, err)
		}
		if _, err := a.Query(context.Background(), publishhandoff.Handoff{}); !isUnsupported(err) {
			t.Fatalf("%s query err = %v (无查询契约=诚实 unsupported)", p, err)
		}
		if _, err := a.VerifyCallback(context.Background(), publishhandoff.CallbackPayload{Body: []byte(`{"ok":1}`)}); !isUnsupported(err) {
			t.Fatalf("%s callback err = %v", p, err)
		}
		if _, err := a.Prepare(context.Background(), manualRequest(p, "h5_share")); !isUnsupported(err) {
			t.Fatalf("%s h5_share must be unsupported (未核实契约不接): %v", p, err)
		}
	}
}

func TestManualEnvironmentVariants(t *testing.T) {
	envs := map[string]string{
		"desktop_browser": "下载素材到手机",
		"ios_safari":      "回到微信",
		"android_browser": "回到微信",
		"target_app":      "目标 App 环境",
	}
	for env, want := range envs {
		a := NewManualAdapter("wechat")
		req := manualRequest("wechat", "manual_material")
		req.Environment = env
		res, err := a.Prepare(context.Background(), req)
		if err != nil {
			t.Fatalf("%s prepare: %v", env, err)
		}
		joined := strings.Join(res.ClientAction.Steps, "\n")
		if !strings.Contains(joined, want) {
			t.Fatalf("%s steps missing %q: %s", env, want, joined)
		}
	}
}

func TestManualCaptionCopyView(t *testing.T) {
	a := NewManualAdapter("wechat")
	req := manualRequest("wechat", "manual_material")
	req.Text = publishhandoff.TextInput{Title: "周末来店", Body: "咖啡买一送一", Topics: []string{"咖啡店"}}
	res, err := a.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	want := "周末来店\n咖啡买一送一\n#咖啡店"
	if res.ClientAction.Caption != want {
		t.Fatalf("caption = %q want %q", res.ClientAction.Caption, want)
	}
	if fmt.Sprint(len(res.ClientAction.Caption)) == "" {
		t.Fatal("unreachable")
	}
}
