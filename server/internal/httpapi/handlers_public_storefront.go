package httpapi

// HUI-4584 G1: 公共门店面数据面读端点（契约消费方：1676/1896/2628/4589/4590）。
//
// 纪律：
//   - GET-only 白名单面，无会话概念；顾客自带 href 永不进入（payload 连
//     target_ref 都不下发——目标地址按方法受控解析，归 HUI-4589）。
//   - 只含已映射(catalog_code 非空)+启用+未撤销+未过期+目标公网 https 的实例
//     （store.ListPublicStoreActions 单点收口）。
//   - storefront_revision 本票恒 0：线上发布版本归 HUI-1676；sections 给默认
//     单 section（模板归 1676）。
//   - 禁止 tenant 密钥/内部 scope 泄漏：payload 无 tenant_id/target_ref/legacy。
//   - 登记制开关 FEATURE_STOREFRONT（默认 off）：路由不注册 + handler gate
//     统一 404，双保险同 FEATURE_TOUCHPOINT_ENTRY 写法。

import (
	"net/http"
	"time"
)

// storefrontSchemaVersion freezes the public payload shape for this ticket's
// data-plane contract.
const storefrontSchemaVersion = "storefront.v1"

func (s *Server) handlePublicStorefront(w http.ResponseWriter, r *http.Request) {
	if !s.Cfg.FeatureStorefront {
		fail(w, http.StatusNotFound, "not_found", "storefront is not available")
		return
	}
	pub, err := s.St.GetStorefrontPublicByCode(r.PathValue("storeCode"))
	if err != nil {
		fail(w, http.StatusNotFound, "not_found", "storefront is not available")
		return
	}
	if pub.TenantLifecycle != "active" {
		// 悬置/导出中的租户不在公共面开放（fail-closed，不区分原因）。
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": "not_found", "state": "unauthorized",
			"message": "storefront is not available",
		})
		return
	}

	now := time.Now().UTC()
	state := "active"
	if pub.StoreStatus != "active" {
		state = "disabled"
	}
	actions, err := s.St.ListPublicStoreActions(pub.StoreID, now)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "storefront lookup failed")
		return
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

	writeJSON(w, http.StatusOK, map[string]any{
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
	})
}
