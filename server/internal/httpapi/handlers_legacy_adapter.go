package httpapi

import (
	"context"
	"fmt"
	"github.com/bianjiefilm/touch-engine/server/internal/actioncatalog"
	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"strings"
	"time"
)

// resolveLegacyAdapter uses the original method records, not an alias
// permission inferred from the old platform-wide capability matrix.
func (s *Server) resolveLegacyAdapter(tenant, platform, kind, environment, scenario string) (channeladmission.ActionCapability, publishhandoff.ActionPlan) {
	rows, _ := s.St.ListActionCapabilities(tenant)
	var fallback channeladmission.ActionCapability
	for _, rec := range rows {
		if rec.Platform != platform || rec.Scenario != scenario || rec.ActionKind != "publish" {
			continue
		}
		// Media kinds are catalogue facts. Never select a link route for a media task.
		media := false
		for _, input := range rec.RequiredInputs {
			if input == "content_pack_version" {
				media = true
			}
		}
		if !media {
			continue
		}
		entry, known := actioncatalog.Lookup(rec.CatalogCode)
		if !known || entry.Variant != kind {
			continue
		}
		plan := publishhandoff.JudgeActionPlan(s.actionGateQuery(rec, environment), rec, publishhandoff.ActionPlanInput{})
		if plan.Allowed {
			return rec, plan
		}
		if fallback.ID == "" {
			fallback = rec
		}
	}
	if fallback.ID == "" {
		fallback = unknownActionCapability("", platform, "", scenario)
	}
	return fallback, publishhandoff.JudgeActionPlan(s.actionGateQuery(fallback, environment), fallback, publishhandoff.ActionPlanInput{})
}
func (s *Server) refreshLegacyAdapterRequest(ctx context.Context, h publishhandoff.ActionHandoff, req publishhandoff.PrepareRequest) (publishhandoff.PrepareRequest, error) {
	p, err := s.Participation.GetForVisitor(ctx, h.ParticipationID, mustLegacyVisitor(s, h.ID))
	if err != nil {
		return req, publishhandoff.ErrNotFound
	}
	resolved := s.St.ResolveLink(p.LinkCode, time.Now())
	if resolved.Outcome != store.OutcomeAvailable || resolved.Campaign.ID != p.CampaignID {
		return req, &publishhandoff.HandoffRefusedError{Reason: "campaign_unavailable", ReasonText: "活动当前不可办理新的发布交接，原记录仍保留。"}
	}
	if s.legacyPublishScenario(h.TenantID, p.CampaignID) != h.Scenario {
		return req, &publishhandoff.HandoffRefusedError{Reason: "scenario_mismatch", ReasonText: "当前活动场景已改变，原记录保留；请先向门店核对。"}
	}
	v, err := s.St.GetCampaignContentVersion(h.TenantID, p.CampaignID, h.ContentPackVersionID)
	if err != nil {
		return req, publishhandoff.ErrNotFound
	}
	if err := contentpack.Distributable(v.Status, v.GrantUntil, time.Now()); err != nil {
		return req, publishhandoff.ErrMediaPreparation
	}
	rec := s.resolveActionCapability(h.TenantID, h.CatalogCode, h.Platform, h.Method, h.Scenario)
	plan := publishhandoff.JudgeActionPlan(s.actionGateQuery(rec, h.Environment), rec, publishhandoff.ActionPlanInput{Text: req.Text, Media: req.Media})
	if !plan.Allowed {
		return req, &publishhandoff.HandoffRefusedError{Reason: plan.Reason, ReasonText: plan.ReasonText, PendingExternal: plan.PendingExternal}
	}
	req.FieldSupport = rec.FieldSupport
	req.Plan = plan
	refs, err := s.PublishHandoff.MediaForVisitor(ctx, h.ID, mustLegacyVisitor(s, h.ID))
	if err != nil {
		return req, err
	}
	req.MediaRefs = map[string]string{}
	for _, ref := range refs {
		req.MediaRefs[ref.AssetID] = ref.URL
	}
	if len(req.MediaRefs) != len(req.Media) {
		return req, fmt.Errorf("%w: legacy media missing", publishhandoff.ErrMediaPreparation)
	}
	return req, nil
}

// The handoff service has already verified the caller. This is server-owned
// provenance only; no visitor input is accepted here or exposed in responses.
func mustLegacyVisitor(s *Server, id string) string {
	var visitor string
	_ = s.St.DB.QueryRow(`SELECT visitor_id FROM publish_handoffs WHERE id=?`, id).Scan(&visitor)
	return strings.TrimSpace(visitor)
}

// A reward campaign never borrows the store-promotion method's permission.
func (s *Server) legacyPublishScenario(tenant, campaign string) string {
	if _, err := s.St.GetPublishConfig(tenant, campaign); err == nil {
		return channeladmission.ActionScenarioRewardTask
	}
	return channeladmission.ActionScenarioStorePromo
}
