package httpapi

import (
	"context"
	"net/http"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/workbench"
)

// GET /api/v1/workbench — task-oriented read model for the merchant admin.
// It composes touch facts only. Contact fields stay out. Unknown external
// metrics are omitted rather than written as zero.
func (s *Server) handleWorkbench(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.allowScopedList(c, w) {
		return
	}
	facts, err := s.workbenchFacts(r.Context(), c)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "workbench failed")
		return
	}
	writeJSON(w, http.StatusOK, workbench.Assemble(facts))
}

func (s *Server) workbenchSeat(c *caller) workbench.Seat {
	seat := workbench.Seat{Role: c.Member.Role, Source: "membership"}
	if ten, err := s.St.GetTenant(c.Member.TenantID); err == nil {
		seat.MerchantName = ten.Name
	}
	if authz.Role(c.Member.Role) != authz.RoleAgent {
		return seat
	}
	rel, err := s.St.GetActiveAgencyRelation(c.Member.TenantID, c.Member.PrincipalRef)
	if err != nil {
		return seat
	}
	seat.Source = "agency"
	seat.RelationID = rel.ID
	return seat
}

func (s *Server) workbenchFacts(ctx context.Context, c *caller) (workbench.Facts, error) {
	var campaigns []store.Campaign
	var err error
	if callerIsStoreManager(c) {
		campaigns, err = s.St.ListCampaignsByStore(c.Member.TenantID, callerStoreScope(c))
	} else {
		campaigns, err = s.St.ListCampaigns(c.Member.TenantID)
	}
	if err != nil {
		return workbench.Facts{}, err
	}
	// HUI-4591 接线:订阅状态按租户事实(无投影行回退 Cfg,旧行为保留)。
	_, effectiveSub := s.billingSubscriptionFact(c.Member.TenantID)
	facts := workbench.Facts{
		Seat:                   s.workbenchSeat(c),
		SubscriptionStatus:     billingEntitlementStatus(effectiveSub),
		Benefits:               []workbench.BenefitFact{},
		LeadsCapture:           s.Cfg.FeatureLeadsCapture,
		LibraryEnabled:         s.Cfg.FeatureAssetLib,
		UploadEnabled:          s.Cfg.FeatureUpload,
		ReturnToCampaignProven: false,
		FollowUpKnown:          false,
		ContentTasksKnown:      false,
		Campaigns:              make([]workbench.CampaignFact, 0, len(campaigns)),
		Assets:                 []workbench.AssetFact{},
		Library:                []workbench.LibraryItem{},
	}
	for _, campaign := range campaigns {
		facts.Campaigns = append(facts.Campaigns, workbench.CampaignFact{
			ID: campaign.ID, Title: campaign.Title, Status: campaign.Status,
		})
		assets, err := s.St.ListCampaignAssets(c.Member.TenantID, campaign.ID)
		if err != nil {
			return workbench.Facts{}, err
		}
		for _, asset := range assets {
			facts.Assets = append(facts.Assets, workbench.AssetFact{
				CampaignID: asset.CampaignID, AssetID: asset.AssetID, Version: asset.Version, CreatedAt: asset.CreatedAt,
			})
		}
	}
	benefits, err := s.St.ListActivityBenefits(c.Member.TenantID)
	if err != nil {
		return workbench.Facts{}, err
	}
	allowed := map[string]bool{}
	for _, campaign := range facts.Campaigns {
		allowed[campaign.ID] = true
	}
	for _, fact := range benefits {
		if !allowed[fact.CampaignID] {
			continue
		}
		facts.Benefits = append(facts.Benefits, workbench.BenefitFact{
			ID: fact.ID, CampaignID: fact.CampaignID, Kind: fact.Kind, FaceMinor: fact.FaceMinor,
		})
	}
	if s.Cfg.FeatureAssetLib {
		items, err := s.St.ListLibAssets(c.Member.TenantID)
		if err != nil {
			return workbench.Facts{}, err
		}
		for _, item := range items {
			if callerIsStoreManager(c) && item.StoreID != "" && item.StoreID != callerStoreScope(c) {
				continue
			}
			facts.Library = append(facts.Library, workbench.LibraryItem{
				ID: item.ID, MediaType: string(item.MediaType), Purpose: item.Purpose, CreatedAt: item.CreatedAt,
			})
		}
	}
	if s.Cfg.FeatureLeadsCapture {
		counts, err := s.St.CountLeadStates(c.Member.TenantID)
		if err != nil {
			return workbench.Facts{}, err
		}
		facts.Leads = make([]workbench.LeadCount, 0, len(counts))
		for _, row := range counts {
			facts.Leads = append(facts.Leads, workbench.LeadCount{
				CampaignID: row.CampaignID, SyncState: row.SyncState, Count: row.Count,
			})
		}
		ids := make([]string, 0, len(facts.Campaigns))
		for _, campaign := range facts.Campaigns {
			ids = append(ids, campaign.ID)
		}
		if summaries, ok := s.followUpSummaries(ctx, c.Member.TenantID, ids); ok {
			facts.FollowUps = summaries
		}
	}
	return facts, nil
}
