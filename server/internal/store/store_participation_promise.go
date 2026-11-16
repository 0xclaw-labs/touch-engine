package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/campaignenable"
)

type participationPromiseOrigin struct {
	legacy                  bool
	created, firstPublished time.Time
	configuredAt            time.Time
	ruleVersion             int
	contentVersion, storeID string
	rewardSlot              string
}

type participationPromiseQuerier interface {
	publishConfigQuerier
	Query(string, ...any) (*sql.Rows, error)
}

// An intact draft-only history proves that saving an editor draft did not
// create a new customer promise. Absence of an enable row alone is not proof:
// the live row and the complete create/update chain must agree too.
func unpublishedDraftEvidence(q participationPromiseQuerier, cfg PublishConfig, born time.Time) error {
	if cfg.EnabledVersion != 0 || cfg.Version < 1 || cfg.Status != "draft" || cfg.EnabledAt != "" || cfg.PausedAt != "" || cfg.Disclosure != "" {
		return ErrPublishConfigSnapshotInvalid
	}
	rows, err := q.Query(`SELECT revision,action,version,payload,created_at FROM campaign_publish_config_revisions WHERE tenant_id=? AND campaign_id=? ORDER BY revision`, cfg.TenantID, cfg.CampaignID)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	var last specPayload
	previous := born
	for rows.Next() {
		var revision, version int
		var action, raw, at string
		if err := rows.Scan(&revision, &action, &version, &raw, &at); err != nil {
			return err
		}
		count++
		wantAction := "update"
		if count == 1 {
			wantAction = "create"
		}
		stamp, timeErr := time.Parse(time.RFC3339Nano, at)
		if revision != count || version != count || action != wantAction || timeErr != nil || stamp.Before(previous) || json.Unmarshal([]byte(raw), &last) != nil || last.Status != "draft" || last.EnabledVersion != 0 || last.Disclosure != "" || campaignenable.ValidateSpec(last.Spec) != nil {
			return ErrPublishConfigSnapshotInvalid
		}
		previous = stamp
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count != cfg.Version || !samePublishPromise(last.Spec, cfg.Spec) {
		return ErrPublishConfigSnapshotInvalid
	}
	return nil
}

// firstPromiseEvidence validates the original earliest publication, rather
// than treating a missing or damaged history as evidence of a legacy contract.
func firstPromiseEvidence(q publishConfigQuerier, cfg PublishConfig, version int) (time.Time, error) {
	query := `SELECT action,version,payload,created_at FROM campaign_publish_config_revisions WHERE tenant_id=? AND campaign_id=? AND action IN ('enable','resume')`
	args := []any{cfg.TenantID, cfg.CampaignID}
	if version > 0 {
		query += ` AND version=?`
		args = append(args, version)
	}
	query += ` ORDER BY revision LIMIT 1`
	var action, raw, at string
	var publishedVersion int
	err := q.QueryRow(query, args...).Scan(&action, &publishedVersion, &raw, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, ErrPublishConfigSnapshotInvalid
	}
	if err != nil {
		return time.Time{}, err
	}
	var p specPayload
	if action != "enable" || publishedVersion <= 0 || json.Unmarshal([]byte(raw), &p) != nil || p.Status != "enabled" || p.EnabledVersion != publishedVersion || campaignenable.ValidateSpec(p.Spec) != nil || !validPublishedDisclosure(p.Spec, publishedVersion, p.Disclosure) {
		return time.Time{}, ErrPublishConfigSnapshotInvalid
	}
	ts, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return time.Time{}, ErrPublishConfigSnapshotInvalid
	}
	return ts, nil
}

func participationOrigin(q participationPromiseQuerier, tenantID, campaignID, id string) (participationPromiseOrigin, error) {
	var origin participationPromiseOrigin
	var born string
	err := q.QueryRow(`SELECT created_at,rule_version,content_pack_version,store_id FROM participations WHERE tenant_id=? AND campaign_id=? AND id=?`, tenantID, campaignID, id).Scan(&born, &origin.ruleVersion, &origin.contentVersion, &origin.storeID)
	if errors.Is(err, sql.ErrNoRows) {
		return origin, ErrNotFound
	}
	if err != nil {
		return origin, err
	}
	origin.created, err = time.Parse(time.RFC3339Nano, born)
	if err != nil {
		return origin, ErrPublishConfigSnapshotInvalid
	}
	row, err := scanPublishConfig(q.QueryRow(`SELECT `+publishConfigCols+` FROM campaign_publish_configs WHERE tenant_id=? AND campaign_id=?`, tenantID, campaignID))
	if errors.Is(err, sql.ErrNoRows) {
		origin.legacy = true
		return origin, nil
	}
	if err != nil {
		return origin, err
	}
	cfg := row.cfg
	configBorn, err := time.Parse(time.RFC3339Nano, cfg.CreatedAt)
	if err != nil {
		return origin, ErrPublishConfigSnapshotInvalid
	}
	origin.configuredAt = configBorn
	if cfg.EnabledVersion == 0 && origin.created.Before(configBorn) {
		if err := unpublishedDraftEvidence(q, cfg, configBorn); err != nil {
			return origin, err
		}
		origin.legacy = true
		return origin, nil
	}
	// Validate the live pin too: absent first-enable evidence cannot downgrade a
	// configured participation to legacy even if its birth predates the config.
	if _, err = publishedConfigVersion(q, cfg, cfg.EnabledVersion); err != nil {
		return origin, err
	}
	origin.firstPublished, err = firstPromiseEvidence(q, cfg, 0)
	if err != nil {
		return origin, err
	}
	if origin.firstPublished.Before(configBorn) {
		return origin, ErrPublishConfigSnapshotInvalid
	}
	if origin.created.Before(configBorn) && origin.created.Before(origin.firstPublished) {
		origin.legacy = true
		return origin, nil
	}
	// A version-number collision is not acceptance of that promise. A configured
	// participation must match its own original published content and timestamp.
	frozen, err := publishedConfigVersion(q, cfg, origin.ruleVersion)
	if err != nil {
		return origin, err
	}
	firstVersion, err := firstPromiseEvidence(q, cfg, origin.ruleVersion)
	if err != nil {
		return origin, err
	}
	if origin.created.Before(firstVersion) || origin.contentVersion != frozen.Spec.ContentVerID {
		return origin, ErrPublishConfigSnapshotInvalid
	}
	allowedStore := false
	for _, id := range frozen.Spec.Reward.StoreIDs {
		if id == origin.storeID {
			allowedStore = true
		}
	}
	if !allowedStore {
		return origin, ErrPublishConfigSnapshotInvalid
	}
	origin.rewardSlot = frozen.Spec.Reward.Slot
	return origin, nil
}

// IsLegacyParticipation preserves only a provable original unconfigured
// contract. The queried participation is always tenant/campaign scoped.
func (s *Store) IsLegacyParticipation(tenantID, campaignID, id string) (bool, error) {
	origin, err := participationOrigin(s.DB, tenantID, campaignID, id)
	return origin.legacy, err
}

// Gate acceptance and the current lifecycle in the same reward transaction.
// A proven hold from before the first draft still belongs to its original
// unconfigured contract; editing that draft cannot retroactively cancel it.
func participationRewardPromiseGateInTx(tx *sql.Tx, tenantID, campaignID, id, slot string, version int, existing *RewardBenefit) error {
	var configured int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM campaign_publish_configs WHERE tenant_id=? AND campaign_id=?`, tenantID, campaignID).Scan(&configured); err != nil {
		return err
	}
	if configured == 0 {
		return nil
	} // original unconfigured ledger contract
	origin, err := participationOrigin(tx, tenantID, campaignID, id)
	if err != nil {
		return fmt.Errorf("%w: participation_promise_unknown: %v", ErrPromiseBlocked, err)
	}
	if origin.ruleVersion != version {
		return fmt.Errorf("%w: participation_rule_mismatch", ErrPromiseBlocked)
	}
	if !origin.legacy {
		if origin.rewardSlot != slot {
			return fmt.Errorf("%w: participation_slot_mismatch", ErrPromiseBlocked)
		}
		return publishedRewardActiveInTx(tx, tenantID, campaignID)
	}
	// A still-active allocation actually made before formal publication is an
	// original legacy reward contract, not a newly accepted formal promise.
	if existing != nil && existing.Status == RewardStatusReserved && existing.RuleVersion == version && existing.BenefitSlot == slot {
		accepted, err := time.Parse(time.RFC3339Nano, existing.CreatedAt)
		if err == nil && !accepted.Before(origin.created) {
			if origin.firstPublished.IsZero() && accepted.Before(origin.configuredAt) {
				var status string
				if err := tx.QueryRow(`SELECT status FROM campaigns WHERE tenant_id=? AND id=?`, tenantID, campaignID).Scan(&status); err != nil {
					return err
				}
				if status != "active" {
					return fmt.Errorf("%w: campaign_not_active", ErrPromiseBlocked)
				}
				return nil
			}
			if accepted.Before(origin.firstPublished) {
				return publishedRewardActiveInTx(tx, tenantID, campaignID)
			}
		}
	}
	return fmt.Errorf("%w: legacy_participation_has_no_formal_promise", ErrPromiseBlocked)
}
