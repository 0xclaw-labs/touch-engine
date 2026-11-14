package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/storefrontversion"
)

// FreezeActionContent resolves concrete immutable version ids on the merchant save path.
// An adoption is a candidate source; only this explicit storefront snapshot publishes it.
func (s *Store) FreezeActionContent(tenantID, storeID string, snap *storefrontversion.ActionSnapshot) error {
	snap.ContentVersionID = ""
	snap.ContentCandidates = []string{}
	packID := strings.TrimPrefix(snap.ContentBindingRef, "pack:")
	if snap.ContentBindingRef == "" {
		b, err := s.GetActionContentBinding(tenantID, snap.ActionID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		packID = b.PackID
		if packID != "" {
			snap.ContentBindingRef = "pack:" + packID
		}
	}
	if packID == "" {
		return nil
	}
	pack, err := s.GetStoreContentPack(packID, tenantID)
	if err != nil {
		return err
	}
	if pack.StoreID != storeID {
		return ErrNotFound
	}
	adoptions, err := s.ListStoreAdoptions(tenantID, storeID)
	if err != nil {
		return err
	}
	candidates := make([]StoreContentPackVersion, 0, len(adoptions))
	var selected StoreContentPackVersion
	for _, adoption := range adoptions {
		// A platform declaration describes the content. Only an explicit adoption
		// for THIS platform authorizes it as a publication candidate.
		if snap.Platform != "" && adoption.Platform != snap.Platform {
			continue
		}
		candidate, err := s.GetStoreContentPackVersion(adoption.VersionID, tenantID)
		if err != nil {
			return err
		}
		if !contentMatchesAction(candidate, *snap) {
			continue
		}
		candidates = append(candidates, candidate)
		if candidate.PackID == packID {
			selected = candidate
		}
	}
	if snap.Platform == "" {
		// Unversioned legacy actions retain their historical store-wide contract.
		selected, err = s.GetStoreAdoptedPackVersion(tenantID, packID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	if selected.ID == "" {
		return nil
	}
	snap.ContentVersionID = selected.ID
	snap.ContentCandidates = append(snap.ContentCandidates, selected.ID)
	seen := map[string]bool{selected.ID: true}
	for _, candidate := range candidates {
		if seen[candidate.ID] || contentpack.Distributable(candidate.Status, candidate.GrantUntil, time.Now()) != nil {
			continue
		}
		seen[candidate.ID] = true
		snap.ContentCandidates = append(snap.ContentCandidates, candidate.ID)
	}
	return nil
}

func contentMatchesAction(v StoreContentPackVersion, snap storefrontversion.ActionSnapshot) bool {
	if snap.Platform == "" || snap.Kind != "publish" {
		return true
	}
	if snap.Variant != "" && string(v.ContentKind) != snap.Variant {
		return false
	}
	for _, platform := range v.PlatformVersions {
		if platform.Platform == snap.Platform {
			return true
		}
	}
	return false
}

// Reject incompatible references already persisted by older publishers. Never
// silently replace them with today's adoption or rewrite immutable snapshots.
func (s *Store) filterFrozenActionContent(row *StoreAction, publishedAt string) error {
	if row.Kind != "publish" || row.ContentCandidates == nil {
		return nil
	}
	ids := make([]string, 0, len(row.ContentCandidates))
	selectedOK := false
	for _, id := range row.ContentCandidates {
		v, err := s.GetStoreContentPackVersion(id, row.TenantID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if !contentMatchesAction(v, actionSnapshot(*row)) {
			continue
		}
		cutoff, err := time.Parse(time.RFC3339Nano, publishedAt)
		if err != nil {
			continue
		}
		events, err := s.ListStoreContentPackVersionEvents(row.TenantID, v.ID)
		if err != nil {
			return err
		}
		adoptedForPlatform := false
		for _, event := range events {
			when, err := time.Parse(time.RFC3339Nano, event.CreatedAt)
			if event.Kind == "adopted" && event.Note == "平台 "+row.Platform+" 明确采用" && err == nil && !when.After(cutoff) {
				adoptedForPlatform = true
			}
		}
		if !adoptedForPlatform {
			continue
		}
		ids = append(ids, id)
		selectedOK = selectedOK || id == row.ContentVersionID
	}
	row.ContentCandidates = ids
	if !selectedOK {
		row.ContentVersionID = ""
	}
	return nil
}

func actionSnapshot(row StoreAction) storefrontversion.ActionSnapshot {
	return storefrontversion.ActionSnapshot{ActionID: row.ID, ActionRevision: row.Revision, CatalogCode: row.CatalogCode, Platform: row.Platform, Kind: row.Kind, Variant: row.Variant, Label: row.Label, TargetRef: row.TargetRef, ContentBindingRef: row.ContentBindingRef, CapabilityRef: row.CapabilityRef, Presentation: row.Presentation}
}
func (s *Store) legacyActionContent(row StoreAction) (StoreAction, error) {
	snap := actionSnapshot(row)
	snap.Platform = "" // legacy candidate pool was store-wide; preserve its existing contract.
	if err := s.FreezeActionContent(row.TenantID, row.StoreID, &snap); err != nil {
		return StoreAction{}, err
	}
	row.ContentVersionID, row.ContentCandidates = snap.ContentVersionID, snap.ContentCandidates
	return row, nil
}

// PublishedContentVersion is exact-version authorization, never a same-pack shortcut.
func (s *Store) PublishedContentVersion(versionID string, at time.Time) (bool, error) {
	v, err := s.GetStoreContentPackVersionByID(versionID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	pack, err := s.GetStoreContentPack(v.PackID, v.TenantID)
	if err != nil {
		return false, err
	}
	rec, err := s.GetStore(pack.StoreID, v.TenantID)
	if err != nil || rec.Status != StoreStatusActive {
		return false, err
	}
	tenant, err := s.GetTenant(v.TenantID)
	if err != nil || tenant.Lifecycle != "active" {
		return false, err
	}
	actions, err := s.ListPublishedStoreActions(pack.StoreID, at)
	if err != nil {
		return false, err
	}
	for _, a := range actions {
		for _, id := range a.ContentCandidates {
			if id == versionID {
				return true, nil
			}
		}
	}
	return false, nil
}

// actionBaseline is authoritative only after a staged write. NULL preserves legacy fallback.
func (s *Store) actionBaseline(storeID string) ([]storefrontversion.ActionSnapshot, bool, error) {
	var raw sql.NullString
	err := s.DB.QueryRow(`SELECT public_baseline FROM store_action_configs WHERE store_id=?`, storeID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !raw.Valid {
		return nil, false, nil
	}
	var snapshots []storefrontversion.ActionSnapshot
	if err := json.Unmarshal([]byte(raw.String), &snapshots); err != nil {
		return nil, false, err
	}
	return snapshots, true, nil
}

func (s *Store) legacyPublishedActions(storeID string, at time.Time) ([]StoreAction, error) {
	snapshots, managed, err := s.actionBaseline(storeID)
	if err != nil {
		return nil, err
	}
	if managed {
		var tenantID string
		if err := s.DB.QueryRow(`SELECT tenant_id FROM stores WHERE id=?`, storeID).Scan(&tenantID); err != nil {
			return nil, err
		}
		// This is the captured pre-versioning public baseline. It has no home
		// publication timestamp and retains the old store-wide content contract.
		// Keep only its frozen IDs; never refill it from today's adoptions. Live
		// action vetoes and per-version distribution checks still apply.
		return s.actionsForFrozenPublication(StorefrontVersion{TenantID: tenantID, StoreID: storeID, Actions: snapshots}, at, false)
	}
	rows, err := s.ListPublicStoreActions(storeID, at)
	if err != nil {
		return nil, err
	}
	out := make([]StoreAction, 0, len(rows))
	for _, row := range rows {
		tenant, storeID, err := s.StoreActionScope(row.ID)
		if err != nil {
			return nil, err
		}
		row.TenantID, row.StoreID = tenant, storeID
		item, err := s.legacyActionContent(row)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func storefrontContentCutoff(v StorefrontVersion) string {
	if v.PublishedAt != "" {
		return v.PublishedAt
	}
	if v.UpdatedAt != "" {
		return v.UpdatedAt
	}
	return v.CreatedAt
}

func (s *Store) restorePublishedContent(v StorefrontVersion, row *StoreAction) error {
	row.ContentCandidates = []string{}
	packID := strings.TrimPrefix(row.ContentBindingRef, "pack:")
	if packID == "" {
		return nil
	}
	// Legacy rows have no content snapshot. Use their recorded publication
	// boundary, never today's adoption or the draft's first creation time.
	cutoff, err := time.Parse(time.RFC3339Nano, storefrontContentCutoff(v))
	if err != nil {
		return nil
	}
	versions, err := s.ListStoreContentPackVersions(v.TenantID, packID)
	if err != nil {
		return err
	}
	var selected StoreContentPackVersion
	var selectedAt time.Time
	for _, candidate := range versions {
		if !contentMatchesAction(candidate, actionSnapshot(*row)) {
			continue
		}
		events, err := s.ListStoreContentPackVersionEvents(v.TenantID, candidate.ID)
		if err != nil {
			return err
		}
		for _, event := range events {
			when, err := time.Parse(time.RFC3339Nano, event.CreatedAt)
			if row.Kind == "publish" && event.Note != "平台 "+row.Platform+" 明确采用" {
				continue
			}
			if event.Kind == "adopted" && err == nil && !when.After(cutoff) && when.After(selectedAt) {
				selected, selectedAt = candidate, when
			}
		}
	}
	if selected.ID != "" {
		row.ContentVersionID = selected.ID
		row.ContentCandidates = []string{selected.ID}
	}
	return nil
}

// VerifyHistoricalSessionContent supports genuine pre-migration frozen choices,
// but an old row is never permission for arbitrary versions of the same pack.
func (s *Store) VerifyHistoricalSessionContent(tenantID, storeID, actionID, versionID, createdAt string) (bool, error) {
	v, err := s.GetStoreContentPackVersion(versionID, tenantID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	pack, err := s.GetStoreContentPack(v.PackID, tenantID)
	if err != nil {
		return false, err
	}
	if pack.StoreID != storeID {
		return false, nil
	}
	// Legacy choices were store-wide. Only the recorded choice is retained,
	// and an adoption event must predate its actual content-selection event.
	cutoff, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return false, nil
	}
	events, err := s.ListStoreContentPackVersionEvents(tenantID, v.ID)
	if err != nil {
		return false, err
	}
	for _, event := range events {
		when, err := time.Parse(time.RFC3339Nano, event.CreatedAt)
		if event.Kind == "adopted" && err == nil && !when.After(cutoff) {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) frozenContentState(v StorefrontVersion, snap storefrontversion.ActionSnapshot, at time.Time) string {
	row := StoreAction{ID: snap.ActionID, TenantID: v.TenantID, StoreID: v.StoreID, Kind: snap.Kind, Platform: snap.Platform, Variant: snap.Variant, ContentBindingRef: snap.ContentBindingRef, ContentVersionID: snap.ContentVersionID, ContentCandidates: snap.ContentCandidates}
	if snap.ContentCandidates == nil {
		if err := s.restorePublishedContent(v, &row); err != nil {
			return storefrontversion.ContentMissing
		}
	}
	if row.ContentBindingRef == "" {
		return storefrontversion.ContentOK
	}
	if row.ContentVersionID == "" {
		return storefrontversion.ContentMissing
	}
	content, err := s.GetStoreContentPackVersion(row.ContentVersionID, v.TenantID)
	if err != nil {
		return storefrontversion.ContentMissing
	}
	if content.PackID != strings.TrimPrefix(row.ContentBindingRef, "pack:") {
		return storefrontversion.ContentMissing
	}
	if !contentMatchesAction(content, snap) {
		return storefrontversion.ContentMissing
	}
	if content.Status == contentpack.StatusBlocked {
		return storefrontversion.ContentBlocked
	}
	if contentpack.Distributable(content.Status, content.GrantUntil, at) != nil {
		return storefrontversion.ContentGrantExpired
	}
	return storefrontversion.ContentOK
}
func (s *Store) StorefrontFrozenActionFacts(v StorefrontVersion, at time.Time) ([]storefrontversion.ActionFact, error) {
	ids := make([]string, 0, len(v.Actions))
	byID := map[string]storefrontversion.ActionSnapshot{}
	for _, snap := range v.Actions {
		ids = append(ids, snap.ActionID)
		byID[snap.ActionID] = snap
	}
	facts, err := s.StorefrontActionFacts(v.TenantID, v.StoreID, ids, at)
	if err != nil {
		return nil, err
	}
	for i := range facts {
		snap := byID[facts[i].ActionID]
		facts[i].ContentState = s.frozenContentState(v, snap, at)
	}
	return facts, nil
}
func (s *Store) storefrontVersionVerdicts(v StorefrontVersion, at time.Time) ([]storefrontversion.Verdict, error) {
	if len(v.Actions) == 0 {
		return []storefrontversion.Verdict{}, nil
	}
	facts, err := s.StorefrontFrozenActionFacts(v, at)
	if err != nil {
		return nil, err
	}
	verdicts, fatal := storefrontversion.Preflight(v.DraftDocument(), facts)
	if len(fatal) > 0 {
		return nil, ErrStorefrontRefDrift
	}
	return verdicts, nil
}

// SessionActionAvailable is a real-time veto over an already frozen session;
// removing an entry from a later page does not rewrite its prior publication.
func (s *Store) SessionActionAvailable(tenantID, storeID, actionID string, at time.Time) (bool, error) {
	row, err := s.GetStoreActionByID(tenantID, storeID, actionID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !row.Enabled || row.Revoked || actionExpired(row.ExpiresAt, at) {
		return false, nil
	}
	rec, err := s.GetStore(storeID, tenantID)
	if err != nil {
		return false, err
	}
	if rec.Status != StoreStatusActive {
		return false, nil
	}
	tenant, err := s.GetTenant(tenantID)
	if err != nil {
		return false, err
	}
	return tenant.Lifecycle == "active", nil
}

// SessionPublishedAction resolves targets from the session's own publication,
// while the current action row retains the independent safety veto.
func (s *Store) SessionPublishedAction(tenantID, storeID, actionID string, storefrontRevision, actionRevision int, at time.Time) (StoreAction, error) {
	available, err := s.SessionActionAvailable(tenantID, storeID, actionID, at)
	if err != nil {
		return StoreAction{}, err
	}
	if !available {
		return StoreAction{}, ErrNotFound
	}
	if storefrontRevision > 0 {
		version, err := s.GetStorefrontVersionForPreview(tenantID, storeID, storefrontRevision)
		if err != nil {
			return StoreAction{}, err
		}
		rows, err := s.ActionsForStorefrontVersion(version, at)
		if err != nil {
			return StoreAction{}, err
		}
		for _, row := range rows {
			if row.ID == actionID && row.Revision == actionRevision {
				return row, nil
			}
		}
		return StoreAction{}, ErrNotFound
	}
	live, err := s.GetStoreActionByID(tenantID, storeID, actionID)
	if err != nil {
		return StoreAction{}, err
	}
	frozen, found, err := s.historicalStorefrontAction(storeID, actionID, actionRevision)
	if err != nil {
		return StoreAction{}, err
	}
	if found {
		live.TargetRef, live.ContentBindingRef = frozen.TargetRef, frozen.ContentBindingRef
		live.Revision = actionRevision
	} else if live.Revision != actionRevision {
		return StoreAction{}, ErrNotFound
	}
	if !publicVisible(live, at) {
		return StoreAction{}, ErrNotFound
	}
	return live, nil
}
