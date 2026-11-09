package store

// HUI-4584 G1: 门店动作实例（store_actions）与公共门店面（store_storefronts）。
//
// 纪律：
//   - action_id 是门店范围内随机的不透明实例 ID（sa_<24hex>），不用自增暴露。
//   - 同店同 catalog_code+platform+variant 可并存多条；kind 不是唯一键。
//   - platform/kind/variant 服务端从 catalog_code 单点推导（actioncatalog），
//     客户端回显值不参与存储。
//   - PUT 是批量原子替换 + config_revision 乐观锁：旧版本零写入；负载内重复
//     action_id 明确拒绝；内容完全一致的重放是幂等（不推高修订、不写快照）。
//   - 每次真实变更写一条候选不可变修订快照（线上由 HUI-1676 发布版本引用）。
//   - 公共投影只放行：已映射目录 + 启用 + 未撤销 + 未过期 + 目标为公网 https
//     的行。停用/撤销/过期/待映射/非正式目标一律不出现（fail-closed）。

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/actioncatalog"
	"github.com/bianjiefilm/touch-engine/server/internal/extrajump"
)

// Replace-level failures. The HTTP layer maps them onto stable codes.
var (
	ErrStaleRevision     = errors.New("store: stale config revision")
	ErrDuplicateActionID = errors.New("store: duplicate action id in payload")
	ErrUnknownActionID   = errors.New("store: action id does not exist in this store")
)

// Storefront is the public storefront row for one store.
type Storefront struct {
	StoreID            string `json:"store_id"`
	TenantID           string `json:"tenant_id"`
	StoreCode          string `json:"store_code"`
	OpeningHours       string `json:"opening_hours"`
	StorefrontRevision int    `json:"storefront_revision"`
}

// StorefrontPublic is the storefront row plus the store/tenant facts the
// public projection needs. Tenant secrets never enter this struct.
type StorefrontPublic struct {
	Storefront
	StoreName       string
	StoreAddress    string
	StoreStatus     string
	TenantLifecycle string
}

// StoreAction is one store action instance.
type StoreAction struct {
	ID                string `json:"action_id"`
	TenantID          string `json:"-"`
	StoreID           string `json:"-"`
	CatalogCode       string `json:"catalog_code"`
	Platform          string `json:"platform"`
	Kind              string `json:"kind"`
	Variant           string `json:"variant"`
	Label             string `json:"label"`
	TargetRef         string `json:"target_ref"`
	ContentBindingRef string `json:"content_binding_ref"`
	CapabilityRef     string `json:"capability_ref"`
	Presentation      string `json:"presentation"`
	SortOrder         int    `json:"order"`
	Enabled           bool   `json:"enabled"`
	Revision          int    `json:"revision"`
	Revoked           bool   `json:"-"`
	ExpiresAt         string `json:"-"`
	LegacyCampaignID  string `json:"legacy_campaign_id,omitempty"`
	LegacyKind        string `json:"legacy_kind,omitempty"`
	CreatedAt         string `json:"-"`
	UpdatedAt         string `json:"-"`
}

// StoreActionInput is one desired item of a ReplaceStoreActions call. The
// client may echo platform/kind/variant; the server derives them from the
// catalog code and ignores the echoed values.
type StoreActionInput struct {
	ID                string
	CatalogCode       string
	Platform          string
	Kind              string
	Variant           string
	Label             string
	TargetRef         string
	ContentBindingRef string
	SortOrder         int
	Enabled           bool
}

// EnsureStorefront returns the storefront row, creating one with a fresh
// opaque store code when missing. The code is stable for the store's life.
func (s *Store) EnsureStorefront(tenantID, storeID string) (Storefront, error) {
	if _, err := s.GetStore(storeID, tenantID); err != nil {
		return Storefront{}, err
	}
	sf, err := s.storefrontByStore(storeID, tenantID)
	if err == nil {
		return sf, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Storefront{}, err
	}
	for attempt := 0; attempt < 5; attempt++ {
		sf = Storefront{
			StoreID: storeID, TenantID: tenantID,
			StoreCode: "sf_" + randHex(8),
		}
		_, err = s.DB.Exec(`INSERT INTO store_storefronts(store_id,tenant_id,store_code,opening_hours,storefront_revision,created_at,updated_at)
			VALUES(?,?,?,?,0,?,?)`, sf.StoreID, sf.TenantID, sf.StoreCode, sf.OpeningHours, now(), now())
		if err == nil {
			s.bumpEpoch()
			return sf, nil
		}
		if !strings.Contains(err.Error(), "UNIQUE") {
			return Storefront{}, err
		}
	}
	return Storefront{}, fmt.Errorf("store: could not allocate a unique store code: %w", err)
}

// storefrontByStore loads the storefront row; ErrNotFound when absent.
func (s *Store) storefrontByStore(storeID, tenantID string) (Storefront, error) {
	var sf Storefront
	var revision int
	err := s.DB.QueryRow(`SELECT store_id,tenant_id,store_code,opening_hours,storefront_revision
		FROM store_storefronts WHERE store_id=? AND tenant_id=?`, storeID, tenantID).
		Scan(&sf.StoreID, &sf.TenantID, &sf.StoreCode, &sf.OpeningHours, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return Storefront{}, ErrNotFound
	}
	if err != nil {
		return Storefront{}, err
	}
	sf.StorefrontRevision = revision
	return sf, nil
}

// StorefrontForStore is EnsureStorefront for the merchant surface: it never
// 404s for an existing store, it provisions the row on first use.
func (s *Store) StorefrontForStore(tenantID, storeID string) (Storefront, error) {
	return s.EnsureStorefront(tenantID, storeID)
}

// GetStorefrontPublicByCode resolves a public store code to the storefront
// plus the store/tenant facts needed by the public projection.
func (s *Store) GetStorefrontPublicByCode(code string) (StorefrontPublic, error) {
	var pub StorefrontPublic
	var revision int
	err := s.DB.QueryRow(`SELECT f.store_id,f.tenant_id,f.store_code,f.opening_hours,f.storefront_revision,
		st.name,st.address,COALESCE(st.status,'active'),t.lifecycle
		FROM store_storefronts f
		JOIN stores st ON st.id = f.store_id
		JOIN tenants t ON t.id = f.tenant_id
		WHERE f.store_code = ?`, strings.TrimSpace(code)).
		Scan(&pub.StoreID, &pub.TenantID, &pub.StoreCode, &pub.OpeningHours, &revision,
			&pub.StoreName, &pub.StoreAddress, &pub.StoreStatus, &pub.TenantLifecycle)
	if errors.Is(err, sql.ErrNoRows) {
		return StorefrontPublic{}, ErrNotFound
	}
	if err != nil {
		return StorefrontPublic{}, err
	}
	pub.StorefrontRevision = revision
	return pub, nil
}

// ListStoreActions returns the store's action instances (order: sort_order,
// created_at, id) with the current config_revision.
func (s *Store) ListStoreActions(tenantID, storeID string) ([]StoreAction, int, error) {
	rev, _, err := s.configRevision(storeID)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.listRows(tenantID, storeID)
	if err != nil {
		return nil, 0, err
	}
	return rows, rev, nil
}

func (s *Store) listRows(tenantID, storeID string) ([]StoreAction, error) {
	rs, err := s.DB.Query(`SELECT id,catalog_code,platform,kind,variant,label,target_ref,content_binding_ref,
		capability_ref,presentation,sort_order,enabled,revision,revoked,expires_at,legacy_campaign_id,legacy_kind,
		created_at,updated_at
		FROM store_actions WHERE tenant_id=? AND store_id=? ORDER BY sort_order, created_at, id`, tenantID, storeID)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []StoreAction
	for rs.Next() {
		item, scanErr := scanStoreAction(rs)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rs.Err()
}

type rowScanner interface{ Scan(dest ...any) error }

func scanStoreAction(rs rowScanner) (StoreAction, error) {
	var item StoreAction
	var enabled, revoked int
	err := rs.Scan(&item.ID, &item.CatalogCode, &item.Platform, &item.Kind, &item.Variant, &item.Label,
		&item.TargetRef, &item.ContentBindingRef, &item.CapabilityRef, &item.Presentation, &item.SortOrder,
		&enabled, &item.Revision, &revoked, &item.ExpiresAt, &item.LegacyCampaignID, &item.LegacyKind,
		&item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return StoreAction{}, err
	}
	item.Enabled = enabled == 1
	item.Revoked = revoked == 1
	return item, nil
}

func (s *Store) configRevision(storeID string) (int, bool, error) {
	var rev int
	err := s.DB.QueryRow(`SELECT config_revision FROM store_action_configs WHERE store_id=?`, storeID).Scan(&rev)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return rev, true, nil
}

// ReplaceStoreActions applies the full desired item set atomically.
//
//   - expectedRevision must equal the current config_revision (ErrStaleRevision,
//     zero writes otherwise).
//   - Inputs with an empty ID are created; inputs with an ID must reference an
//     existing row of THIS store (ErrUnknownActionID); the same ID twice in one
//     payload is ErrDuplicateActionID (explicit refusal, never last-one-wins).
//   - Existing rows absent from the payload are deleted.
//   - platform/kind/variant derive from catalog_code.
//   - Identical content replays the current state (no revision bump, no snapshot).
//   - Every real change bumps config_revision and stores one immutable snapshot.
func (s *Store) ReplaceStoreActions(tenantID, storeID string, expectedRevision int, inputs []StoreActionInput) (int, []StoreAction, error) {
	if _, err := s.GetStore(storeID, tenantID); err != nil {
		return 0, nil, err
	}
	seen := map[string]bool{}
	for _, in := range inputs {
		if in.ID == "" {
			continue
		}
		if seen[in.ID] {
			return 0, nil, ErrDuplicateActionID
		}
		seen[in.ID] = true
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback()

	current, err := currentRevision(tx, storeID)
	if err != nil {
		return 0, nil, err
	}
	if expectedRevision != current {
		return 0, nil, ErrStaleRevision
	}
	existing, err := loadRowsInTx(tx, tenantID, storeID)
	if err != nil {
		return 0, nil, err
	}

	desired := make([]StoreAction, 0, len(inputs))
	for _, in := range inputs {
		entry, ok := actioncatalog.Lookup(strings.TrimSpace(in.CatalogCode))
		if !ok {
			return 0, nil, fmt.Errorf("%w: catalog_code %q", ErrUnknownCatalogCode, in.CatalogCode)
		}
		item := StoreAction{
			TenantID: tenantID, StoreID: storeID,
			CatalogCode: entry.Code, Platform: entry.Platform, Kind: entry.Kind, Variant: entry.Variant,
			Label:             strings.TrimSpace(in.Label),
			TargetRef:         strings.TrimSpace(in.TargetRef),
			ContentBindingRef: strings.TrimSpace(in.ContentBindingRef),
			Presentation:      "link",
			SortOrder:         in.SortOrder,
			Enabled:           in.Enabled,
			Revision:          1,
		}
		if in.ID != "" {
			prev, ok := existingByID(existing, in.ID)
			if !ok {
				return 0, nil, fmt.Errorf("%w: %s", ErrUnknownActionID, in.ID)
			}
			item.ID = prev.ID
			item.CreatedAt = prev.CreatedAt
			item.Revision = prev.Revision
			item.LegacyCampaignID = prev.LegacyCampaignID
			item.LegacyKind = prev.LegacyKind
			item.Revoked = prev.Revoked
			item.ExpiresAt = prev.ExpiresAt
			item.CapabilityRef = prev.CapabilityRef
			if !sameContent(prev, item) {
				item.Revision = prev.Revision + 1
			}
		} else {
			item.ID = "sa_" + randHex(12)
			item.CreatedAt = now()
		}
		item.UpdatedAt = now()
		desired = append(desired, item)
	}

	if sameSet(existing, desired) {
		return current, existing, nil
	}

	keep := map[string]bool{}
	for _, item := range desired {
		keep[item.ID] = true
	}
	for _, prev := range existing {
		if keep[prev.ID] {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM store_actions WHERE id=? AND tenant_id=? AND store_id=?`,
			prev.ID, tenantID, storeID); err != nil {
			return 0, nil, err
		}
	}
	for _, item := range desired {
		if _, err := tx.Exec(`INSERT INTO store_actions(
				id,tenant_id,store_id,catalog_code,platform,kind,variant,label,target_ref,content_binding_ref,
				capability_ref,presentation,sort_order,enabled,revision,revoked,expires_at,legacy_campaign_id,legacy_kind,
				created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET
				catalog_code=excluded.catalog_code, platform=excluded.platform, kind=excluded.kind,
				variant=excluded.variant, label=excluded.label, target_ref=excluded.target_ref,
				content_binding_ref=excluded.content_binding_ref, sort_order=excluded.sort_order,
				enabled=excluded.enabled, revision=excluded.revision, updated_at=excluded.updated_at`,
			item.ID, item.TenantID, item.StoreID, item.CatalogCode, item.Platform, item.Kind, item.Variant,
			item.Label, item.TargetRef, item.ContentBindingRef, item.CapabilityRef, item.Presentation,
			item.SortOrder, boolInt(item.Enabled), item.Revision, boolInt(item.Revoked), item.ExpiresAt,
			item.LegacyCampaignID, item.LegacyKind, item.CreatedAt, item.UpdatedAt); err != nil {
			return 0, nil, err
		}
	}

	next := current + 1
	if _, err := tx.Exec(`INSERT INTO store_action_configs(store_id,config_revision,updated_at) VALUES(?,?,?)
		ON CONFLICT(store_id) DO UPDATE SET config_revision=excluded.config_revision, updated_at=excluded.updated_at`,
		storeID, next, now()); err != nil {
		return 0, nil, err
	}
	payload, err := json.Marshal(snapshotPayload(desired))
	if err != nil {
		return 0, nil, err
	}
	if _, err := tx.Exec(`INSERT INTO store_action_snapshots(id,store_id,config_revision,payload,created_at)
		VALUES(?,?,?,?,?)`, "sas_"+randHex(12), storeID, next, string(payload), now()); err != nil {
		return 0, nil, err
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, err
	}
	s.bumpEpoch()
	return next, desired, nil
}

// ErrUnknownCatalogCode marks a payload catalog_code outside A01-A30.
var ErrUnknownCatalogCode = errors.New("store: unknown catalog_code")

// RestoreActionBindingRefs is the HUI-4584×HUI-3553 wiring rollback (中央集成
// 2026-11-10): after ReplaceStoreActions has committed but BindActionContent
// failed, the transitional content_binding_ref column goes back to the
// PRE-ROUND value per action. ids missing from refs are reset to the empty
// string — a row
// born in the failed round must not keep a reference that was never validated.
// Deliberately no revision bump and no snapshot: the replace-level revision
// already recorded the round; only the transitional read copy is reverted
// (the authoritative index lives in store_action_content_bindings).
func (s *Store) RestoreActionBindingRefs(tenantID, storeID string, refs map[string]string) error {
	rows, err := s.listRows(tenantID, storeID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		want := strings.TrimSpace(refs[row.ID]) // missing id → ""
		if want == row.ContentBindingRef {
			continue
		}
		if _, err := s.DB.Exec(
			`UPDATE store_actions SET content_binding_ref=?, updated_at=? WHERE id=? AND tenant_id=? AND store_id=?`,
			want, now(), row.ID, tenantID, storeID); err != nil {
			return err
		}
	}
	return nil
}

func snapshotPayload(items []StoreAction) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"action_id": item.ID, "catalog_code": item.CatalogCode, "platform": item.Platform,
			"kind": item.Kind, "variant": item.Variant, "label": item.Label, "target_ref": item.TargetRef,
			"content_binding_ref": item.ContentBindingRef, "order": item.SortOrder,
			"enabled": item.Enabled, "revision": item.Revision,
		})
	}
	return out
}

func currentRevision(tx *sql.Tx, storeID string) (int, error) {
	var rev int
	err := tx.QueryRow(`SELECT config_revision FROM store_action_configs WHERE store_id=?`, storeID).Scan(&rev)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return rev, err
}

func loadRowsInTx(tx *sql.Tx, tenantID, storeID string) ([]StoreAction, error) {
	rs, err := tx.Query(`SELECT id,catalog_code,platform,kind,variant,label,target_ref,content_binding_ref,
		capability_ref,presentation,sort_order,enabled,revision,revoked,expires_at,legacy_campaign_id,legacy_kind,
		created_at,updated_at
		FROM store_actions WHERE tenant_id=? AND store_id=? ORDER BY sort_order, created_at, id`, tenantID, storeID)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []StoreAction
	for rs.Next() {
		item, scanErr := scanStoreAction(rs)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rs.Err()
}

func existingByID(items []StoreAction, id string) (StoreAction, bool) {
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return StoreAction{}, false
}

// sameContent compares the merchant-editable surface (legacy/revoke/expiry
// state is preserved untouched on update, so it is not part of the compare).
func sameContent(a, b StoreAction) bool {
	return a.CatalogCode == b.CatalogCode && a.Platform == b.Platform && a.Kind == b.Kind &&
		a.Variant == b.Variant && a.Label == b.Label && a.TargetRef == b.TargetRef &&
		a.ContentBindingRef == b.ContentBindingRef && a.SortOrder == b.SortOrder && a.Enabled == b.Enabled
}

// sameSet reports whether the existing rows equal the desired set (id + full
// content + per-item revision). Used for the idempotent replay fast path.
func sameSet(existing, desired []StoreAction) bool {
	if len(existing) != len(desired) {
		return false
	}
	byID := map[string]StoreAction{}
	for _, item := range existing {
		byID[item.ID] = item
	}
	for _, item := range desired {
		prev, ok := byID[item.ID]
		if !ok || !sameContent(prev, item) || prev.Revision != item.Revision {
			return false
		}
	}
	return true
}

// StoreActionScope resolves the trusted tenant+store that own an action id.
// The id alone is NOT an authorization: callers must still authorize the
// resolved scope. Unknown ids answer ErrNotFound (uniform, no existence leak).
func (s *Store) StoreActionScope(actionID string) (string, string, error) {
	var tenantID, storeID string
	err := s.DB.QueryRow(`SELECT tenant_id,store_id FROM store_actions WHERE id=?`, actionID).
		Scan(&tenantID, &storeID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	return tenantID, storeID, nil
}

// GetStoreActionByID resolves one action strictly within its tenant+store
// (used by the validate surface: the action id is NOT an authorization).
func (s *Store) GetStoreActionByID(tenantID, storeID, actionID string) (StoreAction, error) {
	var item StoreAction
	rs, err := s.DB.Query(`SELECT id,catalog_code,platform,kind,variant,label,target_ref,content_binding_ref,
		capability_ref,presentation,sort_order,enabled,revision,revoked,expires_at,legacy_campaign_id,legacy_kind,
		created_at,updated_at
		FROM store_actions WHERE tenant_id=? AND store_id=? AND id=?`, tenantID, storeID, actionID)
	if err != nil {
		return StoreAction{}, err
	}
	defer rs.Close()
	if !rs.Next() {
		return StoreAction{}, ErrNotFound
	}
	item, err = scanStoreAction(rs)
	if err != nil {
		return StoreAction{}, err
	}
	return item, nil
}

// ListPublicStoreActions is the public projection source: only rows that may
// be offered to a guest right now. Anything else stays invisible.
func (s *Store) ListPublicStoreActions(storeID string, now time.Time) ([]StoreAction, error) {
	rs, err := s.DB.Query(`SELECT id,catalog_code,platform,kind,variant,label,target_ref,content_binding_ref,
		capability_ref,presentation,sort_order,enabled,revision,revoked,expires_at,legacy_campaign_id,legacy_kind,
		created_at,updated_at
		FROM store_actions WHERE store_id=? ORDER BY sort_order, created_at, id`, storeID)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []StoreAction
	for rs.Next() {
		item, scanErr := scanStoreAction(rs)
		if scanErr != nil {
			return nil, scanErr
		}
		if !publicVisible(item, now) {
			continue
		}
		out = append(out, item)
	}
	return out, rs.Err()
}

func publicVisible(item StoreAction, now time.Time) bool {
	if item.CatalogCode == "" || !item.Enabled || item.Revoked {
		return false
	}
	if actionExpired(item.ExpiresAt, now) {
		return false
	}
	// 目标必须已是公网 https（保存门已保证非 legacy 行；legacy 行照此收口）。
	if item.TargetRef == "" || !extrajump.OfficialAddress(item.TargetRef) {
		return false
	}
	return true
}

func actionExpired(raw string, now time.Time) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return true
		}
	}
	return !t.After(now)
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("store: entropy unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}
