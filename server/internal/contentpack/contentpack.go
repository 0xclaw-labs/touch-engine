// Package contentpack implements HUI-3553 活动内容包与可视化选材: the pure,
// deterministic domain rules for adoptable content pack versions, explicit
// adoption, ecosystem return-candidates, and controlled media handoff URLs.
//
// 纪律(票面拍板):
//   - 内容包引用素材版本而不是复制文件:版本行冻结 (asset_id, asset_ref,
//     sha256) 三元组快照,复用 HUI-1666 assetlib 基线,零物理文件仓库;
//   - 版本不可变:内容列零 UPDATE;改文案/改价 = 新开版本;源头回流与晚到
//     旧稿一律只落候选,绝不覆盖当前采用版;
//   - 采用必须显式:候选 → 商家预览/编辑 → 明确采用(活动×平台指针);
//     采用后顾客侧返回同一版本,不随机刷新换片;
//   - 授权撤销/重大内容问题 = block:阻止新分发,旧参与保留原版本与处理记录
//     (处理记录归 store 层事件表,本包定义语义);
//   - 「帮我制作」交接:幂等键去重(同一交接重试不新建制作工程);未接通
//     真实引擎时如实不可用,绝不伪造能力;
//   - 媒体交接 URL 受控:权限校验、有效期(默认 10 分钟、上限 1 小时)、
//     content-type 白名单、Content-Disposition 按发布器要求;非永久公有化
//     整个素材库;文件字节零过本服务。
package contentpack

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ---- enumerations -------------------------------------------------------------

// ContentKind is the v1 content-form enumeration (视频或图文).
type ContentKind string

const (
	ContentKindVideo      ContentKind = "video"      // 成片 + 封面
	ContentKindImageText  ContentKind = "imagetext"  // 图文(≥1 图 + 正文)
)

// Valid reports whether k is one of the v1 content kinds.
func (k ContentKind) Valid() bool {
	switch k {
	case ContentKindVideo, ContentKindImageText:
		return true
	}
	return false
}

// VersionStatus is the workflow state of a version. Content columns never
// change; only status transitions move.
type VersionStatus string

const (
	StatusDraft    VersionStatus = "draft"    // 商家编辑中
	StatusCandidate VersionStatus = "candidate" // 候选(含回流候选),待商家预览/采用
	StatusAdopted  VersionStatus = "adopted"  // 已采用(曾是某平台当前版)
	StatusRetired  VersionStatus = "retired"  // 被新版本取代(内容保留可追溯)
	StatusBlocked  VersionStatus = "blocked"  // 撤权/重大内容问题:阻止新分发
)

// Source is where a version came from (复用 assetlib 的来源词汇,回流是新增项).
type Source string

const (
	SourceMerchantUpload    Source = "merchant_upload"     // 商家上传的已有内容
	SourceAssetLib          Source = "asset_lib"           // 从素材库选择组织
	SourceProductPhotoReturn Source = "product_photo_return" // 产品图生态成果回流(HUI-1745 接线点)
)

// Valid reports whether s is one of the v1 sources.
func (s Source) Valid() bool {
	switch s {
	case SourceMerchantUpload, SourceAssetLib, SourceProductPhotoReturn:
		return true
	}
	return false
}

// ---- machine reason codes ------------------------------------------------------

// Machine reason codes (written into 422/409/410 responses' error fields).
const (
	ReasonBadContentKind          = "bad_content_kind"
	ReasonBadTitle                = "bad_title"
	ReasonMissingTitle            = "missing_title"
	ReasonBadBody                 = "bad_body"
	ReasonMissingBody             = "missing_body"
	ReasonBadStoreName            = "bad_store_name"
	ReasonMissingStoreName        = "missing_store_name"
	ReasonBadPriceNote            = "bad_price_note"
	ReasonMissingPriceNote        = "missing_price_note"
	ReasonBadValidNote            = "bad_valid_note"
	ReasonMissingValidNote        = "missing_valid_note"
	ReasonBadTopics               = "bad_topics"
	ReasonBadGrantRef             = "bad_grant_ref"
	ReasonMissingVideoRef         = "missing_video_ref"
	ReasonMissingCoverRef         = "missing_cover_ref"
	ReasonMissingImageText        = "missing_image_text"
	ReasonMissingPlatform         = "missing_platform"
	ReasonBadPlatformVersion      = "bad_platform_version"
	ReasonBadSnapshot             = "bad_snapshot"
	ReasonBadGrantUntil           = "bad_grant_until"
	ReasonBadSource               = "bad_source"
	ReasonMissingSource           = "missing_source"
	ReasonBadIdempotencyKey       = "bad_idempotency_key"
	ReasonMissingIdempotencyKey   = "missing_idempotency_key"
	ReasonMissingBlockReason      = "missing_block_reason"
	ReasonGrantExpired            = "grant_expired"
	ReasonNotDistributable        = "not_distributable"
	ReasonMediaMissing            = "media_missing"
	ReasonBadHandoffTTL           = "bad_handoff_ttl"
	ReasonBadContentType          = "bad_content_type"
	ReasonBadDisposition          = "bad_disposition"
	ReasonMissingHandoffFilename  = "missing_handoff_filename"
	ReasonHandoffExpired          = "handoff_expired"
	ReasonHandoffInvalid          = "handoff_invalid"
)

// ValidationError carries a machine-readable reason for HTTP mapping.
type ValidationError struct {
	Reason string
	Field  string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("contentpack: %s (%s)", e.Reason, e.Field)
}

func invalid(reason, field string) error {
	return &ValidationError{Reason: reason, Field: field}
}

// NewValidationError is the exported constructor for layered callers.
func NewValidationError(reason, field string) error {
	return &ValidationError{Reason: reason, Field: field}
}

// ---- version spec (创建载荷;存储层再校验一次) -------------------------------------

// Field length caps.
const (
	MaxTitle          = 128
	MaxBody           = 4096
	MaxTopicLen       = 64
	MaxTopics         = 20
	MaxStoreName      = 128
	MaxPriceNote      = 256
	MaxValidNote      = 256
	MaxPlatformName   = 64
	MaxPlatformNote   = 256
	MaxSourceVersion  = 128
	MaxGrantRef       = 256
	MaxGrantRefs      = 16
	MaxIdempotencyKey = 128
	MaxBlockReason    = 512
)

// AssetSnapshot freezes one lib-asset reference (asset_id + asset_ref + sha256):
// the version anchors the CONTENT, so later library updates never drift an
// adopted version (源素材更新不静默换片).
type AssetSnapshot struct {
	AssetID string `json:"asset_id"`
	AssetRef string `json:"asset_ref"`
	SHA256  string `json:"sha256"`
}

func (a AssetSnapshot) empty() bool { return a.AssetID == "" && a.AssetRef == "" && a.SHA256 == "" }

// IsEmpty reports whether the snapshot carries no reference (exported for
// the HTTP layer; cross-package views use it to omit empty media refs).
func (a AssetSnapshot) IsEmpty() bool { return a.empty() }

func (a AssetSnapshot) valid() bool {
	return strings.TrimSpace(a.AssetID) != "" && strings.TrimSpace(a.AssetRef) != "" &&
		isSHA256Hex(NormalizeSHA256(a.SHA256))
}

// PlatformVersion is one platform adaptation of the SAME source content
// (每版本可追溯同一 source_version).
type PlatformVersion struct {
	Platform      string `json:"platform"`
	Note          string `json:"note"`
	SourceVersion string `json:"source_version"`
}

func (p PlatformVersion) valid() bool {
	if strings.TrimSpace(p.Platform) == "" || len(p.Platform) > MaxPlatformName {
		return false
	}
	if len(p.Note) > MaxPlatformNote || strings.TrimSpace(p.SourceVersion) == "" ||
		len(p.SourceVersion) > MaxSourceVersion {
		return false
	}
	return true
}

// Marking carries the necessary ad / incentive / AI disclosure requirements
// (必要的广告/激励/AI 标识要求;不做规避平台风控的伪装).
type Marking struct {
	AdRequired         bool `json:"ad_required"`
	IncentiveDisclosed bool `json:"incentive_disclosed"`
	AIGenerated        bool `json:"ai_generated"`
}

// VersionSpec is the creation payload for one pack version.
type VersionSpec struct {
	ContentKind      ContentKind
	Video            AssetSnapshot // video 必填
	Cover            AssetSnapshot // video 必填
	ImageText        []AssetSnapshot // imagetext 必填(≥1)
	Title            string
	Body             string
	Topics           []string
	StoreName        string // 实际门店
	PriceNote        string // 实际价格
	ValidNote        string // 实际有效期
	PlatformVersions []PlatformVersion
	GrantRefs        []string // 授权引用快照
	GrantUntil       string   // 授权有效期(RFC3339,空=未声明到期)
	Marking          Marking
	Source           Source
	IdempotencyKey   string // 回流/制作工程去重键(可空;非空则租户内唯一)
}

// NormalizeText trims the edges of a text field.
func NormalizeText(s string) string { return strings.TrimSpace(s) }

// NormalizeSHA256 canonicalizes a fingerprint.
func NormalizeSHA256(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// NormalizeTopics trims, drops empties and dedupes preserving order.
func NormalizeTopics(topics []string) []string {
	out := make([]string, 0, len(topics))
	seen := make(map[string]bool, len(topics))
	for _, t := range topics {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// ValidateVersionSpec enforces the creation matrix: kind in enum; video needs
// video+cover snapshots; imagetext needs ≥1 snapshot; title/body/store/price/
// validity each mandatory with independent reason codes; platform adaptations
// must each carry platform + same-source traceability; grant_until (when set)
// must be RFC3339; the idempotency key (when set) must fit its cap.
func ValidateVersionSpec(in VersionSpec) error {
	in.Title = NormalizeText(in.Title)
	in.Body = NormalizeText(in.Body)
	in.StoreName = NormalizeText(in.StoreName)
	in.PriceNote = NormalizeText(in.PriceNote)
	in.ValidNote = NormalizeText(in.ValidNote)
	in.GrantUntil = NormalizeText(in.GrantUntil)
	kind := ContentKind(NormalizeText(string(in.ContentKind)))
	source := Source(NormalizeText(string(in.Source)))

	if !kind.Valid() {
		return invalid(ReasonBadContentKind, "content_kind")
	}
	if in.Title == "" {
		return invalid(ReasonMissingTitle, "title")
	}
	if len(in.Title) > MaxTitle {
		return invalid(ReasonBadTitle, "title")
	}
	if in.Body == "" {
		return invalid(ReasonMissingBody, "body")
	}
	if len(in.Body) > MaxBody {
		return invalid(ReasonBadBody, "body")
	}
	switch kind {
	case ContentKindVideo:
		if in.Video.empty() {
			return invalid(ReasonMissingVideoRef, "video")
		}
		if !in.Video.valid() {
			return invalid(ReasonBadSnapshot, "video")
		}
		if in.Cover.empty() {
			return invalid(ReasonMissingCoverRef, "cover")
		}
		if !in.Cover.valid() {
			return invalid(ReasonBadSnapshot, "cover")
		}
	case ContentKindImageText:
		if len(in.ImageText) == 0 {
			return invalid(ReasonMissingImageText, "image_text")
		}
		for i, img := range in.ImageText {
			if !img.valid() {
				return invalid(ReasonBadSnapshot, fmt.Sprintf("image_text[%d]", i))
			}
		}
	}
	if in.StoreName == "" {
		return invalid(ReasonMissingStoreName, "store_name")
	}
	if len(in.StoreName) > MaxStoreName {
		return invalid(ReasonBadStoreName, "store_name")
	}
	if in.PriceNote == "" {
		return invalid(ReasonMissingPriceNote, "price_note")
	}
	if len(in.PriceNote) > MaxPriceNote {
		return invalid(ReasonBadPriceNote, "price_note")
	}
	if in.ValidNote == "" {
		return invalid(ReasonMissingValidNote, "valid_note")
	}
	if len(in.ValidNote) > MaxValidNote {
		return invalid(ReasonBadValidNote, "valid_note")
	}
	topics := NormalizeTopics(in.Topics)
	if len(topics) > MaxTopics {
		return invalid(ReasonBadTopics, "topics")
	}
	for _, t := range topics {
		if len(t) > MaxTopicLen {
			return invalid(ReasonBadTopics, "topics")
		}
	}
	for i, pv := range in.PlatformVersions {
		if strings.TrimSpace(pv.Platform) == "" {
			return invalid(ReasonMissingPlatform, fmt.Sprintf("platform_versions[%d]", i))
		}
		if !pv.valid() {
			return invalid(ReasonBadPlatformVersion, fmt.Sprintf("platform_versions[%d]", i))
		}
	}
	if len(in.GrantRefs) > MaxGrantRefs {
		return invalid(ReasonBadTitle, "grant_refs")
	}
	for i, g := range in.GrantRefs {
		if strings.TrimSpace(g) == "" || len(g) > MaxGrantRef {
			return invalid(ReasonBadGrantRef, fmt.Sprintf("grant_refs[%d]", i))
		}
	}
	if in.GrantUntil != "" {
		if _, err := time.Parse(time.RFC3339, in.GrantUntil); err != nil {
			return invalid(ReasonBadGrantUntil, "grant_until")
		}
	}
	switch {
	case source == "":
		return invalid(ReasonMissingSource, "source")
	case !source.Valid():
		return invalid(ReasonBadSource, "source")
	}
	if key := NormalizeText(in.IdempotencyKey); key != "" && len(key) > MaxIdempotencyKey {
		return invalid(ReasonBadIdempotencyKey, "idempotency_key")
	}
	return nil
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// ---- adoption / distribution safety ---------------------------------------------

// GrantExpired reports whether the version's grant validity has passed at now.
// Empty grant_until never expires (HUI-1732 物理授权面落地前的引用级声明).
func GrantExpired(grantUntil string, now time.Time) bool {
	if NormalizeText(grantUntil) == "" {
		return false
	}
	until, err := time.Parse(time.RFC3339, grantUntil)
	if err != nil {
		return true // unparseable = treat as expired (fail-closed)
	}
	return !now.Before(until)
}

// Distributable answers whether a version in status with grantUntil may be
// handed to NEW distribution (采用/快照新分发). Blocked refuses always; an
// expired grant refuses; other statuses distribute.
func Distributable(status VersionStatus, grantUntil string, now time.Time) error {
	if status == StatusBlocked {
		return invalid(ReasonNotDistributable, "status")
	}
	if GrantExpired(grantUntil, now) {
		return invalid(ReasonGrantExpired, "grant_until")
	}
	return nil
}

// ValidateBlockReason enforces a non-empty human reason (处理记录需要商家语言).
func ValidateBlockReason(reason string) error {
	r := NormalizeText(reason)
	switch {
	case r == "":
		return invalid(ReasonMissingBlockReason, "reason")
	case len(r) > MaxBlockReason:
		return invalid(ReasonBadBody, "reason")
	}
	return nil
}

// ValidateIdempotencyKey checks the dedup key half (回流候选/制作交接共用).
func ValidateIdempotencyKey(key string) error {
	k := NormalizeText(key)
	switch {
	case k == "":
		return invalid(ReasonMissingIdempotencyKey, "idempotency_key")
	case len(k) > MaxIdempotencyKey:
		return invalid(ReasonBadIdempotencyKey, "idempotency_key")
	}
	return nil
}

// ---- controlled media handoff (受控下载 URL,供 HUI-1670 消费) -----------------------

// Handoff TTL discipline: default 10 minutes, hard cap 1 hour.
const (
	HandoffTTLDefault = 10 * time.Minute
	HandoffTTLMax     = time.Hour
)

// allowedContentTypes is the small whitelist the v1 publishers accept.
var allowedContentTypes = map[string]bool{
	"video/mp4":       true,
	"video/quicktime": true,
	"image/jpeg":      true,
	"image/png":       true,
	"image/webp":      true,
}

// HandoffClaim is the payload bound into a signed, expiring handoff token.
type HandoffClaim struct {
	VersionID   string    `json:"v"`
	AssetID     string    `json:"a"`
	SHA256      string    `json:"s"`
	ContentType string    `json:"ct"`
	Disposition string    `json:"cd"` // attachment | inline
	Filename    string    `json:"fn"`
	ExpiresAt   time.Time `json:"exp"`
}

// ContentDisposition renders the header value the publisher requires.
func (c HandoffClaim) ContentDisposition() string {
	if c.Disposition == "inline" {
		return "inline"
	}
	return fmt.Sprintf("attachment; filename=%q", c.Filename)
}

// NewHandoffClaim validates and fills a claim; ttl zero = default, above the
// cap refused (非永久公有化:短时效受控 URL).
func NewHandoffClaim(versionID, assetID, sha256, contentType, disposition, filename string, now time.Time, ttl time.Duration) (HandoffClaim, error) {
	switch {
	case strings.TrimSpace(versionID) == "" || strings.TrimSpace(assetID) == "":
		return HandoffClaim{}, invalid(ReasonMediaMissing, "version_id/asset_id")
	case !isSHA256Hex(NormalizeSHA256(sha256)):
		return HandoffClaim{}, invalid(ReasonBadSnapshot, "sha256")
	case !allowedContentTypes[contentType]:
		return HandoffClaim{}, invalid(ReasonBadContentType, "content_type")
	}
	switch disposition {
	case "attachment":
		if strings.TrimSpace(filename) == "" {
			return HandoffClaim{}, invalid(ReasonMissingHandoffFilename, "filename")
		}
	case "inline":
	default:
		return HandoffClaim{}, invalid(ReasonBadDisposition, "disposition")
	}
	if ttl == 0 {
		ttl = HandoffTTLDefault
	}
	if ttl < 0 || ttl > HandoffTTLMax {
		return HandoffClaim{}, invalid(ReasonBadHandoffTTL, "ttl")
	}
	return HandoffClaim{
		VersionID: strings.TrimSpace(versionID), AssetID: strings.TrimSpace(assetID),
		SHA256: NormalizeSHA256(sha256), ContentType: contentType, Disposition: disposition,
		Filename: strings.TrimSpace(filename), ExpiresAt: now.Add(ttl),
	}, nil
}

// handoffDomain separates this HMAC use from every other secret use.
const handoffDomain = "contentpack/media-handoff/v1"

func signHandoff(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(handoffDomain+"\x00"+secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// MintHandoff returns "<base64url(payload)>.<hex(hmac)>".
func MintHandoff(secret string, claim HandoffClaim) (string, error) {
	if strings.TrimSpace(secret) == "" {
		return "", invalid(ReasonHandoffInvalid, "secret")
	}
	payload, err := json.Marshal(claim)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + signHandoff(secret, []byte(body)), nil
}

// VerifyHandoff checks the signature, decodes the claim and enforces expiry.
// Any problem is a machine reason (handoff_invalid / handoff_expired).
func VerifyHandoff(secret, token string, now time.Time) (HandoffClaim, error) {
	if strings.TrimSpace(secret) == "" {
		return HandoffClaim{}, invalid(ReasonHandoffInvalid, "secret")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return HandoffClaim{}, invalid(ReasonHandoffInvalid, "token")
	}
	if !hmac.Equal([]byte(signHandoff(secret, []byte(parts[0]))), []byte(parts[1])) {
		return HandoffClaim{}, invalid(ReasonHandoffInvalid, "token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return HandoffClaim{}, invalid(ReasonHandoffInvalid, "token")
	}
	var claim HandoffClaim
	if err := json.Unmarshal(payload, &claim); err != nil {
		return HandoffClaim{}, invalid(ReasonHandoffInvalid, "token")
	}
	if !now.Before(claim.ExpiresAt) {
		return HandoffClaim{}, invalid(ReasonHandoffExpired, "expires_at")
	}
	return claim, nil
}
