// Package adapters: HUI-4585/4586/4587/4588 四平台适配器(各自文件,经
// Service.SetAdapter 注册)。包面纪律(1670 冻结契约 §包面):
// publish_handoffs / publish_handoff_events 两表仍只由 publishhandoff 本包
// 修改;本包只写各票迁移(0119-0123,0140)的平台侧 provider/state 表。
//
// 共性纪律(裁定书):
//   - 不适用的方法返回 publishhandoff.ErrUnsupported(诚实缺位,不伪造成功);
//   - Prepare 只在 3535 允许后才会被调用(闸在 JudgeActionRoute,adapter 亦做
//     纵深防御,拒绝任何未获准 plan 或微信容器直接唤起);
//   - fixture 回执/测试密钥只进 _test.go;生产未配置凭据=适配器不注册,
//     1670 的诚实回退面(manual_export+说明)接管;
//   - 绝不自造"抖音 HMAC 协议"冒充官方验签。
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
	"sync"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
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

// DouyinShareBinding is the server-authoritative mapping
// share_id -> (tenant, store, action, session, attempt, content_pack_version, app_id).
type DouyinShareBinding struct {
	ID                   string
	TenantID             string
	StoreID              string
	HandoffID            string
	ShareID              string
	ActionID             string
	SessionID            string
	AttemptID            string
	ContentVersion       string
	ContentPackVersionID string
	AppRef               string
	AppID                string // client_key
	Method               string
	Scenario             string
	CreatedAt            time.Time
	ExpiresAt            time.Time
	RevokedAt            string
}

// DouyinBindingExpectation specifies the server-side invariants that a resolved
// share_id mapping must satisfy before any result can be associated.
type DouyinBindingExpectation struct {
	TenantID             string
	StoreID              string
	HandoffID            string
	ActionID             string
	SessionID            string
	AttemptID            string
	ContentVersion       string
	ContentPackVersionID string
	AppRef               string
	AppID                string // client_key
	Now                  time.Time
}

// DouyinAdapter implements the douyin platform contract (HUI-4585).
type DouyinAdapter struct {
	mu          sync.Mutex
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
	if cfg.ShareTTL <= 0 || cfg.ShareTTL > time.Hour {
		cfg.ShareTTL = time.Hour
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	return &DouyinAdapter{
		db:          database,
		clientKey:   strings.TrimSpace(cfg.ClientKey),
		appRef:      strings.TrimSpace(cfg.AppRef),
		miniappPath: strings.TrimSpace(cfg.MiniappPath),
		shareTTL:    cfg.ShareTTL,
		signer:      cfg.Signer,
		query:       cfg.Query,
		now:         cfg.Now,
	}
}

// Platform implements Adapter.
func (a *DouyinAdapter) Platform() string { return "douyin" }

// AssessRoute evaluates this adapter's structured six-state availability
// against a 3535 channeladmission record without leaking credentials.
func (a *DouyinAdapter) AssessRoute(in DouyinRouteEvalInput) DouyinRouteAssessment {
	in.ClientKeyReady = strings.TrimSpace(a.clientKey) != ""
	in.SignerReady = a.signer != nil
	in.MiniappPathReady = strings.TrimSpace(a.miniappPath) != ""
	return EvaluateDouyinRoute(in)
}

// Prepare builds the official H5 share handoff (h5_share / douyin_h5) or the
// separately approved miniapp task route (miniapp / douyin_miniapp_task).
// Defense-in-depth: refuses any unapproved 3535 plan (including store_promo
// not_admitted) and blocks WeChat WebView containers before minting share_id.
func (a *DouyinAdapter) Prepare(ctx context.Context, req publishhandoff.PrepareRequest) (publishhandoff.PrepareResult, error) {
	if strings.TrimSpace(a.clientKey) == "" {
		return publishhandoff.PrepareResult{}, fmt.Errorf("adapters: douyin client_key missing: official H5 签名与关联所需的部署凭据未配置")
	}
	routeMethod, supported := canonicalDouyinRoute(req.Handoff.Method)
	if !supported {
		// 抖音其余方法(含 manual_material=not_admitted)不由本 adapter 承接。
		return publishhandoff.PrepareResult{}, fmt.Errorf("%w: douyin method %q is not served by this adapter", publishhandoff.ErrUnsupported, req.Handoff.Method)
	}
	// 纵深准入闸:未通过 3535 准入裁决(refused/pending_external/manual_only/!Allowed)
	// 一律拒绝,零落行、零可执行外发 payload。
	if !req.Plan.Allowed ||
		req.Plan.PendingExternal ||
		req.Plan.Kind != publishhandoff.PlanOfficialHandoff ||
		req.Plan.Reason == channeladmission.ReasonNotAdmitted {
		return publishhandoff.PrepareResult{}, fmt.Errorf("%w: plan=%q reason=%q scenario=%q",
			ErrDouyinRouteNotAdmitted, req.Plan.Kind, req.Plan.Reason, req.Handoff.Scenario)
	}
	// 微信容器阻拦:微信内置浏览器不能直接唤起抖音官方 H5/任务小程序。
	env := firstNonEmptyDouyin(req.Environment, req.Handoff.Environment)
	if isWechatContainer(env) {
		return publishhandoff.PrepareResult{}, fmt.Errorf("%w: environment=%q", ErrDouyinWechatContainerBlocked, env)
	}
	switch routeMethod {
	case DouyinMethodH5:
		return a.prepareH5Share(ctx, req)
	case DouyinMethodMiniappTask:
		return a.prepareMiniappTask(ctx, req)
	default:
		return publishhandoff.PrepareResult{}, fmt.Errorf("%w: douyin method %q is not served by this adapter", publishhandoff.ErrUnsupported, req.Handoff.Method)
	}
}

// prepareH5Share: 官方 H5 参数/签名、素材 URL 规范编码与用户主动唤起。字段只有
// 获准支持才传入——capability 三态之外的通用 media/title/body query 一律不拼
// (票面红线)。
func (a *DouyinAdapter) prepareH5Share(ctx context.Context, req publishhandoff.PrepareRequest) (publishhandoff.PrepareResult, error) {
	if a.signer == nil {
		return publishhandoff.PrepareResult{}, fmt.Errorf("adapters: douyin official signer not configured")
	}
	// 1) 先校验并规范编码 HTTPS 素材 URL(含中文/特殊字符),避免非法请求浪费 share_id 或签名调用。
	rawVideo := firstRefFor(req, publishhandoff.MediaVideo)
	rawMedia := rawVideo
	mediaKey := "video_path"
	if rawMedia == "" {
		rawMedia = firstRefFor(req, publishhandoff.MediaImage)
		mediaKey = "image_path"
	}
	canonicalMediaURL, err := CanonicalizeDouyinMediaURL(rawMedia)
	if err != nil {
		return publishhandoff.PrepareResult{}, err
	}

	// 2) 服务器端建立/复用 share_id -> (tenant,store,action,session,attempt,content_pack_version,app_id) 权威映射。
	now := a.now()
	binding, err := a.shareIDFor(ctx, req, DouyinMethodH5, now)
	if err != nil {
		return publishhandoff.PrepareResult{}, err
	}

	// 3) 仅在准入允许且素材规范后调用官方 openTicket 签名器,仅白名单采纳公开签名字段。
	signed, serr := a.signer.SignShare(ctx, DouyinShareSignInput{
		ClientKey:    a.clientKey,
		ShareID:      binding.ShareID,
		ContentVer:   req.Handoff.ContentVersion,
		MediaURL:     canonicalMediaURL,
		TimestampSec: fmt.Sprintf("%d", now.UTC().Unix()),
	})
	if serr != nil {
		return publishhandoff.PrepareResult{}, fmt.Errorf("adapters: douyin share sign failed: %s", sanitizeDouyinDiagnostic(serr.Error()))
	}

	params := map[string]string{
		"sdk":              DouyinMethodH5,
		"client_key":       a.clientKey,
		"share_id":         binding.ShareID,
		"state":            binding.ShareID, // 官方 state 严格绑定服务器 share_id
		"share_expires_at": rfc3339Adapter(binding.ExpiresAt),
		mediaKey:           canonicalMediaURL,
		"media_url":        canonicalMediaURL,
	}
	for _, k := range []string{"nonce_str", "timestamp", "signature"} {
		v := strings.TrimSpace(signed[k])
		if v == "" {
			return publishhandoff.PrepareResult{}, fmt.Errorf("%w: missing %s", ErrDouyinSignatureInvalid, k)
		}
		params[k] = v
	}
	if err := VerifyShareSignatureFreshness(params, now, 10*time.Minute); err != nil {
		return publishhandoff.PrepareResult{}, err
	}

	// 字段只按 3535 FieldSupport 白名单传入,绝不拼通用 media/body query。
	if req.FieldSupport["title"] == channeladmission.CapSupported && strings.TrimSpace(req.Text.Title) != "" {
		params["title"] = req.Text.Title
	}
	if req.FieldSupport["topic"] == channeladmission.CapSupported && len(req.Text.Topics) > 0 {
		tags, _ := json.Marshal(req.Text.Topics)
		params["hashtag_list"] = string(tags)
	}

	unsupported := append([]string(nil), req.Plan.UnsupportedFields...)
	act := publishhandoff.ClientAction{
		Type:   publishhandoff.ClientActionJSMediaHandoff,
		Params: params,
		Steps: []string{
			"确认下方为本抖音官方交接参数后，由你本人主动唤起抖音",
			"在抖音内选择本次视频素材并自行完成编辑与发布",
			"系统以官方 share_id 关联本次交接；只打开抖音或返回 H5 均不算发布成功",
		},
		Note: "抖音商铺宣传通用投稿未获准入；本路线只在官方获准的前提下由平台闸门放行。签名/回执能力以部署配置为准，缺失处如实标注。",
	}
	return publishhandoff.PrepareResult{ClientAction: act, UnsupportedFields: unsupported}, nil
}

// prepareMiniappTask: 仅在独立获准时登记的专项路线(miniapp/reward_task)。
// 资格由 3535 记录裁决(默认 unknown→pending_external,本函数到不了);这里
// 只实现协议形状:打开任务小程序路由并携带 share_id 关联;完成语义以平台
// 官方回执为准,不在客户端假设。
func (a *DouyinAdapter) prepareMiniappTask(ctx context.Context, req publishhandoff.PrepareRequest) (publishhandoff.PrepareResult, error) {
	miniappPath := strings.TrimSpace(a.miniappPath)
	if miniappPath == "" {
		return publishhandoff.PrepareResult{}, fmt.Errorf("adapters: douyin miniapp route path missing: 专项任务小程序路由未配置")
	}
	if strings.Contains(strings.ToLower(miniappPath), "javascript:") {
		return publishhandoff.PrepareResult{}, fmt.Errorf("adapters: douyin miniapp route path invalid")
	}
	now := a.now()
	binding, err := a.shareIDFor(ctx, req, DouyinMethodMiniappTask, now)
	if err != nil {
		return publishhandoff.PrepareResult{}, err
	}
	act := publishhandoff.ClientAction{
		Type:        publishhandoff.ClientActionOpenMiniapp,
		MiniappPath: miniappPath,
		Params: map[string]string{
			"sdk":              DouyinMethodMiniappTask,
			"share_id":         binding.ShareID,
			"client_key":       a.clientKey,
			"share_expires_at": rfc3339Adapter(binding.ExpiresAt),
		},
		Note: "专项任务路线必须另有对应主体、场景、奖励玩法许可；完成语义以平台官方结果为准，本系统不做本地成功假设。",
	}
	return publishhandoff.PrepareResult{ClientAction: act, UnsupportedFields: append([]string(nil), req.Plan.UnsupportedFields...)}, nil
}

// shareIDFor mints (or replays) the server-side authoritative share_id mapping
// (0119 + 0140). 同一交接幂等复用同一 share_id;过期/换内容作废旧行重铸;
// 上下文(tenant/store/action/session/attempt/app)不符立即拒绝。
func (a *DouyinAdapter) shareIDFor(ctx context.Context, req publishhandoff.PrepareRequest, routeMethod string, now time.Time) (DouyinShareBinding, error) {
	if a.db == nil {
		return DouyinShareBinding{}, fmt.Errorf("adapters: douyin database not configured")
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	var b DouyinShareBinding
	var createdStr, expiresStr string
	err := a.db.QueryRow(`SELECT id, tenant_id, store_id, handoff_id, share_id, content_version,
		content_pack_version_id, action_id, action_session_id, attempt_id, app_ref, client_key,
		method, scenario, created_at, expires_at, revoked_at
		FROM douyin_share_ids
		WHERE handoff_id=? AND tenant_id=? AND revoked_at=''
		ORDER BY created_at DESC LIMIT 1`,
		req.Handoff.ID, req.Handoff.TenantID).
		Scan(&b.ID, &b.TenantID, &b.StoreID, &b.HandoffID, &b.ShareID, &b.ContentVersion,
			&b.ContentPackVersionID, &b.ActionID, &b.SessionID, &b.AttemptID, &b.AppRef, &b.AppID,
			&b.Method, &b.Scenario, &createdStr, &expiresStr, &b.RevokedAt)

	switch {
	case err == nil:
		if b.StoreID != "" && req.Handoff.StoreID != "" && b.StoreID != req.Handoff.StoreID {
			return DouyinShareBinding{}, fmt.Errorf("%w: store", ErrDouyinStoreMismatch)
		}
		if b.AppRef != a.appRef || (b.AppID != "" && b.AppID != a.clientKey) {
			return DouyinShareBinding{}, fmt.Errorf("%w: app_ref/client_key", ErrDouyinAppMismatch)
		}
		if b.ActionID != req.Handoff.ActionID ||
			b.SessionID != req.Handoff.SessionID ||
			(b.AttemptID != "" && req.Handoff.AttemptID != "" && b.AttemptID != req.Handoff.AttemptID) ||
			(b.Method != "" && b.Method != routeMethod) ||
			(b.Scenario != "" && req.Handoff.Scenario != "" && b.Scenario != req.Handoff.Scenario) {
			return DouyinShareBinding{}, fmt.Errorf("%w: douyin share context mismatch", ErrDouyinContextMismatch)
		}
		createdAt, _ := time.Parse(time.RFC3339Nano, createdStr)
		expiresAt, perr := time.Parse(time.RFC3339Nano, expiresStr)
		b.CreatedAt, b.ExpiresAt = createdAt, expiresAt

		contentChanged := (b.ContentVersion != "" && req.Handoff.ContentVersion != "" && b.ContentVersion != req.Handoff.ContentVersion) ||
			(b.ContentPackVersionID != "" && req.Handoff.ContentPackVersionID != "" && b.ContentPackVersionID != req.Handoff.ContentPackVersionID)
		if b.RevokedAt != "" || contentChanged || perr != nil || !now.Before(expiresAt) {
			// 作废旧映射,下面重铸(旧行保留审计)。
			if _, uerr := a.db.Exec(`UPDATE douyin_share_ids SET revoked_at=? WHERE handoff_id=? AND tenant_id=? AND revoked_at=''`,
				rfc3339Adapter(now), req.Handoff.ID, req.Handoff.TenantID); uerr != nil {
				return DouyinShareBinding{}, fmt.Errorf("adapters: douyin share revoke: %w", uerr)
			}
			break
		}
		return b, nil
	case errors.Is(err, sql.ErrNoRows):
	default:
		return DouyinShareBinding{}, fmt.Errorf("adapters: douyin share lookup: %w", err)
	}

	source, ok := a.signer.(interface {
		NewShareID(context.Context) (string, error)
	})
	if !ok {
		return DouyinShareBinding{}, fmt.Errorf("adapters: douyin official share_id source not configured")
	}
	shareID, err := source.NewShareID(ctx)
	if err != nil {
		return DouyinShareBinding{}, fmt.Errorf("adapters: douyin official share_id failed: %s", sanitizeDouyinDiagnostic(err.Error()))
	}
	shareID = strings.TrimSpace(shareID)
	if shareID == "" {
		return DouyinShareBinding{}, fmt.Errorf("adapters: douyin empty official share_id")
	}

	id := newAdapterID("phdsi_")
	expires := now.Add(a.shareTTL)
	if _, err := a.db.Exec(`INSERT INTO douyin_share_ids
		(id, tenant_id, store_id, handoff_id, share_id, content_version, content_pack_version_id,
		 action_id, action_session_id, attempt_id, app_ref, client_key, method, scenario,
		 created_at, expires_at, revoked_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, req.Handoff.TenantID, req.Handoff.StoreID, req.Handoff.ID, shareID,
		req.Handoff.ContentVersion, req.Handoff.ContentPackVersionID,
		req.Handoff.ActionID, req.Handoff.SessionID, req.Handoff.AttemptID,
		a.appRef, a.clientKey, routeMethod, req.Handoff.Scenario,
		rfc3339Adapter(now), rfc3339Adapter(expires), ""); err != nil {
		return DouyinShareBinding{}, fmt.Errorf("adapters: douyin share insert: %w", err)
	}
	return DouyinShareBinding{
		ID:                   id,
		TenantID:             req.Handoff.TenantID,
		StoreID:              req.Handoff.StoreID,
		HandoffID:            req.Handoff.ID,
		ShareID:              shareID,
		ActionID:             req.Handoff.ActionID,
		SessionID:            req.Handoff.SessionID,
		AttemptID:            req.Handoff.AttemptID,
		ContentVersion:       req.Handoff.ContentVersion,
		ContentPackVersionID: req.Handoff.ContentPackVersionID,
		AppRef:               a.appRef,
		AppID:                a.clientKey,
		Method:               routeMethod,
		Scenario:             req.Handoff.Scenario,
		CreatedAt:            now.UTC(),
		ExpiresAt:            expires.UTC(),
	}, nil
}

// ResolveShareBinding resolves the server-authoritative share_id mapping and
// strictly validates tenant, store, app, context, content version, revocation,
// and expiry (using Go time.Time comparison, never SQLite string comparison).
func (a *DouyinAdapter) ResolveShareBinding(_ context.Context, shareID string, want DouyinBindingExpectation) (DouyinShareBinding, error) {
	if a.db == nil || strings.TrimSpace(shareID) == "" {
		return DouyinShareBinding{}, ErrDouyinShareNotFound
	}
	now := want.Now
	if now.IsZero() {
		now = a.now()
	}
	var b DouyinShareBinding
	var createdStr, expiresStr string
	err := a.db.QueryRow(`SELECT id, tenant_id, store_id, handoff_id, share_id, content_version,
		content_pack_version_id, action_id, action_session_id, attempt_id, app_ref, client_key,
		method, scenario, created_at, expires_at, revoked_at
		FROM douyin_share_ids
		WHERE share_id=?
		ORDER BY created_at DESC LIMIT 1`, strings.TrimSpace(shareID)).
		Scan(&b.ID, &b.TenantID, &b.StoreID, &b.HandoffID, &b.ShareID, &b.ContentVersion,
			&b.ContentPackVersionID, &b.ActionID, &b.SessionID, &b.AttemptID, &b.AppRef, &b.AppID,
			&b.Method, &b.Scenario, &createdStr, &expiresStr, &b.RevokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return DouyinShareBinding{}, ErrDouyinShareNotFound
	}
	if err != nil {
		return DouyinShareBinding{}, fmt.Errorf("adapters: douyin share resolve: %w", err)
	}
	b.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
	expiresAt, perr := time.Parse(time.RFC3339Nano, expiresStr)
	b.ExpiresAt = expiresAt

	// Strict binding verification order: tenant -> store -> app -> handoff/context -> content -> revoked -> expiry.
	if want.TenantID != "" && b.TenantID != want.TenantID {
		return DouyinShareBinding{}, fmt.Errorf("%w: want=%q got=%q", ErrDouyinTenantMismatch, want.TenantID, b.TenantID)
	}
	if want.StoreID != "" && b.StoreID != "" && b.StoreID != want.StoreID {
		return DouyinShareBinding{}, fmt.Errorf("%w: want=%q got=%q", ErrDouyinStoreMismatch, want.StoreID, b.StoreID)
	}
	wantAppRef := want.AppRef
	if wantAppRef == "" {
		wantAppRef = a.appRef
	}
	if b.AppRef != wantAppRef {
		return DouyinShareBinding{}, fmt.Errorf("%w: app_ref mismatch", ErrDouyinAppMismatch)
	}
	wantAppID := want.AppID
	if wantAppID == "" {
		wantAppID = a.clientKey
	}
	if wantAppID != "" && b.AppID != "" && b.AppID != wantAppID {
		return DouyinShareBinding{}, fmt.Errorf("%w: app_id mismatch", ErrDouyinAppMismatch)
	}
	if want.HandoffID != "" && b.HandoffID != want.HandoffID {
		return DouyinShareBinding{}, fmt.Errorf("%w: handoff_id mismatch", ErrDouyinContextMismatch)
	}
	if want.ActionID != "" && b.ActionID != "" && b.ActionID != want.ActionID {
		return DouyinShareBinding{}, fmt.Errorf("%w: action_id mismatch", ErrDouyinContextMismatch)
	}
	if want.SessionID != "" && b.SessionID != "" && b.SessionID != want.SessionID {
		return DouyinShareBinding{}, fmt.Errorf("%w: session_id mismatch", ErrDouyinContextMismatch)
	}
	if want.AttemptID != "" && b.AttemptID != "" && b.AttemptID != want.AttemptID {
		return DouyinShareBinding{}, fmt.Errorf("%w: attempt_id mismatch", ErrDouyinContextMismatch)
	}
	if want.ContentVersion != "" && b.ContentVersion != want.ContentVersion {
		return DouyinShareBinding{}, fmt.Errorf("%w: content_version mismatch", ErrDouyinContentVersionMismatch)
	}
	if want.ContentPackVersionID != "" && b.ContentPackVersionID != "" && b.ContentPackVersionID != want.ContentPackVersionID {
		return DouyinShareBinding{}, fmt.Errorf("%w: content_pack_version_id mismatch", ErrDouyinContentVersionMismatch)
	}
	if b.RevokedAt != "" {
		return DouyinShareBinding{}, ErrDouyinShareRevoked
	}
	if perr != nil || !now.Before(expiresAt) {
		return DouyinShareBinding{}, ErrDouyinShareExpired
	}
	return b, nil
}

// RevokeShareBinding explicitly revokes any active share_id for a handoff
// (for example on customer cancellation or content replacement).
func (a *DouyinAdapter) RevokeShareBinding(_ context.Context, tenantID, handoffID string) error {
	if a.db == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, err := a.db.Exec(`UPDATE douyin_share_ids SET revoked_at=? WHERE tenant_id=? AND handoff_id=? AND revoked_at=''`,
		rfc3339Adapter(a.now()), tenantID, handoffID)
	return err
}

// Submit: 抖音 H5/专项路线都是顾客主动唤起的交接,没有服务端投稿通道——
// 诚实 unsupported,状态不推进,绝不伪造提交。
func (a *DouyinAdapter) Submit(ctx context.Context, h publishhandoff.Handoff, p publishhandoff.PrepareResult) error {
	return fmt.Errorf("%w: douyin h5/task routes are user-invoked handoffs; there is no server-side submit channel", publishhandoff.ErrUnsupported)
}

// Query re-checks via the official share-result source (先回查不重投).
// 严格校验服务器 share_id 权威映射(租户/门店/尝试/内容版本/应用/有效期)与
// 事实分级(审核/公开范围/后续删除不被 create 抹平);仅显式 published/public
// 且非终态才铸 Receipt(与回调同一绑定→去重→确认管线);其余照录不确认,
// 无官方查询来源或回执缺失保持 unknown。
func (a *DouyinAdapter) Query(ctx context.Context, h publishhandoff.Handoff) (publishhandoff.QueryOutcome, error) {
	now := a.now()
	shareID, itemID := a.lookupRefs(h.TenantID, h.ID)
	if strings.TrimSpace(shareID) == "" {
		return publishhandoff.QueryOutcome{
			State:  "unknown",
			Detail: map[string]string{"reason": "share_id_missing", "fact_level": string(DouyinFactOfficialUnknown)},
		}, nil
	}
	binding, berr := a.ResolveShareBinding(ctx, shareID, DouyinBindingExpectation{
		TenantID:             h.TenantID,
		StoreID:              h.StoreID,
		HandoffID:            h.ID,
		AttemptID:            h.AttemptID,
		ContentVersion:       h.ContentVersion,
		ContentPackVersionID: h.ContentPackVersionID,
		AppRef:               a.appRef,
		AppID:                a.clientKey,
		Now:                  now,
	})
	if berr != nil {
		return publishhandoff.QueryOutcome{
			State: "unknown",
			Detail: map[string]string{
				"reason":     douyinBindingReasonCode(berr),
				"fact_level": string(DouyinFactOfficialUnknown),
			},
		}, nil
	}
	if a.query == nil {
		return publishhandoff.QueryOutcome{
			State:  "unknown",
			Detail: map[string]string{"reason": "official_query_unconfigured", "fact_level": string(DouyinFactOfficialUnknown)},
		}, nil
	}

	state, published, observedItem, detail, qerr := a.query.QueryShare(ctx, binding.ShareID)
	if qerr != nil {
		// 查询失败≠失败结果:照录 unknown,脱敏不泄露凭据,不重投不确认。
		return publishhandoff.QueryOutcome{
			State: "unknown",
			Detail: map[string]string{
				"reason":     "official_query_failed",
				"error":      sanitizeDouyinDiagnostic(qerr.Error()),
				"fact_level": string(DouyinFactOfficialUnknown),
			},
		}, nil
	}
	if strings.TrimSpace(observedItem) != "" {
		itemID = strings.TrimSpace(observedItem)
	}

	obs := ClassifyDouyinObservation(state, published, detail)
	safeDetail := sanitizeDouyinDetailMap(detail)
	safeDetail["fact_level"] = string(obs.Level)
	if obs.ModerationState != "" {
		safeDetail["moderation_state"] = obs.ModerationState
	}
	if obs.VisibilityScope != "" {
		safeDetail["visibility_scope"] = obs.VisibilityScope
	}
	deletedAt := ""
	if obs.Deleted {
		safeDetail["deleted"] = "true"
		deletedAt = firstNonEmptyDouyin(detail["deleted_at"], rfc3339Adapter(now))
	}

	if err := a.saveRef(h.TenantID, h.ID, binding.ShareID, itemID, obs.CanonicalState, obs.ModerationState, obs.VisibilityScope, deletedAt, safeDetail, now); err != nil {
		return publishhandoff.QueryOutcome{}, err
	}

	out := publishhandoff.QueryOutcome{State: obs.CanonicalState, Detail: safeDetail}

	// 终态(已取消/已失败)乱序查询绝不铸造确认回执。
	if h.Status == publishhandoff.StateCanceled || h.Status == publishhandoff.StateFailed {
		out.Detail["terminal_state_guard"] = string(h.Status)
		return out, nil
	}

	if obs.ConfirmsPublish && strings.TrimSpace(itemID) != "" {
		// 平台侧受信凭据:五绑定+HandoffID 回流确认管线(query alone never
		// confirms——确认随绑定通过的回执语义到达)。
		out.Receipt = &publishhandoff.Receipt{
			ReceiptID:       "douyin-q-" + binding.ShareID + "-" + itemID,
			Platform:        "douyin",
			AppRef:          a.appRef,
			TenantID:        h.TenantID,
			ParticipationID: h.ParticipationID,
			AttemptID:       h.AttemptID,
			ContentVersion:  h.ContentVersion,
			HandoffID:       h.ID,
			PostID:          itemID,
			Outcome:         publishhandoff.ReceiptOutcomePublished,
			SignedAt:        now,
		}
	}
	return out, nil
}

func douyinBindingReasonCode(err error) string {
	switch {
	case errors.Is(err, ErrDouyinShareExpired):
		return "share_id_expired"
	case errors.Is(err, ErrDouyinShareRevoked):
		return "share_id_revoked"
	case errors.Is(err, ErrDouyinAppMismatch):
		return "app_binding_mismatch"
	case errors.Is(err, ErrDouyinTenantMismatch):
		return "tenant_binding_mismatch"
	case errors.Is(err, ErrDouyinStoreMismatch):
		return "store_binding_mismatch"
	case errors.Is(err, ErrDouyinContentVersionMismatch):
		return "content_version_mismatch"
	case errors.Is(err, ErrDouyinContextMismatch):
		return "handoff_context_mismatch"
	default:
		return "share_id_missing_or_invalid"
	}
}

// VerifyCallback: 本票范围内没有已核实的抖音服务端 webhook 签名契约——拒绝
// 一切回调(验签在先、信任在后;不伪造验签器,绝不自造"抖音 HMAC 协议")。
func (a *DouyinAdapter) VerifyCallback(ctx context.Context, raw publishhandoff.CallbackPayload) (publishhandoff.Receipt, error) {
	return publishhandoff.Receipt{}, fmt.Errorf("%w: douyin has no verified server callback contract in this ticket; refusing every callback", publishhandoff.ErrUnsupported)
}

func (a *DouyinAdapter) lookupRefs(tenantID, handoffID string) (shareID, itemID string) {
	if a.db == nil {
		return "", ""
	}
	_ = a.db.QueryRow(`SELECT share_id FROM douyin_share_ids
		WHERE tenant_id=? AND handoff_id=? AND revoked_at='' ORDER BY created_at DESC LIMIT 1`, tenantID, handoffID).Scan(&shareID)
	_ = a.db.QueryRow(`SELECT COALESCE(provider_item,'') FROM douyin_provider_refs
		WHERE tenant_id=? AND handoff_id=?`, tenantID, handoffID).Scan(&itemID)
	return shareID, itemID
}

func (a *DouyinAdapter) saveRef(tenantID, handoffID, shareID, itemID, state, moderationState, visibilityScope, deletedAt string, detail map[string]string, now time.Time) error {
	raw, _ := json.Marshal(detail)
	if _, err := a.db.Exec(`INSERT INTO douyin_provider_refs
		(id, tenant_id, handoff_id, share_id, provider_item, state, moderation_state,
		 visibility_scope, deleted_at, last_checked_at, detail_json, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(handoff_id) DO UPDATE SET share_id=excluded.share_id,
		  provider_item=CASE WHEN excluded.provider_item!='' THEN excluded.provider_item ELSE douyin_provider_refs.provider_item END,
		  state=excluded.state,
		  moderation_state=excluded.moderation_state,
		  visibility_scope=excluded.visibility_scope,
		  deleted_at=CASE WHEN excluded.deleted_at!='' THEN excluded.deleted_at ELSE douyin_provider_refs.deleted_at END,
		  last_checked_at=excluded.last_checked_at,
		  detail_json=excluded.detail_json,
		  updated_at=excluded.updated_at`,
		newAdapterID("phdpr_"), tenantID, handoffID, shareID, itemID, state,
		moderationState, visibilityScope, deletedAt,
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
