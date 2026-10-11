package httpapi

import (
	"encoding/json"

	"github.com/bianjiefilm/touch-engine/server/internal/leads"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// campaignLeadTrace is the AG06-readable source on one submission.
// Asset and version come from the campaign's own asset rows, never from the visitor body.
func (s *Server) campaignLeadTrace(campaign store.Campaign, linkCode, submissionRef string) leads.SourceTrace {
	trace := leads.SourceTrace{
		TraceID:      "lead:" + submissionRef,
		ReturnTarget: "/c/" + linkCode,
	}
	if campaign.OrderRef != "" {
		trace.GrantRef = campaign.OrderRef
	}
	assets, err := s.St.ListCampaignAssets(campaign.TenantID, campaign.ID)
	if err != nil || len(assets) == 0 {
		return trace
	}
	last := assets[len(assets)-1]
	trace.CampaignVersion = last.Version
	trace.AssetRef = last.AssetID
	return trace
}

// traceFromSubmitFact copies the trace already stored on this submission's
// submit fact. It does not recompute the short code, asset, or grant.
func traceFromSubmitFact(payload string) (leads.SourceTrace, error) {
	var env struct {
		Payload struct {
			TraceID         string `json:"trace_id"`
			GrantRef        string `json:"grant_ref"`
			ReturnTarget    string `json:"return_target"`
			CampaignVersion string `json:"campaign_version"`
			AssetRef        string `json:"asset_ref"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		return leads.SourceTrace{}, err
	}
	return leads.SourceTrace{
		TraceID:         env.Payload.TraceID,
		GrantRef:        env.Payload.GrantRef,
		ReturnTarget:    env.Payload.ReturnTarget,
		CampaignVersion: env.Payload.CampaignVersion,
		AssetRef:        env.Payload.AssetRef,
	}, nil
}
