package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/assetlib"
	"github.com/bianjiefilm/touch-engine/server/internal/custpublish"
	"github.com/bianjiefilm/touch-engine/server/internal/matrixconsume"
	"github.com/bianjiefilm/touch-engine/server/internal/matrixhandoff"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

const matrixDisclosure = "这是商家活动草稿，不是已经发出的内容。"

// handleMatrixDraft records a merchant matrix draft for one campaign.
// A draft is not an outbound send. Lead and UGC stay independently enabled.
// The request body is ignored: engineering ids are resolved on the server.
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
	if s.Matrix == nil {
		writeMatrixOutcome(w, http.StatusOK, disabledMatrix(surfaces, matrixconsume.ReasonPermissionSupply))
		return
	}
	tenant, err := s.St.GetTenant(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "tenant lookup failed")
		return
	}
	if strings.TrimSpace(tenant.BrandID) == "" {
		writeMatrixOutcome(w, http.StatusOK, disabledMatrix(surfaces, "brand_unbound"))
		return
	}
	if strings.TrimSpace(camp.StoreID) == "" {
		writeMatrixOutcome(w, http.StatusOK, disabledMatrix(surfaces, "store_unbound"))
		return
	}
	expiry, ok := matrixOfferExpiry(camp.EndsAt, time.Now().UTC())
	if !ok {
		writeMatrixOutcome(w, http.StatusOK, disabledMatrix(surfaces, matrixhandoff.ReasonOfferExpired))
		return
	}
	assets, err := s.St.ListCampaignAssets(camp.TenantID, camp.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "asset lookup failed")
		return
	}
	video, version, found, err := s.matrixVideo(camp.TenantID, assets)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "asset library lookup failed")
		return
	}
	if !found {
		writeMatrixOutcome(w, http.StatusOK, disabledMatrix(surfaces, matrixhandoff.ReasonNeedsVideo))
		return
	}
	offer := strings.TrimSpace(camp.PublicContent)
	if offer == "" {
		offer = strings.TrimSpace(camp.Title)
	}
	out, err := s.Matrix.Schedule(r.Context(), time.Now().UTC(), matrixconsume.Actor{
		PrincipalID: c.Principal.ID,
		TenantID:    c.Member.TenantID,
		BrandID:     tenant.BrandID,
		Role:        c.Member.Role,
	}, matrixhandoff.Request{
		TenantID:            camp.TenantID,
		BrandID:             tenant.BrandID,
		StoreID:             camp.StoreID,
		ActivityID:          camp.ID,
		ActivityVersion:     version,
		Assets:              []matrixhandoff.Asset{video},
		OfferText:           offer,
		OfferExpiry:         expiry,
		Disclosure:          matrixDisclosure,
		ReturnLocationToken: "return:" + camp.ID,
	}, surfaces)
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

func disabledMatrix(surfaces matrixconsume.Surfaces, reason string) matrixconsume.Outcome {
	return matrixconsume.Outcome{
		MatrixButton: matrixconsume.Button{Enabled: false, Reason: reason},
		LeadButton:   matrixconsume.Button{Enabled: surfaces.LeadEnabled},
		UGCButton:    matrixconsume.Button{Enabled: surfaces.UGCEnabled},
	}
}

func writeMatrixOutcome(w http.ResponseWriter, status int, out matrixconsume.Outcome) {
	writeJSON(w, status, map[string]any{
		"matrix_button":     map[string]any{"enabled": out.MatrixButton.Enabled, "reason": out.MatrixButton.Reason},
		"lead_button":       map[string]any{"enabled": out.LeadButton.Enabled, "reason": out.LeadButton.Reason},
		"ugc_button":        map[string]any{"enabled": out.UGCButton.Enabled, "reason": out.UGCButton.Reason},
		"outbound_complete": false,
		"copy":              out.Copy,
		"draft_id":          out.Draft.ID,
		"draft_status":      out.Draft.Status,
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

func (s *Server) matrixVideo(tenantID string, assets []store.CampaignAsset) (matrixhandoff.Asset, int, bool, error) {
	libs, err := s.St.ListLibAssets(tenantID)
	if err != nil {
		return matrixhandoff.Asset{}, 0, false, err
	}
	byRef := map[string]store.LibAsset{}
	byID := map[string]store.LibAsset{}
	for _, lib := range libs {
		if lib.Candidate || lib.MediaType != assetlib.MediaTypeVideo || strings.TrimSpace(lib.GrantRef) == "" {
			continue
		}
		byRef[lib.AssetRef] = lib
		byID[lib.ID] = lib
	}
	var found matrixhandoff.Asset
	version := 0
	ok := false
	for _, asset := range assets {
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
	return found, version, ok, nil
}

func campaignAssetVersion(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 {
		return 1
	}
	return n
}
