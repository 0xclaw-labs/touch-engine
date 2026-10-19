// store_content_packs.go: HUI-3553 活动内容包持久层(additive)。
//
// 纪律:
//   - 版本不可变:内容列零 UPDATE 路径;改文案/改价/源素材更新 = 新开版本;
//     状态迁移(candidate/adopted/retired/blocked)是工作流,不是内容;
//   - 引用必须显式:版本创建时逐张核对引用的 lib_assets 行在同租户存在
//     (跨租户/缺失一律 media_missing,绝不泄露存在性),引用快照
//     (asset_id, asset_ref, sha256) 冻结进版本行;
//   - 回流候选一律落 candidate 新版本,绝不覆盖当前采用版;同幂等键重放 =
//     返回既有行(先查后插,单写连接无竞态),零新建;
//   - 采用必须显式:adopt 覆盖 (campaign, platform) 指针,旧版 retired +
//     superseded 事件;blocked / grant_expired 拒绝(Distributable 单点裁决);
//   - block:原因必填,指针保留(旧参与保留原版本),新分发被 Distributable 拒绝;
//   - 事件表只追加:处理记录永不改写删除;
//   - handoff brief:幂等键去重(同一交接重试不新建制作工程),引擎未接由
//     HTTP 层如实作答,本层只负责落库与重放。

package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
)

// Sentinel errors for the content-pack uniqueness contracts.
var (
	ErrDuplicateContentPack = errors.New("store: duplicate content pack")
	ErrDuplicatePackVersion = errors.New("store: duplicate pack version")
)

// ---- content packs --------------------------------------------------------------

// ContentPack is one campaign-bound bundle of adoptable versions.
type ContentPack struct {
	ID         string `json:"id"`
	TenantID   string `json:"tenant_id"`
	CampaignID string `json:"campaign_id"`
	StoreID    string `json:"store_id,omitempty"`
	Title      string `json:"title"`
	CreatedBy  string `json:"created_by"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

const contentPackCols = `id,tenant_id,campaign_id,store_id,title,created_by,created_at,updated_at`

func scanContentPack(sc interface{ Scan(...any) error }) (ContentPack, error) {
	var p ContentPack
	var storeID sql.NullString
	err := sc.Scan(&p.ID, &p.TenantID, &p.CampaignID, &storeID, &p.Title, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt)
	p.StoreID = storeID.String
	return p, err
}

// CreateContentPack registers a bundle for a campaign in the same tenant.
// (tenant, campaign, title) unique -> ErrDuplicateContentPack.
func (s *Store) CreateContentPack(tenantID, campaignID, storeID, title, createdBy string) (ContentPack, error) {
	title = strings.TrimSpace(title)
	if title == "" || len(title) > contentpack.MaxTitle {
		return ContentPack{}, contentpack.NewValidationError(contentpack.ReasonBadTitle, "title")
	}
	if _, err := s.GetCampaign(campaignID, tenantID); err != nil {
		return ContentPack{}, err
	}
	if storeID != "" {
		if _, err := s.GetStore(storeID, tenantID); err != nil {
			return ContentPack{}, err
		}
	}
	var existing int
	if err := s.DB.QueryRow(
		`SELECT COUNT(1) FROM content_packs WHERE tenant_id=? AND campaign_id=? AND title=?`,
		tenantID, campaignID, title).Scan(&existing); err != nil {
		return ContentPack{}, err
	}
	if existing > 0 {
		return ContentPack{}, ErrDuplicateContentPack
	}
	p := ContentPack{
		ID: newID("cpk_"), TenantID: tenantID, CampaignID: campaignID, StoreID: storeID,
		Title: title, CreatedBy: createdBy, CreatedAt: now(), UpdatedAt: now(),
	}
	_, err := s.DB.Exec(
		`INSERT INTO content_packs(`+contentPackCols+`) VALUES(?,?,?,?,?,?,?,?)`,
		p.ID, p.TenantID, p.CampaignID, nullable(p.StoreID), p.Title, p.CreatedBy, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return ContentPack{}, err
	}
	return p, nil
}

// GetContentPack is tenant-scoped; foreign ids are ErrNotFound.
func (s *Store) GetContentPack(id, tenantID string) (ContentPack, error) {
	p, err := scanContentPack(s.DB.QueryRow(
		`SELECT `+contentPackCols+` FROM content_packs WHERE id=? AND tenant_id=?`, id, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return ContentPack{}, ErrNotFound
	}
	return p, err
}

// ListContentPacks returns the campaign's packs oldest-first (campaignID "" =
// the whole tenant, oldest-first).
func (s *Store) ListContentPacks(tenantID, campaignID string) ([]ContentPack, error) {
	q := `SELECT ` + contentPackCols + ` FROM content_packs WHERE tenant_id=?`
	args := []any{tenantID}
	if campaignID != "" {
		q += ` AND campaign_id=?`
		args = append(args, campaignID)
	}
	q += ` ORDER BY created_at, id`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ContentPack, 0)
	for rows.Next() {
		p, err := scanContentPack(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---- versions (不可变) -------------------------------------------------------------

// ContentPackVersion is one immutable adoptable version. Content columns are
// written once at creation and never updated.
type ContentPackVersion struct {
	ID               string                        `json:"id"`
	TenantID         string                        `json:"tenant_id"`
	PackID           string                        `json:"pack_id"`
	VersionNo        int                           `json:"version_no"`
	ContentKind      contentpack.ContentKind       `json:"content_kind"`
	Status           contentpack.VersionStatus     `json:"status"`
	Source           contentpack.Source            `json:"source"`
	Video            contentpack.AssetSnapshot     `json:"video"`
	Cover            contentpack.AssetSnapshot     `json:"cover"`
	ImageText        []contentpack.AssetSnapshot   `json:"image_text"`
	Title            string                        `json:"title"`
	Body             string                        `json:"body"`
	Topics           []string                      `json:"topics"`
	StoreName        string                        `json:"store_name"`
	PriceNote        string                        `json:"price_note"`
	ValidNote        string                        `json:"valid_note"`
	PlatformVersions []contentpack.PlatformVersion `json:"platform_versions"`
	GrantRefs        []string                      `json:"grant_refs"`
	GrantUntil       string                        `json:"grant_until"`
	Marking          contentpack.Marking           `json:"marking"`
	BlockedReason    string                        `json:"blocked_reason,omitempty"`
	IdempotencyKey   string                        `json:"idempotency_key,omitempty"`
	CreatedBy        string                        `json:"created_by"`
	CreatedAt        string                        `json:"created_at"`
}

const contentPackVersionCols = `id,tenant_id,pack_id,version_no,content_kind,status,source,` +
	`video_asset_id,video_asset_ref,video_sha256,cover_asset_id,cover_asset_ref,cover_sha256,` +
	`image_text,title,body,topics,store_name,price_note,valid_note,platform_versions,` +
	`grant_refs,grant_until,marking,blocked_reason,idempotency_key,created_by,created_at`

func scanContentPackVersion(sc interface{ Scan(...any) error }) (ContentPackVersion, error) {
	var v ContentPackVersion
	var imageText, topics, platformVersions, grantRefs, marking string
	var idemKey sql.NullString
	err := sc.Scan(&v.ID, &v.TenantID, &v.PackID, &v.VersionNo, &v.ContentKind, &v.Status, &v.Source,
		&v.Video.AssetID, &v.Video.AssetRef, &v.Video.SHA256,
		&v.Cover.AssetID, &v.Cover.AssetRef, &v.Cover.SHA256,
		&imageText, &v.Title, &v.Body, &topics, &v.StoreName, &v.PriceNote, &v.ValidNote,
		&platformVersions, &grantRefs, &v.GrantUntil, &marking, &v.BlockedReason, &idemKey,
		&v.CreatedBy, &v.CreatedAt)
	if err != nil {
		return ContentPackVersion{}, err
	}
	v.ImageText = unmarshalSnapshots(imageText)
	v.Topics = unmarshalTags(topics)
	v.PlatformVersions = unmarshalPlatformVersions(platformVersions)
	v.GrantRefs = unmarshalTags(grantRefs)
	v.Marking = unmarshalMarking(marking)
	v.IdempotencyKey = idemKey.String
	return v, nil
}

func marshalJSONDefault(v any, empty string) string {
	b, err := json.Marshal(v)
	if err != nil {
		return empty
	}
	return string(b)
}

func unmarshalSnapshots(raw string) []contentpack.AssetSnapshot {
	out := []contentpack.AssetSnapshot{}
	if raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	if out == nil {
		out = []contentpack.AssetSnapshot{}
	}
	return out
}

func unmarshalPlatformVersions(raw string) []contentpack.PlatformVersion {
	out := []contentpack.PlatformVersion{}
	if raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	if out == nil {
		out = []contentpack.PlatformVersion{}
	}
	return out
}

func unmarshalMarking(raw string) contentpack.Marking {
	if raw == "" {
		return contentpack.Marking{}
	}
	var m contentpack.Marking
	_ = json.Unmarshal([]byte(raw), &m)
	return m
}

// freezeAsset resolves the lib asset row in the same tenant and returns the
// frozen snapshot (missing/cross-tenant -> media_missing, 存在性不泄露).
func (s *Store) freezeAsset(tenantID, assetID string) (contentpack.AssetSnapshot, error) {
	a, err := s.GetLibAsset(assetID, tenantID)
	if errors.Is(err, ErrNotFound) {
		return contentpack.AssetSnapshot{}, contentpack.NewValidationError(contentpack.ReasonMediaMissing, "asset_ref")
	}
	if err != nil {
		return contentpack.AssetSnapshot{}, err
	}
	return contentpack.AssetSnapshot{AssetID: a.ID, AssetRef: a.AssetRef, SHA256: a.SHA256}, nil
}

// resolveSpec fills every referenced asset snapshot from lib_assets (same
// tenant; missing/cross-tenant -> media_missing, 商家面只传素材 ID 不传技术
// 字段), THEN runs the domain creation matrix over the RESOLVED spec
// (引用级快照先落定,声明矩阵再裁决 —— 同 UpsertCampaignRules 复用域校验纪律).
func (s *Store) resolveSpec(tenantID string, spec contentpack.VersionSpec) (contentpack.VersionSpec, error) {
	if spec.ContentKind == contentpack.ContentKindVideo && strings.TrimSpace(spec.Video.AssetID) != "" {
		video, err := s.freezeAsset(tenantID, spec.Video.AssetID)
		if err != nil {
			return contentpack.VersionSpec{}, err
		}
		spec.Video = video
	}
	if spec.ContentKind == contentpack.ContentKindVideo && strings.TrimSpace(spec.Cover.AssetID) != "" {
		cover, err := s.freezeAsset(tenantID, spec.Cover.AssetID)
		if err != nil {
			return contentpack.VersionSpec{}, err
		}
		spec.Cover = cover
	}
	for i := range spec.ImageText {
		if strings.TrimSpace(spec.ImageText[i].AssetID) == "" {
			continue
		}
		img, err := s.freezeAsset(tenantID, spec.ImageText[i].AssetID)
		if err != nil {
			return contentpack.VersionSpec{}, err
		}
		spec.ImageText[i] = img
	}
	spec.Title = contentpack.NormalizeText(spec.Title)
	spec.Body = contentpack.NormalizeText(spec.Body)
	spec.StoreName = contentpack.NormalizeText(spec.StoreName)
	spec.PriceNote = contentpack.NormalizeText(spec.PriceNote)
	spec.ValidNote = contentpack.NormalizeText(spec.ValidNote)
	spec.Topics = contentpack.NormalizeTopics(spec.Topics)
	spec.GrantUntil = contentpack.NormalizeText(spec.GrantUntil)
	spec.IdempotencyKey = contentpack.NormalizeText(spec.IdempotencyKey)
	if err := contentpack.ValidateVersionSpec(spec); err != nil {
		return contentpack.VersionSpec{}, err
	}
	return spec, nil
}

// CreateContentPackVersion freezes one new immutable version (draft or
// candidate at creation). The version number is per-pack monotonic.
func (s *Store) CreateContentPackVersion(tenantID, packID string, spec contentpack.VersionSpec, status contentpack.VersionStatus, createdBy string) (ContentPackVersion, error) {
	if status != contentpack.StatusDraft && status != contentpack.StatusCandidate {
		return ContentPackVersion{}, contentpack.NewValidationError(contentpack.ReasonNotDistributable, "status")
	}
	pack, err := s.GetContentPack(packID, tenantID)
	if err != nil {
		return ContentPackVersion{}, err
	}
	spec, err = s.resolveSpec(tenantID, spec)
	if err != nil {
		return ContentPackVersion{}, err
	}
	if spec.IdempotencyKey != "" {
		if _, err := s.versionByIdempotencyKey(tenantID, spec.IdempotencyKey); err == nil {
			return ContentPackVersion{}, ErrDuplicatePackVersion
		} else if !errors.Is(err, ErrNotFound) {
			return ContentPackVersion{}, err
		}
	}
	v, err := s.insertVersion(tenantID, pack, spec, status, createdBy, "created")
	if err != nil {
		return ContentPackVersion{}, err
	}
	return v, nil
}

// AddReturnCandidate records an ecosystem return (HUI-1745 接线点) as a
// CANDIDATE version — never overwriting the current adoption. Same
// idempotency key replays the existing candidate row (同一回流重试不新建).
func (s *Store) AddReturnCandidate(tenantID, packID string, spec contentpack.VersionSpec, actor string) (ContentPackVersion, bool, error) {
	if contentpack.Source(contentpack.NormalizeText(string(spec.Source))) != contentpack.SourceProductPhotoReturn {
		return ContentPackVersion{}, false, contentpack.NewValidationError(contentpack.ReasonBadSource, "source")
	}
	if err := contentpack.ValidateIdempotencyKey(spec.IdempotencyKey); err != nil {
		return ContentPackVersion{}, false, err
	}
	key := contentpack.NormalizeText(spec.IdempotencyKey)
	// 1) replay: same key returns the frozen row (identical retry, zero insert).
	cur, err := s.versionByIdempotencyKey(tenantID, key)
	switch {
	case err == nil:
		if cur.PackID != packID {
			return ContentPackVersion{}, false, contentpack.NewValidationError(contentpack.ReasonBadIdempotencyKey, "idempotency_key")
		}
		return cur, true, nil
	case !errors.Is(err, ErrNotFound):
		return ContentPackVersion{}, false, err
	}
	// 2) fresh candidate.
	pack, err := s.GetContentPack(packID, tenantID)
	if err != nil {
		return ContentPackVersion{}, false, err
	}
	spec, err = s.resolveSpec(tenantID, spec)
	if err != nil {
		return ContentPackVersion{}, false, err
	}
	v, err := s.insertVersion(tenantID, pack, spec, contentpack.StatusCandidate, actor, "returned")
	if errors.Is(err, errIdemRace) {
		// concurrent same-key insert: replay wins (单写连接下实际不可达,防御保留)
		cur, rerr := s.versionByIdempotencyKey(tenantID, key)
		if rerr == nil {
			return cur, true, nil
		}
	}
	if err != nil {
		return ContentPackVersion{}, false, err
	}
	return v, false, nil
}

// errIdemRace marks the defensive same-key insert race.
var errIdemRace = errors.New("store: idempotency key race")

func (s *Store) versionByIdempotencyKey(tenantID, key string) (ContentPackVersion, error) {
	v, err := scanContentPackVersion(s.DB.QueryRow(
		`SELECT `+contentPackVersionCols+` FROM content_pack_versions WHERE tenant_id=? AND idempotency_key=?`,
		tenantID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return ContentPackVersion{}, ErrNotFound
	}
	return v, err
}

func (s *Store) nextVersionNo(tenantID, packID string) (int, error) {
	var n any
	if err := s.DB.QueryRow(
		`SELECT MAX(version_no) FROM content_pack_versions WHERE tenant_id=? AND pack_id=?`,
		tenantID, packID).Scan(&n); err != nil {
		return 0, err
	}
	if n == nil {
		return 1, nil
	}
	no, ok := n.(int64)
	if !ok {
		return 0, errors.New("store: bad version_no")
	}
	return int(no) + 1, nil
}

func (s *Store) insertVersion(tenantID string, pack ContentPack, spec contentpack.VersionSpec, status contentpack.VersionStatus, createdBy, eventKind string) (ContentPackVersion, error) {
	no, err := s.nextVersionNo(tenantID, pack.ID)
	if err != nil {
		return ContentPackVersion{}, err
	}
	key := any(nil)
	if spec.IdempotencyKey != "" {
		key = spec.IdempotencyKey
	}
	v := ContentPackVersion{
		ID: newID("cpv_"), TenantID: tenantID, PackID: pack.ID, VersionNo: no,
		ContentKind: spec.ContentKind, Status: status, Source: spec.Source,
		Video: spec.Video, Cover: spec.Cover, ImageText: spec.ImageText,
		Title: spec.Title, Body: spec.Body, Topics: spec.Topics,
		StoreName: spec.StoreName, PriceNote: spec.PriceNote, ValidNote: spec.ValidNote,
		PlatformVersions: spec.PlatformVersions, GrantRefs: spec.GrantRefs, GrantUntil: spec.GrantUntil,
		Marking: spec.Marking, IdempotencyKey: spec.IdempotencyKey,
		CreatedBy: createdBy, CreatedAt: now(),
	}
	_, err = s.DB.Exec(
		`INSERT INTO content_pack_versions(`+contentPackVersionCols+`) VALUES(`+
			strings.Repeat("?,", 27)+"?)",
		v.ID, v.TenantID, v.PackID, v.VersionNo, v.ContentKind, v.Status, v.Source,
		v.Video.AssetID, v.Video.AssetRef, v.Video.SHA256,
		v.Cover.AssetID, v.Cover.AssetRef, v.Cover.SHA256,
		marshalJSONDefault(v.ImageText, "[]"), v.Title, v.Body, marshalJSONDefault(v.Topics, "[]"),
		v.StoreName, v.PriceNote, v.ValidNote, marshalJSONDefault(v.PlatformVersions, "[]"),
		marshalJSONDefault(v.GrantRefs, "[]"), v.GrantUntil, marshalJSONDefault(v.Marking, "{}"),
		"", key, v.CreatedBy, v.CreatedAt)
	if err != nil {
		return ContentPackVersion{}, err
	}
	if err := s.appendVersionEvent(tenantID, v.ID, eventKind, "", createdBy); err != nil {
		return ContentPackVersion{}, err
	}
	return v, nil
}

// GetContentPackVersion is tenant-scoped; foreign ids are ErrNotFound.
func (s *Store) GetContentPackVersion(id, tenantID string) (ContentPackVersion, error) {
	v, err := scanContentPackVersion(s.DB.QueryRow(
		`SELECT `+contentPackVersionCols+` FROM content_pack_versions WHERE id=? AND tenant_id=?`, id, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return ContentPackVersion{}, ErrNotFound
	}
	return v, err
}

// ListContentPackVersions returns the pack's versions oldest-first.
func (s *Store) ListContentPackVersions(tenantID, packID string) ([]ContentPackVersion, error) {
	rows, err := s.DB.Query(
		`SELECT `+contentPackVersionCols+` FROM content_pack_versions WHERE tenant_id=? AND pack_id=? ORDER BY version_no`,
		tenantID, packID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ContentPackVersion, 0)
	for rows.Next() {
		v, err := scanContentPackVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ---- version events (处理记录,只追加) ------------------------------------------------

// ContentPackVersionEvent is one append-only processing record.
type ContentPackVersionEvent struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	VersionID string `json:"version_id"`
	Kind      string `json:"kind"`
	Note      string `json:"note"`
	Actor     string `json:"actor"`
	CreatedAt string `json:"created_at"`
}

func (s *Store) appendVersionEvent(tenantID, versionID, kind, note, actor string) error {
	e := ContentPackVersionEvent{
		ID: newID("cpe_"), TenantID: tenantID, VersionID: versionID,
		Kind: kind, Note: note, Actor: actor, CreatedAt: now(),
	}
	_, err := s.DB.Exec(
		`INSERT INTO content_pack_version_events(id,tenant_id,version_id,kind,note,actor,created_at) VALUES(?,?,?,?,?,?,?)`,
		e.ID, e.TenantID, e.VersionID, e.Kind, e.Note, e.Actor, e.CreatedAt)
	if err != nil {
		return err
	}
	return nil
}

// ListContentPackVersionEvents returns the version's records oldest-first.
func (s *Store) ListContentPackVersionEvents(tenantID, versionID string) ([]ContentPackVersionEvent, error) {
	rows, err := s.DB.Query(
		`SELECT id,tenant_id,version_id,kind,note,actor,created_at FROM content_pack_version_events WHERE tenant_id=? AND version_id=? ORDER BY created_at, id`,
		tenantID, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ContentPackVersionEvent, 0)
	for rows.Next() {
		var e ContentPackVersionEvent
		if err := rows.Scan(&e.ID, &e.TenantID, &e.VersionID, &e.Kind, &e.Note, &e.Actor, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- adoption (活动×平台当前指针) ----------------------------------------------------

// ContentPackAdoption is the CURRENT adopted version pointer for one
// (campaign, platform). Switching versions overwrites the pointer; the old
// version's content row never changes (history in the event ledger).
type ContentPackAdoption struct {
	ID         string `json:"id"`
	TenantID   string `json:"tenant_id"`
	CampaignID string `json:"campaign_id"`
	Platform   string `json:"platform"`
	VersionID  string `json:"version_id"`
	AdoptedBy  string `json:"adopted_by"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

const adoptionCols = `id,tenant_id,campaign_id,platform,version_id,adopted_by,created_at,updated_at`

func scanAdoption(sc interface{ Scan(...any) error }) (ContentPackAdoption, error) {
	var a ContentPackAdoption
	err := sc.Scan(&a.ID, &a.TenantID, &a.CampaignID, &a.Platform, &a.VersionID, &a.AdoptedBy, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

// AdoptContentPackVersion makes version the CURRENT one for (campaign,
// platform). Blocked and grant-expired versions are refused (Distributable
// 单点裁决); the previously adopted version is retired with a superseded
// event; the new one is stamped adopted.
func (s *Store) AdoptContentPackVersion(tenantID, packID, versionID, platform, adoptedBy string, at time.Time) (ContentPackAdoption, error) {
	platform = strings.TrimSpace(platform)
	if platform == "" || len(platform) > contentpack.MaxPlatformName {
		return ContentPackAdoption{}, contentpack.NewValidationError(contentpack.ReasonMissingPlatform, "platform")
	}
	pack, err := s.GetContentPack(packID, tenantID)
	if err != nil {
		return ContentPackAdoption{}, err
	}
	v, err := s.GetContentPackVersion(versionID, tenantID)
	if err != nil {
		return ContentPackAdoption{}, err
	}
	if v.PackID != pack.ID {
		return ContentPackAdoption{}, ErrNotFound
	}
	nowText := at.UTC().Format(time.RFC3339Nano)
	if err := contentpack.Distributable(v.Status, v.GrantUntil, at); err != nil {
		return ContentPackAdoption{}, err
	}

	// previous pointer (if any) -> retire + supersede event
	prev, err := scanAdoption(s.DB.QueryRow(
		`SELECT `+adoptionCols+` FROM content_pack_adoptions WHERE tenant_id=? AND campaign_id=? AND platform=?`,
		tenantID, pack.CampaignID, platform))
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ContentPackAdoption{}, err
	}
	if found && prev.VersionID != v.ID {
		if _, err := s.DB.Exec(
			`UPDATE content_pack_versions SET status=? WHERE id=? AND tenant_id=?`,
			contentpack.StatusRetired, prev.VersionID, tenantID); err != nil {
			return ContentPackAdoption{}, err
		}
		if err := s.appendVersionEvent(tenantID, prev.VersionID, "superseded", "被新采用版本取代:"+v.ID, adoptedBy); err != nil {
			return ContentPackAdoption{}, err
		}
	}
	if found {
		if _, err := s.DB.Exec(
			`UPDATE content_pack_adoptions SET version_id=?,adopted_by=?,updated_at=? WHERE id=? AND tenant_id=?`,
			v.ID, adoptedBy, nowText, prev.ID, tenantID); err != nil {
			return ContentPackAdoption{}, err
		}
		prev.VersionID = v.ID
		prev.AdoptedBy = adoptedBy
		prev.UpdatedAt = nowText
	} else {
		prev = ContentPackAdoption{
			ID: newID("cpa_"), TenantID: tenantID, CampaignID: pack.CampaignID, Platform: platform,
			VersionID: v.ID, AdoptedBy: adoptedBy, CreatedAt: nowText, UpdatedAt: nowText,
		}
		if _, err := s.DB.Exec(
			`INSERT INTO content_pack_adoptions(`+adoptionCols+`) VALUES(?,?,?,?,?,?,?,?)`,
			prev.ID, prev.TenantID, prev.CampaignID, prev.Platform, prev.VersionID, prev.AdoptedBy, prev.CreatedAt, prev.UpdatedAt); err != nil {
			return ContentPackAdoption{}, err
		}
	}
	if v.Status != contentpack.StatusAdopted {
		if _, err := s.DB.Exec(
			`UPDATE content_pack_versions SET status=? WHERE id=? AND tenant_id=?`,
			contentpack.StatusAdopted, v.ID, tenantID); err != nil {
			return ContentPackAdoption{}, err
		}
	}
	if err := s.appendVersionEvent(tenantID, v.ID, "adopted", "平台 "+platform+" 明确采用", adoptedBy); err != nil {
		return ContentPackAdoption{}, err
	}
	return prev, nil
}

// GetAdoptedVersion resolves the CURRENT adopted version for (campaign,
// platform). No adoption -> ErrNotFound. The returned version may be blocked
// or expired: distribution gates re-check via contentpack.Distributable.
func (s *Store) GetAdoptedVersion(tenantID, campaignID, platform string) (ContentPackVersion, error) {
	var versionID string
	err := s.DB.QueryRow(
		`SELECT version_id FROM content_pack_adoptions WHERE tenant_id=? AND campaign_id=? AND platform=?`,
		tenantID, campaignID, strings.TrimSpace(platform)).Scan(&versionID)
	if errors.Is(err, sql.ErrNoRows) {
		return ContentPackVersion{}, ErrNotFound
	}
	if err != nil {
		return ContentPackVersion{}, err
	}
	return s.GetContentPackVersion(versionID, tenantID)
}

// ListAdoptions returns the campaign's platform pointers oldest-first.
func (s *Store) ListAdoptions(tenantID, campaignID string) ([]ContentPackAdoption, error) {
	rows, err := s.DB.Query(
		`SELECT `+adoptionCols+` FROM content_pack_adoptions WHERE tenant_id=? AND campaign_id=? ORDER BY created_at, id`,
		tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ContentPackAdoption, 0)
	for rows.Next() {
		a, err := scanAdoption(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// BlockContentPackVersion marks 撤权/重大内容问题: the pointer is preserved
// (旧参与保留原版本) but new distribution refuses; the reason and actor land
// in the append-only event ledger.
func (s *Store) BlockContentPackVersion(tenantID, packID, versionID, reason, actor string, at time.Time) (ContentPackVersion, error) {
	if err := contentpack.ValidateBlockReason(reason); err != nil {
		return ContentPackVersion{}, err
	}
	pack, err := s.GetContentPack(packID, tenantID)
	if err != nil {
		return ContentPackVersion{}, err
	}
	v, err := s.GetContentPackVersion(versionID, tenantID)
	if err != nil {
		return ContentPackVersion{}, err
	}
	if v.PackID != pack.ID {
		return ContentPackVersion{}, ErrNotFound
	}
	if v.Status == contentpack.StatusBlocked {
		return v, nil // idempotent
	}
	if _, err := s.DB.Exec(
		`UPDATE content_pack_versions SET status=?,blocked_reason=? WHERE id=? AND tenant_id=?`,
		contentpack.StatusBlocked, contentpack.NormalizeText(reason), v.ID, tenantID); err != nil {
		return ContentPackVersion{}, err
	}
	if err := s.appendVersionEvent(tenantID, v.ID, "blocked", contentpack.NormalizeText(reason), actor); err != nil {
		return ContentPackVersion{}, err
	}
	return s.GetContentPackVersion(v.ID, tenantID)
}

// ---- production handoff briefs (幂等去重,引擎未接) ------------------------------------

// HandoffInput is the「帮我制作」brief payload (no file bytes, ever).
type HandoffInput struct {
	Purpose        string
	StoreBrief     string
	DesiredForm    string
	AssetIDs       []string
	IdempotencyKey string
	// MX-OPS R2 (HUI-3553): merchant execution preference + policy reference.
	// Only narrows within the server-authorized template set; no priority field.
	ExecutionPreference string
	PolicyRef           string
	PolicyVersion       int
}

// ContentHandoff is one recorded production-intent brief.
type ContentHandoff struct {
	ID             string   `json:"id"`
	TenantID       string   `json:"tenant_id"`
	CampaignID     string   `json:"campaign_id"`
	Purpose        string   `json:"purpose"`
	StoreBrief     string   `json:"store_brief"`
	DesiredForm    string   `json:"desired_form"`
	AssetIDs       []string `json:"asset_ids"`
	IdempotencyKey string   `json:"idempotency_key,omitempty"`
	CreatedBy      string   `json:"created_by"`
	CreatedAt      string   `json:"created_at"`
	// MX-OPS R2 (HUI-3553): execution preference, policy reference and the
	// authoritative queue projection (public shape only, internal_debug stripped).
	ExecutionPreference string          `json:"execution_preference"`
	PolicyRef           string          `json:"policy_ref,omitempty"`
	PolicyVersion       int             `json:"policy_version,omitempty"`
	JobRef              string          `json:"job_ref,omitempty"`
	JobQueueView        json.RawMessage `json:"job_queue_view,omitempty"`
}

const handoffCols = `id,tenant_id,campaign_id,purpose,store_brief,desired_form,asset_ids,idempotency_key,created_by,created_at,execution_preference,policy_ref,policy_version,job_ref,job_queue_view`

func scanHandoff(sc interface{ Scan(...any) error }) (ContentHandoff, error) {
	var h ContentHandoff
	var idemKey string
	var rawAssets string
	var rawQueue string
	err := sc.Scan(&h.ID, &h.TenantID, &h.CampaignID, &h.Purpose, &h.StoreBrief, &h.DesiredForm, &rawAssets, &idemKey, &h.CreatedBy, &h.CreatedAt,
		&h.ExecutionPreference, &h.PolicyRef, &h.PolicyVersion, &h.JobRef, &rawQueue)
	h.AssetIDs = unmarshalTags(rawAssets)
	h.IdempotencyKey = idemKey
	if strings.TrimSpace(rawQueue) != "" {
		h.JobQueueView = json.RawMessage(rawQueue)
	}
	return h, err
}

// CreateContentHandoff records the brief; the same idempotency key replays the
// existing row (同一交接重试不新建制作工程). The engine is NOT invoked here —
// the HTTP layer answers availability honestly.
func (s *Store) CreateContentHandoff(tenantID, campaignID string, in HandoffInput, createdBy string) (ContentHandoff, bool, error) {
	if err := contentpack.ValidateIdempotencyKey(in.IdempotencyKey); err != nil {
		return ContentHandoff{}, false, err
	}
	if contentpack.NormalizeText(in.Purpose) == "" {
		return ContentHandoff{}, false, contentpack.NewValidationError(contentpack.ReasonMissingTitle, "purpose")
	}
	if _, err := s.GetCampaign(campaignID, tenantID); err != nil {
		return ContentHandoff{}, false, err
	}
	key := contentpack.NormalizeText(in.IdempotencyKey)
	var existing int
	if err := s.DB.QueryRow(
		`SELECT COUNT(1) FROM content_handoffs WHERE tenant_id=? AND idempotency_key=?`,
		tenantID, key).Scan(&existing); err != nil {
		return ContentHandoff{}, false, err
	}
	if existing > 0 {
		h, err := scanHandoff(s.DB.QueryRow(
			`SELECT `+handoffCols+` FROM content_handoffs WHERE tenant_id=? AND idempotency_key=?`, tenantID, key))
		return h, err == nil, err
	}
	// Absent preference stays absent ("unspecified"): do not silently claim the
	// merchant chose immediate. The migration default only backfills old rows.
	preference := contentpack.NormalizeText(in.ExecutionPreference)
	policyVersion := in.PolicyVersion
	if policyVersion < 0 {
		policyVersion = 0
	}
	h := ContentHandoff{
		ID: newID("cho_"), TenantID: tenantID, CampaignID: campaignID,
		Purpose: contentpack.NormalizeText(in.Purpose), StoreBrief: in.StoreBrief,
		DesiredForm: contentpack.NormalizeText(in.DesiredForm),
		AssetIDs:    in.AssetIDs, IdempotencyKey: key, CreatedBy: createdBy, CreatedAt: now(),
		ExecutionPreference: preference, PolicyRef: in.PolicyRef, PolicyVersion: policyVersion,
	}
	_, err := s.DB.Exec(
		`INSERT INTO content_handoffs(`+handoffCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		h.ID, h.TenantID, h.CampaignID, h.Purpose, h.StoreBrief, h.DesiredForm,
		marshalJSONDefault(h.AssetIDs, "[]"), h.IdempotencyKey, h.CreatedBy, h.CreatedAt,
		h.ExecutionPreference, h.PolicyRef, h.PolicyVersion, h.JobRef, "")
	if err != nil {
		return ContentHandoff{}, false, err
	}
	return h, false, nil
}

// GetContentHandoff returns one tenant-scoped brief (ErrNotFound on cross-tenant).
func (s *Store) GetContentHandoff(tenantID, handoffID string) (ContentHandoff, error) {
	h, err := scanHandoff(s.DB.QueryRow(
		`SELECT `+handoffCols+` FROM content_handoffs WHERE tenant_id=? AND id=?`, tenantID, handoffID))
	if errors.Is(err, sql.ErrNoRows) {
		return ContentHandoff{}, ErrNotFound
	}
	return h, err
}

// GetContentHandoffByID returns one handoff WITHOUT tenant scoping (HUI-3553
// W5 trusted-service producer seam). Callers MUST be trusted service producers
// and MUST use the returned row's TenantID for any write — the row is the only
// tenant truth, request headers are never trusted. Unknown id -> ErrNotFound
// (uniform, no existence leak).
func (s *Store) GetContentHandoffByID(handoffID string) (ContentHandoff, error) {
	h, err := scanHandoff(s.DB.QueryRow(
		`SELECT `+handoffCols+` FROM content_handoffs WHERE id=?`, handoffID))
	if errors.Is(err, sql.ErrNoRows) {
		return ContentHandoff{}, ErrNotFound
	}
	return h, err
}

// UpdateContentHandoffQueueView stores the PUBLIC projection of a contract
// job_queue_view (internal_debug already stripped by the caller). It never
// reads or rewrites other handoff fields, and cross-tenant ids are ErrNotFound.
func (s *Store) UpdateContentHandoffQueueView(tenantID, handoffID, jobRef string, publicView []byte) (ContentHandoff, error) {
	res, err := s.DB.Exec(
		`UPDATE content_handoffs SET job_ref=?, job_queue_view=? WHERE tenant_id=? AND id=?`,
		jobRef, string(publicView), tenantID, handoffID)
	if err != nil {
		return ContentHandoff{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return ContentHandoff{}, err
	}
	if n == 0 {
		return ContentHandoff{}, ErrNotFound
	}
	return s.GetContentHandoff(tenantID, handoffID)
}

// ListContentHandoffs returns the campaign's briefs oldest-first.
func (s *Store) ListContentHandoffs(tenantID, campaignID string) ([]ContentHandoff, error) {
	rows, err := s.DB.Query(
		`SELECT `+handoffCols+` FROM content_handoffs WHERE tenant_id=? AND campaign_id=? ORDER BY created_at, id`,
		tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ContentHandoff, 0)
	for rows.Next() {
		h, err := scanHandoff(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
