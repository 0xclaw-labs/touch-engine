// Package adapters: HUI-4585/4586/4587/4588 四平台适配器(各自文件,经
// Service.SetAdapter 注册)。包面纪律(1670 冻结契约 §包面):
// publish_handoffs / publish_handoff_events 两表仍只由 publishhandoff 本包
// 修改;本包只写各票迁移(0119-0123)的平台侧 provider/state 表。
//
// 共性纪律(裁定书):
//   - 不适用的方法返回 publishhandoff.ErrUnsupported(诚实缺位,不伪造成功);
//   - Prepare 只在 3535 允许后才会被调用(闸在 JudgeActionRoute,adapter 只
//     消费裁决;准入拒绝时 StartAction 零落行,adapter 永远看不到 refused 行);
//   - fixture 回执/测试密钥只进 _test.go;生产未配置凭据=适配器不注册,
//     1670 的诚实回退面(manual_export+说明)接管;
//   - 环境六枚举逐平台给值:能力真源在 3535 记录(受控端点更新),本包只在
//     交接说明中表达各环境的真实操作差异,不自建能力白名单。
package adapters

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff"
)

func newAdapterID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("adapters: entropy unavailable: " + err.Error())
	}
	return prefix + hex.EncodeToString(b[:])
}

func rfc3339Adapter(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// DouyinSigner is the pluggable official H5 signature step. 抖音 H5 分享协议
// 的签名机制(openTicket/client_key 签名)以官方文档为准;本票不发明签名算法
// (票面红线:不自造"抖音HMAC协议")。部署方接入官方签名后注册实现;nil/未注册
// = Prepare 诚实报错列出缺项,绝不带伪签名字段出门。
type DouyinSigner interface {
	// SignShare returns the official signature fields for one share request.
	// 返回的键值对只进 client_action.Params(类型化载荷,非可执行 JS)。
	SignShare(ctx context.Context, in DouyinShareSignInput) (map[string]string, error)
}

// DouyinShareSignInput is what the official signature covers.
type DouyinShareSignInput struct {
	ClientKey    string
	ShareID      string
	ContentVer   string
	MediaURL     string
	TimestampSec string
}

// DouyinQuerySource is the official share-result query endpoint adapter
// (share_id→内部 handoff 的服务器映射的查询侧)。测试注入 fixture 实现;
// 生产未配置 = 无查询来源,Query 诚实回答 unknown。
type DouyinQuerySource interface {
	// QueryShare queries the official API for one share_id. State is the
	// platform-side observation word (照录); Published=true ONLY when the
	// official response explicitly reports published content. ItemID 是平台
	// 侧内容引用(如有)。
	QueryShare(ctx context.Context, shareID string) (state string, published bool, itemID string, detail map[string]string, err error)
}

// DouyinAdapter implements the douyin platform contract (HUI-4585).
type DouyinAdapter struct {
	db          *sql.DB
	clientKey   string
	appRef      string
	miniappPath string
	shareTTL    time.Duration
	signer      DouyinSigner      // nil = 未接入官方签名(诚实缺位)
	query       DouyinQuerySource // nil = 无官方查询来源(诚实 unknown)
	now         func() time.Time
}

// DouyinConfig carries the deployment config. clientKey/appRef come from
// server env; signer/query are injected (production wiring or test fixtures).
type DouyinConfig struct {
	ClientKey   string // PUBLISH_HANDOFF_DOUYIN_CLIENT_KEY
	AppRef      string // 本部署官方应用身份
	MiniappPath string // 获准专项任务小程序路由(仅独立获准时配置;空=专项路线诚实缺位)
	ShareTTL    time.Duration
	Signer      DouyinSigner
	Query       DouyinQuerySource
	Now         func() time.Time
}

// NewDouyinAdapter builds the adapter. Callers MUST only register it when the
// deployment actually provides ClientKey; otherwise the 1670 fallback face
// stays honest ("自动交接通道未接入").
func NewDouyinAdapter(database *sql.DB, cfg DouyinConfig) *DouyinAdapter {
	if cfg.ShareTTL <= 0 {
		cfg.ShareTTL = 2 * time.Hour
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	return &DouyinAdapter{db: database, clientKey: cfg.ClientKey, appRef: cfg.AppRef,
		miniappPath: cfg.MiniappPath, shareTTL: cfg.ShareTTL, signer: cfg.Signer,
		query: cfg.Query, now: cfg.Now}
}

// Platform implements Adapter.
func (a *DouyinAdapter) Platform() string { return "douyin" }

// Prepare builds the official H5 share handoff (h5_share) or the separately
// approved miniapp task route (miniapp). Store-promo generic投稿 not_admitted
// 从不到达本函数(gate 零落行;adapter 不提供手工绕过)。
func (a *DouyinAdapter) Prepare(ctx context.Context, req publishhandoff.PrepareRequest) (publishhandoff.PrepareResult, error) {
	if strings.TrimSpace(a.clientKey) == "" {
		return publishhandoff.PrepareResult{}, fmt.Errorf("adapters: douyin client_key missing: official H5 签名与关联所需的部署凭据未配置")
	}
	switch req.Handoff.Method {
	case "h5_share":
		return a.prepareH5Share(ctx, req)
	case "miniapp":
		return a.prepareMiniappTask(req)
	default:
		// 抖音其余方法(含 manual_material=not_admitted)不由本 adapter 承接。
		return publishhandoff.PrepareResult{}, fmt.Errorf("%w: douyin method %q is not served by this adapter", publishhandoff.ErrUnsupported, req.Handoff.Method)
	}
}

// prepareH5Share: 官方 H5 参数/签名、素材 URL 规范与用户主动唤起。字段只有
// 获准支持才传入——capability 三态之外的通用 media/title/body query 一律不拼
// (票面红线)。
func (a *DouyinAdapter) prepareH5Share(ctx context.Context, req publishhandoff.PrepareRequest) (publishhandoff.PrepareResult, error) {
	now := a.now()
	shareID, err := a.shareIDFor(req, now)
	if err != nil {
		return publishhandoff.PrepareResult{}, err
	}
	mediaURL := firstRefFor(req, publishhandoff.MediaVideo)
	if mediaURL == "" {
		mediaURL = firstRefFor(req, publishhandoff.MediaImage)
	}
	params := map[string]string{
		"sdk":              "douyin_h5",
		"client_key":       a.clientKey,
		"share_id":         shareID,
		"state":            "user_invoke", // 用户主动唤起;系统绝不代点
		"share_expires_at": now.Add(a.shareTTL).UTC().Format(time.RFC3339),
	}
	unsupported := append([]string(nil), req.Plan.UnsupportedFields...)
	if a.signer == nil {
		// 官方签名机制未接入:绝不伪造签名字段;诚实说明缺项(交接仍按 1670
		// 契约交付类型化参数,签名缺失在 UnsupportedFields 逐项解释)。
		unsupported = append(unsupported, "官方签名：抖音 H5 官方签名机制尚未接入（不自造签名协议），签名接入前此参数缺失，平台可能拒绝唤起。")
	} else {
		signed, serr := a.signer.SignShare(ctx, DouyinShareSignInput{
			ClientKey: a.clientKey, ShareID: shareID,
			ContentVer:   req.Handoff.ContentVersion,
			MediaURL:     mediaURL,
			TimestampSec: fmt.Sprintf("%d", now.Unix()),
		})
		if serr != nil {
			return publishhandoff.PrepareResult{}, fmt.Errorf("adapters: douyin share sign: %w", serr)
		}
		for k, v := range signed {
			params[k] = v
		}
	}
	if mediaURL != "" {
		params["media_url"] = mediaURL
	}
	act := publishhandoff.ClientAction{
		Type:   publishhandoff.ClientActionJSMediaHandoff,
		Params: params,
		Steps: []string{
			"确认下方为本抖音官方交接参数后，由你本人主动唤起抖音",
			"在抖音内选择本次视频素材并自行完成编辑与发布",
			"系统以官方 share_id 关联本次交接；只打开抖音不算素材交接通过",
		},
		Note: "抖音商铺宣传通用投稿未获准入；本路线只在官方获准的前提下由平台闸门放行。签名/回执能力以部署配置为准，缺失处如实标注。",
	}
	return publishhandoff.PrepareResult{ClientAction: act, UnsupportedFields: unsupported}, nil
}

// prepareMiniappTask: 仅在独立获准时登记的专项路线(miniapp/reward_task)。
// 资格由 3535 记录裁决(当前 unknown→pending_external,本函数到不了);这里
// 只实现协议形状:打开任务小程序路由并携带 share_id 关联;success 必须读
// 平台 result——tt.* 回调的 result 语义在 Query/回执侧核对,不在客户端假设。
func (a *DouyinAdapter) prepareMiniappTask(req publishhandoff.PrepareRequest) (publishhandoff.PrepareResult, error) {
	miniappPath := strings.TrimSpace(a.miniappPath)
	if miniappPath == "" {
		return publishhandoff.PrepareResult{}, fmt.Errorf("adapters: douyin miniapp route path missing: 专项任务小程序路由未配置")
	}
	shareID, err := a.shareIDFor(req, a.now())
	if err != nil {
		return publishhandoff.PrepareResult{}, err
	}
	act := publishhandoff.ClientAction{
		Type:        publishhandoff.ClientActionOpenMiniapp,
		MiniappPath: miniappPath,
		Params: map[string]string{
			"sdk": "douyin_miniapp_task", "share_id": shareID,
			"client_key": a.clientKey,
		},
		Note: "专项任务路线必须另有对应主体、场景、奖励玩法许可；完成语义以平台 result 为准，本系统不做本地成功假设。",
	}
	return publishhandoff.PrepareResult{ClientAction: act}, nil
}

// shareIDFor mints (or replays) the server-side share_id mapping (0119).
// 同一交接幂等复用同一 share_id;过期/换内容作废旧行重铸。
func (a *DouyinAdapter) shareIDFor(req publishhandoff.PrepareRequest, now time.Time) (string, error) {
	var shareID, contentVer, revokedAt, expiresAt string
	err := a.db.QueryRow(`SELECT share_id, content_version, revoked_at, expires_at
		FROM douyin_share_ids WHERE handoff_id=?`, req.Handoff.ID).
		Scan(&shareID, &contentVer, &revokedAt, &expiresAt)
	switch {
	case err == nil:
		expired, perr := time.Parse(time.RFC3339Nano, expiresAt)
		stale := contentVer != "" && req.Handoff.ContentVersion != "" && contentVer != req.Handoff.ContentVersion
		if revokedAt != "" || stale || (perr == nil && !now.Before(expired)) {
			// 作废旧映射,下面重铸(旧行保留审计)。
			if _, uerr := a.db.Exec(`UPDATE douyin_share_ids SET revoked_at=? WHERE handoff_id=? AND revoked_at=''`,
				rfc3339Adapter(now), req.Handoff.ID); uerr != nil {
				return "", fmt.Errorf("adapters: douyin share revoke: %w", uerr)
			}
			break
		}
		return shareID, nil
	case errors.Is(err, sql.ErrNoRows):
	default:
		return "", fmt.Errorf("adapters: douyin share lookup: %w", err)
	}
	id := newAdapterID("phdsi_")
	shareID = newAdapterID("dshare_")
	expires := now.Add(a.shareTTL)
	if _, err := a.db.Exec(`INSERT INTO douyin_share_ids
		(id, tenant_id, handoff_id, share_id, content_version, action_id,
		 action_session_id, app_ref, created_at, expires_at, revoked_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		id, req.Handoff.TenantID, req.Handoff.ID, shareID, req.Handoff.ContentVersion,
		req.Handoff.ActionID, req.Handoff.SessionID, a.appRef,
		rfc3339Adapter(now), rfc3339Adapter(expires), ""); err != nil {
		return "", fmt.Errorf("adapters: douyin share insert: %w", err)
	}
	return shareID, nil
}

// Submit: 抖音 H5/专项路线都是顾客主动唤起的交接,没有服务端投稿通道——
// 诚实 unsupported,状态不推进,绝不伪造提交。
func (a *DouyinAdapter) Submit(ctx context.Context, h publishhandoff.Handoff, p publishhandoff.PrepareResult) error {
	return fmt.Errorf("%w: douyin h5/task routes are user-invoked handoffs; there is no server-side submit channel", publishhandoff.ErrUnsupported)
}

// Query re-checks via the official share-result source (先回查不重投). 显式
// published 才铸 Receipt(与回调同一绑定→去重→确认管线);其余照录 unknown。
// 无官方查询来源 = state unknown(诚实,不造成功)。
func (a *DouyinAdapter) Query(ctx context.Context, h publishhandoff.Handoff) (publishhandoff.QueryOutcome, error) {
	now := a.now()
	shareID, itemID := a.lookupRefs(h.ID)
	if a.query == nil || strings.TrimSpace(shareID) == "" {
		return publishhandoff.QueryOutcome{State: "unknown",
			Detail: map[string]string{"reason": "无官方查询来源或 share_id 映射缺失"}}, nil
	}
	state, published, observedItem, detail, qerr := a.query.QueryShare(ctx, shareID)
	if qerr != nil {
		// 查询失败≠失败结果:照录 unknown,不重投不确认。
		return publishhandoff.QueryOutcome{State: "unknown",
			Detail: map[string]string{"reason": "official query failed", "error": qerr.Error()}}, nil
	}
	if observedItem != "" {
		itemID = observedItem
	}
	if err := a.saveRef(h.TenantID, h.ID, shareID, itemID, state, detail, now); err != nil {
		return publishhandoff.QueryOutcome{}, err
	}
	out := publishhandoff.QueryOutcome{State: state, Detail: detail}
	if published && strings.TrimSpace(itemID) != "" {
		// 平台侧受信凭据:五绑定+HandoffID 回流确认管线(query alone never
		// confirms——确认随绑定通过的回执语义到达)。
		out.Receipt = &publishhandoff.Receipt{
			ReceiptID: "douyin-q-" + shareID + "-" + itemID,
			Platform:  "douyin", AppRef: a.appRef,
			TenantID: h.TenantID, ParticipationID: h.ParticipationID,
			AttemptID: h.AttemptID, ContentVersion: h.ContentVersion,
			HandoffID: h.ID, PostID: itemID,
			Outcome: publishhandoff.ReceiptOutcomePublished, SignedAt: now,
		}
	}
	return out, nil
}

// VerifyCallback: 本票范围内没有已核实的抖音服务端 webhook 签名契约——拒绝
// 一切回调(验签在先、信任在后;不伪造验签器,不自造协议)。
func (a *DouyinAdapter) VerifyCallback(ctx context.Context, raw publishhandoff.CallbackPayload) (publishhandoff.Receipt, error) {
	return publishhandoff.Receipt{}, fmt.Errorf("%w: douyin has no verified server callback contract in this ticket; refusing every callback", publishhandoff.ErrUnsupported)
}

func (a *DouyinAdapter) lookupRefs(handoffID string) (shareID, itemID string) {
	_ = a.db.QueryRow(`SELECT share_id FROM douyin_share_ids
		WHERE handoff_id=? AND revoked_at='' ORDER BY created_at DESC LIMIT 1`, handoffID).Scan(&shareID)
	_ = a.db.QueryRow(`SELECT COALESCE(provider_item,'') FROM douyin_provider_refs
		WHERE handoff_id=?`, handoffID).Scan(&itemID)
	return shareID, itemID
}

func (a *DouyinAdapter) saveRef(tenantID, handoffID, shareID, itemID, state string, detail map[string]string, now time.Time) error {
	raw, _ := json.Marshal(detail)
	if _, err := a.db.Exec(`INSERT INTO douyin_provider_refs
		(id, tenant_id, handoff_id, share_id, provider_item, state, last_checked_at, detail_json, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(handoff_id) DO UPDATE SET share_id=excluded.share_id,
		  provider_item=CASE WHEN excluded.provider_item!='' THEN excluded.provider_item ELSE douyin_provider_refs.provider_item END,
		  state=excluded.state, last_checked_at=excluded.last_checked_at,
		  detail_json=excluded.detail_json, updated_at=excluded.updated_at`,
		newAdapterID("phdpr_"), tenantID, handoffID, shareID, itemID, state,
		rfc3339Adapter(now), string(raw), rfc3339Adapter(now), rfc3339Adapter(now)); err != nil {
		return fmt.Errorf("adapters: douyin provider ref save: %w", err)
	}
	return nil
}

// firstRefFor returns the controlled URL for the first media of the kind.
func firstRefFor(req publishhandoff.PrepareRequest, kind string) string {
	for _, m := range req.Media {
		if m.Kind == kind && req.MediaRefs != nil {
			if u, ok := req.MediaRefs[m.AssetID]; ok && strings.TrimSpace(u) != "" {
				return u
			}
		}
	}
	return ""
}
