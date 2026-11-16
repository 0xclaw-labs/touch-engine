package httpapi

import (
	"errors"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// mintCampaignHandoffMedia is called only for a stored handoff snapshot. Exact
// participation+attempt provenance, campaign/store membership and every media
// hash/type are checked before issuing the existing real-media claim format.
func (s *Server) mintCampaignHandoffMedia(h publishhandoff.Handoff, items []publishhandoff.MediaItem) ([]publishhandoff.MediaRef, bool, error) {
	_, err := s.St.GetStoreContentPackVersion(h.ContentPackVersionID, h.TenantID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	if !s.Cfg.FeatureContentPacks {
		return nil, true, publishhandoff.ErrMediaPreparation
	}
	var campaignID, storeID, attemptVersion string
	err = s.St.DB.QueryRow(`SELECT p.campaign_id,p.store_id,a.content_pack_version
 FROM publish_handoffs h JOIN participations p ON p.id=h.participation_id AND p.tenant_id=h.tenant_id AND p.campaign_id=h.campaign_id AND p.store_id=h.store_id AND p.visitor_id=h.visitor_id
 JOIN participation_attempts a ON a.id=h.attempt_id AND a.participation_id=p.id AND a.tenant_id=p.tenant_id
 WHERE h.id=? AND h.tenant_id=? AND h.content_pack_version_id=? AND a.status='handed_off' AND a.platform=h.platform`, h.ID, h.TenantID, h.ContentPackVersionID).Scan(&campaignID, &storeID, &attemptVersion)
	if err != nil || attemptVersion != h.ContentPackVersionID {
		return nil, true, publishhandoff.ErrMediaPreparation
	}
	v, err := s.St.GetCampaignContentVersion(h.TenantID, campaignID, h.ContentPackVersionID)
	if err != nil || v.StoreMedia == nil {
		return nil, true, publishhandoff.ErrMediaPreparation
	}
	if err := contentpack.Distributable(v.Status, v.GrantUntil, time.Now()); err != nil {
		return nil, true, err
	}
	expected := storeHandoffMedia(*v.StoreMedia)
	if len(expected) != len(items) {
		return nil, true, publishhandoff.ErrMediaPreparation
	}
	out := make([]publishhandoff.MediaRef, 0, len(items))
	for i, m := range items {
		if m != expected[i] {
			return nil, true, publishhandoff.ErrMediaPreparation
		}
		row, err := s.St.GetContentMedia(m.AssetID, h.TenantID)
		if err != nil || row.StoreID != storeID || row.SHA256 != m.SHA256 || row.MimeType != m.ContentType {
			return nil, true, publishhandoff.ErrMediaPreparation
		}
		f, err := s.Media.Open(m.SHA256, controlledMediaExt(m.ContentType))
		if err != nil {
			return nil, true, publishhandoff.ErrMediaPreparation
		}
		f.Close()
		claim, err := contentpack.NewMediaAccessClaim(v.ID, m.AssetID, contentpack.MediaPurposePlatformFetch, "", time.Now(), 0)
		if err != nil {
			return nil, true, err
		}
		claim.SourceKind, claim.SourceRef = contentpack.MediaSourceCampaignHandoff, h.ID
		token, err := contentpack.MintMediaAccess(s.Cfg.InternalToken, claim)
		if err != nil {
			return nil, true, err
		}
		if err := s.St.RecordMediaAccessGrant(h.TenantID, v.ID, m.AssetID, claim.Purpose, "", claim.ExpiresAt); err != nil {
			return nil, true, err
		}
		out = append(out, publishhandoff.MediaRef{Kind: m.Kind, AssetID: m.AssetID, URL: strings.TrimRight(s.Cfg.PublicBaseURL, "/") + "/api/v1/public/content/media/" + token, SHA256: m.SHA256, ContentType: m.ContentType, Filename: m.Filename, ExpiresAt: claim.ExpiresAt.UTC().Format(time.RFC3339)})
	}
	return out, true, nil
}
