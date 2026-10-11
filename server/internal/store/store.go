// Package store is the SQL layer. Every business query is tenant-scoped and
// fully parameterized; ids and short codes are random; timestamps are RFC3339
// UTC. The only global lookup is ResolveLink (public route by opaque code).
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/campaign"
)

// ErrNotFound is returned when a row does not exist for the given scope.
var ErrNotFound = errors.New("store: not found")

// Store wraps the sqlite handle.
type Store struct {
	DB *sql.DB
	// epoch is the structural generation (HUI-2981): every management write
	// to a table the cached read model consumes (tenants, stores, campaigns,
	// campaign_links, lead_forms, nfc_tags) bumps it. Cache entries carry the
	// epoch they were loaded under, so an admin edit deterministically
	// invalidates every affected entry before the next request is served.
	// Factual writes (public_view_stats, lead_submissions) never bump it:
	// their staleness is bounded by cache TTL instead.
	epoch atomic.Uint64
}

func New(db *sql.DB) *Store { return &Store{DB: db} }

// Epoch is the current structural generation.
func (s *Store) Epoch() uint64 { return s.epoch.Load() }

// bumpEpoch advances the structural generation after a successful write.
func (s *Store) bumpEpoch() { s.epoch.Add(1) }

func newID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("store: entropy unavailable: " + err.Error())
	}
	return prefix + hex.EncodeToString(b[:])
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// ---- tenants / members ------------------------------------------------------

type Tenant struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CreatedAt  string `json:"created_at"`
	BrandID    string `json:"brand_id,omitempty"`
	Lifecycle  string `json:"lifecycle,omitempty"`
	ChargeHold bool   `json:"charge_hold,omitempty"`
}

func (s *Store) CreateTenant(name string) (Tenant, error) {
	t := Tenant{ID: newID("tnt_"), Name: name, CreatedAt: now(), Lifecycle: "active"}
	_, err := s.DB.Exec(`INSERT INTO tenants(id,name,created_at) VALUES(?,?,?)`, t.ID, t.Name, t.CreatedAt)
	if err == nil {
		s.bumpEpoch()
	}
	return t, err
}

func (s *Store) GetTenant(id string) (Tenant, error) {
	var t Tenant
	var hold int
	err := s.DB.QueryRow(`SELECT id,name,created_at,brand_id,lifecycle,charge_hold FROM tenants WHERE id=?`, id).
		Scan(&t.ID, &t.Name, &t.CreatedAt, &t.BrandID, &t.Lifecycle, &hold)
	if errors.Is(err, sql.ErrNoRows) {
		return Tenant{}, ErrNotFound
	}
	t.ChargeHold = hold == 1
	return t, err
}

type Member struct {
	ID           string `json:"id"`
	TenantID     string `json:"tenant_id"`
	PrincipalRef string `json:"principal_ref"`
	Role         string `json:"role"`
	// StoreScope: "" / NULL = 总部(全门店);非空 = 仅该门店(store id)。
	// HUI-1674:服务端作用域事实,只有 store_manager 行允许非空。
	StoreScope  string `json:"store_scope,omitempty"`
	Enabled     bool   `json:"enabled"`
	DisplayName string `json:"display_name"`
	CreatedBy   string `json:"created_by"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

const memberCols = `id,tenant_id,principal_ref,role,store_scope,enabled,display_name,created_by,created_at,updated_at`

func (s *Store) CreateMember(tenantID, principalRef, role, displayName, createdBy string, enabled bool) (Member, error) {
	return s.CreateMemberScoped(tenantID, principalRef, role, displayName, createdBy, enabled, "")
}

// CreateMemberScoped is CreateMember with an explicit store scope (""=总部/全门店).
func (s *Store) CreateMemberScoped(tenantID, principalRef, role, displayName, createdBy string, enabled bool, storeScope string) (Member, error) {
	m := Member{
		ID: newID("mem_"), TenantID: tenantID, PrincipalRef: principalRef,
		Role: role, Enabled: enabled, DisplayName: displayName,
		StoreScope: storeScope,
		CreatedBy:  createdBy, CreatedAt: now(), UpdatedAt: now(),
	}
	_, err := s.DB.Exec(
		`INSERT INTO members(id,tenant_id,principal_ref,role,store_scope,enabled,display_name,created_by,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.TenantID, m.PrincipalRef, m.Role, nullable(m.StoreScope), boolInt(m.Enabled), m.DisplayName, m.CreatedBy, m.CreatedAt, m.UpdatedAt)
	return m, err
}

func (s *Store) GetMemberByPrincipal(tenantID, principalRef string) (Member, error) {
	row := s.DB.QueryRow(
		`SELECT `+memberCols+` FROM members WHERE tenant_id=? AND principal_ref=?`, tenantID, principalRef)
	return scanMember(row)
}

func (s *Store) GetMember(id string) (Member, error) {
	row := s.DB.QueryRow(`SELECT `+memberCols+` FROM members WHERE id=?`, id)
	return scanMember(row)
}

func scanMember(row *sql.Row) (Member, error) {
	var m Member
	var enabled int
	var storeScope sql.NullString
	err := row.Scan(&m.ID, &m.TenantID, &m.PrincipalRef, &m.Role, &storeScope, &enabled, &m.DisplayName, &m.CreatedBy, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Member{}, ErrNotFound
	}
	if err != nil {
		return Member{}, err
	}
	m.Enabled = enabled == 1
	m.StoreScope = storeScope.String
	return m, nil
}

// UpdateMember changes role/enabled/display_name only. principal_ref is
// immutable by construction: there is no parameter for it here.
func (s *Store) UpdateMember(id string, role *string, enabled *bool, displayName *string) (Member, error) {
	return s.UpdateMemberScoped(id, role, enabled, displayName, nil)
}

// UpdateMemberScoped additionally allows moving/clearing the store scope
// (storeScope nil=unchanged, pointer to ""=clear, pointer to id=rebind).
func (s *Store) UpdateMemberScoped(id string, role *string, enabled *bool, displayName *string, storeScope *string) (Member, error) {
	cur, err := s.GetMember(id)
	if err != nil {
		return Member{}, err
	}
	if role != nil {
		cur.Role = *role
	}
	if enabled != nil {
		cur.Enabled = *enabled
	}
	if displayName != nil {
		cur.DisplayName = *displayName
	}
	if storeScope != nil {
		cur.StoreScope = *storeScope
	}
	cur.UpdatedAt = now()
	_, err = s.DB.Exec(`UPDATE members SET role=?,enabled=?,display_name=?,store_scope=?,updated_at=? WHERE id=?`,
		cur.Role, boolInt(cur.Enabled), cur.DisplayName, nullable(cur.StoreScope), cur.UpdatedAt, cur.ID)
	return cur, err
}

// ListMembershipsByPrincipal returns the principal's enabled memberships.
// Disabled rows and other principals are not included.
func (s *Store) ListMembershipsByPrincipal(principalRef string) ([]Member, error) {
	rows, err := s.DB.Query(
		`SELECT `+memberCols+` FROM members WHERE principal_ref=? AND enabled=1 ORDER BY created_at`, principalRef)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Member, 0)
	for rows.Next() {
		var m Member
		var enabled int
		var storeScope sql.NullString
		if err := rows.Scan(&m.ID, &m.TenantID, &m.PrincipalRef, &m.Role, &storeScope, &enabled, &m.DisplayName, &m.CreatedBy, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		m.Enabled = enabled == 1
		m.StoreScope = storeScope.String
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) ListMembers(tenantID string) ([]Member, error) {
	rows, err := s.DB.Query(
		`SELECT `+memberCols+` FROM members WHERE tenant_id=? ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Member, 0)
	for rows.Next() {
		var m Member
		var enabled int
		var storeScope sql.NullString
		if err := rows.Scan(&m.ID, &m.TenantID, &m.PrincipalRef, &m.Role, &storeScope, &enabled, &m.DisplayName, &m.CreatedBy, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		m.Enabled = enabled == 1
		m.StoreScope = storeScope.String
		out = append(out, m)
	}
	return out, rows.Err()
}

// ---- stores (门店) ----------------------------------------------------------

// Store lifecycle (HUI-1674). 停用门店 = 禁止新建活动 + 公共页标注;存量活动/
// 短码/标签逐个显式处理,存储层绝不级联改动。
const (
	StoreStatusActive   = "active"
	StoreStatusDisabled = "disabled"
)

type StoreRecord struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	Name      string `json:"name"`
	Address   string `json:"address"`
	Status    string `json:"status"`
	CreatedBy string `json:"created_by"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

const storeCols = `id,tenant_id,name,address,status,created_by,created_at,updated_at`

func (s *Store) CreateStore(tenantID, name, address, createdBy string) (StoreRecord, error) {
	r := StoreRecord{ID: newID("sto_"), TenantID: tenantID, Name: name, Address: address,
		Status:    StoreStatusActive,
		CreatedBy: createdBy, CreatedAt: now(), UpdatedAt: now()}
	_, err := s.DB.Exec(
		`INSERT INTO stores(`+storeCols+`) VALUES(?,?,?,?,?,?,?,?)`,
		r.ID, r.TenantID, r.Name, r.Address, r.Status, r.CreatedBy, r.CreatedAt, r.UpdatedAt)
	if err == nil {
		s.bumpEpoch()
	}
	return r, err
}

func (s *Store) GetStore(id, tenantID string) (StoreRecord, error) {
	row := s.DB.QueryRow(`SELECT `+storeCols+` FROM stores WHERE id=? AND tenant_id=?`, id, tenantID)
	var r StoreRecord
	err := row.Scan(&r.ID, &r.TenantID, &r.Name, &r.Address, &r.Status, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return StoreRecord{}, ErrNotFound
	}
	return r, err
}

// UpdateStore changes name/address only (org_owner surface). nil = unchanged.
func (s *Store) UpdateStore(id, tenantID string, name, address *string) (StoreRecord, error) {
	cur, err := s.GetStore(id, tenantID)
	if err != nil {
		return StoreRecord{}, err
	}
	if name != nil {
		cur.Name = *name
	}
	if address != nil {
		cur.Address = *address
	}
	cur.UpdatedAt = now()
	_, err = s.DB.Exec(`UPDATE stores SET name=?,address=?,updated_at=? WHERE id=? AND tenant_id=?`,
		cur.Name, cur.Address, cur.UpdatedAt, id, tenantID)
	if err == nil {
		s.bumpEpoch()
	}
	return cur, err
}

// SetStoreStatus flips the store lifecycle flag. It NEVER cascades: campaigns,
// links and tags of the store keep their own state (逐个显式处理).
func (s *Store) SetStoreStatus(id, tenantID string, status string) (StoreRecord, error) {
	if status != StoreStatusActive && status != StoreStatusDisabled {
		return StoreRecord{}, fmt.Errorf("store: bad status %q", status)
	}
	if _, err := s.GetStore(id, tenantID); err != nil {
		return StoreRecord{}, err
	}
	_, err := s.DB.Exec(`UPDATE stores SET status=?,updated_at=? WHERE id=? AND tenant_id=?`,
		status, now(), id, tenantID)
	if err != nil {
		return StoreRecord{}, err
	}
	s.bumpEpoch()
	return s.GetStore(id, tenantID)
}

func (s *Store) ListStores(tenantID string) ([]StoreRecord, error) {
	rows, err := s.DB.Query(`SELECT `+storeCols+` FROM stores WHERE tenant_id=? ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]StoreRecord, 0)
	for rows.Next() {
		var r StoreRecord
		if err := rows.Scan(&r.ID, &r.TenantID, &r.Name, &r.Address, &r.Status, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- campaigns --------------------------------------------------------------

type Campaign struct {
	ID            string `json:"id"`
	TenantID      string `json:"tenant_id"`
	Title         string `json:"title"`
	PublicContent string `json:"public_content"`
	Status        string `json:"status"`
	StartsAt      string `json:"starts_at"`
	EndsAt        string `json:"ends_at"`
	StoreID       string `json:"store_id,omitempty"`
	// OrderRef is an optional OPAQUE source-private reference (order-handoff v1
	// semantics). This app never interprets it. Omitted from every public view.
	OrderRef  string `json:"order_ref,omitempty"`
	CreatedBy string `json:"created_by"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

const campaignCols = `id,tenant_id,title,public_content,status,starts_at,ends_at,store_id,order_ref,created_by,created_at,updated_at`

func scanCampaign(sc interface{ Scan(...any) error }) (Campaign, error) {
	var c Campaign
	var storeID, orderRef sql.NullString
	err := sc.Scan(&c.ID, &c.TenantID, &c.Title, &c.PublicContent, &c.Status, &c.StartsAt, &c.EndsAt,
		&storeID, &orderRef, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return Campaign{}, err
	}
	c.StoreID, c.OrderRef = storeID.String, orderRef.String
	return c, nil
}

type NewCampaign struct {
	TenantID      string
	Title         string
	PublicContent string
	StartsAt      string
	EndsAt        string
	StoreID       string
	OrderRef      string // optional; empty = NULL (无订单活动)
	CreatedBy     string
}

func (s *Store) CreateCampaign(n NewCampaign) (Campaign, error) {
	c := Campaign{
		ID: newID("cmp_"), TenantID: n.TenantID, Title: n.Title, PublicContent: n.PublicContent,
		Status: string(campaign.StatusDraft), StartsAt: n.StartsAt, EndsAt: n.EndsAt,
		StoreID: n.StoreID, CreatedBy: n.CreatedBy, CreatedAt: now(), UpdatedAt: now(),
	}
	_, err := s.DB.Exec(
		`INSERT INTO campaigns(`+campaignCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.TenantID, c.Title, c.PublicContent, c.Status, c.StartsAt, c.EndsAt,
		nullable(c.StoreID), nullable(n.OrderRef), c.CreatedBy, c.CreatedAt, c.UpdatedAt)
	if err == nil {
		s.bumpEpoch()
	}
	return c, err
}

func (s *Store) GetCampaign(id, tenantID string) (Campaign, error) {
	row := s.DB.QueryRow(`SELECT `+campaignCols+` FROM campaigns WHERE id=? AND tenant_id=?`, id, tenantID)
	c, err := scanCampaign(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Campaign{}, ErrNotFound
	}
	return c, err
}

func (s *Store) ListCampaigns(tenantID string) ([]Campaign, error) {
	rows, err := s.DB.Query(`SELECT `+campaignCols+` FROM campaigns WHERE tenant_id=? ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Campaign, 0)
	for rows.Next() {
		c, err := scanCampaign(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListCampaignsByStore narrows the list to one store (HUI-1674 store_manager
// scope). The tenant is mandatory; the store filter is a bound parameter.
func (s *Store) ListCampaignsByStore(tenantID, storeID string) ([]Campaign, error) {
	rows, err := s.DB.Query(
		`SELECT `+campaignCols+` FROM campaigns WHERE tenant_id=? AND store_id=? ORDER BY created_at DESC`,
		tenantID, storeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Campaign, 0)
	for rows.Next() {
		c, err := scanCampaign(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CampaignPatch carries optional field updates; nil leaves the field unchanged.
// Status changes go through TransitionCampaign, never through this method.
type CampaignPatch struct {
	Title         *string
	PublicContent *string
	StartsAt      *string
	EndsAt        *string
	StoreID       *string
}

func (s *Store) UpdateCampaign(id, tenantID string, p CampaignPatch) (Campaign, error) {
	cur, err := s.GetCampaign(id, tenantID)
	if err != nil {
		return Campaign{}, err
	}
	if p.Title != nil {
		cur.Title = *p.Title
	}
	if p.PublicContent != nil {
		cur.PublicContent = *p.PublicContent
	}
	if p.StartsAt != nil {
		cur.StartsAt = *p.StartsAt
	}
	if p.EndsAt != nil {
		cur.EndsAt = *p.EndsAt
	}
	if p.StoreID != nil {
		cur.StoreID = *p.StoreID
	}
	cur.UpdatedAt = now()
	_, err = s.DB.Exec(
		`UPDATE campaigns SET title=?,public_content=?,starts_at=?,ends_at=?,store_id=?,updated_at=? WHERE id=? AND tenant_id=?`,
		cur.Title, cur.PublicContent, cur.StartsAt, cur.EndsAt, nullable(cur.StoreID), cur.UpdatedAt, id, tenantID)
	if err == nil {
		s.bumpEpoch()
	}
	return cur, err
}

// TransitionCampaign applies a lifecycle change validated by the campaign
// package. Illegal transitions are refused without touching the row.
func (s *Store) TransitionCampaign(id, tenantID string, to campaign.Status) (Campaign, error) {
	cur, err := s.GetCampaign(id, tenantID)
	if err != nil {
		return Campaign{}, err
	}
	from := campaign.Status(cur.Status)
	if _, err := campaign.Transition(from, to); err != nil {
		return Campaign{}, err
	}
	cur.Status = string(to)
	cur.UpdatedAt = now()
	_, err = s.DB.Exec(`UPDATE campaigns SET status=?,updated_at=? WHERE id=? AND tenant_id=?`,
		cur.Status, cur.UpdatedAt, id, tenantID)
	if err == nil {
		s.bumpEpoch()
	}
	return cur, err
}

// ---- campaign links (受控定位短码) --------------------------------------------

type CampaignLink struct {
	ID         string `json:"id"`
	TenantID   string `json:"tenant_id"`
	CampaignID string `json:"campaign_id"`
	Code       string `json:"code"`
	Enabled    bool   `json:"enabled"`
	CreatedBy  string `json:"created_by"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

const linkCols = `id,tenant_id,campaign_id,code,enabled,created_by,created_at,updated_at`

func scanLink(sc interface{ Scan(...any) error }) (CampaignLink, error) {
	var l CampaignLink
	var enabled int
	err := sc.Scan(&l.ID, &l.TenantID, &l.CampaignID, &l.Code, &enabled, &l.CreatedBy, &l.CreatedAt, &l.UpdatedAt)
	if err != nil {
		return CampaignLink{}, err
	}
	l.Enabled = enabled == 1
	return l, nil
}

// CreateLink mints a fresh opaque short code for the campaign. The code is
// generated here (server-side entropy); callers cannot supply one.
func (s *Store) CreateLink(tenantID, campaignID, createdBy string) (CampaignLink, error) {
	if _, err := s.GetCampaign(campaignID, tenantID); err != nil {
		return CampaignLink{}, err
	}
	code, err := campaign.NewShortcode()
	if err != nil {
		return CampaignLink{}, err
	}
	l := CampaignLink{ID: newID("lnk_"), TenantID: tenantID, CampaignID: campaignID, Code: code,
		Enabled: true, CreatedBy: createdBy, CreatedAt: now(), UpdatedAt: now()}
	_, err = s.DB.Exec(
		`INSERT INTO campaign_links(`+linkCols+`) VALUES(?,?,?,?,?,?,?,?)`,
		l.ID, l.TenantID, l.CampaignID, l.Code, boolInt(l.Enabled), l.CreatedBy, l.CreatedAt, l.UpdatedAt)
	if err == nil {
		s.bumpEpoch()
	}
	return l, err
}

func (s *Store) GetLink(id, tenantID string) (CampaignLink, error) {
	row := s.DB.QueryRow(`SELECT `+linkCols+` FROM campaign_links WHERE id=? AND tenant_id=?`, id, tenantID)
	l, err := scanLink(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CampaignLink{}, ErrNotFound
	}
	return l, err
}

func (s *Store) ListLinksByCampaign(tenantID, campaignID string) ([]CampaignLink, error) {
	rows, err := s.DB.Query(
		`SELECT `+linkCols+` FROM campaign_links WHERE tenant_id=? AND campaign_id=? ORDER BY created_at DESC`,
		tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]CampaignLink, 0)
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// SetLinkEnabled stops/restarts a short code. Stopping never deletes the row:
// the mapping stays auditable and restorable.
func (s *Store) SetLinkEnabled(id, tenantID string, enabled bool) (CampaignLink, error) {
	cur, err := s.GetLink(id, tenantID)
	if err != nil {
		return CampaignLink{}, err
	}
	cur.Enabled = enabled
	cur.UpdatedAt = now()
	_, err = s.DB.Exec(`UPDATE campaign_links SET enabled=?,updated_at=? WHERE id=? AND tenant_id=?`,
		boolInt(enabled), cur.UpdatedAt, id, tenantID)
	if err == nil {
		s.bumpEpoch()
	}
	return cur, err
}

// ---- ResolveOutcome: public route five-state resolution -----------------------

// ResolveOutcome is the machine-readable verdict of a public short-code lookup.
//
// Five required states map as:
//
//	有效   -> OutcomeAvailable
//	停用   -> OutcomeLinkDisabled
//	过期   -> OutcomeExpired / OutcomeNotStarted / OutcomeEnded
//	暂停   -> OutcomePaused / OutcomeDraft
//	不存在 -> OutcomeNotFound
type ResolveOutcome string

const (
	OutcomeAvailable    ResolveOutcome = "available"
	OutcomeNotFound     ResolveOutcome = "not_found"
	OutcomeLinkDisabled ResolveOutcome = "link_disabled"
	OutcomeNotStarted   ResolveOutcome = "not_started"
	OutcomeExpired      ResolveOutcome = "expired"
	OutcomePaused       ResolveOutcome = "paused"
	OutcomeDraft        ResolveOutcome = "draft"
	OutcomeEnded        ResolveOutcome = "ended"
)

type ResolvedLink struct {
	Outcome  ResolveOutcome
	Campaign Campaign // valid only when Outcome == OutcomeAvailable
	Link     CampaignLink
	// StoreUnavailable (HUI-1674): the campaign's bound store is disabled.
	// 停用门店不级联下线活动——公共页照常展示,仅附「门店暂不可用」标注。
	// Only ever set together with OutcomeAvailable.
	StoreUnavailable bool
}

// ResolveLink performs the public, unauthenticated short-code lookup.
// A malformed code is indistinguishable from an unknown one (both NotFound) so
// probing cannot enumerate the code space by shape.
func (s *Store) ResolveLink(code string, at time.Time) ResolvedLink {
	if !campaign.ValidShortcode(code) {
		return ResolvedLink{Outcome: OutcomeNotFound}
	}
	row := s.DB.QueryRow(`SELECT `+linkCols+` FROM campaign_links WHERE code=?`, code)
	l, err := scanLink(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ResolvedLink{Outcome: OutcomeNotFound}
	}
	if err != nil {
		// a failed lookup must never look available; surface as not_found and log
		return ResolvedLink{Outcome: OutcomeNotFound}
	}
	c, err := s.GetCampaign(l.CampaignID, l.TenantID)
	if err != nil {
		return ResolvedLink{Outcome: OutcomeNotFound}
	}
	if !l.Enabled {
		return ResolvedLink{Outcome: OutcomeLinkDisabled, Campaign: c, Link: l}
	}
	switch campaign.Status(c.Status) {
	case campaign.StatusActive:
		// window check
		switch w := (campaign.Window{StartsAt: c.StartsAt, EndsAt: c.EndsAt}).ResolveTime(at); w {
		case "expired":
			return ResolvedLink{Outcome: OutcomeExpired, Campaign: c, Link: l}
		case "not_started":
			return ResolvedLink{Outcome: OutcomeNotStarted, Campaign: c, Link: l}
		}
		return ResolvedLink{Outcome: OutcomeAvailable, Campaign: c, Link: l,
			StoreUnavailable: s.storeDisabled(c.TenantID, c.StoreID)}
	case campaign.StatusPaused:
		return ResolvedLink{Outcome: OutcomePaused, Campaign: c, Link: l}
	case campaign.StatusDraft:
		return ResolvedLink{Outcome: OutcomeDraft, Campaign: c, Link: l}
	case campaign.StatusEnded:
		return ResolvedLink{Outcome: OutcomeEnded, Campaign: c, Link: l}
	default:
		return ResolvedLink{Outcome: OutcomeNotFound}
	}
}

// storeDisabled reports whether the store exists and is disabled. Unbound
// campaigns (no store) and lookup failures are never "disabled" (fail open for
// the notice only; routing never depends on it).
func (s *Store) storeDisabled(tenantID, storeID string) bool {
	if storeID == "" {
		return false
	}
	st, err := s.GetStore(storeID, tenantID)
	if err != nil {
		return false
	}
	return st.Status == StoreStatusDisabled
}

// ---- campaign assets (素材引用) -----------------------------------------------

type CampaignAsset struct {
	ID         string `json:"id"`
	TenantID   string `json:"tenant_id"`
	CampaignID string `json:"campaign_id"`
	AssetID    string `json:"asset_id"`
	Version    string `json:"version"`
	CreatedBy  string `json:"created_by"`
	CreatedAt  string `json:"created_at"`
}

const assetCols = `id,tenant_id,campaign_id,asset_id,version,created_by,created_at`

func (s *Store) AddCampaignAsset(tenantID, campaignID, assetID, version, createdBy string) (CampaignAsset, error) {
	if _, err := s.GetCampaign(campaignID, tenantID); err != nil {
		return CampaignAsset{}, err
	}
	a := CampaignAsset{ID: newID("ast_"), TenantID: tenantID, CampaignID: campaignID,
		AssetID: assetID, Version: version, CreatedBy: createdBy, CreatedAt: now()}
	_, err := s.DB.Exec(
		`INSERT INTO campaign_assets(`+assetCols+`) VALUES(?,?,?,?,?,?,?)`,
		a.ID, a.TenantID, a.CampaignID, a.AssetID, a.Version, a.CreatedBy, a.CreatedAt)
	return a, err
}

func (s *Store) ListCampaignAssets(tenantID, campaignID string) ([]CampaignAsset, error) {
	rows, err := s.DB.Query(
		`SELECT `+assetCols+` FROM campaign_assets WHERE tenant_id=? AND campaign_id=? ORDER BY created_at`,
		tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]CampaignAsset, 0)
	for rows.Next() {
		var a CampaignAsset
		if err := rows.Scan(&a.ID, &a.TenantID, &a.CampaignID, &a.AssetID, &a.Version, &a.CreatedBy, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) RemoveCampaignAsset(id, tenantID, campaignID string) error {
	res, err := s.DB.Exec(`DELETE FROM campaign_assets WHERE id=? AND tenant_id=? AND campaign_id=?`, id, tenantID, campaignID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- helpers ---------------------------------------------------------------

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
