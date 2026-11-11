package httpapi

// HUI-4584 G1: 公共门店面数据面读端点（契约消费方：1676/1896/2628/4589/4590）。
// HUI-1676 接管：storefront_revision 0→N（发布指针归版本域），有发布版本时
// 公共投影渲染「发布时冻结的白名单载荷 + 实时否决」。
//
// 纪律：
//   - GET-only 白名单面，无会话概念；顾客自带 href 永不进入（payload 连
//     target_ref 都不下发——目标地址按方法受控解析，归 HUI-4589）。
//   - 有发布版本（指针>0）：sections/actions/activity_cards/policy_links 来自
//     版本行冻结载荷（回滚显示对应版本），实时撤权/过期/停用/停店在读取层
//     另行否决（版本显示对应版本，绝不复活失效权限）。
//   - 无发布版本（指针=0）：4584 默认投影逐字节保留（已映射+启用+未撤销+
//     未过期+目标公网 https 的实例；默认单 section）。
//   - 禁止 tenant 密钥/内部 scope 泄漏：payload 无 tenant_id/target_ref/legacy。
//   - 登记制开关 FEATURE_STOREFRONT（默认 off）：路由不注册 + handler gate
//     统一 404，双保险同 FEATURE_TOUCHPOINT_ENTRY 写法。
//   - 缓存（FEATURE_PUBLIC_CACHE 才启用）：渲染结果按 storeCode 进 readcache，
//     epoch 校验挂在每次发布/回滚 bumpEpoch —— revision 切换即 0 请求过期
//     （HUI-2981 既有缓存纪律复用，错误永不进缓存）。

import (
	"net/http"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/storefrontversion"
)

// storefrontSchemaVersion freezes the public payload shape for this ticket's
// data-plane contract.
const storefrontSchemaVersion = "storefront.v1"

func (s *Server) handlePublicStorefront(w http.ResponseWriter, r *http.Request) {
	if !s.Cfg.FeatureStorefront {
		fail(w, http.StatusNotFound, "not_found", "storefront is not available")
		return
	}
	code := r.PathValue("storeCode")
	if s.Cfg.FeaturePublicCache {
		if payload, ok := s.StorefrontCache.Get("sf:" + code); ok {
			writeJSON(w, http.StatusOK, payload)
			return
		}
	}
	payload, status := s.buildPublicStorefront(code)
	if payload == nil {
		fail(w, http.StatusNotFound, "not_found", "storefront is not available")
		return
	}
	if status != http.StatusOK {
		writeJSON(w, status, payload)
		return
	}
	if s.Cfg.FeaturePublicCache {
		s.StorefrontCache.Set("sf:"+code, payload)
	}
	writeJSON(w, http.StatusOK, payload)
}

// buildPublicStorefront renders one storefront payload: published version when
// one exists, the 4584 live fallback otherwise. (nil, 0) = unavailable (404).
func (s *Server) buildPublicStorefront(code string) (map[string]any, int) {
	pub, err := s.St.GetStorefrontPublicByCode(code)
	if err != nil {
		return nil, 0
	}
	if pub.TenantLifecycle != "active" {
		// 悬置/导出中的租户不在公共面开放（fail-closed，不区分原因）。
		return map[string]any{
			"error": "not_found", "state": "unauthorized",
			"message": "storefront is not available",
		}, http.StatusNotFound
	}

	now := time.Now().UTC()
	state := "active"
	if pub.StoreStatus != "active" {
		state = "disabled"
	}

	// 发布版本优先：冻结载荷 + 实时否决（撤权/过期/停用动作不出现）。
	if version, err := s.St.GetStorefrontPublishedByCode(code); err == nil && version.Revision > 0 {
		rec := store.StoreRecord{ID: pub.StoreID, TenantID: pub.TenantID,
			Name: pub.StoreName, Address: pub.StoreAddress, Status: pub.StoreStatus}
		payload := s.storefrontPublicPayload(rec, version, pub.OpeningHours, now)
		payload["store"].(map[string]any)["state"] = state
		if state != "active" {
			// 停店：动作清单清空（与 4584 默认投影同一收口），版本指针仍如实。
			payload["actions"] = []any{}
			payload["sections"] = []any{}
		}
		return payload, http.StatusOK
	}

	actions, err := s.St.ListPublicStoreActions(pub.StoreID, now)
	if err != nil {
		return map[string]any{"error": "internal", "message": "storefront lookup failed"}, http.StatusInternalServerError
	}
	if state != "active" {
		actions = nil
	}

	ids := make([]string, 0, len(actions))
	payload := make([]map[string]any, 0, len(actions))
	for _, item := range actions {
		ids = append(ids, item.ID)
		payload = append(payload, map[string]any{
			"action_id":       item.ID,
			"action_revision": item.Revision,
			"catalog_code":    item.CatalogCode,
			"platform":        item.Platform,
			"kind":            item.Kind,
			"variant":         item.Variant,
			"label":           item.Label,
			"presentation":    item.Presentation,
			"capability_ref":  item.CapabilityRef,
		})
	}

	return map[string]any{
		"schema_version":      storefrontSchemaVersion,
		"store_code":          pub.StoreCode,
		"storefront_revision": pub.StorefrontRevision,
		"store": map[string]any{
			"name":          pub.StoreName,
			"address":       pub.StoreAddress,
			"opening_hours": pub.OpeningHours,
			"state":         state,
		},
		"sections": []map[string]any{
			{"section_id": "default", "title": "门店服务", "action_ids": ids},
		},
		"actions":        payload,
		"activity_cards": []any{},
		"policy_links":   []any{},
	}, http.StatusOK
}

// storefrontPublicPayload renders a frozen version row into the whitelist
// public shape. Shared by the public endpoint (published revision) and the
// session-constrained preview face (draft/published/rollback-target).
//
// 实时否决仍然生效：冻结载荷只决定「显示哪个版本」，撤权/过期/停用/目标失效
// 的动作此刻不出现（fail-closed）；套餐引用失效（retired/paused）同样剔除；
// 活动卡片只带 campaign_code + state（白名单），不复制活动配置。
func (s *Server) storefrontPublicPayload(rec store.StoreRecord, version store.StorefrontVersion, openingHours string, now time.Time) map[string]any {
	visible := map[string]bool{}
	if live, err := s.St.ListPublicStoreActions(rec.ID, now); err == nil {
		for _, item := range live {
			visible[item.ID] = true
		}
	}
	frozen := map[string]storefrontversion.ActionSnapshot{}
	for _, a := range version.Actions {
		frozen[a.ActionID] = a
	}

	sections := make([]map[string]any, 0, len(version.Sections))
	actions := make([]map[string]any, 0, len(version.Actions))
	for _, sec := range version.Sections {
		switch sec.Type {
		case "actions":
			ids := make([]string, 0, len(sec.ActionIDs))
			for _, id := range sec.ActionIDs {
				if !visible[id] {
					continue // 实时否决：撤权/过期/停用动作此刻不可执行
				}
				snap, ok := frozen[id]
				if !ok {
					continue // 冻结载荷缺失(不可能;防御性收口)
				}
				ids = append(ids, id)
				actions = append(actions, map[string]any{
					"action_id":       snap.ActionID,
					"action_revision": snap.ActionRevision,
					"catalog_code":    snap.CatalogCode,
					"platform":        snap.Platform,
					"kind":            snap.Kind,
					"variant":         snap.Variant,
					"label":           snap.Label,
					"presentation":    snap.Presentation,
					"capability_ref":  snap.CapabilityRef,
					"section_id":      snap.SectionID,
				})
			}
			if len(ids) == 0 {
				continue
			}
			sections = append(sections, map[string]any{
				"section_id": sec.ID, "type": sec.Type, "title": sec.Title, "action_ids": ids,
			})
		case "offers":
			refs := make([]string, 0, len(sec.OfferRefs))
			for _, ref := range sec.OfferRefs {
				if offer, err := s.St.GetDealOffer(ref, version.TenantID); err == nil && offer.Status == "active" {
					refs = append(refs, ref) // 失效套餐(退役/暂停)实时剔除
				}
			}
			if len(refs) == 0 {
				continue
			}
			sections = append(sections, map[string]any{
				"section_id": sec.ID, "type": sec.Type, "title": sec.Title, "offer_refs": refs,
			})
		}
	}

	activityCards := make([]map[string]any, 0, len(version.CampaignRefs))
	for _, ref := range version.CampaignRefs {
		if camp, err := s.St.GetCampaign(ref, version.TenantID); err == nil {
			activityCards = append(activityCards, map[string]any{
				"campaign_code": camp.ID, "state": camp.Status,
			})
		}
	}

	return map[string]any{
		"schema_version":      storefrontSchemaVersion,
		"store_code":          version.StoreCode,
		"storefront_revision": version.Revision,
		"store": map[string]any{
			"name":          rec.Name,
			"address":       rec.Address,
			"opening_hours": openingHours,
			"state":         "active",
		},
		"sections":       sections,
		"actions":        actions,
		"activity_cards": activityCards,
		"policy_links":   []any{},
	}
}
