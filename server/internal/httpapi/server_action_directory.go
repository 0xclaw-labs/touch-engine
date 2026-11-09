package httpapi

// 中央集成接线(2026-11-10): HUI-4584 动作目录 ← HUI-3546 ActionDirectory 端口。
//
// Server 自身实现 participation.ActionDirectory:目录的权威是 store 层的
// 已发布动作投影,这里只做一次受信解析,绝不在 httpapi 建第二份目录。
//
// fail-closed 语义(端口注释即语义):未知 / 未发布 / 停用 / 撤销 / 过期 /
// 目录未映射 / 目标非公网 https / 门店停用 / 租户悬置,一律 (zero,false,nil) ——
// participation 把 !ok 收敛为 ErrNotFound(统一 404,反枚举);端口错误才走
// error(500 语义)。门店/租户事实与公共门店面同一道门:store status=active
// 且 tenant lifecycle=active;动作可见性与 ListPublicStoreActions 单点收口
// 完全一致(已映射+启用+未撤销+未过期+公网 https 目标)。
//
// ActionRef 字段逐项来源:
//   - TenantID/StoreID: StoreActionScope(受信接缝,id 不是授权);
//   - CatalogCode/Platform/Kind/ActionRevision: store_actions 行;
//   - StorefrontRevision: store_storefronts 行(懒建,稳定码);
//   - RuleVersion/RuleSnapshot: 仅 legacy 挂活动行 —— campaign_rules 当前版
//     本 + Ruleset JSON 快照;无活动行 0/""(绝不伪造活动上下文);
//   - CampaignID/Window: legacy_campaign_id → campaigns 行(活动行消失时照常
//     服务但不带活动上下文);窗口过期由 participation 自行裁决(ErrFrozen);
//   - ContentPackVersion: 内容绑定表 → 包的当前采用指针,且仅当可分发
//     (blocked/授权过期一律 "" —— 目录不发放不可分发内容的引用);
//   - Enabled: 恒 true(只有全部门槛通过才 ok=true;停用动作根本到不了这里)。

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/participation"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// 编译期自检:装配必须满足 participation.ActionDirectory。
var _ participation.ActionDirectory = (*Server)(nil)

// ResolveAction implements participation.ActionDirectory over the published
// store-action projection (HUI-4584). See the file header for the
// fail-closed contract.
func (s *Server) ResolveAction(_ context.Context, actionID string) (participation.ActionRef, bool, error) {
	tenantID, storeID, err := s.St.StoreActionScope(actionID)
	if errors.Is(err, store.ErrNotFound) {
		return participation.ActionRef{}, false, nil // 未知动作:统一 not-found,不泄漏存在性
	}
	if err != nil {
		return participation.ActionRef{}, false, err
	}
	// 已发布门店:tenant lifecycle active + store status active(公共门店面同门)。
	rec, err := s.St.GetStore(storeID, tenantID)
	if errors.Is(err, store.ErrNotFound) {
		return participation.ActionRef{}, false, nil
	}
	if err != nil {
		return participation.ActionRef{}, false, err
	}
	if rec.Status != store.StoreStatusActive {
		return participation.ActionRef{}, false, nil
	}
	ten, err := s.St.GetTenant(tenantID)
	if errors.Is(err, store.ErrNotFound) {
		return participation.ActionRef{}, false, nil
	}
	if err != nil {
		return participation.ActionRef{}, false, err
	}
	if ten.Lifecycle != "active" {
		return participation.ActionRef{}, false, nil
	}
	// 动作可见性:只认公共投影单点收口过的行。
	items, err := s.St.ListPublicStoreActions(storeID, time.Now())
	if err != nil {
		return participation.ActionRef{}, false, err
	}
	var item store.StoreAction
	found := false
	for _, it := range items {
		if it.ID == actionID {
			item, found = it, true
			break
		}
	}
	if !found {
		return participation.ActionRef{}, false, nil // 未发布/停用/撤销/过期/目标不合法
	}
	sf, err := s.St.StorefrontForStore(tenantID, storeID)
	if err != nil {
		return participation.ActionRef{}, false, err
	}
	ref := participation.ActionRef{
		ActionID:           item.ID,
		CatalogCode:        item.CatalogCode,
		Platform:           item.Platform,
		Kind:               item.Kind,
		TenantID:           tenantID,
		StoreID:            storeID,
		StorefrontRevision: sf.StorefrontRevision,
		ActionRevision:     item.Revision,
		Enabled:            true, // 只有全门槛通过才到达这里
	}
	// 活动上下文(仅 legacy 挂活动行;活动行消失不拦动作,绝不伪造 id)。
	if item.LegacyCampaignID != "" {
		cmp, err := s.St.GetCampaign(item.LegacyCampaignID, tenantID)
		if errors.Is(err, store.ErrNotFound) {
			// 活动行已消失:动作照常服务,但不携带活动上下文。
		} else if err != nil {
			return participation.ActionRef{}, false, err
		} else {
			ref.CampaignID = cmp.ID
			ref.Window = participation.Window{StartsAt: cmp.StartsAt, ExpiresAt: cmp.EndsAt}
			rule, rerr := s.St.GetCampaignRules(tenantID, cmp.ID)
			if rerr == nil {
				ref.RuleVersion = rule.Version
				if raw, merr := json.Marshal(rule.Ruleset); merr == nil {
					ref.RuleSnapshot = string(raw)
				}
			} else if !errors.Is(rerr, store.ErrNotFound) {
				return participation.ActionRef{}, false, rerr
			}
		}
	}
	// 已采用内容版本:绑定 → 包 → 当前采用指针;不可分发一律 ""(fail-closed)。
	b, berr := s.St.GetActionContentBinding(tenantID, item.ID)
	if errors.Is(berr, store.ErrNotFound) {
		b = store.ActionContentBinding{} // 无绑定:内容引用留空
	} else if berr != nil {
		return participation.ActionRef{}, false, berr
	}
	if b.PackID != "" {
		v, verr := s.St.GetStoreAdoptedPackVersion(tenantID, b.PackID)
		if errors.Is(verr, store.ErrNotFound) {
			// 无采用指针:目录不发放内容引用。
		} else if verr != nil {
			return participation.ActionRef{}, false, verr
		} else if contentpack.Distributable(v.Status, v.GrantUntil, time.Now()) == nil {
			ref.ContentPackVersion = v.ID
		}
	}
	return ref, true, nil
}
