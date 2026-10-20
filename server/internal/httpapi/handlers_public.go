package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/campaign"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// ---- public guest surface -----------------------------------------------------
//
// 游客(无商家账户)只读明确公开的活动字段。纪律:
//   - 无会话概念:本面永不读 cookie/session,永不写库;
//   - 白名单字段:标题/有效期/公开内容;租户 id、principal、内部 asset id、
//     订单引用、门店内部字段一律不出现;
//   - 非有效态统一 HTTP 404(状态码不区分,防止探测短码存在性),仅文案区分;
//   - 其他方法 405;本面到生成/付费调用之间不存在任何代码路径。

// publicLinkView is the whitelist payload for the public activity page.
type publicLinkView struct {
	State         string `json:"state"` // machine-readable: available|link_disabled|not_started|expired|paused|draft|ended|not_found
	Title         string `json:"title,omitempty"`
	PublicContent string `json:"public_content,omitempty"`
	StartsAt      string `json:"starts_at,omitempty"`
	EndsAt        string `json:"ends_at,omitempty"`
	// StoreNotice (HUI-1674): 白名单常量 "store_unavailable" — 活动绑定的门店
	// 已停用(公共页标注「门店暂不可用」)。不含任何门店内部字段;仅 available
	// 态出现,非可用态保持单字段白名单形状。
	StoreNotice string `json:"store_notice,omitempty"`
	// MerchantName is the tenant display name (the store the guest is visiting).
	// Brand shell is separate and must not replace it.
	MerchantName string            `json:"merchant_name,omitempty"`
	StoreName    string            `json:"store_name,omitempty"`
	BrandShell   *publicBrandShell `json:"brand_shell,omitempty"`
	// Capabilities are configuration facts the guest page may rely on. They are
	// booleans only: no endpoint, tenant, or internal flag value is disclosed.
	Capabilities *publicCapabilities `json:"capabilities,omitempty"`
}

// publicCapabilities mirrors the registration gates. A false value means the
// action is not available on this deployment; the page must not render a dead
// primary action for it.
type publicCapabilities struct {
	Participation bool `json:"participation"`
	ContentPacks  bool `json:"content_packs"`
	RewardOps     bool `json:"reward_ops"`
	LeadsCapture  bool `json:"leads_capture"`
}

func (s *Server) publicCapabilities() *publicCapabilities {
	return &publicCapabilities{
		Participation: s.Cfg.FeatureParticipation,
		ContentPacks:  s.Cfg.FeatureContentPacks,
		RewardOps:     s.Cfg.FeatureRewardOps,
		LeadsCapture:  s.Cfg.FeatureLeadsCapture,
	}
}

type publicBrandShell struct {
	DisplayName    string `json:"display_name,omitempty"`
	SupportName    string `json:"support_name,omitempty"`
	SupportContact string `json:"support_contact,omitempty"`
}

// storeNoticeUnavailable is the only value store_notice may carry.
const storeNoticeUnavailable = "store_unavailable"

// attachPublicNames adds the store the guest is visiting. Names only:
// tenant id, store id, address, and agency fields stay off this payload.
func (s *Server) attachPublicNames(view *publicLinkView, res store.ResolvedLink) {
	tenantID := res.Link.TenantID
	if tenantID == "" {
		tenantID = res.Campaign.TenantID
	}
	if tenantID != "" {
		if t, err := s.St.GetTenant(tenantID); err == nil {
			view.MerchantName = t.Name
		}
	}
	if res.Campaign.StoreID != "" && tenantID != "" {
		if st, err := s.St.GetStore(res.Campaign.StoreID, tenantID); err == nil {
			view.StoreName = st.Name
		}
	}
}

func (s *Server) handlePublicLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "public activity pages are read-only")
		return
	}
	code := strings.TrimSpace(r.PathValue("code"))

	// HUI-2981 option C (FEATURE_PUBLIC_CACHE, default off): pure display
	// reads may come from the bounded local cache. The five-state outcome is
	// RE-DERIVED from the cached rows at time.Now() on every hit (windows are
	// never frozen into an entry), and the epoch check makes any admin edit
	// visible to the very next request. Names attach from the cached rows so
	// a hit costs zero SQL. Everything else (flag off, cache machinery
	// failure) resolves exactly as before.
	var res store.ResolvedLink
	var rows *store.ResolvedRows
	if s.Cfg.FeaturePublicCache && s.PubCache != nil && !s.PubCache.Disabled() && campaign.ValidShortcode(code) {
		// malformed codes never enter the cache (probe floods cannot evict
		// real entries with junk); they resolve by shape, exactly as before.
		if loaded, _, err := s.PubCache.GetOrLoad(code, func() (store.ResolvedRows, error) {
			return s.St.LoadResolvedRows(code), nil
		}); err == nil {
			rows = &loaded
			res = store.DecideLink(loaded, time.Now())
		}
	}
	if rows == nil {
		res = s.St.ResolveLink(code, time.Now())
	}

	if s.Cfg.FeatureBrand {
		if done := s.writeBrandedPublic(w, r, code, res); done {
			return
		}
	}
	view := publicLinkView{State: string(res.Outcome)}
	if res.Outcome == store.OutcomeAvailable {
		view.Capabilities = s.publicCapabilities()
		// A store-less campaign cannot create a participation row; do not offer
		// a primary action that would end in a server error.
		view.Capabilities.Participation = view.Capabilities.Participation && strings.TrimSpace(res.Campaign.StoreID) != ""
		view.Title = res.Campaign.Title
		view.PublicContent = res.Campaign.PublicContent
		view.StartsAt = res.Campaign.StartsAt
		view.EndsAt = res.Campaign.EndsAt
		if rows != nil {
			attachNamesFromRows(&view, *rows)
		} else {
			s.attachPublicNames(&view, res)
		}
		if res.StoreUnavailable {
			view.StoreNotice = storeNoticeUnavailable
		}
		writeJSON(w, http.StatusOK, view)
		return
	}
	// every non-available state: uniform 404 + state-specific text
	writeJSON(w, http.StatusNotFound, view)
}

// attachNamesFromRows is the zero-SQL twin of attachPublicNames: same fields,
// same swallow-on-missing semantics, fed from the cached rows.
func attachNamesFromRows(view *publicLinkView, rows store.ResolvedRows) {
	if rows.HasTenant {
		view.MerchantName = rows.Tenant.Name
		if rows.Campaign.StoreID != "" && rows.HasStore {
			view.StoreName = rows.Store.Name
		}
	}
}
