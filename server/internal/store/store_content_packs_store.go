// store_content_packs_store.go: HUI-3553 门店根内容包持久层(additive)。
//
// 纪律(裁定书):
//   - 门店是根:包归 tenant/store,campaign 可空;无活动门店同样可以组织
//     可采用内容(不为无活动门店强建活动);
//   - 版本不可变:内容列零 UPDATE;媒体快照(media_id/sha/mime/bytes/时长/
//     宽高)在版本创建时从 content_media 实测行冻结,媒体行后续变化不漂移
//     已采用版本;
//   - 媒体解析 fail-closed:同租户且同店才可用;跨租户/跨店/缺失/kind 错配
//     一律 media_missing,绝不泄露存在性;
//   - 采用权威:(tenant, store, platform) 指针,显式采用才写;换版覆盖指针,
//     旧版 retired + superseded 事件,旧版本行内容零变化;
//   - 唯一候选登记事务(AddStoreReturnCandidate,4595 接线点):回流一律落
//     candidate,同幂等键重放返回同一行,绝不覆盖当前采用指针;
//   - 采用后建立绑定(BindActionContent):版本必须已明确采用才允许;
//     content_binding_ref 的写端点归 4584,本方法是其内部调用;
//   - 事件/签发审计只追加,永不改写删除。

package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

// ---- content media (受控真实媒体元数据) ---------------------------------------------

// ContentMedia is one verified real-media row (bytes live in the controlled
// media directory, content-addressed; the database never stores bytes).
type ContentMedia struct {
	ID         string             `json:"id"`
	TenantID   string             `json:"tenant_id"`
	StoreID    string             `json:"store_id"`
	Kind       upload.MediaKind   `json:"kind"`
	MimeType   string             `json:"mime_type"`
	ByteSize   int64              `json:"byte_size"`
	SHA256     string             `json:"sha256"`
	DurationMS int64              `json:"duration_ms"`
	Width      int                `json:"width"`
	Height     int                `json:"height"`
	Filename   string             `json:"filename"`
	MediaRef   string             `json:"media_ref"`
	CreatedBy  string             `json:"created_by"`
	CreatedAt  string             `json:"created_at"`
}

const contentMediaCols = `id,tenant_id,store_id,kind,mime_type,byte_size,sha256,duration_ms,width,height,filename,media_ref,created_by,created_at`

func scanContentMedia(sc interface{ Scan(...any) error }) (ContentMedia, error) {
	var m ContentMedia
	err := sc.Scan(&m.ID, &m.TenantID, &m.StoreID, &m.Kind, &m.MimeType, &m.ByteSize, &m.SHA256,
		&m.DurationMS, &m.Width, &m.Height, &m.Filename, &m.MediaRef, &m.CreatedBy, &m.CreatedAt)
	return m, err
}

// RegisterContentMedia records one verified upload. Same tenant + store +
// sha256 replays the existing row (平台重复取材零新增;跨店不共享,内容归店).
func (s *Store) RegisterContentMedia(tenantID, storeID string, meta upload.MediaMeta, filename, createdBy string) (ContentMedia, bool, error) {
	if _, err := s.GetStore(storeID, tenantID); err != nil {
		return ContentMedia{}, false, err
	}
	filename = strings.TrimSpace(filename)
	var existing int
	if err := s.DB.QueryRow(
		`SELECT COUNT(1) FROM content_media WHERE tenant_id=? AND store_id=? AND sha256=?`,
		tenantID, storeID, meta.SHA256).Scan(&existing); err != nil {
		return ContentMedia{}, false, err
	}
	if existing > 0 {
		m, err := scanContentMedia(s.DB.QueryRow(
			`SELECT `+contentMediaCols+` FROM content_media WHERE tenant_id=? AND store_id=? AND sha256=?`,
			tenantID, storeID, meta.SHA256))
		return m, err == nil, err
	}
	m := ContentMedia{
		ID: newID("cmd_"), TenantID: tenantID, StoreID: storeID,
		Kind: meta.Kind, MimeType: meta.MIME, ByteSize: meta.ByteSize, SHA256: meta.SHA256,
		DurationMS: meta.DurationMS, Width: meta.Width, Height: meta.Height,
		Filename: filename, MediaRef: meta.MediaRef, CreatedBy: createdBy, CreatedAt: now(),
	}
	_, err := s.DB.Exec(
		`INSERT INTO content_media(`+contentMediaCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.TenantID, m.StoreID, string(m.Kind), m.MimeType, m.ByteSize, m.SHA256,
		m.DurationMS, m.Width, m.Height, m.Filename, m.MediaRef, m.CreatedBy, m.CreatedAt)
	if err != nil {
		return ContentMedia{}, false, err
	}
	return m, false, nil
}

// GetContentMedia is tenant-scoped; foreign ids are ErrNotFound.
func (s *Store) GetContentMedia(id, tenantID string) (ContentMedia, error) {
	m, err := scanContentMedia(s.DB.QueryRow(
		`SELECT `+contentMediaCols+` FROM content_media WHERE id=? AND tenant_id=?`, id, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return ContentMedia{}, ErrNotFound
	}
	return m, err
}

// ListContentMedia returns the store's verified media oldest-first.
func (s *Store) ListContentMedia(tenantID, storeID string) ([]ContentMedia, error) {
	rows, err := s.DB.Query(
		`SELECT `+contentMediaCols+` FROM content_media WHERE tenant_id=? AND store_id=? ORDER BY created_at, id`,
		tenantID, storeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ContentMedia, 0)
	for rows.Next() {
		m, err := scanContentMedia(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ---- store content packs (门店根) ---------------------------------------------------

// StoreContentPack is one store-rooted bundle. Legacy marks campaign-domain
// packs surfaced through the query adapter (数据在旧表,只读投影).
type StoreContentPack struct {
	ID         string `json:"id"`
	TenantID   string `json:"tenant_id"`
	StoreID    string `json:"store_id"`
	CampaignID string `json:"campaign_id,omitempty"`
	Title      string `json:"title"`
	CreatedBy  string `json:"created_by"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
	Legacy     bool   `json:"legacy"`
}

const storeContentPackCols = `id,tenant_id,store_id,campaign_id,title,created_by,created_at,updated_at`

func scanStoreContentPack(sc interface{ Scan(...any) error }) (StoreContentPack, error) {
	var p StoreContentPack
	var campaignID sql.NullString
	err := sc.Scan(&p.ID, &p.TenantID, &p.StoreID, &campaignID, &p.Title, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt)
	p.CampaignID = campaignID.String
	return p, err
}

// CreateStoreContentPack registers a store-rooted bundle; campaignID may be
// empty. A bound campaign must exist in the tenant and (when it carries a
// store) must belong to this store.
func (s *Store) CreateStoreContentPack(tenantID, storeID, campaignID, title, createdBy string) (StoreContentPack, error) {
	title = strings.TrimSpace(title)
	if title == "" || len(title) > contentpack.MaxTitle {
		return StoreContentPack{}, contentpack.NewValidationError(contentpack.ReasonBadTitle, "title")
	}
	if _, err := s.GetStore(storeID, tenantID); err != nil {
		return StoreContentPack{}, err
	}
	if campaignID != "" {
		c, err := s.GetCampaign(campaignID, tenantID)
		if err != nil {
			return StoreContentPack{}, err
		}
		if c.StoreID != "" && c.StoreID != storeID {
			return StoreContentPack{}, ErrNotFound
		}
	}
	var existing int
	if err := s.DB.QueryRow(
		`SELECT COUNT(1) FROM store_content_packs WHERE tenant_id=? AND store_id=? AND title=?`,
		tenantID, storeID, title).Scan(&existing); err != nil {
		return StoreContentPack{}, err
	}
	if existing > 0 {
		return StoreContentPack{}, ErrDuplicateContentPack
	}
	p := StoreContentPack{
		ID: newID("scpk_"), TenantID: tenantID, StoreID: storeID, CampaignID: campaignID,
		Title: title, CreatedBy: createdBy, CreatedAt: now(), UpdatedAt: now(),
	}
	campaign := any(nil)
	if p.CampaignID != "" {
		campaign = p.CampaignID
	}
	_, err := s.DB.Exec(
		`INSERT INTO store_content_packs(`+storeContentPackCols+`) VALUES(?,?,?,?,?,?,?,?)`,
		p.ID, p.TenantID, p.StoreID, campaign, p.Title, p.CreatedBy, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return StoreContentPack{}, err
	}
	return p, nil
}

// GetStoreContentPack is tenant-scoped; foreign ids are ErrNotFound. It reads
// ONLY store-domain rows — legacy campaign packs keep their old access paths.
func (s *Store) GetStoreContentPack(id, tenantID string) (StoreContentPack, error) {
	p, err := scanStoreContentPack(s.DB.QueryRow(
		`SELECT `+storeContentPackCols+` FROM store_content_packs WHERE id=? AND tenant_id=?`, id, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return StoreContentPack{}, ErrNotFound
	}
	return p, err
}

// ListStoreContentPacks returns the store's packs oldest-first. 查询适配(裁
// 定书): the store's LEGACY campaign-domain packs (content_packs × campaigns)
// are projected alongside store-domain rows with Legacy=true so the merchant
// sees one unified list — the underlying rows never move.
func (s *Store) ListStoreContentPacks(tenantID, storeID string) ([]StoreContentPack, error) {
	if _, err := s.GetStore(storeID, tenantID); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(
		`SELECT `+storeContentPackCols+` FROM store_content_packs WHERE tenant_id=? AND store_id=?
		 UNION ALL
		 SELECT cp.id, cp.tenant_id, COALESCE(c.store_id,''), cp.campaign_id, cp.title, cp.created_by, cp.created_at, cp.updated_at
		   FROM content_packs cp JOIN campaigns c ON c.id = cp.campaign_id AND c.tenant_id = cp.tenant_id
		  WHERE cp.tenant_id=? AND c.store_id=?
		  ORDER BY created_at, id`,
		tenantID, storeID, tenantID, storeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]StoreContentPack, 0)
	for rows.Next() {
		p, err := scanStoreContentPack(rows)
		if err != nil {
			return nil, err
		}
		// rows from the second branch are the legacy projection
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close() // 显式释放:后续 COUNT 查询不得与未收尾的迭代器共享连接
	for i := range out {
		var legacy int
		if err := s.DB.QueryRow(
			`SELECT COUNT(1) FROM content_packs WHERE id=? AND tenant_id=?`, out[i].ID, tenantID).Scan(&legacy); err != nil {
			return nil, err
		}
		out[i].Legacy = legacy > 0
	}
	return out, nil
}

// ---- versions (不可变;媒体快照冻结) --------------------------------------------------

// StoreMediaSnapshot is the frozen per-media reference inside a version.
type StoreMediaSnapshot struct {
	MediaID    string `json:"media_id"`
	SHA256     string `json:"sha256"`
	MimeType   string `json:"mime_type"`
	ByteSize   int64  `json:"byte_size"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	Order      int    `json:"order,omitempty"`
}

// IsEmpty reports whether the snapshot carries no media reference.
func (m StoreMediaSnapshot) IsEmpty() bool { return m.MediaID == "" && m.SHA256 == "" }

// StoreVersionSpec is the merchant-language creation payload: media IDs only
// (商家不填技术引用), snapshots are resolved and frozen by this layer.
type StoreVersionSpec struct {
	ContentKind       contentpack.ContentKind
	VideoMediaID      string
	CoverMediaID      string
	ImageTextMediaIDs []string
	Title             string
	Body              string
	Topics            []string
	StoreName         string
	PriceNote         string
	ValidNote         string
	PlatformVersions  []contentpack.PlatformVersion
	GrantRefs         []string
	GrantUntil        string
	Marking           contentpack.Marking
	Source            contentpack.Source
	IdempotencyKey    string
}

// StoreContentPackVersion is one immutable store-domain version.
type StoreContentPackVersion struct {
	ID               string                        `json:"id"`
	TenantID         string                        `json:"tenant_id"`
	PackID           string                        `json:"pack_id"`
	VersionNo        int                           `json:"version_no"`
	ContentKind      contentpack.ContentKind       `json:"content_kind"`
	Status           contentpack.VersionStatus     `json:"status"`
	Source           contentpack.Source            `json:"source"`
	Video            StoreMediaSnapshot            `json:"video"`
	Cover            StoreMediaSnapshot            `json:"cover"`
	ImageText        []StoreMediaSnapshot          `json:"image_text"`
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

// imageTextSnapshot is the frozen JSON row shape for image_text.
type imageTextSnapshot struct {
	MediaID  string `json:"media_id"`
	SHA256   string `json:"sha256"`
	MimeType string `json:"mime_type"`
	ByteSize int64  `json:"byte_size"`
	Order    int    `json:"order"`
}

const storeVersionCols = `id,tenant_id,pack_id,version_no,content_kind,status,source,` +
	`video_media_id,video_sha256,video_mime,video_bytes,video_duration_ms,video_width,video_height,` +
	`cover_media_id,cover_sha256,cover_mime,cover_bytes,` +
	`image_text,title,body,topics,store_name,price_note,valid_note,platform_versions,` +
	`grant_refs,grant_until,marking,blocked_reason,idempotency_key,created_by,created_at`

func scanStoreVersion(sc interface{ Scan(...any) error }) (StoreContentPackVersion, error) {
	var v StoreContentPackVersion
	var imageText, topics, platformVersions, grantRefs, marking string
	var idemKey sql.NullString
	err := sc.Scan(&v.ID, &v.TenantID, &v.PackID, &v.VersionNo, &v.ContentKind, &v.Status, &v.Source,
		&v.Video.MediaID, &v.Video.SHA256, &v.Video.MimeType, &v.Video.ByteSize, &v.Video.DurationMS, &v.Video.Width, &v.Video.Height,
		&v.Cover.MediaID, &v.Cover.SHA256, &v.Cover.MimeType, &v.Cover.ByteSize,
		&imageText, &v.Title, &v.Body, &topics, &v.StoreName, &v.PriceNote, &v.ValidNote,
		&platformVersions, &grantRefs, &v.GrantUntil, &marking, &v.BlockedReason, &idemKey,
		&v.CreatedBy, &v.CreatedAt)
	if err != nil {
		return StoreContentPackVersion{}, err
	}
	v.Topics = unmarshalTags(topics)
	v.PlatformVersions = unmarshalPlatformVersions(platformVersions)
	v.GrantRefs = unmarshalTags(grantRefs)
	v.Marking = unmarshalMarking(marking)
	v.IdempotencyKey = idemKey.String
	v.ImageText = []StoreMediaSnapshot{}
	if imageText != "" {
		var rows []imageTextSnapshot
		_ = json.Unmarshal([]byte(imageText), &rows)
		for _, r := range rows {
			v.ImageText = append(v.ImageText, StoreMediaSnapshot{
				MediaID: r.MediaID, SHA256: r.SHA256, MimeType: r.MimeType,
				ByteSize: r.ByteSize, Order: r.Order,
			})
		}
	}
	return v, nil
}

// storeVersionByIdempotencyKey resolves a replay row (tenant-scoped).
func (s *Store) storeVersionByIdempotencyKey(tenantID, key string) (StoreContentPackVersion, error) {
	v, err := scanStoreVersion(s.DB.QueryRow(
		`SELECT `+storeVersionCols+` FROM store_content_pack_versions WHERE tenant_id=? AND idempotency_key=?`,
		tenantID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return StoreContentPackVersion{}, ErrNotFound
	}
	return v, err
}

// resolveStoreMedia resolves one media reference: same tenant AND same store,
// matching kind; anything else is media_missing (存在性不泄露).
func (s *Store) resolveStoreMedia(tenantID, storeID, mediaID string, want upload.MediaKind) (ContentMedia, error) {
	if strings.TrimSpace(mediaID) == "" {
		return ContentMedia{}, contentpack.NewValidationError(contentpack.ReasonMediaMissing, "media_id")
	}
	m, err := s.GetContentMedia(mediaID, tenantID)
	if errors.Is(err, ErrNotFound) {
		return ContentMedia{}, contentpack.NewValidationError(contentpack.ReasonMediaMissing, "media_id")
	}
	if err != nil {
		return ContentMedia{}, err
	}
	if m.StoreID != storeID || m.Kind != want {
		return ContentMedia{}, contentpack.NewValidationError(contentpack.ReasonMediaMissing, "media_id")
	}
	return m, nil
}

func (s *Store) nextStoreVersionNo(tenantID, packID string) (int, error) {
	var n any
	if err := s.DB.QueryRow(
		`SELECT MAX(version_no) FROM store_content_pack_versions WHERE tenant_id=? AND pack_id=?`,
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

// freezeStoreSpec validates media references (same tenant+store, matching
// kind) and runs the domain declaration matrix over the text fields (同
// resolveSpec 纪律:媒体解析先行,声明矩阵后裁决).
func (s *Store) freezeStoreSpec(tenantID, storeID string, spec StoreVersionSpec) (StoreVersionSpec, error) {
	resolved := placeholders{video: false, cover: false, images: 0}
	switch spec.ContentKind {
	case contentpack.ContentKindVideo:
		if _, err := s.resolveStoreMedia(tenantID, storeID, spec.VideoMediaID, upload.KindVideo); err != nil {
			return StoreVersionSpec{}, err
		}
		resolved.video = true
		if _, err := s.resolveStoreMedia(tenantID, storeID, spec.CoverMediaID, upload.KindImage); err != nil {
			return StoreVersionSpec{}, err
		}
		resolved.cover = true
	case contentpack.ContentKindImageText:
		for _, id := range spec.ImageTextMediaIDs {
			if _, err := s.resolveStoreMedia(tenantID, storeID, id, upload.KindImage); err != nil {
				return StoreVersionSpec{}, err
			}
			resolved.images++
		}
	default:
		return StoreVersionSpec{}, contentpack.NewValidationError(contentpack.ReasonBadContentKind, "content_kind")
	}
	spec.Title = contentpack.NormalizeText(spec.Title)
	spec.Body = contentpack.NormalizeText(spec.Body)
	spec.StoreName = contentpack.NormalizeText(spec.StoreName)
	spec.PriceNote = contentpack.NormalizeText(spec.PriceNote)
	spec.ValidNote = contentpack.NormalizeText(spec.ValidNote)
	spec.Topics = contentpack.NormalizeTopics(spec.Topics)
	spec.GrantUntil = contentpack.NormalizeText(spec.GrantUntil)
	spec.IdempotencyKey = contentpack.NormalizeText(spec.IdempotencyKey)

	// Reuse the domain declaration matrix via a translated VersionSpec. Media
	// presence mirrors the resolved references; the sentinel sha/ref satisfy
	// the matrix's snapshot checks (real snapshots freeze in insertStoreVersion).
	anchor := contentpack.AssetSnapshot{AssetID: "resolved", AssetRef: "resolved", SHA256: strings.Repeat("0", 64)}
	domainSpec := contentpack.VersionSpec{
		ContentKind: spec.ContentKind,
		Title:       spec.Title, Body: spec.Body, Topics: spec.Topics,
		StoreName: spec.StoreName, PriceNote: spec.PriceNote, ValidNote: spec.ValidNote,
		PlatformVersions: spec.PlatformVersions, GrantRefs: spec.GrantRefs, GrantUntil: spec.GrantUntil,
		Marking: spec.Marking, Source: spec.Source, IdempotencyKey: spec.IdempotencyKey,
	}
	switch spec.ContentKind {
	case contentpack.ContentKindVideo:
		domainSpec.Video = anchor
		domainSpec.Cover = anchor
	default:
		for i := 0; i < resolved.images; i++ {
			domainSpec.ImageText = append(domainSpec.ImageText, anchor)
		}
	}
	if err := contentpack.ValidateVersionSpec(domainSpec); err != nil {
		return StoreVersionSpec{}, err
	}
	return spec, nil
}

type placeholders struct {
	video  bool
	cover  bool
	images int
}

// CreateStoreContentPackVersion freezes one new immutable store-domain
// version. Unknown/duplicated media fail media_missing; duplicate idempotency
// keys fail ErrDuplicatePackVersion (同旧契约).
func (s *Store) CreateStoreContentPackVersion(tenantID, packID string, spec StoreVersionSpec, status contentpack.VersionStatus, createdBy string) (StoreContentPackVersion, error) {
	if status != contentpack.StatusDraft && status != contentpack.StatusCandidate {
		return StoreContentPackVersion{}, contentpack.NewValidationError(contentpack.ReasonNotDistributable, "status")
	}
	pack, err := s.GetStoreContentPack(packID, tenantID)
	if err != nil {
		return StoreContentPackVersion{}, err
	}
	spec, err = s.freezeStoreSpec(tenantID, pack.StoreID, spec)
	if err != nil {
		return StoreContentPackVersion{}, err
	}
	if spec.IdempotencyKey != "" {
		if _, err := s.storeVersionByIdempotencyKey(tenantID, spec.IdempotencyKey); err == nil {
			return StoreContentPackVersion{}, ErrDuplicatePackVersion
		} else if !errors.Is(err, ErrNotFound) {
			return StoreContentPackVersion{}, err
		}
	}
	return s.insertStoreVersion(tenantID, pack, spec, status, createdBy, "created")
}

func (s *Store) insertStoreVersion(tenantID string, pack StoreContentPack, spec StoreVersionSpec, status contentpack.VersionStatus, createdBy, eventKind string) (StoreContentPackVersion, error) {
	no, err := s.nextStoreVersionNo(tenantID, pack.ID)
	if err != nil {
		return StoreContentPackVersion{}, err
	}
	key := any(nil)
	if spec.IdempotencyKey != "" {
		key = spec.IdempotencyKey
	}
	v := StoreContentPackVersion{
		ID: newID("scpv_"), TenantID: tenantID, PackID: pack.ID, VersionNo: no,
		ContentKind: spec.ContentKind, Status: status, Source: spec.Source,
		Title: spec.Title, Body: spec.Body, Topics: spec.Topics,
		StoreName: spec.StoreName, PriceNote: spec.PriceNote, ValidNote: spec.ValidNote,
		PlatformVersions: spec.PlatformVersions, GrantRefs: spec.GrantRefs, GrantUntil: spec.GrantUntil,
		Marking: spec.Marking, IdempotencyKey: spec.IdempotencyKey,
		CreatedBy: createdBy, CreatedAt: now(),
	}
	// freeze snapshots from the CURRENT media rows (immutability guarantee).
	switch spec.ContentKind {
	case contentpack.ContentKindVideo:
		video, err := s.resolveStoreMedia(tenantID, pack.StoreID, spec.VideoMediaID, upload.KindVideo)
		if err != nil {
			return StoreContentPackVersion{}, err
		}
		cover, err := s.resolveStoreMedia(tenantID, pack.StoreID, spec.CoverMediaID, upload.KindImage)
		if err != nil {
			return StoreContentPackVersion{}, err
		}
		v.Video = snapshotOf(video, 0)
		v.Cover = snapshotOf(cover, 0)
	case contentpack.ContentKindImageText:
		for i, id := range spec.ImageTextMediaIDs {
			img, err := s.resolveStoreMedia(tenantID, pack.StoreID, id, upload.KindImage)
			if err != nil {
				return StoreContentPackVersion{}, err
			}
			v.ImageText = append(v.ImageText, snapshotOf(img, i))
		}
	}
	imageTextRows := make([]imageTextSnapshot, 0, len(v.ImageText))
	for _, img := range v.ImageText {
		imageTextRows = append(imageTextRows, imageTextSnapshot{
			MediaID: img.MediaID, SHA256: img.SHA256, MimeType: img.MimeType, ByteSize: img.ByteSize, Order: img.Order,
		})
	}
	_, err = s.DB.Exec(
		`INSERT INTO store_content_pack_versions(`+storeVersionCols+`) VALUES(`+
			strings.Repeat("?,", 32)+"?)",
		v.ID, v.TenantID, v.PackID, v.VersionNo, v.ContentKind, v.Status, v.Source,
		v.Video.MediaID, v.Video.SHA256, v.Video.MimeType, v.Video.ByteSize, v.Video.DurationMS, v.Video.Width, v.Video.Height,
		v.Cover.MediaID, v.Cover.SHA256, v.Cover.MimeType, v.Cover.ByteSize,
		marshalJSONDefault(imageTextRows, "[]"), v.Title, v.Body, marshalJSONDefault(v.Topics, "[]"),
		v.StoreName, v.PriceNote, v.ValidNote, marshalJSONDefault(v.PlatformVersions, "[]"),
		marshalJSONDefault(v.GrantRefs, "[]"), v.GrantUntil, marshalJSONDefault(v.Marking, "{}"),
		"", key, v.CreatedBy, v.CreatedAt)
	if err != nil {
		return StoreContentPackVersion{}, err
	}
	if err := s.appendStoreVersionEvent(tenantID, v.ID, eventKind, "", createdBy); err != nil {
		return StoreContentPackVersion{}, err
	}
	return v, nil
}

func snapshotOf(m ContentMedia, order int) StoreMediaSnapshot {
	return StoreMediaSnapshot{
		MediaID: m.ID, SHA256: m.SHA256, MimeType: m.MimeType, ByteSize: m.ByteSize,
		DurationMS: m.DurationMS, Width: m.Width, Height: m.Height, Order: order,
	}
}

// AddStoreReturnCandidate is the UNIQUE candidate-registration transaction
// for trusted production results (HUI-4595 接线点;本票 exported 方法,HTTP 内部
// 端点归 4595). Same idempotency key replays the frozen row; the current
// adoption pointer is never touched (旧回流不覆盖新采用).
func (s *Store) AddStoreReturnCandidate(tenantID, packID string, spec StoreVersionSpec, actor string) (StoreContentPackVersion, bool, error) {
	if contentpack.Source(contentpack.NormalizeText(string(spec.Source))) != contentpack.SourceProductPhotoReturn {
		return StoreContentPackVersion{}, false, contentpack.NewValidationError(contentpack.ReasonBadSource, "source")
	}
	if err := contentpack.ValidateIdempotencyKey(spec.IdempotencyKey); err != nil {
		return StoreContentPackVersion{}, false, err
	}
	key := contentpack.NormalizeText(spec.IdempotencyKey)
	if cur, err := s.storeVersionByIdempotencyKey(tenantID, key); err == nil {
		if cur.PackID != packID {
			return StoreContentPackVersion{}, false, contentpack.NewValidationError(contentpack.ReasonBadIdempotencyKey, "idempotency_key")
		}
		return cur, true, nil
	} else if !errors.Is(err, ErrNotFound) {
		return StoreContentPackVersion{}, false, err
	}
	pack, err := s.GetStoreContentPack(packID, tenantID)
	if err != nil {
		return StoreContentPackVersion{}, false, err
	}
	spec, err = s.freezeStoreSpec(tenantID, pack.StoreID, spec)
	if err != nil {
		return StoreContentPackVersion{}, false, err
	}
	v, err := s.insertStoreVersion(tenantID, pack, spec, contentpack.StatusCandidate, actor, "returned")
	if err != nil {
		return StoreContentPackVersion{}, false, err
	}
	return v, false, nil
}

// GetStoreContentPackVersion is tenant-scoped; foreign ids are ErrNotFound.
func (s *Store) GetStoreContentPackVersion(id, tenantID string) (StoreContentPackVersion, error) {
	v, err := scanStoreVersion(s.DB.QueryRow(
		`SELECT `+storeVersionCols+` FROM store_content_pack_versions WHERE id=? AND tenant_id=?`, id, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return StoreContentPackVersion{}, ErrNotFound
	}
	return v, err
}

// GetStoreContentPackVersionByID returns one version WITHOUT tenant scoping
// (the B05 public face resolves the tenant from the ROW — never from request
// input; 同 GetContentHandoffByID 受信服务接缝纪律). Callers MUST treat the
// returned row's TenantID as the only tenant truth. Unknown id -> ErrNotFound.
func (s *Store) GetStoreContentPackVersionByID(versionID string) (StoreContentPackVersion, error) {
	v, err := scanStoreVersion(s.DB.QueryRow(
		`SELECT `+storeVersionCols+` FROM store_content_pack_versions WHERE id=?`, versionID))
	if errors.Is(err, sql.ErrNoRows) {
		return StoreContentPackVersion{}, ErrNotFound
	}
	return v, err
}

// ListStoreContentPackVersions returns the pack's versions oldest-first.
func (s *Store) ListStoreContentPackVersions(tenantID, packID string) ([]StoreContentPackVersion, error) {
	rows, err := s.DB.Query(
		`SELECT `+storeVersionCols+` FROM store_content_pack_versions WHERE tenant_id=? AND pack_id=? ORDER BY version_no`,
		tenantID, packID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]StoreContentPackVersion, 0)
	for rows.Next() {
		v, err := scanStoreVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ---- version events (只追加处理记录) --------------------------------------------------

// StoreContentPackVersionEvent is one append-only processing record.
type StoreContentPackVersionEvent struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	VersionID string `json:"version_id"`
	Kind      string `json:"kind"`
	Note      string `json:"note"`
	Actor     string `json:"actor"`
	CreatedAt string `json:"created_at"`
}

func (s *Store) appendStoreVersionEvent(tenantID, versionID, kind, note, actor string) error {
	e := StoreContentPackVersionEvent{
		ID: newID("scpe_"), TenantID: tenantID, VersionID: versionID,
		Kind: kind, Note: note, Actor: actor, CreatedAt: now(),
	}
	_, err := s.DB.Exec(
		`INSERT INTO store_content_pack_version_events(id,tenant_id,version_id,kind,note,actor,created_at) VALUES(?,?,?,?,?,?,?)`,
		e.ID, e.TenantID, e.VersionID, e.Kind, e.Note, e.Actor, e.CreatedAt)
	return err
}

// ListStoreContentPackVersionEvents returns the version's records oldest-first.
func (s *Store) ListStoreContentPackVersionEvents(tenantID, versionID string) ([]StoreContentPackVersionEvent, error) {
	rows, err := s.DB.Query(
		`SELECT id,tenant_id,version_id,kind,note,actor,created_at FROM store_content_pack_version_events WHERE tenant_id=? AND version_id=? ORDER BY created_at, id`,
		tenantID, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]StoreContentPackVersionEvent, 0)
	for rows.Next() {
		var e StoreContentPackVersionEvent
		if err := rows.Scan(&e.ID, &e.TenantID, &e.VersionID, &e.Kind, &e.Note, &e.Actor, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- adoption (门店×平台当前指针) ----------------------------------------------------

// StoreContentPackAdoption is the CURRENT adopted version pointer for one
// (store, platform). 采用动作记录操作者与时间.
type StoreContentPackAdoption struct {
	ID         string `json:"id"`
	TenantID   string `json:"tenant_id"`
	StoreID    string `json:"store_id"`
	CampaignID string `json:"campaign_id,omitempty"`
	Platform   string `json:"platform"`
	PackID     string `json:"pack_id"`
	VersionID  string `json:"version_id"`
	AdoptedBy  string `json:"adopted_by"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

const storeAdoptionCols = `id,tenant_id,store_id,campaign_id,platform,pack_id,version_id,adopted_by,created_at,updated_at`

func scanStoreAdoption(sc interface{ Scan(...any) error }) (StoreContentPackAdoption, error) {
	var a StoreContentPackAdoption
	var campaignID sql.NullString
	err := sc.Scan(&a.ID, &a.TenantID, &a.StoreID, &campaignID, &a.Platform, &a.PackID, &a.VersionID, &a.AdoptedBy, &a.CreatedAt, &a.UpdatedAt)
	a.CampaignID = campaignID.String
	return a, err
}

// AdoptStoreContentPackVersion makes version the CURRENT one for (store,
// platform). Blocked and grant-expired versions are refused (Distributable
// 单点裁决); the previously adopted version retires with a superseded event
// and its content row NEVER changes.
func (s *Store) AdoptStoreContentPackVersion(tenantID, packID, versionID, platform, adoptedBy string, at time.Time) (StoreContentPackAdoption, error) {
	platform = strings.TrimSpace(platform)
	if platform == "" || len(platform) > contentpack.MaxPlatformName {
		return StoreContentPackAdoption{}, contentpack.NewValidationError(contentpack.ReasonMissingPlatform, "platform")
	}
	pack, err := s.GetStoreContentPack(packID, tenantID)
	if err != nil {
		return StoreContentPackAdoption{}, err
	}
	v, err := s.GetStoreContentPackVersion(versionID, tenantID)
	if err != nil {
		return StoreContentPackAdoption{}, err
	}
	if v.PackID != pack.ID {
		return StoreContentPackAdoption{}, ErrNotFound
	}
	if err := contentpack.Distributable(v.Status, v.GrantUntil, at); err != nil {
		return StoreContentPackAdoption{}, err
	}
	nowText := at.UTC().Format(time.RFC3339Nano)

	prev, err := scanStoreAdoption(s.DB.QueryRow(
		`SELECT `+storeAdoptionCols+` FROM store_content_pack_adoptions WHERE tenant_id=? AND store_id=? AND platform=? AND pack_id=?`,
		tenantID, pack.StoreID, platform, pack.ID))
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return StoreContentPackAdoption{}, err
	}
	if found && prev.VersionID != v.ID {
		if _, err := s.DB.Exec(
			`UPDATE store_content_pack_versions SET status=? WHERE id=? AND tenant_id=?`,
			contentpack.StatusRetired, prev.VersionID, tenantID); err != nil {
			return StoreContentPackAdoption{}, err
		}
		if err := s.appendStoreVersionEvent(tenantID, prev.VersionID, "superseded", "被新采用版本取代:"+v.ID, adoptedBy); err != nil {
			return StoreContentPackAdoption{}, err
		}
	}
	var adoption StoreContentPackAdoption
	if found {
		if _, err := s.DB.Exec(
			`UPDATE store_content_pack_adoptions SET pack_id=?,version_id=?,adopted_by=?,updated_at=? WHERE id=? AND tenant_id=?`,
			pack.ID, v.ID, adoptedBy, nowText, prev.ID, tenantID); err != nil {
			return StoreContentPackAdoption{}, err
		}
		adoption = prev
		adoption.PackID = pack.ID
		adoption.VersionID = v.ID
		adoption.AdoptedBy = adoptedBy
		adoption.UpdatedAt = nowText
	} else {
		adoption = StoreContentPackAdoption{
			ID: newID("scpa_"), TenantID: tenantID, StoreID: pack.StoreID, CampaignID: pack.CampaignID,
			Platform: platform, PackID: pack.ID, VersionID: v.ID, AdoptedBy: adoptedBy,
			CreatedAt: nowText, UpdatedAt: nowText,
		}
		campaign := any(nil)
		if adoption.CampaignID != "" {
			campaign = adoption.CampaignID
		}
		if _, err := s.DB.Exec(
			`INSERT INTO store_content_pack_adoptions(`+storeAdoptionCols+`) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			adoption.ID, adoption.TenantID, adoption.StoreID, campaign, adoption.Platform,
			adoption.PackID, adoption.VersionID, adoption.AdoptedBy, adoption.CreatedAt, adoption.UpdatedAt); err != nil {
			return StoreContentPackAdoption{}, err
		}
	}
	if v.Status != contentpack.StatusAdopted {
		if _, err := s.DB.Exec(
			`UPDATE store_content_pack_versions SET status=? WHERE id=? AND tenant_id=?`,
			contentpack.StatusAdopted, v.ID, tenantID); err != nil {
			return StoreContentPackAdoption{}, err
		}
	}
	if err := s.appendStoreVersionEvent(tenantID, v.ID, "adopted", "平台 "+platform+" 明确采用", adoptedBy); err != nil {
		return StoreContentPackAdoption{}, err
	}
	return adoption, nil
}

// GetStoreAdoptedPackVersion resolves the pack's current adopted version (the
// most recent adoption pointer rooted in this pack; per (store, platform,
// pack) 唯一,跨平台取最近). No adoption -> ErrNotFound.
func (s *Store) GetStoreAdoptedPackVersion(tenantID, packID string) (StoreContentPackVersion, error) {
	var versionID string
	err := s.DB.QueryRow(
		`SELECT version_id FROM store_content_pack_adoptions WHERE tenant_id=? AND pack_id=? ORDER BY updated_at DESC, id LIMIT 1`,
		tenantID, packID).Scan(&versionID)
	if errors.Is(err, sql.ErrNoRows) {
		return StoreContentPackVersion{}, ErrNotFound
	}
	if err != nil {
		return StoreContentPackVersion{}, err
	}
	return s.GetStoreContentPackVersion(versionID, tenantID)
}

// ListStoreAdoptedVersions returns every CURRENT adopted version of the
// store (the「换一个」candidate pool source; callers re-check Distributable).
// NOTE: version rows are fetched AFTER the pointer rows close — the SQLite
// driver allows one connection and nested queries on an open iterator
// deadlock (同仓内单连接纪律).
func (s *Store) ListStoreAdoptedVersions(tenantID, storeID string) ([]StoreContentPackVersion, error) {
	rows, err := s.DB.Query(
		`SELECT version_id FROM store_content_pack_adoptions WHERE tenant_id=? AND store_id=? ORDER BY updated_at, id`,
		tenantID, storeID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0)
	for rows.Next() {
		var versionID string
		if err := rows.Scan(&versionID); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, versionID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	out := make([]StoreContentPackVersion, 0, len(ids))
	for _, versionID := range ids {
		v, err := s.GetStoreContentPackVersion(versionID, tenantID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// ListStoreAdoptions returns the store's platform pointers oldest-first.
func (s *Store) ListStoreAdoptions(tenantID, storeID string) ([]StoreContentPackAdoption, error) {
	rows, err := s.DB.Query(
		`SELECT `+storeAdoptionCols+` FROM store_content_pack_adoptions WHERE tenant_id=? AND store_id=? ORDER BY created_at, id`,
		tenantID, storeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]StoreContentPackAdoption, 0)
	for rows.Next() {
		a, err := scanStoreAdoption(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// BlockStoreContentPackVersion marks 撤权/重大内容问题: the pointer is
// preserved (旧参与保留原版本) but new distribution refuses.
func (s *Store) BlockStoreContentPackVersion(tenantID, packID, versionID, reason, actor string, at time.Time) (StoreContentPackVersion, error) {
	if err := contentpack.ValidateBlockReason(reason); err != nil {
		return StoreContentPackVersion{}, err
	}
	pack, err := s.GetStoreContentPack(packID, tenantID)
	if err != nil {
		return StoreContentPackVersion{}, err
	}
	v, err := s.GetStoreContentPackVersion(versionID, tenantID)
	if err != nil {
		return StoreContentPackVersion{}, err
	}
	if v.PackID != pack.ID {
		return StoreContentPackVersion{}, ErrNotFound
	}
	if v.Status == contentpack.StatusBlocked {
		return v, nil // idempotent
	}
	if _, err := s.DB.Exec(
		`UPDATE store_content_pack_versions SET status=?,blocked_reason=? WHERE id=? AND tenant_id=?`,
		contentpack.StatusBlocked, contentpack.NormalizeText(reason), v.ID, tenantID); err != nil {
		return StoreContentPackVersion{}, err
	}
	if err := s.appendStoreVersionEvent(tenantID, v.ID, "blocked", contentpack.NormalizeText(reason), actor); err != nil {
		return StoreContentPackVersion{}, err
	}
	return s.GetStoreContentPackVersion(v.ID, tenantID)
}

// ---- action content binding (采用后建立绑定;写端点归 4584) ------------------------------

// ActionContentBinding is the server-side authority row behind an action's
// content reference (B05 读面按此解析 tenant/store/pack).
type ActionContentBinding struct {
	ID             string `json:"id"`
	TenantID       string `json:"tenant_id"`
	StoreID        string `json:"store_id"`
	ActionID       string `json:"action_id"`
	PackID         string `json:"pack_id"`
	LastVersionID  string `json:"last_version_id"`
	ActionRevision int    `json:"action_revision"`
	CreatedBy      string `json:"created_by"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

const actionBindingCols = `id,tenant_id,store_id,action_id,pack_id,last_version_id,action_revision,created_by,created_at,updated_at`

func scanActionBinding(sc interface{ Scan(...any) error }) (ActionContentBinding, error) {
	var b ActionContentBinding
	err := sc.Scan(&b.ID, &b.TenantID, &b.StoreID, &b.ActionID, &b.PackID, &b.LastVersionID, &b.ActionRevision, &b.CreatedBy, &b.CreatedAt, &b.UpdatedAt)
	return b, err
}

// BindActionContent is the「采用后建立绑定」internal call (采用后才建立对外内容
// 绑定): version must be the pack's CURRENT adopted version; pack and store
// must match. Re-binding overwrites the same (tenant, action) row.
func (s *Store) BindActionContent(tenantID, storeID, actionID, packID, versionID string, actionRevision int, actor string, at time.Time) (ActionContentBinding, error) {
	actionID = strings.TrimSpace(actionID)
	if actionID == "" {
		return ActionContentBinding{}, contentpack.NewValidationError(contentpack.ReasonMissingTitle, "action_id")
	}
	if _, err := s.GetStore(storeID, tenantID); err != nil {
		return ActionContentBinding{}, err
	}
	pack, err := s.GetStoreContentPack(packID, tenantID)
	if err != nil {
		return ActionContentBinding{}, err
	}
	if pack.StoreID != storeID {
		return ActionContentBinding{}, ErrNotFound
	}
	v, err := s.GetStoreContentPackVersion(versionID, tenantID)
	if err != nil {
		return ActionContentBinding{}, err
	}
	if v.PackID != pack.ID {
		return ActionContentBinding{}, ErrNotFound
	}
	cur, err := s.GetStoreAdoptedPackVersion(tenantID, pack.ID)
	if err != nil {
		// 未采用(或指针不在本包):拒绝建立绑定。
		if errors.Is(err, ErrNotFound) {
			return ActionContentBinding{}, contentpack.NewValidationError(contentpack.ReasonNotDistributable, "version")
		}
		return ActionContentBinding{}, err
	}
	if cur.ID != v.ID {
		return ActionContentBinding{}, contentpack.NewValidationError(contentpack.ReasonNotDistributable, "version")
	}
	if actionRevision < 0 {
		actionRevision = 0
	}
	nowText := at.UTC().Format(time.RFC3339Nano)
	var existing int
	if err := s.DB.QueryRow(
		`SELECT COUNT(1) FROM store_action_content_bindings WHERE tenant_id=? AND action_id=?`,
		tenantID, actionID).Scan(&existing); err != nil {
		return ActionContentBinding{}, err
	}
	if existing > 0 {
		prev, err := scanActionBinding(s.DB.QueryRow(
			`SELECT `+actionBindingCols+` FROM store_action_content_bindings WHERE tenant_id=? AND action_id=?`,
			tenantID, actionID))
		if err != nil {
			return ActionContentBinding{}, err
		}
		if _, err := s.DB.Exec(
			`UPDATE store_action_content_bindings SET pack_id=?,last_version_id=?,action_revision=?,created_by=?,updated_at=? WHERE id=? AND tenant_id=?`,
			pack.ID, v.ID, actionRevision, actor, nowText, prev.ID, tenantID); err != nil {
			return ActionContentBinding{}, err
		}
		prev.PackID = pack.ID
		prev.LastVersionID = v.ID
		prev.ActionRevision = actionRevision
		prev.CreatedBy = actor
		prev.UpdatedAt = nowText
		return prev, nil
	}
	b := ActionContentBinding{
		ID: newID("sacb_"), TenantID: tenantID, StoreID: storeID, ActionID: actionID,
		PackID: pack.ID, LastVersionID: v.ID, ActionRevision: actionRevision,
		CreatedBy: actor, CreatedAt: nowText, UpdatedAt: nowText,
	}
	_, err = s.DB.Exec(
		`INSERT INTO store_action_content_bindings(`+actionBindingCols+`) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		b.ID, b.TenantID, b.StoreID, b.ActionID, b.PackID, b.LastVersionID, b.ActionRevision, b.CreatedBy, b.CreatedAt, b.UpdatedAt)
	if err != nil {
		return ActionContentBinding{}, err
	}
	return b, nil
}

// GetActionContentBinding is tenant-scoped; unknown actions are ErrNotFound.
func (s *Store) GetActionContentBinding(tenantID, actionID string) (ActionContentBinding, error) {
	b, err := scanActionBinding(s.DB.QueryRow(
		`SELECT `+actionBindingCols+` FROM store_action_content_bindings WHERE tenant_id=? AND action_id=?`,
		tenantID, strings.TrimSpace(actionID)))
	if errors.Is(err, sql.ErrNoRows) {
		return ActionContentBinding{}, ErrNotFound
	}
	return b, err
}

// GetActionContentBindingAnyTenant resolves the binding row by action_id
// alone (the B05 public face has no session; the ROW is the tenant truth —
// action_id is a lookup key, never an authorization). PRECONDITION: action_id
// is a server-minted instance ID with global uniqueness (HUI-4584 action
// catalog PK discipline); a cross-tenant action_id collision would resolve to
// an arbitrary tenant, which is why ids are minted, never chosen by callers.
// Unknown -> ErrNotFound.
func (s *Store) GetActionContentBindingAnyTenant(actionID string) (ActionContentBinding, error) {
	b, err := scanActionBinding(s.DB.QueryRow(
		`SELECT `+actionBindingCols+` FROM store_action_content_bindings WHERE action_id=?`,
		strings.TrimSpace(actionID)))
	if errors.Is(err, sql.ErrNoRows) {
		return ActionContentBinding{}, ErrNotFound
	}
	return b, err
}

// ClearActionContentBinding is the「解绑」internal call (HUI-4584 actions PUT
// 写入侧,中央集成 2026-11-10): the merchant put content_binding_ref='' on an
// action that previously carried one (or deleted the action row entirely), so
// the authoritative binding index must follow. Deletes the
// store_action_content_bindings row and appends one audit event on the last
// bound version (kind superseded — the reference moved on; CHECK 词表内唯一
// 合适的只追加词). Absent binding is a no-op (idempotent unbind).
func (s *Store) ClearActionContentBinding(tenantID, actionID string) error {
	b, err := s.GetActionContentBinding(tenantID, actionID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	if _, err := s.DB.Exec(
		`DELETE FROM store_action_content_bindings WHERE id=? AND tenant_id=?`,
		b.ID, tenantID); err != nil {
		return err
	}
	return s.appendStoreVersionEvent(tenantID, b.LastVersionID, "superseded",
		"action content binding cleared (unbind by merchant actions PUT)", "")
}

// ---- media access grant ledger (只追加签发审计) ----------------------------------------

// MediaAccessGrant is one issued credential's audit row.
type MediaAccessGrant struct {
	ID         string `json:"id"`
	TenantID   string `json:"tenant_id"`
	VersionID  string `json:"version_id"`
	MediaID    string `json:"media_id"`
	Purpose    string `json:"purpose"`
	SessionRef string `json:"session_ref,omitempty"`
	ExpiresAt  string `json:"expires_at"`
	CreatedAt  string `json:"created_at"`
}

// RecordMediaAccessGrant appends one issuance row (续签/重试各成一行).
func (s *Store) RecordMediaAccessGrant(tenantID, versionID, mediaID, purpose, sessionRef string, expiresAt time.Time) error {
	g := MediaAccessGrant{
		ID: newID("mag_"), TenantID: tenantID, VersionID: versionID, MediaID: mediaID,
		Purpose: purpose, SessionRef: sessionRef,
		ExpiresAt: expiresAt.UTC().Format(time.RFC3339), CreatedAt: now(),
	}
	_, err := s.DB.Exec(
		`INSERT INTO media_access_grants(id,tenant_id,version_id,media_id,purpose,session_ref,expires_at,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		g.ID, g.TenantID, g.VersionID, g.MediaID, g.Purpose, g.SessionRef, g.ExpiresAt, g.CreatedAt)
	return err
}

// ListMediaAccessGrants returns the version's issuance rows oldest-first.
func (s *Store) ListMediaAccessGrants(tenantID, versionID string) ([]MediaAccessGrant, error) {
	rows, err := s.DB.Query(
		`SELECT id,tenant_id,version_id,media_id,purpose,session_ref,expires_at,created_at FROM media_access_grants WHERE tenant_id=? AND version_id=? ORDER BY created_at, id`,
		tenantID, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]MediaAccessGrant, 0)
	for rows.Next() {
		var g MediaAccessGrant
		if err := rows.Scan(&g.ID, &g.TenantID, &g.VersionID, &g.MediaID, &g.Purpose, &g.SessionRef, &g.ExpiresAt, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
