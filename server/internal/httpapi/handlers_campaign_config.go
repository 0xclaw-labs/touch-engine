// handlers_campaign_config.go: HUI-1676 增量(商家配置与真实启用)的 HTTP 面。
//
// 纪律:
//   - 开关:复用 FEATURE_CAMPAIGN_RULES(默认 off)= 路由不注册 + 本 gate
//     再答统一 404(与规则 CRUD 同一双保险写法);本票就是规则编辑器,
//     一个开关管整个编辑器;
//   - 授权:全部走 authz.ActionManageCampaignRules(仅 org_owner);
//     跨租户活动一律 404 不可见(campaignScoped 掩码);
//   - 服务端单点判定:启用前检查 = campaignenable.Check 纯函数,事实集由
//     本层从既有真值只读组装(contentpack 3553 采纳与授权、channeladmission
//     3535 真实 Gate(RewardPlay=true)、reward_inventory 1671 库存只读、
//     campaigns 期限、stores 权限);本层不写参与/奖励表;
//   - 保存草稿只做形状校验(ValidateSpec);完整启用检查只在 enable 端点
//     (拒绝带机器原因码 + field,web 落到具体字段);
//   - 预览是快照读取,零写路径、零奖励消耗;enabled 之外一律 409 not_enabled,
//     不制造「已承诺奖励」假象;
//   - 暂停只动 campaign_publish_configs 一张表:参与/奖励零触碰,老参与
//     核验/履约入口天然保留。
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/campaignenable"
	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// authzManageRules is the single authorization action for the whole rule
// editor surface (rules CRUD + publish config): org_owner only.
const authzManageRules = authz.ActionManageCampaignRules

// GET /api/v1/campaigns/{id}/publish-config — the saved config (404 when never configured).
func (s *Server) handlePublishConfigGet(w http.ResponseWriter, r *http.Request) {
	if !s.rulesGate(w) {
		return
	}
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authzManageRules, w)
	if !ok {
		return
	}
	cfg, err := s.St.GetPublishConfig(c.Member.TenantID, camp.ID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "publish config not configured")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "publish config lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

// PUT /api/v1/campaigns/{id}/publish-config — save the draft (full-replace).
// Saving over an enabled config demotes it to draft: the new version must pass
// the enable check again before it is live.
func (s *Server) handlePublishConfigPut(w http.ResponseWriter, r *http.Request) {
	if !s.rulesGate(w) {
		return
	}
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authzManageRules, w)
	if !ok {
		return
	}
	var in campaignenable.Spec
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return
	}
	if err := campaignenable.ValidateSpec(in); err != nil {
		fail(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	cfg, err := s.St.UpsertPublishConfigDraft(c.Member.TenantID, camp.ID, in, c.Member.PrincipalRef)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "publish config save failed")
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

// POST /api/v1/campaigns/{id}/publish-config/enable — the only path to enabled.
// Refusals answer 422 with {error: <machine reason>, field: <config field>}.
func (s *Server) handlePublishConfigEnable(w http.ResponseWriter, r *http.Request) {
	if !s.rulesGate(w) {
		return
	}
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authzManageRules, w)
	if !ok {
		return
	}
	cfg, err := s.St.GetPublishConfig(c.Member.TenantID, camp.ID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "publish config not configured")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "publish config lookup failed")
		return
	}
	facts, err := s.assembleEnableFacts(c.Member.TenantID, camp, cfg, time.Now().UTC())
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "enable facts assembly failed")
		return
	}
	if d := campaignenable.Check(cfg.Spec, facts); !d.Allowed {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": d.Reason, "field": d.Field,
			"message": "publish config is not enableable yet",
		})
		return
	}
	disclosure := campaignenable.BuildDisclosure(cfg.Spec, cfg.Version, time.Now().UTC()).JSON()
	if err := s.St.EnablePublishConfig(c.Member.TenantID, camp.ID, cfg.Version, disclosure, c.Member.PrincipalRef); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(w, http.StatusNotFound, "not_found", "publish config not configured")
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "publish config enable failed")
		return
	}
	out, err := s.St.GetPublishConfig(c.Member.TenantID, camp.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "publish config lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /api/v1/campaigns/{id}/publish-config/pause — 紧急暂停: stops new
// publish-reward activity; participations/rewards are untouched (staff
// fulfillment keeps its own read paths).
func (s *Server) handlePublishConfigPause(w http.ResponseWriter, r *http.Request) {
	if !s.rulesGate(w) {
		return
	}
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authzManageRules, w)
	if !ok {
		return
	}
	err := s.St.PausePublishConfig(c.Member.TenantID, camp.ID, c.Member.PrincipalRef)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "no enabled publish config")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "publish config pause failed")
		return
	}
	out, err := s.St.GetPublishConfig(c.Member.TenantID, camp.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "publish config lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// GET /api/v1/campaigns/{id}/publish-config/snapshot — the frozen告知快照 plus
// the G3 sync contract in the rewardops.Snapshot JSON shape. Only an enabled
// config serves a snapshot (draft/paused = not_enabled: no promised reward).
func (s *Server) handlePublishConfigSnapshot(w http.ResponseWriter, r *http.Request) {
	if !s.rulesGate(w) {
		return
	}
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authzManageRules, w)
	if !ok {
		return
	}
	cfg, err := s.St.GetPublishConfig(c.Member.TenantID, camp.ID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "publish config not configured")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "publish config lookup failed")
		return
	}
	if cfg.Status != "enabled" {
		writeJSON(w, http.StatusConflict, map[string]any{"state": "not_enabled"})
		return
	}
	disc, perr := campaignenable.ParseDisclosure(cfg.Disclosure)
	if perr != nil {
		fail(w, http.StatusInternalServerError, "internal", "publish disclosure unreadable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"campaign_id":      camp.ID,
		"version":          cfg.EnabledVersion,
		"allowed_evidence": []string{cfg.Spec.VerificationMode},
		"slots":            []string{cfg.Spec.Reward.Slot},
		"disclosure":       disc,
	})
}

// GET /api/v1/campaigns/{id}/publish-config/revisions — the immutable trail.
func (s *Server) handlePublishConfigRevisions(w http.ResponseWriter, r *http.Request) {
	if !s.rulesGate(w) {
		return
	}
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authzManageRules, w)
	if !ok {
		return
	}
	items, err := s.St.ListPublishConfigRevisions(c.Member.TenantID, camp.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "publish revisions lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ---- enable facts assembly (read-only over existing truths) --------------------

// assembleEnableFacts builds campaignenable.Facts from the store's existing
// truths. Every source is read-only; nothing here writes participation or
// reward tables.
func (s *Server) assembleEnableFacts(tenantID string, camp store.Campaign, cfg store.PublishConfig, now time.Time) (campaignenable.Facts, error) {
	facts := campaignenable.Facts{Now: now, RuleVersion: cfg.Version}

	// 内容事实(3553):所选版本必须是本活动的当前采用版,且可分发
	// (adopted + 授权未过期;blocked 单列原因)。
	adoptions, err := s.St.ListAdoptions(tenantID, camp.ID)
	if err != nil {
		return facts, err
	}
	adopted := false
	for _, a := range adoptions {
		if a.VersionID == cfg.Spec.ContentVerID {
			adopted = true
			break
		}
	}
	facts.ContentAdopted = adopted
	if adopted {
		ver, err := s.St.GetContentPackVersion(cfg.Spec.ContentVerID, tenantID)
		if err != nil {
			return facts, err
		}
		facts.ContentBlocked = ver.Status == contentpack.StatusBlocked
		facts.ContentDistributable = contentpack.Distributable(ver.Status, ver.GrantUntil, now) == nil
	}

	// 渠道事实(3535):逐所选路线过真实 Gate(RewardPlay=true);未登记路线
	// 按 unknown 规则 fail-closed,绝不默认允许。
	admissions, err := s.St.ListChannelAdmissions(tenantID)
	if err != nil {
		return facts, err
	}
	byKey := make(map[string]channeladmission.Record, len(admissions))
	for _, rec := range admissions {
		byKey[rec.IdentityKey()] = rec
	}
	for _, sel := range cfg.Spec.Channels {
		rec, known := byKey[sel.Platform+"|"+sel.EntryMode+"|"+sel.Scenario]
		if !known {
			facts.ChannelVerdicts = append(facts.ChannelVerdicts, campaignenable.ChannelVerdict{
				Ref: sel, Allowed: false, Reason: channeladmission.StatusUnknown,
			})
			continue
		}
		d := channeladmission.Gate(channeladmission.GateInput{
			Now: now, TenantID: tenantID, RewardPlay: true,
		}, rec)
		facts.ChannelVerdicts = append(facts.ChannelVerdicts, campaignenable.ChannelVerdict{
			Ref: sel, Allowed: d.Allowed, Reason: d.Reason,
		})
	}

	// 权益事实(1671,只读):引用存在 + 版本对齐 + 有余量。批次按
	// (campaign, slot) 组织,版本不齐 = G3 还没被告知这一版 = 拒绝。
	inventory, err := s.St.ListRewardInventory(tenantID, camp.ID)
	if err != nil {
		return facts, err
	}
	for _, inv := range inventory {
		if inv.BenefitSlot != cfg.Spec.Reward.Slot {
			continue
		}
		facts.BenefitRefPresent = true
		if inv.RuleVersion == cfg.Version {
			facts.BenefitVersionOK = true
			if inv.AvailableQty > 0 {
				facts.BenefitStockOK = true
			}
		}
	}

	// 期限事实:活动未结束;领取窗在活动期内且窗口内部有序(兑换期不早于
	// 领取期截止;兑换可长于活动期,券的核销期独立)。
	facts.CampaignEnded = campaignEndedAt(camp, now)
	facts.WindowOK = windowsOK(cfg.Spec, camp, now)

	// 门店事实:配置门店都必须真实存在于本租户;活动绑定了门店时,配置
	// 必须包含它(错店拒绝)。
	facts.StoreScopeOK = true
	hasBound := false
	for _, sid := range cfg.Spec.Reward.StoreIDs {
		if _, err := s.St.GetStore(sid, tenantID); err != nil {
			facts.StoreScopeOK = false
			break
		}
		if camp.StoreID != "" && sid == camp.StoreID {
			hasBound = true
		}
	}
	if facts.StoreScopeOK && camp.StoreID != "" && !hasBound {
		facts.StoreScopeOK = false
	}
	return facts, nil
}

func campaignEndedAt(camp store.Campaign, now time.Time) bool {
	if strings.TrimSpace(camp.EndsAt) == "" {
		return false
	}
	end, err := time.Parse(time.RFC3339, camp.EndsAt)
	if err != nil {
		// 不可解析的活动结束时间按未结束处理(形状校验归活动面,这里不越权)。
		return false
	}
	return !now.Before(end)
}

// windowsOK checks window ordering and the claim window living inside the
// campaign period. All four values are required (ValidateSpec enforces RFC3339
// shape; the enable check requires presence and order).
func windowsOK(spec campaignenable.Spec, camp store.Campaign, now time.Time) bool {
	r := spec.Reward
	if r.ClaimFrom == "" || r.ClaimUntil == "" || r.RedeemFrom == "" || r.RedeemUntil == "" {
		return false
	}
	cf, e1 := time.Parse(time.RFC3339, r.ClaimFrom)
	cu, e2 := time.Parse(time.RFC3339, r.ClaimUntil)
	rf, e3 := time.Parse(time.RFC3339, r.RedeemFrom)
	ru, e4 := time.Parse(time.RFC3339, r.RedeemUntil)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
		return false
	}
	if cf.After(cu) || rf.After(ru) || cu.After(ru) {
		return false
	}
	if s := strings.TrimSpace(camp.StartsAt); s != "" {
		if st, err := time.Parse(time.RFC3339, s); err == nil && cf.Before(st) {
			return false
		}
	}
	if strings.TrimSpace(camp.EndsAt) != "" {
		if end, err := time.Parse(time.RFC3339, camp.EndsAt); err == nil && cu.After(end) {
			return false
		}
	}
	return true
}
