package publishhandoff

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
)

// AdaptLegacy retains the original participation, attempt, snapshot and access
// credential. Only executable method parameters are added to the same row.
func (s *Service) AdaptLegacy(ctx context.Context, id, visitor string, rec channeladmission.ActionCapability, q channeladmission.ActionQuery) (ActionHandoff, error) {
	h, err := s.GetForVisitor(ctx, id, visitor)
	if err != nil {
		return ActionHandoff{}, err
	}
	if h.Scenario != rec.Scenario {
		return ActionHandoff{}, &HandoffRefusedError{Reason: "scenario_mismatch", ReasonText: "原交接的获准场景与当前方式不同，不能借用其他场景继续投稿。"}
	}
	text, media, err := s.SnapshotOf(ctx, h.ID, h.TenantID)
	if err != nil {
		return ActionHandoff{}, err
	}
	refs, err := s.MediaForVisitor(ctx, h.ID, visitor)
	if err != nil {
		return ActionHandoff{}, err
	}
	urls := map[string]string{}
	for _, ref := range refs {
		urls[ref.AssetID] = ref.URL
	}
	plan := JudgeActionPlan(q, rec, ActionPlanInput{Text: text, Media: media, MediaRefs: urls})
	if !plan.Allowed {
		return ActionHandoff{}, &HandoffRefusedError{Reason: plan.Reason, ReasonText: plan.ReasonText, PendingExternal: plan.PendingExternal}
	}
	raw, err := json.Marshal(plan.ClientAction)
	if err != nil {
		return ActionHandoff{}, err
	}
	_, err = s.db.Exec(`UPDATE publish_handoffs SET method=?,catalog_code=?,environment=?,plan_kind=?,evidence_level=?,client_action_json=?,unsupported_fields_json=? WHERE id=? AND tenant_id=? AND payload_kind='' AND method=''`, rec.Method, rec.CatalogCode, q.Environment, plan.Kind, plan.EvidenceLevel, string(raw), mustJSON(plan.UnsupportedFields), h.ID, h.TenantID)
	if err != nil {
		return ActionHandoff{}, err
	}
	return s.RefreshActionPrepare(ctx, id, visitor)
}
func (s *Service) FindLegacyAdapter(ctx context.Context, participationID, platform, visitor string) (ActionHandoff, error) {
	var id string
	err := s.db.QueryRow(`SELECT id FROM publish_handoffs WHERE participation_id=? AND platform=? AND visitor_id=? AND payload_kind='' ORDER BY created_at DESC LIMIT 1`, participationID, platform, visitor).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ActionHandoff{}, ErrNotFound
	}
	if err != nil {
		return ActionHandoff{}, err
	}
	original, err := s.GetForVisitor(ctx, id, visitor)
	if err != nil {
		return ActionHandoff{}, err
	}
	full, err := s.actionByID(original.TenantID, id)
	if err != nil {
		return full, err
	}
	if full.Method == "" {
		return full, nil
	}
	return s.RefreshActionPrepare(ctx, id, visitor)
}
