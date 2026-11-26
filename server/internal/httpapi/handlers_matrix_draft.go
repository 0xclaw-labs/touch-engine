package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/assetlib"
	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/custpublish"
	"github.com/bianjiefilm/touch-engine/server/internal/matrixconsume"
	"github.com/bianjiefilm/touch-engine/server/internal/matrixhandoff"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

const matrixDisclosure = "这是商家活动草稿，不是已经发出的内容。"
const matrixStoreDisclosure = "这是门店商家账号受限草稿，不是已经发出的内容。"

type matrixDraftOptions struct {
	PackID                string `json:"pack_id"`
	VersionID             string `json:"version_id"`
	DeclaredContentType   string `json:"declared_content_type"`
	OfferText             string `json:"offer_text"`
	OfferExpiry           string `json:"offer_expiry"`
	Disclosure            string `json:"disclosure"`
	IdempotencyKey        string `json:"idempotency_key"`
	ActingAsPersonalPayer bool   `json:"acting_as_personal_payer"`
	PaymentScope          string `json:"payment_scope"`
}

func parseMatrixDraftOptions(r *http.Request) matrixDraftOptions {
	if r == nil || r.Body == nil {
		return matrixDraftOptions{}
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 32<<10))
	if err != nil || len(strings.TrimSpace(string(raw))) == 0 {
		return matrixDraftOptions{}
	}
	var opts matrixDraftOptions
	// Forged engineering fields (brand_id, tenant_id, sha256, payer_id,
	// account_pool, etc.) are intentionally ignored: authoritative IDs are
	// always resolved server-side.
	_ = json.Unmarshal(raw, &opts)
	return opts
}

// handleMatrixDraft records a merchant matrix draft for one campaign.
// A draft is not an outbound send. Lead and UGC stay independently enabled.
// Engineering ids are resolved on the server; forged body ids are ignored.
func (s *Server) handleMatrixDraft(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if c == nil || c.Member == nil {
		fail(w, http.StatusForbidden, "not_member", "principal is not a member of this tenant")
		return
	}
	if !c.Member.Enabled {
		fail(w, http.StatusForbidden, "member_disabled", "member disabled")
		return
	}
	opts := parseMatrixDraftOptions(r)
	camp, err := s.St.GetCampaign(r.PathValue("id"), c.Member.TenantID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "campaign not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "campaign lookup failed")
		return
	}
	surfaces, err := s.matrixSurfaces(camp)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "surface lookup failed")
		return
	}
	returnURL := "/work/campaigns/" + camp.ID
	if s.Matrix == nil {
		writeMatrixOutcome(w, http.StatusOK, disabledMatrix(surfaces, matrixconsume.ReasonPermissionSupply, returnURL))
		return
	}
	tenant, err := s.St.GetTenant(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "tenant lookup failed")
		return
	}
	if strings.TrimSpace(tenant.BrandID) == "" {
		writeMatrixOutcome(w, http.StatusOK, disabledMatrix(surfaces, "brand_unbound", returnURL))
		return
	}
	if strings.TrimSpace(camp.StoreID) == "" {
		writeMatrixOutcome(w, http.StatusOK, disabledMatrix(surfaces, "store_unbound", returnURL))
		return
	}
	now := time.Now().UTC()
	expiry, ok := matrixOfferExpiry(camp.EndsAt, now)
	if !ok {
		writeMatrixOutcome(w, http.StatusOK, disabledMatrix(surfaces, matrixhandoff.ReasonOfferExpired, returnURL))
		return
	}
	assets, err := s.St.ListCampaignAssets(camp.TenantID, camp.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "asset lookup failed")
		return
	}
	video, version, hasNonVideo, found, err := s.matrixVideo(camp.TenantID, assets)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "asset library lookup failed")
		return
	}
	if !found {
		reason := matrixhandoff.ReasonNeedsVideo
		if hasNonVideo && strings.EqualFold(strings.TrimSpace(opts.DeclaredContentType), "video") {
			reason = matrixhandoff.ReasonWrongContentType
		}
		writeMatrixOutcome(w, http.StatusOK, disabledMatrix(surfaces, reason, returnURL))
		return
	}
	offer := strings.TrimSpace(camp.PublicContent)
	if offer == "" {
		offer = strings.TrimSpace(camp.Title)
	}
	actor := s.matrixActorFor(c, tenant.BrandID, opts.PaymentScope, now)
	out, err := s.Matrix.Schedule(r.Context(), now, actor, matrixhandoff.Request{
		TenantID:              camp.TenantID,
		BrandID:               tenant.BrandID,
		StoreID:               camp.StoreID,
		ScopeKind:             matrixhandoff.ScopeCampaign,
		ActivityID:            camp.ID,
		ActivityVersion:       version,
		Assets:                []matrixhandoff.Asset{video},
		OfferText:             offer,
		OfferExpiry:           expiry,
		Disclosure:            matrixDisclosure,
		ReturnLocationToken:   "return:" + camp.ID,
		ReturnURL:             returnURL,
		IdempotencyKey:        strings.TrimSpace(opts.IdempotencyKey),
		ActingAsPersonalPayer: opts.ActingAsPersonalPayer,
	}, surfaces)
	out.ActivityVersion = version
	if out.ReturnURL == "" {
		out.ReturnURL = returnURL
	}
	writeMatrixScheduleResult(w, out, err)
}

// handleStoreMatrixDraft records a merchant matrix draft directly from a
// store's adopted/selected content pack version WITHOUT requiring any campaign.
// Customer storefront, QR/NFC touchpoints, and leads remain independent.
func (s *Server) handleStoreMatrixDraft(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if c == nil || c.Member == nil {
		fail(w, http.StatusForbidden, "not_member", "principal is not a member of this tenant")
		return
	}
	if !c.Member.Enabled {
		fail(w, http.StatusForbidden, "member_disabled", "member disabled")
		return
	}
	storeID := r.PathValue("id")
	sto, err := s.St.GetStore(storeID, c.Member.TenantID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "store not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "store lookup failed")
		return
	}
	if sto.Status != "active" {
		fail(w, http.StatusConflict, "store_disabled", "store is not active")
		return
	}
	opts := parseMatrixDraftOptions(r)
	surfaces, err := s.storeMatrixSurfaces(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "surface lookup failed")
		return
	}
	returnURL := "/work/stores/" + sto.ID + "/content"
	if pid := strings.TrimSpace(opts.PackID); pid != "" {
		returnURL += "?pack_id=" + pid
	}
	if s.Matrix == nil {
		writeMatrixOutcome(w, http.StatusOK, disabledMatrix(surfaces, matrixconsume.ReasonPermissionSupply, returnURL))
		return
	}
	tenant, err := s.St.GetTenant(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "tenant lookup failed")
		return
	}
	if strings.TrimSpace(tenant.BrandID) == "" {
		writeMatrixOutcome(w, http.StatusOK, disabledMatrix(surfaces, "brand_unbound", returnURL))
		return
	}
	now := time.Now().UTC()
	ver, pack, found, err := s.resolveStoreMatrixVersion(c.Member.TenantID, sto.ID, opts.PackID, opts.VersionID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "store content lookup failed")
		return
	}
	if !found {
		writeMatrixOutcome(w, http.StatusOK, disabledMatrix(surfaces, matrixhandoff.ReasonNeedsVideo, returnURL))
		return
	}
	if returnURL == "/work/stores/"+sto.ID+"/content" && pack.ID != "" {
		returnURL += "?pack_id=" + pack.ID
	}
	if ver.Status == contentpack.StatusBlocked {
		writeMatrixOutcome(w, http.StatusOK, disabledMatrix(surfaces, matrixhandoff.ReasonAssetRevoked, returnURL))
		return
	}
	var grantExpiry time.Time
	if strings.TrimSpace(ver.GrantUntil) != "" {
		t, ok := matrixOfferExpiry(ver.GrantUntil, now)
		if !ok {
			writeMatrixOutcome(w, http.StatusOK, disabledMatrix(surfaces, matrixhandoff.ReasonAssetExpired, returnURL))
			return
		}
		grantExpiry = t
	}
	expiryRaw := strings.TrimSpace(opts.OfferExpiry)
	if expiryRaw == "" {
		expiryRaw = strings.TrimSpace(ver.GrantUntil)
	}
	expiry, ok := matrixOfferExpiry(expiryRaw, now)
	if !ok {
		writeMatrixOutcome(w, http.StatusOK, disabledMatrix(surfaces, matrixhandoff.ReasonOfferExpired, returnURL))
		return
	}
	// Image is NEVER video; needs_video is NEVER success; never transcode/charge.
	if ver.ContentKind != contentpack.ContentKindVideo || ver.Video.IsEmpty() {
		reason := matrixhandoff.ReasonNeedsVideo
		if strings.EqualFold(strings.TrimSpace(opts.DeclaredContentType), "video") {
			reason = matrixhandoff.ReasonWrongContentType
		}
		writeMatrixOutcome(w, http.StatusOK, disabledMatrix(surfaces, reason, returnURL))
		return
	}
	grantRef := ""
	for _, g := range ver.GrantRefs {
		if strings.TrimSpace(g) != "" {
			grantRef = strings.TrimSpace(g)
			break
		}
	}
	if grantRef == "" {
		writeMatrixOutcome(w, http.StatusOK, disabledMatrix(surfaces, matrixhandoff.ReasonAssetRevoked, returnURL))
		return
	}
	offer := strings.TrimSpace(opts.OfferText)
	if offer == "" {
		offer = strings.TrimSpace(ver.PriceNote)
	}
	if offer == "" {
		offer = strings.TrimSpace(ver.Title)
	}
	if offer == "" {
		offer = strings.TrimSpace(pack.Title)
	}
	disclosure := strings.TrimSpace(opts.Disclosure)
	if disclosure == "" {
		disclosure = matrixStoreDisclosure
	}
	asset := matrixhandoff.Asset{
		ID:                  ver.Video.MediaID,
		Hash:                ver.Video.SHA256,
		Kind:                "video",
		GrantRef:            grantRef,
		ExpiresAt:           grantExpiry,
		DeclaredContentType: strings.TrimSpace(opts.DeclaredContentType),
	}
	actor := s.matrixActorFor(c, tenant.BrandID, opts.PaymentScope, now)
	out, err := s.Matrix.Schedule(r.Context(), now, actor, matrixhandoff.Request{
		TenantID:              c.Member.TenantID,
		BrandID:               tenant.BrandID,
		StoreID:               sto.ID,
		ScopeKind:             matrixhandoff.ScopeStore,
		ActivityID:            "",
		ActivityVersion:       maxInt(ver.VersionNo, 1),
		ContentPackID:         pack.ID,
		ContentVersionID:      ver.ID,
		Assets:                []matrixhandoff.Asset{asset},
		OfferText:             offer,
		OfferExpiry:           expiry,
		Disclosure:            disclosure,
		ReturnLocationToken:   "return:store:" + sto.ID,
		ReturnURL:             returnURL,
		IdempotencyKey:        strings.TrimSpace(opts.IdempotencyKey),
		ActingAsPersonalPayer: opts.ActingAsPersonalPayer,
	}, surfaces)
	out.ActivityVersion = maxInt(ver.VersionNo, 1)
	if out.ReturnURL == "" {
		out.ReturnURL = returnURL
	}
	writeMatrixScheduleResult(w, out, err)
}

// handleCampaignMatrixDraftList returns merchant matrix drafts scoped to one campaign.
func (s *Server) handleCampaignMatrixDraftList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if c == nil || c.Member == nil || !c.Member.Enabled {
		fail(w, http.StatusForbidden, "forbidden", "active tenant membership required")
		return
	}
	camp, err := s.St.GetCampaign(r.PathValue("id"), c.Member.TenantID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "campaign not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "campaign lookup failed")
		return
	}
	var drafts []matrixhandoff.Draft
	if s.Matrix != nil {
		drafts = s.Matrix.DraftsForScope(c.Member.TenantID, "", camp.ID)
	}
	writeMatrixDraftList(w, drafts)
}

// handleStoreMatrixDraftList returns merchant matrix drafts scoped to one store.
func (s *Server) handleStoreMatrixDraftList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if c == nil || c.Member == nil || !c.Member.Enabled {
		fail(w, http.StatusForbidden, "forbidden", "active tenant membership required")
		return
	}
	sto, err := s.St.GetStore(r.PathValue("id"), c.Member.TenantID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "store not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "store lookup failed")
		return
	}
	var drafts []matrixhandoff.Draft
	if s.Matrix != nil {
		drafts = s.Matrix.DraftsForScope(c.Member.TenantID, sto.ID, "")
	}
	writeMatrixDraftList(w, drafts)
}

// handleMatrixDraftConfirm approves a recorded merchant draft without
// overwriting previously approved plan content.
func (s *Server) handleMatrixDraftConfirm(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if c == nil || c.Member == nil || !c.Member.Enabled {
		fail(w, http.StatusForbidden, "forbidden", "active tenant membership required")
		return
	}
	if s.Matrix == nil {
		fail(w, http.StatusServiceUnavailable, matrixconsume.ReasonMatrixSupply, "matrix service is not configured")
		return
	}
	draftID := r.PathValue("draftId")
	if _, ok := s.Matrix.GetDraftForTenant(c.Member.TenantID, draftID); !ok {
		fail(w, http.StatusNotFound, "not_found", "matrix draft not found")
		return
	}
	tenant, err := s.St.GetTenant(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "tenant lookup failed")
		return
	}
	now := time.Now().UTC()
	res, err := s.Matrix.ConfirmDraft(now, s.matrixActorFor(c, tenant.BrandID, "", now), draftID)
	if err != nil {
		writeMatrixDraftActionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, matrixDraftView(res.Draft))
}

// handleMatrixDraftReceipt reconciles an external matrix status receipt for a
// recorded merchant draft while enforcing stale-receipt protection, layered
// states, null unknown metrics, and strict customer separation.
func (s *Server) handleMatrixDraftReceipt(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if c == nil || c.Member == nil || !c.Member.Enabled {
		fail(w, http.StatusForbidden, "forbidden", "active tenant membership required")
		return
	}
	if s.Matrix == nil {
		fail(w, http.StatusServiceUnavailable, matrixconsume.ReasonMatrixSupply, "matrix service is not configured")
		return
	}
	draftID := r.PathValue("draftId")
	if _, ok := s.Matrix.GetDraftForTenant(c.Member.TenantID, draftID); !ok {
		fail(w, http.StatusNotFound, "not_found", "matrix draft not found")
		return
	}
	var in struct {
		PlanID                 string `json:"plan_id"`
		TargetState            string `json:"target_state"`
		ActivityVersion        int    `json:"activity_version"`
		Sequence               int64  `json:"sequence"`
		OfficialVerified       bool   `json:"official_verified"`
		OfficialPublishGranted bool   `json:"official_publish_granted"`
		Reach                  *int64 `json:"reach"`
		Likes                  *int64 `json:"likes"`
		Leads                  *int64 `json:"leads"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid receipt payload")
		return
	}
	tenant, err := s.St.GetTenant(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "tenant lookup failed")
		return
	}
	// Official publish authority can NEVER be self-claimed from a caller body.
	// It stays fail-closed unless both a real matrix supply URL is configured
	// AND the request carries the server's trusted internal verification token.
	officialGranted := false
	receiptVerified := false
	if s.Cfg.MatrixDraftURL != "" && s.trustedMatrixReceiptHeader(r) && in.OfficialPublishGranted && in.OfficialVerified {
		officialGranted = true
		receiptVerified = true
	}
	now := time.Now().UTC()
	actor := s.matrixActorFor(c, tenant.BrandID, "", now)
	actor.OfficialPublishGranted = officialGranted
	outcome, err := s.Matrix.ApplyReceipt(now, actor, draftID, matrixhandoff.Receipt{
		PlanID:           strings.TrimSpace(in.PlanID),
		TargetState:      strings.TrimSpace(in.TargetState),
		ActivityVersion:  in.ActivityVersion,
		Sequence:         in.Sequence,
		OfficialVerified: receiptVerified,
		Reach:            in.Reach,
		Likes:            in.Likes,
		Leads:            in.Leads,
	})
	if err != nil {
		writeMatrixDraftActionError(w, err)
		return
	}
	resp := matrixDraftView(outcome.Draft)
	resp["applied"] = outcome.Applied
	resp["ignored"] = outcome.Ignored
	if outcome.Reason != "" {
		resp["reason"] = outcome.Reason
	}
	writeJSON(w, http.StatusOK, resp)
}

func writeMatrixScheduleResult(w http.ResponseWriter, out matrixconsume.Outcome, err error) {
	switch {
	case errors.Is(err, matrixconsume.ErrCrossTenant):
		writeMatrixOutcome(w, http.StatusForbidden, out)
	case errors.Is(err, matrixconsume.ErrNotDraft):
		writeMatrixOutcome(w, http.StatusConflict, out)
	case err != nil:
		writeMatrixOutcome(w, http.StatusUnprocessableEntity, out)
	default:
		writeMatrixOutcome(w, http.StatusOK, out)
	}
}

func writeMatrixDraftActionError(w http.ResponseWriter, err error) {
	reason := matrixhandoff.ReasonOf(err)
	switch reason {
	case matrixhandoff.ReasonNotFound:
		fail(w, http.StatusNotFound, reason, "matrix draft not found")
	case matrixhandoff.ReasonCrossTenant, matrixhandoff.ReasonCrossBrand,
		matrixhandoff.ReasonOperatorNotPublisher, matrixhandoff.ReasonDelegateRevoked,
		matrixhandoff.ReasonDelegatePaymentForbidden, matrixhandoff.ReasonCustomerPathForbidden,
		matrixhandoff.ReasonForbidden, matrixhandoff.ReasonNoOfficialAuthority:
		fail(w, http.StatusForbidden, reason, "action forbidden for merchant publish plan")
	case matrixhandoff.ReasonOfferExpired, matrixhandoff.ReasonNeedsVideo,
		matrixhandoff.ReasonPlanRefMissing, matrixhandoff.ReasonPlanIDMismatch,
		matrixhandoff.ReasonInvalid, matrixhandoff.ReasonNotApproved:
		fail(w, http.StatusUnprocessableEntity, reason, "matrix draft state rejected")
	default:
		fail(w, http.StatusInternalServerError, "internal", "matrix draft action failed")
	}
}

func writeMatrixDraftList(w http.ResponseWriter, drafts []matrixhandoff.Draft) {
	items := make([]map[string]any, 0, len(drafts))
	for _, d := range drafts {
		items = append(items, matrixDraftView(d))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":                      items,
		"customer_evidence_eligible": false,
		"customer_reward_eligible":   false,
	})
}

func matrixDraftView(d matrixhandoff.Draft) map[string]any {
	return map[string]any{
		"draft_id":                   d.ID,
		"draft_version":              d.Version,
		"scope_kind":                 d.ScopeKind,
		"store_id":                   d.Activity.StoreID,
		"activity_id":                d.Activity.ActivityID,
		"content_pack_id":            d.Activity.ContentPackID,
		"content_version_id":         d.Activity.ContentVersionID,
		"activity_version":           d.Activity.Version,
		"plan_id":                    d.Plan.ID,
		"plan_state":                 d.PlanState,
		"draft_status":               d.Status,
		"activity_status":            d.ActivityStatus,
		"return_url":                 d.ReturnURL,
		"return_location_token":      d.ReturnLocationToken,
		"approved":                   d.Approved,
		"superseded":                 d.Superseded,
		"official_authority_granted": d.OfficialAuthorityGranted,
		"metrics":                    d.Metrics,
		"copy":                       d.Copy,
		"outbound_complete":          false,
		"customer_publish":           false,
		"customer_evidence_eligible": false,
		"customer_reward_eligible":   false,
	}
}

func disabledMatrix(surfaces matrixconsume.Surfaces, reason, returnURL string) matrixconsume.Outcome {
	return matrixconsume.Outcome{
		MatrixButton:             matrixconsume.Button{Enabled: false, Reason: reason},
		LeadButton:               matrixconsume.Button{Enabled: surfaces.LeadEnabled},
		UGCButton:                matrixconsume.Button{Enabled: surfaces.UGCEnabled},
		ReturnURL:                returnURL,
		Metrics:                  matrixhandoff.PlanMetrics{},
		OfficialAuthorityGranted: false,
		CustomerEvidenceEligible: false,
		CustomerRewardEligible:   false,
	}
}

func writeMatrixOutcome(w http.ResponseWriter, status int, out matrixconsume.Outcome) {
	writeJSON(w, status, map[string]any{
		"matrix_button":              map[string]any{"enabled": out.MatrixButton.Enabled, "reason": out.MatrixButton.Reason},
		"lead_button":                map[string]any{"enabled": out.LeadButton.Enabled, "reason": out.LeadButton.Reason},
		"ugc_button":                 map[string]any{"enabled": out.UGCButton.Enabled, "reason": out.UGCButton.Reason},
		"outbound_complete":          false,
		"activity_version":           out.ActivityVersion,
		"copy":                       out.Copy,
		"draft_id":                   out.Draft.ID,
		"draft_status":               out.Draft.Status,
		"plan_id":                    out.PlanID,
		"plan_state":                 out.PlanState,
		"return_url":                 out.ReturnURL,
		"metrics":                    out.Metrics,
		"official_authority_granted": out.OfficialAuthorityGranted,
		"customer_evidence_eligible": false,
		"customer_reward_eligible":   false,
	})
}

func matrixOfferExpiry(endsAt string, now time.Time) (time.Time, bool) {
	endsAt = strings.TrimSpace(endsAt)
	if endsAt == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, endsAt)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, endsAt)
	}
	if err != nil || !t.After(now) {
		return time.Time{}, false
	}
	return t, true
}

func (s *Server) matrixSurfaces(camp store.Campaign) (matrixconsume.Surfaces, error) {
	lead := false
	if s.Cfg.FeatureLeadsCapture {
		form, err := s.St.GetLeadFormByCampaign(camp.ID)
		switch {
		case errors.Is(err, store.ErrNotFound):
		case err != nil:
			return matrixconsume.Surfaces{}, err
		case form.Enabled && form.TenantID == camp.TenantID:
			lead = true
		}
	}
	notes, err := s.St.ListPublishAdapterNotes(camp.TenantID)
	if err != nil {
		return matrixconsume.Surfaces{}, err
	}
	ugc := false
	for _, row := range custpublish.Matrix(notes) {
		if row.Capability(custpublish.CapPreview).Enabled {
			ugc = true
			break
		}
	}
	return matrixconsume.Surfaces{LeadEnabled: lead, UGCEnabled: ugc}, nil
}

func (s *Server) storeMatrixSurfaces(tenantID string) (matrixconsume.Surfaces, error) {
	notes, err := s.St.ListPublishAdapterNotes(tenantID)
	if err != nil {
		return matrixconsume.Surfaces{}, err
	}
	ugc := false
	for _, row := range custpublish.Matrix(notes) {
		if row.Capability(custpublish.CapPreview).Enabled {
			ugc = true
			break
		}
	}
	return matrixconsume.Surfaces{LeadEnabled: s.Cfg.FeatureLeadsCapture, UGCEnabled: ugc}, nil
}

func (s *Server) resolveStoreMatrixVersion(tenantID, storeID, packID, versionID string) (store.StoreContentPackVersion, store.StoreContentPack, bool, error) {
	packID = strings.TrimSpace(packID)
	versionID = strings.TrimSpace(versionID)
	if versionID != "" {
		v, err := s.St.GetStoreContentPackVersion(versionID, tenantID)
		if errors.Is(err, store.ErrNotFound) {
			return store.StoreContentPackVersion{}, store.StoreContentPack{}, false, nil
		}
		if err != nil {
			return store.StoreContentPackVersion{}, store.StoreContentPack{}, false, err
		}
		p, err := s.St.GetStoreContentPack(v.PackID, tenantID)
		if errors.Is(err, store.ErrNotFound) || (err == nil && (p.StoreID != storeID || (packID != "" && p.ID != packID))) {
			return store.StoreContentPackVersion{}, store.StoreContentPack{}, false, nil
		}
		if err != nil {
			return store.StoreContentPackVersion{}, store.StoreContentPack{}, false, err
		}
		return v, p, true, nil
	}
	if packID != "" {
		p, err := s.St.GetStoreContentPack(packID, tenantID)
		if errors.Is(err, store.ErrNotFound) || (err == nil && p.StoreID != storeID) {
			return store.StoreContentPackVersion{}, store.StoreContentPack{}, false, nil
		}
		if err != nil {
			return store.StoreContentPackVersion{}, store.StoreContentPack{}, false, err
		}
		v, err := s.St.GetStoreAdoptedPackVersion(tenantID, p.ID)
		if errors.Is(err, store.ErrNotFound) {
			return store.StoreContentPackVersion{}, p, false, nil
		}
		if err != nil {
			return store.StoreContentPackVersion{}, p, false, err
		}
		return v, p, true, nil
	}
	adopted, err := s.St.ListStoreAdoptedVersions(tenantID, storeID)
	if err != nil {
		return store.StoreContentPackVersion{}, store.StoreContentPack{}, false, err
	}
	if len(adopted) == 0 {
		return store.StoreContentPackVersion{}, store.StoreContentPack{}, false, nil
	}
	// Prefer a video version when multiple adopted versions exist.
	chosen := adopted[0]
	for _, candidate := range adopted {
		if candidate.ContentKind == contentpack.ContentKindVideo && !candidate.Video.IsEmpty() {
			chosen = candidate
			break
		}
	}
	p, err := s.St.GetStoreContentPack(chosen.PackID, tenantID)
	if errors.Is(err, store.ErrNotFound) {
		return store.StoreContentPackVersion{}, store.StoreContentPack{}, false, nil
	}
	if err != nil {
		return store.StoreContentPackVersion{}, store.StoreContentPack{}, false, err
	}
	return chosen, p, true, nil
}

func (s *Server) matrixVideo(tenantID string, assets []store.CampaignAsset) (matrixhandoff.Asset, int, bool, bool, error) {
	libs, err := s.St.ListLibAssets(tenantID)
	if err != nil {
		return matrixhandoff.Asset{}, 0, false, false, err
	}
	byRef := map[string]store.LibAsset{}
	byID := map[string]store.LibAsset{}
	nonVideoByRef := map[string]bool{}
	nonVideoByID := map[string]bool{}
	for _, lib := range libs {
		if lib.Candidate {
			continue
		}
		if lib.MediaType != assetlib.MediaTypeVideo {
			nonVideoByRef[lib.AssetRef] = true
			nonVideoByID[lib.ID] = true
			continue
		}
		if strings.TrimSpace(lib.GrantRef) == "" {
			continue
		}
		byRef[lib.AssetRef] = lib
		byID[lib.ID] = lib
	}
	var found matrixhandoff.Asset
	version := 0
	ok := false
	hasNonVideo := false
	for _, asset := range assets {
		if nonVideoByRef[asset.AssetID] || nonVideoByID[asset.AssetID] {
			hasNonVideo = true
		}
		lib, hit := byRef[asset.AssetID]
		if !hit {
			lib, hit = byID[asset.AssetID]
		}
		if !hit {
			continue
		}
		found = matrixhandoff.Asset{ID: lib.AssetRef, Hash: lib.SHA256, Kind: "video", GrantRef: lib.GrantRef}
		version = campaignAssetVersion(asset.Version)
		ok = true
	}
	return found, version, hasNonVideo, ok, nil
}

func campaignAssetVersion(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 {
		return 1
	}
	return n
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// matrixActorFor resolves a server-authoritative matrixconsume.Actor from the
// authenticated tenant membership. Agency members ("agent") are resolved against
// live agency_relations: revoked/inactive agencies fail closed as revoked
// delegates, while active agencies map to operator_admin (agency management
// authority is NEVER brand social-account publish authority).
func (s *Server) matrixActorFor(c *caller, brandID, paymentScope string, now time.Time) matrixconsume.Actor {
	role := strings.TrimSpace(c.Member.Role)
	revoked := false
	if authz.Role(role) == authz.RoleAgent {
		if s.St != nil && s.St.HasValidAgencyRelation(c.Member.TenantID, c.Member.PrincipalRef, now) {
			role = matrixhandoff.RoleOperatorAdmin
		} else {
			role = matrixhandoff.RoleDelegate
			revoked = true
		}
	}
	return matrixconsume.Actor{
		PrincipalID:  c.Principal.ID,
		TenantID:     c.Member.TenantID,
		BrandID:      brandID,
		Role:         role,
		Revoked:      revoked,
		PaymentScope: strings.TrimSpace(paymentScope),
	}
}

func (s *Server) trustedMatrixReceiptHeader(r *http.Request) bool {
	if r == nil || strings.TrimSpace(s.Cfg.InternalToken) == "" {
		return false
	}
	got := strings.TrimSpace(r.Header.Get("X-Touch-Internal-Token"))
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.Cfg.InternalToken)) == 1
}
