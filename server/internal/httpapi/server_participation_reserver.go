package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/campaign"
	"github.com/bianjiefilm/touch-engine/server/internal/campaignenable"
	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/participation"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// campaignBenefitReserver is the trusted G2→G3 adapter. Its only allocation
// write is the original reward ledger transaction; no HTTP self-call or second
// stock authority is introduced.
type campaignBenefitReserver struct {
	srv   *Server
	clock func() time.Time
}

func (a *campaignBenefitReserver) now() time.Time {
	if a.clock != nil {
		return a.clock().UTC()
	}
	return a.srv.rewardTime()
}

var _ participation.BenefitReserver = (*campaignBenefitReserver)(nil)
var _ participation.StartResolver = (*campaignBenefitReserver)(nil)
var _ participation.ReservationReader = (*campaignBenefitReserver)(nil)

func startRefused(status int, reason, field, message string) error {
	return &participation.StartRefusedError{Status: status, Reason: reason, Field: field, Message: message}
}

// ResolveStart freezes the published reward rule, independently of an action
// session's campaign_rules anti-abuse version. A chosen work is never swapped.
func (a *campaignBenefitReserver) ResolveStart(ctx context.Context, in participation.StartInput) (participation.StartInput, error) {
	s := a.srv
	if err := ctx.Err(); err != nil {
		return in, err
	}
	camp, err := s.St.GetCampaign(in.CampaignID, in.TenantID)
	if err != nil {
		return in, participation.ErrNotFound
	}
	if camp.StoreID != "" && camp.StoreID != in.StoreID {
		return in, participation.ErrTargetMismatch
	}
	_, err = s.St.GetPublishConfig(in.TenantID, in.CampaignID)
	if errors.Is(err, store.ErrNotFound) {
		if in.RuleVersion == 0 {
			in.RuleVersion = 1
		}
		if in.ContentPackVersion != "" && s.Cfg.FeatureContentPacks {
			rows, err := s.St.ListCampaignContentAdoptions(in.TenantID, in.CampaignID)
			if err != nil {
				return in, err
			}
			allowed := false
			for _, row := range rows {
				if row.VersionID == in.ContentPackVersion {
					allowed = true
				}
			}
			if !allowed {
				return in, participation.ErrNotFound
			}
			v, err := s.St.GetCampaignContentVersion(in.TenantID, in.CampaignID, in.ContentPackVersion)
			if err != nil {
				return in, participation.ErrNotFound
			}
			if err := contentpack.Distributable(v.Status, v.GrantUntil, a.now()); err != nil {
				reason := contentpack.ReasonNotDistributable
				var ve *contentpack.ValidationError
				if errors.As(err, &ve) {
					reason = ve.Reason
				}
				return in, startRefused(409, reason, "content_version_id", "所选内容暂不可分发")
			}
		}
		return in, nil
	}
	if err != nil {
		return in, err
	}
	cfg, err := s.St.GetPublishedConfig(in.TenantID, in.CampaignID)
	if err != nil || cfg.Status != "enabled" {
		return in, startRefused(409, "not_enabled", "publish_config", "活动承诺尚未启用或已暂停，暂不能开始新的参与")
	}
	if camp.Status != "active" {
		return in, startRefused(409, "campaign_not_active", "campaign", "活动当前不接受新的参与")
	}
	if in.ExpectedRewardVersion != 0 && in.ExpectedRewardVersion != cfg.Version {
		return in, startRefused(409, "version_mismatch", "rule_version", "生效承诺已更新，请重新查看本次内容与规则")
	}
	if in.ContentPackVersion != "" && in.ContentPackVersion != cfg.Spec.ContentVerID {
		return in, startRefused(409, "version_mismatch", "content_version_id", "本次已选择的作品不属于当前生效承诺，未替换你的原选择")
	}
	facts, err := s.assemblePublishedFacts(in.TenantID, camp, cfg, a.now())
	if err != nil {
		return in, err
	}
	if d := campaignenable.Check(cfg.Spec, facts); !d.Allowed {
		return in, startRefused(422, d.Reason, d.Field, "当前生效承诺暂不可开始新的参与，请核对名额与活动条件")
	}
	if reason := (campaign.Window{StartsAt: cfg.Spec.Reward.ClaimFrom, EndsAt: cfg.Spec.Reward.ClaimUntil}).ResolveTime(a.now()); reason != "" {
		return in, startRefused(409, "claim_"+reason, "claim_window", "当前不在原生效承诺的参与领取期内")
	}
	in.RuleVersion = cfg.Version
	in.ContentPackVersion = cfg.Spec.ContentVerID
	in.Window = participation.Window{StartsAt: cfg.Spec.Reward.ClaimFrom, ExpiresAt: cfg.Spec.Reward.ClaimUntil}
	return in, nil
}

func (a *campaignBenefitReserver) originalPromise(ctx context.Context, ref participation.ParticipationRef) (store.PublishConfig, store.Campaign, bool, error) {
	if err := ctx.Err(); err != nil {
		return store.PublishConfig{}, store.Campaign{}, false, err
	}
	s := a.srv
	var stable, created string
	err := s.St.DB.QueryRowContext(ctx, `SELECT benefit_slot,created_at FROM participations WHERE id=? AND tenant_id=? AND store_id=? AND campaign_id=? AND rule_version=? AND content_pack_version=?`, ref.ParticipationID, ref.TenantID, ref.StoreID, ref.CampaignID, ref.RuleVersion, ref.ContentPackVersion).Scan(&stable, &created)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && stable != ref.BenefitSlot) {
		return store.PublishConfig{}, store.Campaign{}, false, participation.ErrNotFound
	}
	if err != nil {
		return store.PublishConfig{}, store.Campaign{}, false, err
	}
	_, err = s.St.GetPublishConfig(ref.TenantID, ref.CampaignID)
	if errors.Is(err, store.ErrNotFound) {
		return store.PublishConfig{}, store.Campaign{}, false, nil
	}
	if err != nil {
		return store.PublishConfig{}, store.Campaign{}, false, err
	}
	legacy, err := s.St.IsLegacyParticipation(ref.TenantID, ref.CampaignID, ref.ParticipationID)
	if err != nil {
		return store.PublishConfig{}, store.Campaign{}, true, err
	}
	if legacy {
		return store.PublishConfig{}, store.Campaign{}, false, nil
	}
	cfg, err := s.St.GetPublishedConfigVersion(ref.TenantID, ref.CampaignID, ref.RuleVersion)
	if err != nil {
		return cfg, store.Campaign{}, true, err
	}
	camp, err := s.St.GetCampaign(ref.CampaignID, ref.TenantID)
	if err != nil {
		return cfg, camp, true, err
	}
	if camp.StoreID != ref.StoreID || cfg.Spec.ContentVerID != ref.ContentPackVersion {
		return cfg, camp, true, participation.ErrTargetMismatch
	}
	first, err := s.St.FirstPublishedConfigAt(ref.TenantID, ref.CampaignID, ref.RuleVersion)
	if err != nil {
		return cfg, camp, true, err
	}
	born, e1 := time.Parse(time.RFC3339Nano, created)
	published, e2 := time.Parse(time.RFC3339Nano, first)
	if e1 != nil || e2 != nil || born.Before(published) {
		return cfg, camp, true, participation.ErrTargetMismatch
	}

	return cfg, camp, true, nil
}

func (a *campaignBenefitReserver) eligibleOriginal(ref participation.ParticipationRef, cfg store.PublishConfig, camp store.Campaign) error {
	if ref.ExpiresAt != "" {
		until, err := time.Parse(time.RFC3339Nano, ref.ExpiresAt)
		if err != nil || !a.now().Before(until) {
			return participation.ErrFrozen
		}
	}
	if cfg.Status != "enabled" || camp.Status != "active" {
		return participation.ErrFrozen
	}
	if reason := (campaign.Window{StartsAt: cfg.Spec.Reward.ClaimFrom, EndsAt: cfg.Spec.Reward.ClaimUntil}).ResolveTime(a.now()); reason != "" {
		return participation.ErrFrozen
	}
	// No new free-stock check here: an existing original hold may be the last
	// unit. The authoritative ledger transaction decides actual new allocation.
	facts, err := a.srv.assembleConfigFacts(ref.TenantID, camp, cfg, a.now(), false, true)
	if err != nil {
		return err
	}
	if d := campaignenable.Check(cfg.Spec, facts); !d.Allowed {
		return startRefused(422, d.Reason, d.Field, "原参与的承诺当前不可办理，请核对原名额与条件")
	}
	return nil
}

func (a *campaignBenefitReserver) ReadReservation(ctx context.Context, ref participation.ParticipationRef) (participation.ReserveResult, error) {
	cfg, camp, configured, err := a.originalPromise(ctx, ref)
	if !configured && err == nil {
		return participation.ReserveResult{NotRequested: true}, nil
	}
	if err != nil {
		return participation.ReserveResult{}, err
	}
	b, err := a.srv.St.RewardBenefitForParticipation(ref.TenantID, ref.CampaignID, ref.ParticipationID, cfg.Spec.Reward.Slot)
	if errors.Is(err, store.ErrNotFound) {
		return participation.ReserveResult{}, participation.ErrReservationUnconfirmed
	}
	if err != nil {
		return participation.ReserveResult{}, err
	}
	if b.RuleVersion != ref.RuleVersion {
		return participation.ReserveResult{}, participation.ErrTargetMismatch
	}
	if b.Status == store.RewardStatusIssued || b.Status == store.RewardStatusRedeemed {
		return participation.ReserveResult{Reserved: true, ReservationRef: b.ID}, nil
	}
	if err := a.eligibleOriginal(ref, cfg, camp); err != nil {
		var refused *participation.StartRefusedError
		if errors.Is(err, participation.ErrFrozen) || errors.As(err, &refused) {
			return participation.ReserveResult{Reason: participation.ReasonConflict}, nil
		}
		return participation.ReserveResult{}, err
	}
	if b.Status != store.RewardStatusReserved {
		return participation.ReserveResult{}, participation.ErrReservationUnconfirmed
	}
	until, err := time.Parse(time.RFC3339Nano, b.ReservedUntil)
	if err != nil || !a.now().Before(until) {
		return participation.ReserveResult{}, participation.ErrReservationUnconfirmed
	}
	return participation.ReserveResult{Reserved: true, ReservationRef: b.ID}, nil
}

func (a *campaignBenefitReserver) Reserve(ctx context.Context, ref participation.ParticipationRef) (participation.ReserveResult, error) {
	cfg, camp, configured, err := a.originalPromise(ctx, ref)
	if !configured && err == nil {
		return participation.ReserveResult{NotRequested: true}, nil
	}
	if err != nil {
		if !errors.Is(err, participation.ErrNotFound) && !errors.Is(err, participation.ErrTargetMismatch) && !errors.Is(err, participation.ErrFrozen) && !errors.Is(err, store.ErrPublishConfigNotEnabled) && !errors.Is(err, store.ErrPublishConfigSnapshotInvalid) {
			return participation.ReserveResult{}, err
		}
		return participation.ReserveResult{Reason: participation.ReasonConflict}, nil
	}
	b, existingErr := a.srv.St.RewardBenefitForParticipation(ref.TenantID, ref.CampaignID, ref.ParticipationID, cfg.Spec.Reward.Slot)
	if existingErr == nil && b.RuleVersion != ref.RuleVersion {
		return participation.ReserveResult{Reason: participation.ReasonConflict}, nil
	}
	if existingErr == nil && (b.Status == store.RewardStatusIssued || b.Status == store.RewardStatusRedeemed) {
		return participation.ReserveResult{Reserved: true, ReservationRef: b.ID}, nil
	}
	if err := a.eligibleOriginal(ref, cfg, camp); err != nil {
		var refused *participation.StartRefusedError
		if errors.Is(err, participation.ErrFrozen) || errors.As(err, &refused) {
			return participation.ReserveResult{Reason: participation.ReasonConflict}, nil
		}
		return participation.ReserveResult{}, err
	}
	if existingErr != nil && !errors.Is(existingErr, store.ErrNotFound) {
		return participation.ReserveResult{}, existingErr
	}
	event := "participation:reserve:" + ref.ParticipationID
	if existingErr == nil {
		until, err := time.Parse(time.RFC3339Nano, b.ReservedUntil)
		if b.Status == store.RewardStatusReserved && err == nil && a.now().Before(until) {
			return participation.ReserveResult{Reserved: true, ReservationRef: b.ID}, nil
		}
		if b.ReservedUntil != "" {
			event += ":" + b.ReservedUntil
		}
	}
	ben, _, err := a.srv.St.ReserveRewardBenefit(store.RewardReserveInput{TenantID: ref.TenantID, CampaignID: ref.CampaignID, ParticipationID: ref.ParticipationID, Slot: cfg.Spec.Reward.Slot, RuleVersion: ref.RuleVersion, EventID: event, Actor: "participation-service", TTLSeconds: 600, Expiry: a.srv.rewardPromiseDeadlines(camp, cfg.Spec.Reward.ClaimUntil)}, a.now().Format(time.RFC3339Nano))
	if err != nil {
		if errors.Is(err, store.ErrRewardStock) {
			return participation.ReserveResult{Reason: participation.ReasonOutOfStock}, nil
		}
		if errors.Is(err, store.ErrPromiseBlocked) || errors.Is(err, store.ErrRewardInvalid) || errors.Is(err, store.ErrRewardConflict) || errors.Is(err, store.ErrRewardUnavailable) {
			return participation.ReserveResult{Reason: participation.ReasonConflict}, nil
		}
		return participation.ReserveResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return participation.ReserveResult{}, err
	}
	if ben.Status != store.RewardStatusReserved && ben.Status != store.RewardStatusIssued && ben.Status != store.RewardStatusRedeemed {
		return participation.ReserveResult{Reason: participation.ReasonConflict}, nil
	}
	return participation.ReserveResult{Reserved: true, ReservationRef: ben.ID}, nil
}

func (s *Server) participationReservationRequired(p participation.Participation) bool {
	legacy, err := s.St.IsLegacyParticipation(p.TenantID, p.CampaignID, p.ID)
	return err != nil || !legacy
}

func (s *Server) publicParticipationView(p participation.Participation) participationView {
	view := newParticipationView(p)
	view.ReservationRequired = s.participationReservationRequired(p)
	return view
}

func (s *Server) rewardTime() time.Time {
	if s.rewardClock != nil {
		return s.rewardClock().UTC()
	}
	return time.Now().UTC()
}
