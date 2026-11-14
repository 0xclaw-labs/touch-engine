package store

import (
	"errors"
	"sort"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
)

// CampaignContentVersion is a read-only union. Legacy assets and real store
// media remain distinct; this adapter neither copies rows nor invents assets.
// Embedded metadata gives existing consumers a stable version contract.
type CampaignContentVersion struct {
	ContentPackVersion
	StoreMedia *StoreContentPackVersion
}

// A recorded revocation remains authoritative even for historical rows whose
// workflow status was incorrectly retired by the former adoption implementation.
func campaignLegacyVersion(v ContentPackVersion) CampaignContentVersion {
	if strings.TrimSpace(v.BlockedReason) != "" {
		v.Status = contentpack.StatusBlocked
	}
	return CampaignContentVersion{ContentPackVersion: v}
}

func campaignStoreVersion(v StoreContentPackVersion) CampaignContentVersion {
	if strings.TrimSpace(v.BlockedReason) != "" {
		v.Status = contentpack.StatusBlocked
	}
	return CampaignContentVersion{ContentPackVersion: ContentPackVersion{
		ID: v.ID, TenantID: v.TenantID, PackID: v.PackID, VersionNo: v.VersionNo, ContentKind: v.ContentKind, Status: v.Status, Source: v.Source,
		Title: v.Title, Body: v.Body, Topics: v.Topics, StoreName: v.StoreName, PriceNote: v.PriceNote, ValidNote: v.ValidNote,
		PlatformVersions: v.PlatformVersions, GrantRefs: v.GrantRefs, GrantUntil: v.GrantUntil, Marking: v.Marking,
		BlockedReason: v.BlockedReason, IdempotencyKey: v.IdempotencyKey, CreatedBy: v.CreatedBy, CreatedAt: v.CreatedAt,
	}, StoreMedia: &v}
}

// GetCampaignContentPack only accepts explicit campaign membership and the
// campaign's current store scope. General store content is never auto-included.
func (s *Store) GetCampaignContentPack(tenantID, campaignID, packID string) (ContentPack, error) {
	camp, err := s.GetCampaign(campaignID, tenantID)
	if err != nil {
		return ContentPack{}, err
	}
	p, err := s.GetContentPack(packID, tenantID)
	if err == nil {
		if p.CampaignID != campaignID || (p.StoreID != "" && p.StoreID != camp.StoreID) {
			return ContentPack{}, ErrNotFound
		}
		return p, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return ContentPack{}, err
	}
	sp, err := s.GetStoreContentPack(packID, tenantID)
	if err != nil {
		return ContentPack{}, err
	}
	if sp.CampaignID != campaignID || sp.StoreID != camp.StoreID || camp.StoreID == "" {
		return ContentPack{}, ErrNotFound
	}
	return ContentPack{ID: sp.ID, TenantID: sp.TenantID, CampaignID: sp.CampaignID, StoreID: sp.StoreID, Title: sp.Title, CreatedBy: sp.CreatedBy, CreatedAt: sp.CreatedAt, UpdatedAt: sp.UpdatedAt}, nil
}

func (s *Store) ListCampaignContentPacks(tenantID, campaignID string) ([]ContentPack, error) {
	camp, err := s.GetCampaign(campaignID, tenantID)
	if err != nil {
		return nil, err
	}
	old, err := s.ListContentPacks(tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	out := make([]ContentPack, 0, len(old))
	for _, p := range old {
		if p.StoreID == "" || p.StoreID == camp.StoreID {
			out = append(out, p)
		}
	}
	if camp.StoreID != "" {
		packs, err := s.ListStoreContentPacks(tenantID, camp.StoreID)
		if err != nil {
			return nil, err
		}
		for _, p := range packs {
			if !p.Legacy && p.CampaignID == campaignID {
				out = append(out, ContentPack{ID: p.ID, TenantID: p.TenantID, CampaignID: p.CampaignID, StoreID: p.StoreID, Title: p.Title, CreatedBy: p.CreatedBy, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt == out[j].CreatedAt {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt < out[j].CreatedAt
	})
	return out, nil
}

func (s *Store) GetCampaignContentVersion(tenantID, campaignID, versionID string) (CampaignContentVersion, error) {
	old, err := s.GetContentPackVersion(versionID, tenantID)
	if err == nil {
		if _, err = s.GetCampaignContentPack(tenantID, campaignID, old.PackID); err != nil {
			return CampaignContentVersion{}, err
		}
		return campaignLegacyVersion(old), nil
	}
	if !errors.Is(err, ErrNotFound) {
		return CampaignContentVersion{}, err
	}
	v, err := s.GetStoreContentPackVersion(versionID, tenantID)
	if err != nil {
		return CampaignContentVersion{}, err
	}
	if _, err = s.GetCampaignContentPack(tenantID, campaignID, v.PackID); err != nil {
		return CampaignContentVersion{}, err
	}
	return campaignStoreVersion(v), nil
}

func (s *Store) ListCampaignContentVersions(tenantID, campaignID, packID string) ([]CampaignContentVersion, error) {
	if _, err := s.GetCampaignContentPack(tenantID, campaignID, packID); err != nil {
		return nil, err
	}
	out := []CampaignContentVersion{}
	if _, err := s.GetStoreContentPack(packID, tenantID); err == nil {
		rows, err := s.ListStoreContentPackVersions(tenantID, packID)
		if err != nil {
			return nil, err
		}
		for _, v := range rows {
			out = append(out, campaignStoreVersion(v))
		}
		return out, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	rows, err := s.ListContentPackVersions(tenantID, packID)
	if err != nil {
		return nil, err
	}
	for _, v := range rows {
		out = append(out, campaignLegacyVersion(v))
	}
	return out, nil
}

// Every platform/pack pointer is retained. Multiple packs on the same platform
// are valid choices and must never be collapsed to the newest store pointer.
func (s *Store) ListCampaignContentAdoptions(tenantID, campaignID string) ([]ContentPackAdoption, error) {
	camp, err := s.GetCampaign(campaignID, tenantID)
	if err != nil {
		return nil, err
	}
	old, err := s.ListAdoptions(tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	out := []ContentPackAdoption{}
	for _, a := range old {
		if _, err := s.GetCampaignContentVersion(tenantID, campaignID, a.VersionID); err == nil {
			out = append(out, a)
		} else if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	if camp.StoreID != "" {
		rows, err := s.ListStoreAdoptions(tenantID, camp.StoreID)
		if err != nil {
			return nil, err
		}
		for _, a := range rows {
			if a.CampaignID != campaignID {
				continue
			}
			v, err := s.GetCampaignContentVersion(tenantID, campaignID, a.VersionID)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if v.PackID != a.PackID {
				continue
			}
			out = append(out, ContentPackAdoption{ID: a.ID, TenantID: a.TenantID, CampaignID: a.CampaignID, Platform: a.Platform, VersionID: a.VersionID, AdoptedBy: a.AdoptedBy, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt})
		}
	}
	return out, nil
}

func (s *Store) GetCampaignAdoptedContent(tenantID, campaignID, platform, packID string) (CampaignContentVersion, error) {
	rows, err := s.ListCampaignContentAdoptions(tenantID, campaignID)
	if err != nil {
		return CampaignContentVersion{}, err
	}
	var found *CampaignContentVersion
	for _, a := range rows {
		if a.Platform != platform {
			continue
		}
		v, err := s.GetCampaignContentVersion(tenantID, campaignID, a.VersionID)
		if err != nil {
			return CampaignContentVersion{}, err
		}
		if packID != "" && v.PackID != packID {
			continue
		}
		if found != nil {
			return CampaignContentVersion{}, ErrNotFound
		} // caller must choose
		found = &v
	}
	if found == nil {
		return CampaignContentVersion{}, ErrNotFound
	}
	return *found, nil
}

// CampaignPublishedContent resolves the enabled configuration, independent of
// today's adoption pointers. Legacy campaigns without config keep their reads.
func (s *Store) CampaignPublishedContent(tenantID, campaignID string) (CampaignContentVersion, PublishConfig, error) {
	cfg, err := s.GetPublishedConfig(tenantID, campaignID)
	if err != nil {
		return CampaignContentVersion{}, cfg, err
	}
	if cfg.Status != "enabled" || cfg.EnabledVersion == 0 {
		return CampaignContentVersion{}, cfg, contentpack.NewValidationError("not_enabled", "content_version_id")
	}
	v, err := s.GetCampaignContentVersion(tenantID, campaignID, cfg.Spec.ContentVerID)
	return v, cfg, err
}
