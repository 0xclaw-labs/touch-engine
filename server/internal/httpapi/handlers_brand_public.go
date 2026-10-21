package httpapi

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/brandctx"
	"github.com/bianjiefilm/touch-engine/server/internal/brandhost"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// publicBrandDecision is how a published short code is shown. Legacy codes
// (no published brand) are not decided here and keep the T0 path.
type publicBrandDecision struct {
	Apply          bool
	State          string
	HTTPStatus     int
	IncludeContent bool
	Shell          *publicBrandShell
	MerchantName   string
	BlockWrites    bool
}

func (s *Server) decidePublicBrand(r *http.Request, res store.ResolvedLink) publicBrandDecision {
	if !s.Cfg.FeatureBrand || res.Link.Code == "" {
		return publicBrandDecision{}
	}
	pub, err := s.St.LinkPublication(res.Link.Code)
	if err != nil || strings.TrimSpace(pub.BrandID) == "" {
		return publicBrandDecision{}
	}
	host, herr := brandhost.FromRequest(r)
	fallback := publicBaseHost(s.Cfg.PublicBaseURL)
	if herr != nil {
		return publicBrandDecision{Apply: true, State: "domain_error", HTTPStatus: http.StatusForbidden, BlockWrites: true}
	}
	if s.Brand == nil {
		return publicBrandDecision{Apply: true, State: "brand_unavailable", HTTPStatus: http.StatusServiceUnavailable, BlockWrites: true}
	}
	read := s.Brand.Read
	current, _ := read(r.Context(), host, false)
	if current.Kind == brandctx.KindUnavailable || current.Kind == brandctx.KindInvalid {
		return publicBrandDecision{Apply: true, State: "brand_unavailable", HTTPStatus: http.StatusServiceUnavailable, BlockWrites: true}
	}
	if brandIDOf(current) != "" && brandIDOf(current) != pub.BrandID {
		return publicBrandDecision{Apply: true, State: "domain_mismatch", HTTPStatus: http.StatusNotFound, BlockWrites: true}
	}
	if current.Kind == brandctx.KindUnknown && host != pub.Host && host != fallback {
		return publicBrandDecision{Apply: true, State: "unknown_brand", HTTPStatus: http.StatusNotFound, BlockWrites: true}
	}
	if current.Kind == brandctx.KindDomain {
		return publicBrandDecision{Apply: true, State: "domain_error", HTTPStatus: http.StatusForbidden, BlockWrites: true}
	}
	shellRes := current
	if brandIDOf(shellRes) != pub.BrandID && pub.Host != "" {
		shellRes, _ = read(r.Context(), pub.Host, false)
	}
	if shellRes.Kind == brandctx.KindUnavailable || shellRes.Kind == brandctx.KindInvalid {
		return publicBrandDecision{Apply: true, State: "brand_unavailable", HTTPStatus: http.StatusServiceUnavailable, BlockWrites: true}
	}
	switch shellRes.Kind {
	case brandctx.KindSuspended:
		return publicBrandDecision{Apply: true, State: "brand_suspended", HTTPStatus: http.StatusForbidden, BlockWrites: true, Shell: shellFrom(shellRes.Manifest)}
	case brandctx.KindRetired:
		return publicBrandDecision{Apply: true, State: "brand_retiring", HTTPStatus: http.StatusForbidden, BlockWrites: true, Shell: shellFrom(shellRes.Manifest)}
	case brandctx.KindDomain:
		return publicBrandDecision{Apply: true, State: "domain_error", HTTPStatus: http.StatusForbidden, BlockWrites: true}
	case brandctx.KindUnknown:
		// The code's own brand host is unknown. A fallback request host is not
		// enough to invent a brand; only a registry-ready published host is.
		return publicBrandDecision{Apply: true, State: "unknown_brand", HTTPStatus: http.StatusNotFound, BlockWrites: true}
	}
	if shellRes.Kind == brandctx.KindReady && (!shellRes.Manifest.AdmitPublic || !shellRes.Manifest.TouchEnabled(s.Cfg.AppID)) {
		return publicBrandDecision{Apply: true, State: "brand_unavailable", HTTPStatus: http.StatusForbidden, BlockWrites: true, Shell: shellFrom(shellRes.Manifest)}
	}
	merchant := ""
	lifecycle := store.LifecycleActive
	if res.Link.TenantID != "" {
		if t, err := s.St.GetTenant(res.Link.TenantID); err == nil {
			merchant = t.Name
			lifecycle = t.Lifecycle
		}
	}
	state := string(res.Outcome)
	status := http.StatusOK
	include := true
	block := false
	if res.Outcome != store.OutcomeAvailable {
		status = http.StatusNotFound
		block = true
	} else {
		switch lifecycle {
		case store.LifecycleSuspended:
			state = "tenant_suspended"
			block = true
		case store.LifecycleSecurityFreeze:
			state = "security_freeze"
			block = true
		case store.LifecycleOffboarding:
			state = "offboarding"
			block = true
		case store.LifecycleRetired:
			state = "tenant_retired"
			block = true
		}
	}
	var shell *publicBrandShell
	if shellRes.Kind == brandctx.KindReady || shellRes.Manifest.DisplayName != "" {
		shell = shellFrom(shellRes.Manifest)
	}
	return publicBrandDecision{
		Apply: true, State: state, HTTPStatus: status, IncludeContent: include,
		Shell: shell, MerchantName: merchant, BlockWrites: block,
	}
}

func (s *Server) writeBrandedPublic(w http.ResponseWriter, r *http.Request, code string, res store.ResolvedLink) bool {
	d := s.decidePublicBrand(r, res)
	if !d.Apply {
		return false
	}
	view := publicLinkView{State: d.State, MerchantName: d.MerchantName, BrandShell: d.Shell}
	if d.State == "available" {
		view.Capabilities = s.publicCapabilities()
		view.Capabilities.Participation = view.Capabilities.Participation && strings.TrimSpace(res.Campaign.StoreID) != ""
	}
	if d.IncludeContent && res.Campaign.ID != "" && d.State != "brand_suspended" && d.State != "brand_retiring" && d.State != "domain_mismatch" && d.State != "unknown_brand" && d.State != "domain_error" && d.State != "brand_unavailable" {
		if res.Outcome == store.OutcomeAvailable || d.State == "tenant_suspended" || d.State == "security_freeze" || d.State == "offboarding" || d.State == "tenant_retired" {
			view.Title = res.Campaign.Title
			view.PublicContent = res.Campaign.PublicContent
			view.StartsAt = res.Campaign.StartsAt
			view.EndsAt = res.Campaign.EndsAt
			if res.Campaign.StoreID != "" {
				if st, err := s.St.GetStore(res.Campaign.StoreID, res.Link.TenantID); err == nil {
					view.StoreName = st.Name
				}
			}
			if res.StoreUnavailable && (d.State == "available" || strings.HasPrefix(d.State, "tenant_") || d.State == "offboarding" || d.State == "security_freeze") {
				view.StoreNotice = storeNoticeUnavailable
			}
		}
	}
	writeJSON(w, d.HTTPStatus, view)
	return true
}

func (s *Server) blockPublicWrite(w http.ResponseWriter, r *http.Request, res store.ResolvedLink) bool {
	d := s.decidePublicBrand(r, res)
	if !d.Apply || !d.BlockWrites {
		return false
	}
	writeJSON(w, d.HTTPStatus, map[string]any{"state": d.State, "message": "this activity is not accepting new participation"})
	return true
}

func brandIDOf(res brandctx.Result) string { return res.Manifest.BrandID }

func shellFrom(m brandctx.Manifest) *publicBrandShell {
	if m.DisplayName == "" && m.SupportName == "" && m.SupportContact == "" {
		return nil
	}
	return &publicBrandShell{DisplayName: m.DisplayName, SupportName: m.SupportName, SupportContact: m.SupportContact}
}

func publicBaseHost(base string) string {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Host == "" {
		return ""
	}
	h, err := brandhost.Canonical(u.Host)
	if err != nil {
		return ""
	}
	return h
}
