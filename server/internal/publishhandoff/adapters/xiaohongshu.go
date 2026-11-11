// xiaohongshu.go: HUI-4586 小红书视频(A02)与图文(A07)适配——官方 JS 媒体交接。
//
// 票面红线:
//   - SDK 自动填标题/文案/话题受到平台限制——载荷白名单绝不含 title/content/
//     topics(文字带入按平台限制给用户主动复制,复制动作只记 copy 事实,
//     不写发布成功);
//   - 官网快照(2026-11-09)显示暂停新接入=资格"未获准"诚实态:闸在 3535
//     (js_media_handoff 记录 status=unknown → pending_external,adapter 到
//     不了);本文件只实现工程面,接入资格由主体控制台核验后走受控端点更新;
//   - JS fail 不等于发布回执:没有正式 JS 成功查询/回执契约时,Submit/Query/
//     VerifyCallback 全部诚实 unsupported,不接受本地伪 success;
//   - 旧小程序发布器合同需独立核验,不能把小程序回调移作 JS 回执。
package adapters

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff"
)

// XhsPublicConfig is the PUBLIC verify config the JS SDK needs at invoke time
// (AppID 级标识;AppSecret 类机密永不进入本结构,更不进浏览器)。
type XhsPublicConfig struct {
	AppID string // 公开的应用标识(无 secret)
}

// XiaohongshuAdapter implements the xiaohongshu platform contract (HUI-4586).
type XiaohongshuAdapter struct {
	db  *sql.DB
	cfg XhsPublicConfig
	ttl time.Duration
	now func() time.Time
}

// NewXiaohongshuAdapter builds the adapter. AppID 为空=公开鉴权参数缺失,
// Prepare 诚实报错列缺项(部署方也可选择不注册,走 1670 回退面)。
func NewXiaohongshuAdapter(database *sql.DB, cfg XhsPublicConfig) *XiaohongshuAdapter {
	return &XiaohongshuAdapter{db: database, cfg: cfg, ttl: time.Hour,
		now: func() time.Time { return time.Now().UTC() }}
}

// Platform implements Adapter.
func (a *XiaohongshuAdapter) Platform() string { return "xiaohongshu" }

// jsPayload is the SDK media payload. 文字字段结构性缺席——白名单即类型本身
// (title/content/topics 在这里写不出来,编译期即红线)。
type jsPayload struct {
	Type   string   `json:"type"`            // video | normal(平台 JS 语义)
	Video  string   `json:"video,omitempty"` // 受控视频引用
	Cover  string   `json:"cover,omitempty"` // 封面引用
	Images []string `json:"images,omitempty"`
}

// Prepare (js_media_handoff): 返回 invoke_xhs_js 形状的类型化 client_action。
// 方法名照 1670 冻结词表=js_media_handoff(票面 invoke_xhs_js 的登记形态),
// Params.sdk=xhs_js 标明目标 SDK。载荷签发落 0121(0121 审计可核"载荷无文字
// 字段");同交接重试回同一载荷(弱网不换稿)。
func (a *XiaohongshuAdapter) Prepare(ctx context.Context, req publishhandoff.PrepareRequest) (publishhandoff.PrepareResult, error) {
	if strings.TrimSpace(a.cfg.AppID) == "" {
		return publishhandoff.PrepareResult{}, fmt.Errorf("adapters: xiaohongshu app id missing: JS 公开鉴权参数未配置")
	}
	if req.Handoff.Method != "js_media_handoff" {
		return publishhandoff.PrepareResult{}, fmt.Errorf("%w: xiaohongshu method %q is not served by this adapter", publishhandoff.ErrUnsupported, req.Handoff.Method)
	}
	payload, ok := xhsPayloadFor(req)
	if !ok {
		return publishhandoff.PrepareResult{}, fmt.Errorf("adapters: xiaohongshu media refs missing: A02 视频或 A07 有序图片均未在受控引用中出现")
	}
	now := a.now()
	params := map[string]string{
		"sdk":    "xhs_js",
		"app_id": a.cfg.AppID,
		"type":   payload.Type,
	}
	raw, _ := json.Marshal(payload)
	params["payload"] = string(raw)
	// 文案独立 copy_view:用户主动复制是另一个显式操作,不代表发布成功/奖励
	// 获准。caption 走 ClientAction.Caption(参考文案,非 SDK 载荷)。
	caption := xhsCaption(req.Text)
	act := publishhandoff.ClientAction{
		Type:    publishhandoff.ClientActionJSMediaHandoff,
		Params:  params,
		Caption: caption,
		Steps: []string{
			"确认下方素材与顺序后，由你本人主动唤起小红书交接",
			"参考文案请从独立复制区自行复制粘贴；系统不会替你自动填写标题/正文/话题",
			"在小红书内自行完成编辑与确认；交接素材不等于已发布",
		},
		MediaNote: "素材为短时效受控引用；过期可在交接页重试获取，不会新建交接。",
		Note:      "小红书官方 JS 为媒体交接通道：SDK 失败只是交接错误；本系统没有 JS 发布回执契约，不会把唤起/失败当发布成功。",
	}
	unsupported := append([]string(nil), req.Plan.UnsupportedFields...)
	if err := a.recordIssued(req.Handoff, payload, params, now); err != nil {
		return publishhandoff.PrepareResult{}, err
	}
	return publishhandoff.PrepareResult{ClientAction: act, UnsupportedFields: unsupported}, nil
}

// Submit: JS 交接由客户端用户主动调用,没有服务端投稿通道——诚实 unsupported。
func (a *XiaohongshuAdapter) Submit(ctx context.Context, h publishhandoff.Handoff, p publishhandoff.PrepareResult) error {
	return fmt.Errorf("%w: xiaohongshu JS handoff is a client-side user invocation; there is no server submit channel", publishhandoff.ErrUnsupported)
}

// Query: 没有正式 JS 成功查询契约——只回查到"无契约"这一事实本身
// (unknown),绝不接受本地伪 success。
func (a *XiaohongshuAdapter) Query(ctx context.Context, h publishhandoff.Handoff) (publishhandoff.QueryOutcome, error) {
	return publishhandoff.QueryOutcome{State: "unknown",
		Detail: map[string]string{"reason": "小红书官方 JS 无已核实成功查询契约；结果保持未知"}}, nil
}

// VerifyCallback: JS 无回执契约;旧小程序发布器合同需独立核验,不能移作 JS
// 回执——拒绝一切回调。
func (a *XiaohongshuAdapter) VerifyCallback(ctx context.Context, raw publishhandoff.CallbackPayload) (publishhandoff.Receipt, error) {
	return publishhandoff.Receipt{}, fmt.Errorf("%w: xiaohongshu JS has no verified callback contract; miniprogram receipts are not transferable", publishhandoff.ErrUnsupported)
}

// xhsPayloadFor builds the media payload from the controlled refs. A02:
// video+cover;A07: 有序 images(保序)。asset 顺序即交接顺序,不重排。
func xhsPayloadFor(req publishhandoff.PrepareRequest) (jsPayload, bool) {
	var video, cover string
	var images []string
	for _, m := range req.Media {
		ref := ""
		if req.MediaRefs != nil {
			ref = req.MediaRefs[m.AssetID]
		}
		if strings.TrimSpace(ref) == "" {
			continue
		}
		switch m.Kind {
		case publishhandoff.MediaVideo:
			if video == "" {
				video = ref
			}
		case publishhandoff.MediaCover:
			if cover == "" {
				cover = ref
			}
		case publishhandoff.MediaImage:
			images = append(images, ref)
		}
	}
	switch {
	case video != "":
		return jsPayload{Type: "video", Video: video, Cover: cover}, true
	case len(images) > 0:
		return jsPayload{Type: "normal", Images: images}, true
	default:
		return jsPayload{}, false
	}
}

// xhsCaption assembles the reference copy for the独立 copy_view (标题+正文+
// 话题拼接;用户主动复制,绝不进 SDK 载荷)。
func xhsCaption(t publishhandoff.TextInput) string {
	parts := make([]string, 0, 3)
	if strings.TrimSpace(t.Title) != "" {
		parts = append(parts, strings.TrimSpace(t.Title))
	}
	if strings.TrimSpace(t.Body) != "" {
		parts = append(parts, strings.TrimSpace(t.Body))
	}
	if len(t.Topics) > 0 {
		tags := make([]string, 0, len(t.Topics))
		for _, tp := range t.Topics {
			tp = strings.TrimSpace(tp)
			if tp == "" {
				continue
			}
			if !strings.HasPrefix(tp, "#") {
				tp = "#" + tp
			}
			tags = append(tags, tp)
		}
		if len(tags) > 0 {
			parts = append(parts, strings.Join(tags, " "))
		}
	}
	return strings.Join(parts, "\n")
}

// recordIssued persists the issued payload (0121). Same handoff = same row
// (重试回同一载荷);载荷哈希可审计"无文字字段"。
func (a *XiaohongshuAdapter) recordIssued(h publishhandoff.ActionHandoff, p jsPayload, params map[string]string, now time.Time) error {
	raw, _ := json.Marshal(p)
	sum := sha256.Sum256(raw)
	publicJSON, _ := json.Marshal(map[string]string{"app_id": a.cfg.AppID})
	refs := []string{}
	if p.Video != "" {
		refs = append(refs, p.Video)
	}
	if p.Cover != "" {
		refs = append(refs, p.Cover)
	}
	refs = append(refs, p.Images...)
	refsJSON, _ := json.Marshal(refs)
	if _, err := a.db.Exec(`INSERT INTO xiaohongshu_handoff_refs
		(id, tenant_id, handoff_id, media_type, payload_sha256, media_refs_json,
		 public_params_json, created_at, expires_at)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(handoff_id) DO UPDATE SET media_type=excluded.media_type,
		  payload_sha256=excluded.payload_sha256, media_refs_json=excluded.media_refs_json,
		  public_params_json=excluded.public_params_json, expires_at=excluded.expires_at`,
		newAdapterID("phxr_"), h.TenantID, h.ID, p.Type, hex.EncodeToString(sum[:]),
		string(refsJSON), string(publicJSON), rfc3339Adapter(now),
		rfc3339Adapter(now.Add(a.ttl))); err != nil {
		return fmt.Errorf("adapters: xiaohongshu issued payload save: %w", err)
	}
	return nil
}
