// kuaishou.go: HUI-4587 快手视频适配——网站 OAuth → 顾客确认 → 服务端投稿
// → 异步结果查询。
//
// 票面红线:
//   - plan.kind=oauth_publish;界面明确这是授权服务端投稿路线,不声称弹出
//     原生编辑器;
//   - OAuth state 绑定 action_session/handoff/目标 App/一次流程(一次性);
//     token 仅服务器保存(0122),客户端永无 secret;
//   - publish 异步接受≠最终公开:result=1/pending=true 不计平台确认;
//     photo_id 查官方作品接口核对最终状态;一次提交键对应一次平台任务,
//     超时或重启先查既有 photo_id,不因轮询失败重复创建作品;
//   - 无已核实 Webhook 契约时 VerifyCallback 返回 unsupported,不自搭回调
//     充官方;
//   - 未授权/取消授权场景由闸门与顾客面回手动指引(manual 路线记录
//     admitted),本 adapter 不接管关注。
package adapters

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff"
)

// errKsJobMissing: 交接尚无平台任务行(Prepare 未跑/行缺失)。
var errKsJobMissing = errors.New("adapters: kuaishou job missing")

// KsToken is one OAuth grant (server-side only; never serialized to the
// customer page).
type KsToken struct {
	OpenID       string
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	Scope        string
}

// KuaishouPlatform is the platform protocol seam (token exchange / upload /
// create / query). Production wiring points it at the official endpoints;
// tests inject fixtures. 端点路径/字段名以部署时官方文档核对为准——本文件
// 不发明协议细节,未核对处以 unverified 标注并挂账报告。
type KuaishouPlatform interface {
	// ExchangeToken swaps one authorization code for a server-side token.
	ExchangeToken(ctx context.Context, code, redirectURI string) (KsToken, error)
	// UploadMedia uploads one file; returns the platform-side upload ref.
	UploadMedia(ctx context.Context, token KsToken, filename, contentType string, b []byte) (ref string, err error)
	// CreatePhoto creates the photo/video work item from uploaded refs.
	// One call = one platform task (不重试调用方语义由本 adapter 把关).
	CreatePhoto(ctx context.Context, token KsToken, videoRef, coverRef, caption string) (photoID string, err error)
	// QueryPhoto queries the official work-item state. Published=true ONLY
	// on an explicit published-class answer (异步接受≠最终公开).
	QueryPhoto(ctx context.Context, token KsToken, photoID string) (state string, published bool, detail map[string]string, err error)
}

// KuaishouConfig carries deployment config. ClientID/ClientSecret are
// SERVER-SIDE credentials (PUBLISH_HANDOFF_KUAISHOU_CLIENT_ID/_SECRET);
// ReturnBase is the same-origin https customer page the OAuth redirect
// returns to. Missing secret = caller must NOT register the adapter (1670
// 诚实回退面接管).
type KuaishouConfig struct {
	ClientID       string
	ClientSecret   string
	AppRef         string
	AuthorizeBase  string // 官方授权页基(部署核对;默认 open.kuaishou.com/oauth2/authorize)
	ReturnBase     string // 同源 https 回跳基(必须 https)
	AuthorizeScope string // 已申请且适用的授权范围(如 user_video_publish,user_video_info)
	StateTTL       time.Duration
	// IncludeCaption: 仅当 3535 field_support 已核对话题/正文携带时由部署
	// 打开(默认 false=能力 unknown 不自动携带,参考文案走顾客确认面复制)。
	IncludeCaption bool
	Platform       KuaishouPlatform
	Now            func() time.Time
}

// KuaishouAdapter implements the kuaishou platform contract (HUI-4587).
type KuaishouAdapter struct {
	db  *sql.DB
	cfg KuaishouConfig
	now func() time.Time
}

func NewKuaishouAdapter(database *sql.DB, cfg KuaishouConfig) *KuaishouAdapter {
	if cfg.StateTTL <= 0 {
		cfg.StateTTL = 15 * time.Minute
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	return &KuaishouAdapter{db: database, cfg: cfg, now: cfg.Now}
}

// Platform implements Adapter.
func (a *KuaishouAdapter) Platform() string { return "kuaishou" }

// OAuthFinisher is the narrow seam the OAuth redirect handler (GET
// /api/v1/public/handoff-callbacks/kuaishou/oauth) drives. httpapi asserts
// the registered kuaishou adapter against this interface.
type OAuthFinisher interface {
	// FinishOAuth validates the one-time state (绑定/一次性/有效期), exchanges
	// the code server-side, persists the token (仅服务器), and returns the
	// validated same-origin https return address for the user-agent redirect.
	FinishOAuth(ctx context.Context, code, state string) (returnTo string, err error)
}

// Prepare (oauth_server_push): 保证 0123 job 行存在(携受控媒体引用持久化),
// 铸/复用一次性 state,产出 oauth_consent 类型化动作。Start 与 Submit 两个
// 时序都会进入本函数:Start 时 MediaRefs 在手(持久化);Submit 时凭 token 面
// 切换到"确认提交"形态。
func (a *KuaishouAdapter) Prepare(ctx context.Context, req publishhandoff.PrepareRequest) (publishhandoff.PrepareResult, error) {
	if req.Handoff.Method != "oauth_server_push" {
		return publishhandoff.PrepareResult{}, fmt.Errorf("%w: kuaishou method %q is not served by this adapter", publishhandoff.ErrUnsupported, req.Handoff.Method)
	}
	if strings.TrimSpace(a.cfg.ClientID) == "" || strings.TrimSpace(a.cfg.ClientSecret) == "" {
		return publishhandoff.PrepareResult{}, fmt.Errorf("adapters: kuaishou oauth credentials missing: 服务端凭据未配置(客户端永无 secret)")
	}
	now := a.now()
	job, err := a.ensureJob(req, now)
	if err != nil {
		return publishhandoff.PrepareResult{}, err
	}
	act := publishhandoff.ClientAction{Type: publishhandoff.ClientActionOAuthConsent, Params: map[string]string{}}
	if job.openID == "" {
		// 未授权:铸/复用一次性 state(绑定 handoff/session/app/return_to)。
		state, authorizeURL, serr := a.stateFor(req, now)
		if serr != nil {
			return publishhandoff.PrepareResult{}, serr
		}
		act.URL = authorizeURL
		act.Params["state"] = state
		act.Params["phase"] = "authorize"
		act.Note = "这是一条授权服务端投稿路线:授权后回到本页,再次确认本次内容才会提交;不弹出原生编辑器。取消授权不会丢失你的素材选择。"
	} else {
		// 已授权:顾客再次确认本次内容后才会提交(确认快照不变)。
		act.Params["phase"] = "confirm_submit"
		act.Params["account_ref"] = job.openID
		act.Note = "授权账号已就绪(服务端确定账号);请确认本次内容后提交。系统以 photo_id 回查官方状态,异步接受不等于最终公开。"
	}
	return publishhandoff.PrepareResult{ClientAction: act,
		UnsupportedFields: append([]string(nil), req.Plan.UnsupportedFields...)}, nil
}

// Submit: 服务端上传/创建(唯一自动投稿入口)。纪律:photo_id 已存在=直接
// 返回(先回查不重投);stage=creating=调用 in-flight,重启后拒绝重复创建;
// 成功只推进到 submitting(异步接受,最终公开待 Query/回执核对)。
func (a *KuaishouAdapter) Submit(ctx context.Context, h publishhandoff.Handoff, p publishhandoff.PrepareResult) error {
	if a.cfg.Platform == nil {
		return fmt.Errorf("adapters: kuaishou platform endpoint not configured")
	}
	now := a.now()
	job, err := a.loadJob(h.TenantID, h.ID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(job.photoID) != "" {
		return nil // 已有平台任务:先回查纪律,绝不重复创建
	}
	if job.stage == "creating" {
		// in-flight 未知:拒重复创建(顾客可恢复=先用查询核对)。
		return fmt.Errorf("%w: kuaishou create in-flight unknown (先前调用结果未知);请使用查询核对既有任务,不重复创建作品", ErrCustomerActionRequired)
	}
	token, err := a.loadToken(h.TenantID, h.ID, now)
	if err != nil {
		// 授权缺失/过期:顾客可恢复(重新授权)。
		return fmt.Errorf("%w: %v", ErrCustomerActionRequired, err)
	}
	refs, err := decodeRefs(job.mediaRefs)
	if err != nil {
		return err
	}
	videoRef, ok := refs["video"]
	if !ok || strings.TrimSpace(videoRef) == "" {
		return fmt.Errorf("%w: kuaishou controlled media refs missing/expired: 请回到交接页重新获取后再提交(交接与内容版本不变)", ErrCustomerActionRequired)
	}
	coverRef := refs["cover"]
	if err := a.setStage(h.TenantID, h.ID, "uploading", now); err != nil {
		return err
	}
	raw, ctype, ferr := a.fetchBytes(ctx, videoRef)
	if ferr != nil {
		_ = a.setStage(h.TenantID, h.ID, "", now) // 回退待重试;错误如实上抛
		return fmt.Errorf("adapters: kuaishou media fetch failed: %w", ferr)
	}
	vref, uerr := a.cfg.Platform.UploadMedia(ctx, token, "video", ctype, raw)
	if uerr != nil {
		_ = a.setStage(h.TenantID, h.ID, "", now)
		return fmt.Errorf("adapters: kuaishou video upload failed: %w", uerr)
	}
	if err := a.setStageToken(h.TenantID, h.ID, "uploaded", vref, now); err != nil {
		return err
	}
	cref := ""
	if strings.TrimSpace(coverRef) != "" {
		craw, cctype, cerr := a.fetchBytes(ctx, coverRef)
		if cerr == nil {
			if r, uerr2 := a.cfg.Platform.UploadMedia(ctx, token, "cover", cctype, craw); uerr2 == nil {
				cref = r
			}
		}
		// 封面上传失败不静默假冒成功:如实缺封面继续会被平台拒时如实上抛。
	}
	// 创建作品 in-flight:先落 creating(崩溃窗口挂账报告:平台无客户端幂等键,
	// creating 态重启后拒绝重复创建,宁可 unknown 不可重复作品)。
	if err := a.setStage(h.TenantID, h.ID, "creating", now); err != nil {
		return err
	}
	photoID, cerr := a.cfg.Platform.CreatePhoto(ctx, token, vref, cref, a.captionFor(h, p))
	if cerr != nil {
		_ = a.setStage(h.TenantID, h.ID, "uploaded", now)
		return fmt.Errorf("adapters: kuaishou create failed: %w", cerr)
	}
	if err := a.setAccepted(h.TenantID, h.ID, token.OpenID, photoID, now); err != nil {
		return err
	}
	return nil // 状态机推进到 submitting(异步接受;最终公开待 Query)
}

// Query: photo_id 查官方作品接口。显式 published 才铸 Receipt(与回调同一
// 绑定→去重→确认管线);审核中/处理中/失败/无权限照录观察词,不造成功。
func (a *KuaishouAdapter) Query(ctx context.Context, h publishhandoff.Handoff) (publishhandoff.QueryOutcome, error) {
	now := a.now()
	job, err := a.loadJob(h.TenantID, h.ID)
	if err != nil {
		return publishhandoff.QueryOutcome{}, err
	}
	if strings.TrimSpace(job.photoID) == "" {
		return publishhandoff.QueryOutcome{State: "pending",
			Detail: map[string]string{"reason": "尚无平台任务关联(photo_id 未生成);提交中或未授权"}}, nil
	}
	token, terr := a.loadToken(h.TenantID, h.ID, now)
	if terr != nil {
		return publishhandoff.QueryOutcome{State: "unknown",
			Detail: map[string]string{"reason": "授权已失效,无法回查官方状态", "photo_id": job.photoID}}, nil
	}
	state, published, detail, qerr := a.cfg.Platform.QueryPhoto(ctx, token, job.photoID)
	if qerr != nil {
		return publishhandoff.QueryOutcome{State: "unknown",
			Detail: map[string]string{"reason": "official query failed", "error": qerr.Error(), "photo_id": job.photoID}}, nil
	}
	if err := a.saveQuery(h.TenantID, h.ID, state, detail, now); err != nil {
		return publishhandoff.QueryOutcome{}, err
	}
	out := publishhandoff.QueryOutcome{State: state, Detail: detail}
	if published {
		out.Receipt = &publishhandoff.Receipt{
			ReceiptID: "kuaishou-q-" + job.photoID + "-" + state,
			Platform:  "kuaishou", AppRef: a.cfg.AppRef,
			TenantID: h.TenantID, ParticipationID: h.ParticipationID,
			AttemptID: h.AttemptID, ContentVersion: h.ContentVersion,
			HandoffID: h.ID, PostID: job.photoID,
			Outcome: publishhandoff.ReceiptOutcomePublished, SignedAt: now,
		}
	}
	return out, nil
}

// VerifyCallback: 无已核实 Webhook 契约——拒绝一切回调,不自搭自定义回调充官方。
func (a *KuaishouAdapter) VerifyCallback(ctx context.Context, raw publishhandoff.CallbackPayload) (publishhandoff.Receipt, error) {
	return publishhandoff.Receipt{}, fmt.Errorf("%w: kuaishou has no verified webhook contract in this ticket; refusing every callback", publishhandoff.ErrUnsupported)
}

// FinishOAuth implements OAuthFinisher (state 校验在先、信任在后)。
func (a *KuaishouAdapter) FinishOAuth(ctx context.Context, code, state string) (string, error) {
	now := a.now()
	if strings.TrimSpace(state) == "" || strings.TrimSpace(code) == "" {
		return "", fmt.Errorf("adapters: kuaishou oauth code/state required")
	}
	if a.cfg.Platform == nil {
		return "", fmt.Errorf("adapters: kuaishou platform endpoint not configured")
	}
	var tenantID, handoffID, returnTo, appRef string
	var expiresAt, usedAt string
	err := a.db.QueryRow(`SELECT tenant_id, handoff_id, return_to, app_ref, expires_at, used_at
		FROM kuaishou_oauth_states WHERE state=?`, state).
		Scan(&tenantID, &handoffID, &returnTo, &appRef, &expiresAt, &usedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("adapters: kuaishou oauth state unknown or already used (重放拒绝)")
	}
	if err != nil {
		return "", fmt.Errorf("adapters: kuaishou oauth state lookup: %w", err)
	}
	if usedAt != "" {
		return "", fmt.Errorf("adapters: kuaishou oauth state replay refused (一次性)")
	}
	exp, perr := time.Parse(time.RFC3339Nano, expiresAt)
	if perr != nil || !now.Before(exp) {
		return "", fmt.Errorf("adapters: kuaishou oauth state expired")
	}
	if a.cfg.AppRef != "" && appRef != a.cfg.AppRef {
		return "", fmt.Errorf("adapters: kuaishou oauth state app mismatch (错 App 拒绝)")
	}
	// 一次性:先占位再交换(并发/重放窗口收窄;交换失败 state 已耗,顾客可
	// 回交接页重新发起授权)。
	if _, uerr := a.db.Exec(`UPDATE kuaishou_oauth_states SET used_at=? WHERE state=? AND used_at=''`,
		rfc3339Adapter(now), state); uerr != nil {
		return "", fmt.Errorf("adapters: kuaishou oauth state consume: %w", uerr)
	}
	token, xerr := a.cfg.Platform.ExchangeToken(ctx, code, a.redirectURI())
	if xerr != nil {
		return "", fmt.Errorf("adapters: kuaishou token exchange failed: %w", xerr)
	}
	if strings.TrimSpace(token.OpenID) == "" || strings.TrimSpace(token.AccessToken) == "" {
		return "", fmt.Errorf("adapters: kuaishou token exchange incomplete: 服务端确定账号失败")
	}
	if _, err := a.db.Exec(`INSERT INTO kuaishou_oauth_tokens
		(id, tenant_id, handoff_id, state, open_id, access_token, refresh_token, expires_at, scope, created_at, revoked_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(handoff_id) DO UPDATE SET state=excluded.state, open_id=excluded.open_id,
		  access_token=excluded.access_token, refresh_token=excluded.refresh_token,
		  expires_at=excluded.expires_at, scope=excluded.scope, revoked_at=''`,
		newAdapterID("phkt_"), tenantID, handoffID, state, token.OpenID,
		token.AccessToken, token.RefreshToken, rfc3339Adapter(token.ExpiresAt),
		token.Scope, rfc3339Adapter(now), ""); err != nil {
		return "", fmt.Errorf("adapters: kuaishou token persist: %w", err)
	}
	sep := "?"
	if strings.Contains(returnTo, "?") {
		sep = "&"
	}
	return returnTo + sep + "handoff_id=" + url.QueryEscape(handoffID) + "&oauth=ok", nil
}

// ---- helpers -----------------------------------------------------------------

func (a *KuaishouAdapter) redirectURI() string {
	base := strings.TrimRight(a.cfg.ReturnBase, "/")
	if base == "" {
		return ""
	}
	return base + "/oauth/kuaishou/callback"
}

// stateFor mints or replays the one-time OAuth state (0122). 绑定
// handoff/session/app/return_to;同交接未用未过期 state 复用;重发授权时旧
// state 一律作废(占位 used_at)。
func (a *KuaishouAdapter) stateFor(req publishhandoff.PrepareRequest, now time.Time) (string, string, error) {
	returnTo, err := a.returnToFor(req)
	if err != nil {
		return "", "", err
	}
	var state, rowReturn, rowSession, rowApp, expiresAt, usedAt string
	err = a.db.QueryRow(`SELECT state, return_to, action_session_id, app_ref, expires_at, used_at
		FROM kuaishou_oauth_states WHERE handoff_id=? ORDER BY created_at DESC LIMIT 1`, req.Handoff.ID).
		Scan(&state, &rowReturn, &rowSession, &rowApp, &expiresAt, &usedAt)
	switch {
	case err == nil:
		exp, perr := time.Parse(time.RFC3339Nano, expiresAt)
		fresh := usedAt == "" && perr == nil && now.Before(exp)
		sameCtx := rowReturn == returnTo && rowSession == req.Handoff.SessionID && rowApp == a.cfg.AppRef
		if fresh && sameCtx {
			return state, a.authorizeURL(state), nil
		}
	case errors.Is(err, sql.ErrNoRows):
	default:
		return "", "", fmt.Errorf("adapters: kuaishou state lookup: %w", err)
	}
	// 旧 state 全部作废(一次性语义只认最新一行)。
	if _, uerr := a.db.Exec(`UPDATE kuaishou_oauth_states SET used_at=? WHERE handoff_id=? AND used_at=''`,
		rfc3339Adapter(now), req.Handoff.ID); uerr != nil {
		return "", "", fmt.Errorf("adapters: kuaishou state supersede: %w", uerr)
	}
	state = newKsState()
	expires := now.Add(a.cfg.StateTTL)
	if _, err := a.db.Exec(`INSERT INTO kuaishou_oauth_states
		(state, tenant_id, handoff_id, action_session_id, app_ref, scope, return_to, created_at, expires_at, used_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		state, req.Handoff.TenantID, req.Handoff.ID, req.Handoff.SessionID, a.cfg.AppRef,
		a.cfg.AuthorizeScope, returnTo, rfc3339Adapter(now), rfc3339Adapter(expires), ""); err != nil {
		return "", "", fmt.Errorf("adapters: kuaishou state insert: %w", err)
	}
	return state, a.authorizeURL(state), nil
}

func (a *KuaishouAdapter) authorizeURL(state string) string {
	base := strings.TrimRight(a.cfg.AuthorizeBase, "/")
	q := url.Values{}
	q.Set("client_id", a.cfg.ClientID)
	q.Set("response_type", "code")
	q.Set("scope", a.cfg.AuthorizeScope)
	q.Set("state", state)
	if redirect := a.redirectURI(); redirect != "" {
		q.Set("redirect_uri", redirect)
	}
	return base + "?" + q.Encode()
}

// returnToFor validates the same-origin https return base (oauth 回跳只回
// 本服务页面;形状不过=诚实错误,不回任意外站)。
func (a *KuaishouAdapter) returnToFor(req publishhandoff.PrepareRequest) (string, error) {
	base := strings.TrimRight(a.cfg.ReturnBase, "/")
	if !strings.HasPrefix(base, "https://") {
		return "", fmt.Errorf("adapters: kuaishou return base must be same-origin https")
	}
	return base + "/handoff/" + url.PathEscape(req.Handoff.ID), nil
}

func newKsState() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("adapters: entropy unavailable: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

type ksJobRow struct {
	photoID   string
	openID    string
	stage     string
	mediaRefs string
}

func (a *KuaishouAdapter) loadJob(tenantID, handoffID string) (ksJobRow, error) {
	var job ksJobRow
	var openID string
	err := a.db.QueryRow(`SELECT COALESCE(photo_id,''), stage, COALESCE(media_refs_json,'{}'), COALESCE(open_id,'')
		FROM kuaishou_publish_jobs WHERE tenant_id=? AND handoff_id=?`, tenantID, handoffID).
		Scan(&job.photoID, &job.stage, &job.mediaRefs, &openID)
	if errors.Is(err, sql.ErrNoRows) {
		return job, fmt.Errorf("%w: 交接未完成 Prepare(先回到交接页)", errKsJobMissing)
	}
	if err != nil {
		return job, fmt.Errorf("adapters: kuaishou job lookup: %w", err)
	}
	job.openID = openID
	return job, nil
}

// ensureJob creates the job row at Prepare time and persists freshly minted
// controlled media refs (Start 时序:MediaRefs 在手;Submit 时序:保留原值).
func (a *KuaishouAdapter) ensureJob(req publishhandoff.PrepareRequest, now time.Time) (ksJobRow, error) {
	job, err := a.loadJob(req.Handoff.TenantID, req.Handoff.ID)
	if err == nil {
		// Submit 时序 Prepare(无 refs)不得覆盖 Start 时序的引用。
		return job, nil
	}
	if !errors.Is(err, errKsJobMissing) {
		return ksJobRow{}, err
	}
	refs := map[string]string{}
	if req.MediaRefs != nil {
		for _, m := range req.Media {
			if u, ok := req.MediaRefs[m.AssetID]; ok && strings.TrimSpace(u) != "" {
				refs[m.Kind] = u
			}
		}
	}
	raw, _ := json.Marshal(refs)
	if _, err := a.db.Exec(`INSERT INTO kuaishou_publish_jobs
		(id, tenant_id, handoff_id, open_id, stage, upload_token, photo_id,
		 submit_state, photo_state, last_query_at, detail_json, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		newAdapterID("phkj_"), req.Handoff.TenantID, req.Handoff.ID, "", "", "", "",
		"", "", "", "{}", rfc3339Adapter(now), rfc3339Adapter(now)); err != nil {
		return ksJobRow{}, fmt.Errorf("adapters: kuaishou job insert: %w", err)
	}
	if len(refs) > 0 {
		if _, uerr := a.db.Exec(`UPDATE kuaishou_publish_jobs SET media_refs_json=?,
			updated_at=? WHERE tenant_id=? AND handoff_id=?`,
			string(raw), rfc3339Adapter(now), req.Handoff.TenantID, req.Handoff.ID); uerr != nil {
			return ksJobRow{}, fmt.Errorf("adapters: kuaishou job refs save: %w", uerr)
		}
	}
	return a.loadJob(req.Handoff.TenantID, req.Handoff.ID)
}

func decodeRefs(raw string) (map[string]string, error) {
	out := map[string]string{}
	if strings.TrimSpace(raw) == "" || raw == "{}" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("adapters: kuaishou job refs decode: %w", err)
	}
	return out, nil
}

func (a *KuaishouAdapter) loadToken(tenantID, handoffID string, now time.Time) (KsToken, error) {
	var tk KsToken
	var expRaw string
	err := a.db.QueryRow(`SELECT open_id, access_token, refresh_token, expires_at, scope
		FROM kuaishou_oauth_tokens WHERE tenant_id=? AND handoff_id=? AND revoked_at=''`,
		tenantID, handoffID).Scan(&tk.OpenID, &tk.AccessToken, &tk.RefreshToken, &expRaw, &tk.Scope)
	if errors.Is(err, sql.ErrNoRows) {
		return tk, fmt.Errorf("adapters: kuaishou oauth token missing: 顾客授权缺失或已取消;请回手动指引或重新授权")
	}
	if err != nil {
		return tk, fmt.Errorf("adapters: kuaishou token lookup: %w", err)
	}
	exp, perr := time.Parse(time.RFC3339Nano, expRaw)
	if perr != nil || !now.Before(exp) {
		return tk, fmt.Errorf("adapters: kuaishou oauth token expired: 授权过期,请重新授权")
	}
	return tk, nil
}

func (a *KuaishouAdapter) setStage(tenantID, handoffID, stage string, now time.Time) error {
	if _, err := a.db.Exec(`UPDATE kuaishou_publish_jobs SET stage=?, updated_at=?
		WHERE tenant_id=? AND handoff_id=?`, stage, rfc3339Adapter(now), tenantID, handoffID); err != nil {
		return fmt.Errorf("adapters: kuaishou job stage: %w", err)
	}
	return nil
}

func (a *KuaishouAdapter) setStageToken(tenantID, handoffID, stage, uploadRef string, now time.Time) error {
	if _, err := a.db.Exec(`UPDATE kuaishou_publish_jobs SET stage=?, upload_token=?, updated_at=?
		WHERE tenant_id=? AND handoff_id=?`, stage, uploadRef, rfc3339Adapter(now), tenantID, handoffID); err != nil {
		return fmt.Errorf("adapters: kuaishou job stage/token: %w", err)
	}
	return nil
}

func (a *KuaishouAdapter) setAccepted(tenantID, handoffID, openID, photoID string, now time.Time) error {
	if _, err := a.db.Exec(`UPDATE kuaishou_publish_jobs SET stage='accepted', open_id=?, photo_id=?,
		submit_state='accepted', updated_at=? WHERE tenant_id=? AND handoff_id=?`,
		openID, photoID, rfc3339Adapter(now), tenantID, handoffID); err != nil {
		return fmt.Errorf("adapters: kuaishou job accept: %w", err)
	}
	return nil
}

func (a *KuaishouAdapter) saveQuery(tenantID, handoffID, state string, detail map[string]string, now time.Time) error {
	raw, _ := json.Marshal(detail)
	if _, err := a.db.Exec(`UPDATE kuaishou_publish_jobs SET photo_state=?, last_query_at=?,
		detail_json=?, updated_at=? WHERE tenant_id=? AND handoff_id=?`,
		state, rfc3339Adapter(now), string(raw), rfc3339Adapter(now), tenantID, handoffID); err != nil {
		return fmt.Errorf("adapters: kuaishou job query save: %w", err)
	}
	return nil
}

// captionFor builds the reference caption. 仅当部署确认 3535 已核准文字携带
// (IncludeCaption=true)才随提交携带;默认不携带(unknown 不自动携带)。
func (a *KuaishouAdapter) captionFor(h publishhandoff.Handoff, p publishhandoff.PrepareResult) string {
	if !a.cfg.IncludeCaption {
		return ""
	}
	if p.ClientAction.Caption != "" {
		return p.ClientAction.Caption
	}
	return ""
}

// fetchBytes pulls one controlled media ref (短 TTL platform_fetch;字节只在
// 服务端投稿路径流转,绝不回传浏览器)。
func (a *KuaishouAdapter) fetchBytes(ctx context.Context, ref string) ([]byte, string, error) {
	// 通过 http 客户端拉取受控引用(测试=fixture server;生产=本服务
	// /api/v1/public/content/media/ 端点)。30s 上限防挂死。
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ref, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("media ref %s status %d", sanitizeRef(ref), resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxKsMediaBytes))
	if err != nil {
		return nil, "", err
	}
	return b, resp.Header.Get("Content-Type"), nil
}

const maxKsMediaBytes = 512 << 20 // 512MB 上限(官方视频上限量级;防滥用)

func sanitizeRef(ref string) string {
	if u, err := url.Parse(ref); err == nil {
		u.RawQuery = ""
		return u.String()
	}
	return "(unparsed)"
}
