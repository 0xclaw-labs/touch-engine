package httpapi

import (
	"context"
	"errors"
	"github.com/bianjiefilm/touch-engine/server/internal/participation"
	"net/http"

	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// Public campaign choices use the last explicitly enabled content, not current
// candidate/adoption pointers. Unconfigured legacy campaigns keep their pool.
func (s *Server) campaignPublicAdoptions(tenant, campaign string) ([]store.ContentPackAdoption, error) {
	_, err := s.St.GetPublishConfig(tenant, campaign)
	if errors.Is(err, store.ErrNotFound) {
		return s.St.ListCampaignContentAdoptions(tenant, campaign)
	}
	if err != nil {
		return nil, err
	}
	v, cfg, err := s.St.CampaignPublishedContent(tenant, campaign)
	if err != nil {
		return nil, err
	}
	out := []store.ContentPackAdoption{}
	for _, ch := range cfg.Spec.Channels {
		out = append(out, store.ContentPackAdoption{TenantID: tenant, CampaignID: campaign, Platform: ch.Platform, VersionID: v.ID})
	}
	return out, nil
}

func (s *Server) campaignPublicVersion(tenant, campaign, platform, pack string) (store.CampaignContentVersion, error) {
	_, err := s.St.GetPublishConfig(tenant, campaign)
	if errors.Is(err, store.ErrNotFound) {
		return s.St.GetCampaignAdoptedContent(tenant, campaign, platform, pack)
	}
	if err != nil {
		return store.CampaignContentVersion{}, err
	}
	v, cfg, err := s.St.CampaignPublishedContent(tenant, campaign)
	if err != nil {
		return v, err
	}
	if pack != "" && v.PackID != pack {
		return store.CampaignContentVersion{}, store.ErrNotFound
	}
	for _, ch := range cfg.Spec.Channels {
		if ch.Platform == platform {
			return v, nil
		}
	}
	return store.CampaignContentVersion{}, store.ErrNotFound
}

// With content packs enabled, clients may choose only an offered immutable ID.
// Configured campaigns always derive content and rules from the enabled promise.
// Unconfigured deployments with content packs off retain opaque legacy refs;
// those refs do not authorize a real-media snapshot or signing operation.
func (s *Server) campaignParticipationSelection(w http.ResponseWriter, tenant, campaignID, selected string, rule int) (string, int, bool) {
	camp, err := s.St.GetCampaign(campaignID, tenant)
	if err != nil {
		fail(w, 404, "not_found", "resource not found")
		return "", 0, false
	}
	resolved, err := (&campaignBenefitReserver{srv: s}).ResolveStart(context.Background(), participation.StartInput{TenantID: tenant, StoreID: camp.StoreID, CampaignID: campaignID, RuleVersion: rule, ExpectedRewardVersion: rule, ContentPackVersion: selected, Window: participation.Window{StartsAt: camp.StartsAt, ExpiresAt: camp.EndsAt}})
	if err != nil {
		writeParticipationError(w, err)
		return "", 0, false
	}
	return resolved.ContentPackVersion, resolved.RuleVersion, true
}
