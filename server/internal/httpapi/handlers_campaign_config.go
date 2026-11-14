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
	"github.com/bianjiefilm/touch-engine/server/internal/campaign"
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
	s.writePublishConfig(w, cfg)
}

// PUT /api/v1/campaigns/{id}/publish-config — save the draft (full-replace).
// Saving updates only the editable version; the published promise and explicit
// pause state stay unchanged until a separate enable/resume/pause action.
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
		// 点评禁奖是准入真值级别的硬拒(3535:点评场景从不携带奖励资格),
		// 机器原因码单独出,web 落到渠道选择区,不是一般形状错误。
		if strings.HasPrefix(err.Error(), campaignenable.ReasonReviewRewardProhibited) {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error":   campaignenable.ReasonReviewRewardProhibited,
				"message": "review channels can never carry a reward; remove them from the selection",
			})
			return
		}
		fail(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	cfg, err := s.St.UpsertPublishConfigDraft(c.Member.TenantID, camp.ID, in, c.Member.PrincipalRef)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "publish config save failed")
		return
	}
	s.writePublishConfig(w, cfg)
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
	var input struct {
		ExpectedVersion int `json:"expected_version"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			fail(w, 400, "bad_request", "invalid enable request")
			return
		}
	}
	if input.ExpectedVersion != 0 && input.ExpectedVersion != cfg.Version {
		fail(w, 409, "version_mismatch", "草稿已更新，请重新查看并确认本次版本")
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
		if errors.Is(err, store.ErrPublishConfigVersionConflict) {
			fail(w, 409, "version_mismatch", "草稿已更新，请重新检查本次版本")
			return
		}
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
	s.writePublishConfig(w, out)
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
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrPublishConfigNotEnabled) {
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
	s.writePublishConfig(w, out)
}

// GET /api/v1/campaigns/{id}/publish-config/snapshot — the frozen告知快照 plus
// the G3 sync contract in the rewardops.Snapshot JSON shape. Only an enabled
// published config serves a snapshot; unenabled/paused return not_enabled.
func (s *Server) handlePublishConfigSnapshot(w http.ResponseWriter, r *http.Request) {
	if !s.rulesGate(w) {
		return
	}
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authzManageRules, w)
	if !ok {
		return
	}
	cfg, err := s.St.GetPublishedConfig(c.Member.TenantID, camp.ID)
	if errors.Is(err, store.ErrPublishConfigNotEnabled) {
		writeJSON(w, http.StatusConflict, map[string]any{"state": "not_enabled"})
		return
	}
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
	return s.assembleConfigFacts(tenantID, camp, cfg, now, true, false)
}

func (s *Server) assemblePublishedFacts(tenantID string, camp store.Campaign, cfg store.PublishConfig, now time.Time) (campaignenable.Facts, error) {
	return s.assembleConfigFacts(tenantID, camp, cfg, now, false, false)
}

func (s *Server) assembleConfigFacts(tenantID string, camp store.Campaign, cfg store.PublishConfig, now time.Time, requireAdoption, allowNoFreeStock bool) (campaignenable.Facts, error) {
	facts := campaignenable.Facts{Now: now, RuleVersion: cfg.Version}

	// 内容事实(3553):所选版本必须是本活动的当前采用版,且可分发
	// (adopted + 授权未过期;blocked 单列原因)。
	adoptions, err := s.St.ListCampaignContentAdoptions(tenantID, camp.ID)
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
	if !requireAdoption {
		adopted = true
	}
	if adopted && requireAdoption {
		for _, ch := range cfg.Spec.Channels {
			matched := false
			for _, a := range adoptions {
				if a.Platform == ch.Platform && a.VersionID == cfg.Spec.ContentVerID {
					matched = true
					break
				}
			}
			if !matched {
				adopted = false
				break
			}
		}
	}
	facts.ContentAdopted = adopted
	if adopted {
		ver, err := s.St.GetCampaignContentVersion(tenantID, camp.ID, cfg.Spec.ContentVerID)
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
	inventory, err := s.St.ListRewardInventoryAt(tenantID, camp.ID, now)
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
			if (inv.AvailableQty > 0 || allowNoFreeStock) && inventoryFitsPublishedPromise(inv, cfg, camp, now) {
				facts.BenefitStockOK = true
			}
		}
	}

	// 期限事实:活动未结束;领取窗在活动期内且窗口内部有序(兑换期不早于
	// 领取期截止;兑换可长于活动期,券的核销期独立)。
	facts.CampaignEnded = camp.Status == "ended" || campaignEndedAt(camp, now)
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

// Management projection reuses the same window and publish preflight as writes.
// It does not reserve stock or change lifecycle; unrelated store services stay open.
type campaignAvailability struct {
	State    string `json:"state"`
	Label    string `json:"label"`
	CanStart bool   `json:"can_start"`
	Reason   string `json:"reason,omitempty"`
}
type campaignManagementView struct {
	store.Campaign
	Availability campaignAvailability `json:"availability"`
}

func (s *Server) campaignManagement(camp store.Campaign) campaignManagementView {
	a := campaignAvailability{State: camp.Status, Label: map[string]string{"draft": "草稿（未发布）", "paused": "已暂停，停止新参与与新发放", "ended": "已结束，已发有效券仍可使用", "active": "可参与"}[camp.Status]}
	now := s.rewardTime()
	window := campaign.Window{StartsAt: camp.StartsAt, EndsAt: camp.EndsAt}
	if reason := window.ResolveTime(now); reason != "" && camp.Status != "ended" {
		a.State = reason
		a.Label = map[string]string{"expired": "参与期已结束，已发有效券仍可使用", "not_started": "尚未到活动开始时间"}[reason]
	} else if camp.Status == "active" {
		a.CanStart = true
		cfg, err := s.St.GetPublishedConfig(camp.TenantID, camp.ID)
		if err == nil {
			if cfg.Status != "enabled" {
				a.State = "reward_not_enabled"
				a.Label = "活动已开放，推广优惠尚未启用"
				a.CanStart = false
			} else if reason := (campaign.Window{StartsAt: cfg.Spec.Reward.ClaimFrom, EndsAt: cfg.Spec.Reward.ClaimUntil}).ResolveTime(now); reason != "" {
				a.State = "claim_" + reason
				a.Label = map[string]string{"not_started": "优惠尚未到领取时间", "expired": "优惠领取期已结束，已发有效券仍可使用"}[reason]
				a.CanStart = false
			} else if facts, err := s.assemblePublishedFacts(camp.TenantID, camp, cfg, now); err != nil {
				a.State = "unknown"
				a.Label = "当前可参与性未读到"
				a.CanStart = false
			} else if verdict := campaignenable.Check(cfg.Spec, facts); !verdict.Allowed {
				a.State = "reward_unavailable"
				a.Label = "推广优惠暂不可参与"
				a.Reason = "请在活动承诺中查看预检缺项；其他门店服务不受影响"
				a.CanStart = false
			}
		} else if errors.Is(err, store.ErrPublishConfigNotEnabled) {
			a.State = "reward_not_enabled"
			a.Label = "活动已开放，推广优惠尚未启用"
			a.CanStart = false
		} else if !errors.Is(err, store.ErrNotFound) {
			a.State = "unknown"
			a.Label = "当前可参与性未读到"
			a.CanStart = false
		}
	}
	return campaignManagementView{Campaign: camp, Availability: a}
}

func parseConfigTime(value string) time.Time { t, _ := time.Parse(time.RFC3339, value); return t }

// Resume only the published revision. A newer draft remains pending and must
// use the enable endpoint with its own channel/inventory preflight.
func (s *Server) handlePublishConfigResume(w http.ResponseWriter, r *http.Request) {
	if !s.rulesGate(w) {
		return
	}
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authzManageRules, w)
	if !ok {
		return
	}
	live, err := s.St.GetPublishedConfig(c.Member.TenantID, camp.ID)
	if errors.Is(err, store.ErrPublishConfigNotEnabled) || errors.Is(err, store.ErrNotFound) {
		fail(w, 409, "not_enabled", "尚无可恢复的已启用版本")
		return
	}
	if err != nil {
		fail(w, 503, "preflight_unavailable", "原生效版本没有读到，未恢复活动")
		return
	}
	var input struct {
		ExpectedVersion int `json:"expected_version"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			fail(w, 400, "bad_request", "invalid resume request")
			return
		}
	}
	if input.ExpectedVersion != 0 && input.ExpectedVersion != live.EnabledVersion {
		fail(w, 409, "version_mismatch", "线上版本已更新，请重新查看后恢复")
		return
	}
	facts, err := s.assembleConfigFacts(c.Member.TenantID, camp, live, time.Now().UTC(), false, true)
	if err != nil {
		fail(w, 503, "preflight_unavailable", "原版本预检没有读到")
		return
	}
	if verdict := campaignenable.Check(live.Spec, facts); !verdict.Allowed {
		writeJSON(w, 422, map[string]any{"error": verdict.Reason, "field": verdict.Field, "message": "原生效版本当前不可恢复，请核对真实缺项"})
		return
	}
	if err := s.St.ResumePublishConfig(c.Member.TenantID, camp.ID, live.EnabledVersion, c.Member.PrincipalRef); err != nil {
		if errors.Is(err, store.ErrPublishConfigVersionConflict) {
			fail(w, 409, "version_mismatch", "线上版本已更新，请重试")
			return
		}
		fail(w, 503, "preflight_unavailable", "恢复没有完成，请回查原状态")
		return
	}
	cfg, err := s.St.GetPublishConfig(c.Member.TenantID, camp.ID)
	if err != nil {
		fail(w, 500, "internal", "publish config lookup failed")
		return
	}
	s.writePublishConfig(w, cfg)
}

func (s *Server) writePublishConfig(w http.ResponseWriter, cfg store.PublishConfig) {
	state := "unknown"
	live, err := s.St.GetPublishedConfig(cfg.TenantID, cfg.CampaignID)
	if err == nil {
		state = live.Status
	} else if errors.Is(err, store.ErrPublishConfigNotEnabled) || errors.Is(err, store.ErrNotFound) {
		state = "not_enabled"
	}
	writeJSON(w, http.StatusOK, struct {
		store.PublishConfig
		PublishedStatus string `json:"published_status"`
	}{PublishConfig: cfg, PublishedStatus: state})
}

// Restoring an existing promise does not need unallocated stock, but never
// relaxes the real batch's ownership, version, type, verification or validity.
func inventoryFitsPublishedPromise(inv store.RewardInventory, cfg store.PublishConfig, camp store.Campaign, at time.Time) bool {
	if inv.TenantID != cfg.TenantID || inv.CampaignID != camp.ID || inv.RuleVersion != cfg.Version || inv.BenefitSlot != cfg.Spec.Reward.Slot || inv.TotalQty <= 0 || inv.Status != store.RewardInventoryActive {
		return false
	}
	kind := cfg.Spec.Reward.Kind
	if kind == campaignenable.KindOnsiteGift || kind == campaignenable.KindOnsiteDiscount {
		kind = store.RewardKindGiftDiscount
	}
	if inv.Kind != kind {
		return false
	}
	if inv.StoreID != "" {
		bound := false
		for _, id := range cfg.Spec.Reward.StoreIDs {
			if id == inv.StoreID {
				bound = true
			}
		}
		if !bound || (camp.StoreID != "" && camp.StoreID != inv.StoreID) {
			return false
		}
	}
	verified := false
	for _, mode := range inv.AllowedEvidence {
		if mode == cfg.Spec.VerificationMode {
			verified = true
		}
	}
	if !verified {
		return false
	}
	if inv.ValidFrom != "" {
		from, err := time.Parse(time.RFC3339, inv.ValidFrom)
		if err != nil || at.Before(from) {
			return false
		}
	}
	if inv.ValidUntil != "" {
		until, err := time.Parse(time.RFC3339, inv.ValidUntil)
		if err != nil || at.After(until) {
			return false
		}
	}
	return true
}
