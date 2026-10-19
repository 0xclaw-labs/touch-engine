// Package httpapi wires the HTTP surface: middleware (internal token ->
// identity session resolve -> tenant member resolution) and handlers.
//
// Fail-closed rules:
//   - missing internal token -> 401
//   - config gate problems   -> 503 config_gate (explicit, lists keys)
//   - identity unreachable   -> 503 identity_unavailable (no fake data)
//   - unauthenticated        -> 401
//   - no membership row      -> 403 not_member (never auto-provision)
//   - cross-tenant           -> 403 cross_tenant
//
// Two disjoint surfaces:
//   - /api/v1/... admin surface: platform session + membership required;
//   - /api/v1/public/... guest surface: NO session concept, GET-only, whitelist
//     fields only. A guest can never mutate anything; there is no code path
//     from the public surface to generation, billing, or any paid call.
package httpapi

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/brandctx"
	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/copyjob"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/leads"
	"github.com/bianjiefilm/touch-engine/server/internal/matrixconsume"
	"github.com/bianjiefilm/touch-engine/server/internal/participation"
	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff"
	"github.com/bianjiefilm/touch-engine/server/internal/readcache"
	"github.com/bianjiefilm/touch-engine/server/internal/redact"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/storemotion"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

const internalTokenHeader = "X-Internal-Token"
const tenantHeader = "X-Tenant-ID"

// HUI-3553 W5: trusted service producer headers (queue-view seam). The token is
// the service's OWN credential from TOUCH_SERVICE_TOKENS — never the shared
// BFF internal token.
const serviceIDHeader = "X-Service-ID"
const serviceTokenHeader = "X-Service-Token"
const serviceProducerRoleQueue = "queue_producer"

// Server is the API server.
type Server struct {
	Cfg     config.Config
	St      *store.Store
	ID      *identity.Client
	Upload  *upload.Client
	Brand   *brandctx.Client
	Log     *log.Logger
	closeDB func()

	// CopyModel replaces the platform text client when tests inject one.
	// Production leaves it nil: missing credentials stay fail-closed.
	CopyModel copyjob.TextModel

	// StoreMotionProbe counts model and render calls for the store-motion
	// slice. Production leaves it nil. The handlers pass it through and do
	// not call it.
	StoreMotionProbe *storemotion.Probe

	// LeadsLimiter gates the public lead-write surface per client IP
	// (single-process sliding window; not distributed by design for T1).
	LeadsLimiter *leads.RateLimiter

	// StaffTokenLimiter (HUI-3562) gates the staff token-entry surface
	// (staff/peek + staff/redeem) per member principal: manual entry of a
	// handoff token must not become a probing channel. Same single-process
	// sliding window as LeadsLimiter; tests may replace it with a tight one.
	StaffTokenLimiter *leads.RateLimiter

	// Matrix consumes a public-permission read and a matrix draft sink.
	// Nil means that supply is missing: only the matrix button is disabled.
	Matrix *matrixconsume.Consumer

	// PubCache / DashCache (HUI-2981 option C) are the bounded local read
	// caches behind FEATURE_PUBLIC_CACHE / FEATURE_DASHBOARD_CACHE. They are
	// always constructed (so the Disable() kill switch exists regardless) but
	// only consulted when the corresponding flag is on. flag off = handlers
	// never touch them = byte-identical legacy paths. Tests may replace them
	// with short-TTL instances.
	PubCache  *readcache.Cache[store.ResolvedRows]
	DashCache *readcache.Cache[dashboardEntry]

	// TpCache (HUI-1665 增量) is the stable-touchpoint twin of PubCache: the
	// /t/{code} alias rows behind the same epoch/TTL discipline. Consulted
	// only when FEATURE_PUBLIC_CACHE is on (one cache-policy decision); alias
	// create/disable/rebind all bumpEpoch, so 0 stale requests.
	TpCache *readcache.Cache[store.TouchpointRows]

	// Participation (HUI-3546) owns the participation/attempt/handoff
	// tables. Constructed always; its routes register only under
	// FEATURE_PARTICIPATION. The BenefitReserver stays nil until the reward
	// domain (HUI-1671) lands: reservations then remain pending and the
	// customer is never told 已保留.
	Participation *participation.Service

	// PublishHandoff (HUI-1670) owns the publish handoff state machine and
	// evidence events. Constructed always; its routes register only under
	// FEATURE_PUBLISH_HANDOFF. Without a receipt secret the verifier refuses
	// everything (NOT_VERIFIED residual) — honest by construction.
	PublishHandoff *publishhandoff.Service
}

// Frozen cache budgets (HUI-2981 comparison report §2.1 — do not tune without
// re-running the same-load comparison): public page entries 3s±1s (staleness
// bound 4s), dashboard entries 10s±2s (bound 12s, carried as as_of), with
// hard entry-count caps and epoch validation on every hit.
const (
	publicCacheCapacity = 10_000
	publicCacheTTL      = 3 * time.Second
	publicCacheJitter   = 2 * time.Second
	dashCacheCapacity   = 512
	dashCacheTTL        = 10 * time.Second
	dashCacheJitter     = 4 * time.Second
)

// dashboardEntry is one cached dashboard aggregation (facts + reference
// dimensions), stamped with the moment it was computed. Responses built from
// it carry as_of; the entry is valid only under the epoch it loaded under.
type dashboardEntry struct {
	facts store.DashboardFacts
	dims  store.DashboardDimensions
	asOf  time.Time
}

// leadsRatePerMinute bounds the public lead-write surface per client IP.
// Generous enough that legitimate retries/duplicates pass (they are idempotent
// anyway); strict enough to stop bulk form spam. Single-process scope by design.
const leadsRatePerMinute = 30

// staffTokenRatePerMinute (HUI-3562) bounds staff token-entry attempts per
// member across staff/peek + staff/redeem. A handoff token already carries
// 128-bit entropy + a checksum prefilter; the limiter is the second lock so
// manual entry can never become a probing channel either.
const staffTokenRatePerMinute = 30

// New builds a Server over an opened database.
func New(cfg config.Config, database *sql.DB, idc *identity.Client, upc *upload.Client, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}
	st := store.New(database)
	return &Server{
		Cfg: cfg, St: st, ID: idc, Upload: upc, Log: logger,
		LeadsLimiter:      leads.NewRateLimiter(leadsRatePerMinute, time.Minute),
		StaffTokenLimiter: leads.NewRateLimiter(staffTokenRatePerMinute, time.Minute),
		Matrix:            matrixFromConfig(cfg),
		PubCache: readcache.New[store.ResolvedRows](readcache.Options{
			Capacity: publicCacheCapacity, TTL: publicCacheTTL, Jitter: publicCacheJitter,
			Epoch: st.Epoch,
		}),
		TpCache: readcache.New[store.TouchpointRows](readcache.Options{
			Capacity: publicCacheCapacity, TTL: publicCacheTTL, Jitter: publicCacheJitter,
			Epoch: st.Epoch,
		}),
		DashCache: readcache.New[dashboardEntry](readcache.Options{
			Capacity: dashCacheCapacity, TTL: dashCacheTTL, Jitter: dashCacheJitter,
			Epoch: st.Epoch,
		}),
		Participation: participation.New(database, nil, participation.Options{}),
		PublishHandoff: publishhandoff.New(database, publishhandoff.Options{
			MediaSecret:      cfg.InternalToken,
			MediaBaseURL:     cfg.PublishHandoffMediaBaseURL,
			ReceiptVerifier:  publishhandoff.NewHMACVerifier(cfg.PublishHandoffReceiptSecret),
			AppRef:           cfg.PublishHandoffAppRef,
			H5EntryBase:      cfg.PublishHandoffH5EntryBase,
			MiniappEntryBase: cfg.PublishHandoffMiniappEntryBase,
		}),
	}
}

// matrixFromConfig installs the permission and draft clients only when a base
// URL is configured. Both empty keeps Matrix nil, which the handler treats as
// missing supply without opening a connection.
func matrixFromConfig(cfg config.Config) *matrixconsume.Consumer {
	perm := strings.TrimSpace(cfg.PublicPermissionURL)
	draft := strings.TrimSpace(cfg.MatrixDraftURL)
	if perm == "" && draft == "" {
		return nil
	}
	return matrixconsume.New(matrixconsume.NewHTTPPermission(perm, nil), matrixconsume.NewHTTPDraftSink(draft, nil), nil)
}

// Open opens the database and returns a ready Server.
func Open(cfg config.Config, logger *log.Logger) (*Server, error) {
	d, err := db.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	idc := &identity.Client{BaseURL: cfg.IdentityBaseURL, Token: cfg.IdentityToken, AppID: cfg.AppID}
	upc := &upload.Client{BaseURL: cfg.UploadBaseURL, Token: cfg.UploadToken, AppID: cfg.AppID}
	s := New(cfg, d, idc, upc, logger)
	if cfg.FeatureBrand {
		s.Brand = &brandctx.Client{BaseURL: cfg.BrandBaseURL, Token: cfg.BrandToken}
	}
	// HUI-3535: load the vendored channel-admission seed truth at startup when
	// the surface is registered. The seed is compile-locked to the versioned
	// contract by package tests; a load failure is a build bug and fails the
	// start (fail-closed) rather than serving an empty admission table.
	if cfg.FeatureChannelAdmission {
		seed, err := channeladmission.EmbeddedSeed()
		if err != nil {
			return nil, err
		}
		if _, err := s.St.LoadAdmissionSeed(seed); err != nil {
			return nil, err
		}
	}
	s.closeDB = func() { d.Close() }
	return s, nil
}

// Close releases the database handle.
func (s *Server) Close() {
	if s.closeDB != nil {
		s.closeDB()
	}
}

// ---- caller context ---------------------------------------------------------

type caller struct {
	Principal identity.Principal
	Member    *store.Member
	Brand     *brandGate
}

type brandCtxKey int

const brandKey brandCtxKey = 2

func brandFrom(r *http.Request) *brandGate {
	if v, ok := r.Context().Value(brandKey).(*brandGate); ok {
		return v
	}
	if c := callerFrom(r); c != nil {
		return c.Brand
	}
	return nil
}

type ctxKey int

const callerKey ctxKey = 1

// serviceProducerKey carries the verified trusted-service identity on the
// queue-view producer path (no session/member exists there).
const serviceProducerKey ctxKey = 3

// serviceProducer is the verified service producer identity.
type serviceProducer struct {
	ServiceID string
}

// serviceProducerFrom returns the verified producer, or nil on the legacy
// session path.
func serviceProducerFrom(ctx context.Context) *serviceProducer {
	if v, ok := ctx.Value(serviceProducerKey).(*serviceProducer); ok {
		return v
	}
	return nil
}

func callerFrom(r *http.Request) *caller {
	if v, ok := r.Context().Value(callerKey).(*caller); ok {
		return v
	}
	return nil
}

// authzMember maps the resolved membership onto the authz core. FEAT-0176:
// agent-role members get AgencyActive backfilled here — the SINGLE server-side
// point where 代管关系 activity enters authorization (any store error answers
// false, fail-closed). Non-agent roles skip the lookup entirely (zero cost,
// existing semantics byte-identical).
func (s *Server) authzMember(c *caller) *authz.Member {
	if c == nil || c.Member == nil {
		return nil
	}
	am := &authz.Member{ID: c.Member.ID, TenantID: c.Member.TenantID, PrincipalRef: c.Member.PrincipalRef,
		Role: authz.Role(c.Member.Role), StoreScope: c.Member.StoreScope, Enabled: c.Member.Enabled}
	if am.Role == authz.RoleAgent {
		am.AgencyActive = s.St.HasActiveAgencyRelation(c.Member.TenantID, c.Member.PrincipalRef)
	}
	return am
}

// ---- responses --------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": code, "message": message})
}

// ---- routing ----------------------------------------------------------------

// Handler returns the root handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealthz)
	// Authorized submission fetch for leads ingest. Contact fields stay off
	// the notify event; the registered source pulls them with the internal token.
	mux.Handle("GET /internal/v1/lead-records/{submissionRef}", s.requireInternal(s.handleLeadRecord))

	// auth (internal token required; no session yet for login/refresh)
	mux.Handle("POST /api/v1/auth/login", s.requireInternal(s.handleLogin))
	mux.Handle("POST /api/v1/auth/refresh", s.requireInternal(s.handleRefresh))
	mux.Handle("POST /api/v1/auth/logout", s.requireInternal(s.handleLogout))

	// authenticated admin surface (商家后台)
	mux.Handle("GET /api/v1/whoami", s.requireSession(s.handleWhoami))
	mux.Handle("GET /api/v1/session/memberships", s.requirePrincipal(s.handleSessionMemberships))
	mux.Handle("GET /api/v1/workbench", s.requireSession(s.handleWorkbench))

	mux.Handle("GET /api/v1/stores", s.requireSession(s.handleStoreList))
	mux.Handle("POST /api/v1/stores", s.requireSession(s.handleStoreCreate))
	mux.Handle("GET /api/v1/stores/{id}", s.requireSession(s.handleStoreGet))
	// HUI-1674 连锁多门店:org_owner 治理面(改店/停用店)。停用不级联:
	// 存量活动逐个显式处理,公共页附「门店暂不可用」标注。
	mux.Handle("PATCH /api/v1/stores/{id}", s.requireSession(s.handleStorePatch))
	mux.Handle("POST /api/v1/stores/{id}/status", s.requireSession(s.handleStoreStatus))

	mux.Handle("GET /api/v1/campaigns", s.requireSession(s.handleCampaignList))
	mux.Handle("POST /api/v1/campaigns", s.requireSession(s.handleCampaignCreate))
	mux.Handle("GET /api/v1/campaigns/{id}", s.requireSession(s.handleCampaignGet))
	mux.Handle("PATCH /api/v1/campaigns/{id}", s.requireSession(s.handleCampaignPatch))
	mux.Handle("POST /api/v1/campaigns/{id}/status", s.requireSession(s.handleCampaignStatus))
	mux.Handle("POST /api/v1/campaigns/{id}/matrix-draft", s.requireSession(s.handleMatrixDraft))

	mux.Handle("GET /api/v1/campaigns/{id}/assets", s.requireSession(s.handleAssetList))
	mux.Handle("POST /api/v1/campaigns/{id}/assets", s.requireSession(s.handleAssetAdd))
	mux.Handle("DELETE /api/v1/campaigns/{id}/assets/{assetId}", s.requireSession(s.handleAssetRemove))

	// HUI-3553 活动内容包:管理面不加 FEATURE 开关(仓内惯例)。管理动作
	// (建包/建版本/明确采用/阻止新分发/回流候选/制作交接/受控媒体交接铸造)
	// 仅 org_owner(authz.manage_content_packs);查询与交接兑换按既有业务角色。
	// 顾客可触达的快照读面单独走 FEATURE_CONTENT_PACKS(默认 off,见下方
	// 公共面注册点)。
	mux.Handle("POST /api/v1/campaigns/{id}/content-packs", s.requireSession(s.handleContentPackCreate))
	mux.Handle("GET /api/v1/campaigns/{id}/content-packs", s.requireSession(s.handleContentPackList))
	mux.Handle("GET /api/v1/content-packs/{packId}", s.requireSession(s.handleContentPackGet))
	mux.Handle("POST /api/v1/content-packs/{packId}/versions", s.requireSession(s.handleContentPackVersionCreate))
	mux.Handle("POST /api/v1/content-packs/{packId}/versions/{versionId}/adopt", s.requireSession(s.handleContentPackVersionAdopt))
	mux.Handle("POST /api/v1/content-packs/{packId}/versions/{versionId}/block", s.requireSession(s.handleContentPackVersionBlock))
	mux.Handle("POST /api/v1/campaigns/{id}/content-packs/{packId}/return-candidates", s.requireSession(s.handleReturnCandidate))
	mux.Handle("GET /api/v1/content-handoffs/capabilities", s.requireSession(s.handleContentHandoffCapabilities))
	mux.Handle("POST /api/v1/campaigns/{id}/content-handoffs", s.requireSession(s.handleContentHandoffCreate))
	// MX-OPS R2 (HUI-3553): restore list + trusted queue-view sync seam. The
	// queue view is contract-validated and stored as the PUBLIC projection only.
	mux.Handle("GET /api/v1/campaigns/{id}/content-handoffs", s.requireSession(s.handleContentHandoffList))
	// HUI-3553 W5: dual auth — trusted service producer (per-service token,
	// tenant from the handoff row) OR legacy session+manage (unchanged).
	mux.Handle("POST /api/v1/content-handoffs/{handoffId}/queue-view", s.requireServiceProducer(s.handleContentHandoffQueueView))
	mux.Handle("POST /api/v1/content-pack-versions/{versionId}/media-handoff", s.requireSession(s.handleMediaHandoffMint))
	mux.Handle("GET /api/v1/media-handoffs/{token}", s.requireSession(s.handleMediaHandoffRedeem))

	mux.Handle("GET /api/v1/campaigns/{id}/links", s.requireSession(s.handleLinkList))
	mux.Handle("POST /api/v1/campaigns/{id}/links", s.requireSession(s.handleLinkCreate))
	mux.Handle("POST /api/v1/campaigns/{id}/links/{linkId}/enabled", s.requireSession(s.handleLinkEnabled))

	// HUI-1668 copy drafts stay on the merchant surface. There is no public
	// route: a guest tap cannot start generation or billing. Without a model
	// authorization the handler keeps the draft structure and usable=false.
	mux.Handle("POST /api/v1/campaigns/{id}/copy-drafts", s.requireSession(s.handleCopyDraftCreate))
	mux.Handle("GET /api/v1/campaigns/{id}/copy-drafts/{draftId}", s.requireSession(s.handleCopyDraftGet))
	mux.Handle("POST /api/v1/campaigns/{id}/copy-drafts/{draftId}/versions", s.requireSession(s.handleCopyVersionAccept))

	// HUI-2748: record store parameters and an unverified Motion declaration.
	// No public route. Changing a parameter does not render or call a model.
	mux.Handle("PUT /api/v1/campaigns/{id}/store-motion", s.requireSession(s.handleStoreMotionPut))
	mux.Handle("GET /api/v1/campaigns/{id}/store-motion", s.requireSession(s.handleStoreMotionGet))
	mux.Handle("GET /api/v1/campaigns/{id}/motion-handoff", s.requireSession(s.handleMotionHandoffGet))

	// Quote → confirm → generate → select → save. No public route: a guest
	// tap cannot start a merchant copy job or a paid model call.
	mux.Handle("POST /api/v1/campaigns/{id}/copy-jobs", s.requireSession(s.handleCopyJobQuote))
	mux.Handle("GET /api/v1/campaigns/{id}/copy-jobs/{jobId}", s.requireSession(s.handleCopyJobGet))
	mux.Handle("POST /api/v1/campaigns/{id}/copy-jobs/{jobId}/confirm", s.requireSession(s.handleCopyJobConfirm))
	mux.Handle("POST /api/v1/campaigns/{id}/copy-jobs/{jobId}/generate", s.requireSession(s.handleCopyJobGenerate))
	mux.Handle("POST /api/v1/campaigns/{id}/copy-jobs/{jobId}/select", s.requireSession(s.handleCopyJobSelect))
	mux.Handle("POST /api/v1/campaigns/{id}/copy-jobs/{jobId}/revoke", s.requireSession(s.handleCopyJobRevoke))
	mux.Handle("POST /api/v1/campaigns/{id}/copy-jobs/{jobId}/save", s.requireSession(s.handleCopyJobSave))

	// QR fallback entry (HUI-1664 FEAT-0165): owner-only export of the
	// canonical short-code URL as a server-rendered PNG (or json payload).
	mux.Handle("GET /api/v1/campaigns/{id}/links/{linkId}/qrcode", s.requireSession(s.handleLinkQRCode))

	// NFC tag management (HUI-1665 FEAT-0166): owner-only CRUD/batch/export.
	// Tags wrap EXISTING campaign_links (no second link model); disable/enable
	// propagates to the bound link so the public route follows the existing
	// five-state logic. No FEATURE gate: this is an admin surface.
	mux.Handle("GET /api/v1/nfc/tag-groups", s.requireSession(s.handleTagGroupList))
	mux.Handle("POST /api/v1/nfc/tag-groups", s.requireSession(s.handleTagGroupCreate))
	mux.Handle("DELETE /api/v1/nfc/tag-groups/{id}", s.requireSession(s.handleTagGroupDelete))
	mux.Handle("GET /api/v1/nfc/tags/export.csv", s.requireSession(s.handleTagExportCSV))
	mux.Handle("GET /api/v1/nfc/tags", s.requireSession(s.handleTagList))
	mux.Handle("POST /api/v1/nfc/tags/batch", s.requireSession(s.handleTagBatchCreate))
	mux.Handle("GET /api/v1/nfc/tags/{id}", s.requireSession(s.handleTagGet))
	mux.Handle("PATCH /api/v1/nfc/tags/{id}", s.requireSession(s.handleTagPatch))
	mux.Handle("DELETE /api/v1/nfc/tags/{id}", s.requireSession(s.handleTagDelete))
	mux.Handle("POST /api/v1/nfc/tags/{id}/status", s.requireSession(s.handleTagStatus))

	// Touchpoint stable entries (HUI-1665 增量): the /work/touchpoints management
	// surface. Tags wrap campaign_links; aliases are the per-card independent
	// entry layer on top (disable/rebind never touch campaign_links). No
	// FEATURE gate: this is an admin surface (仓内惯例). Reuse the NFC face
	// authorization (org_owner全租户 / store_manager本店, 服务端强制作用域).
	mux.Handle("GET /api/v1/touchpoints", s.requireSession(s.handleTouchpointList))
	mux.Handle("POST /api/v1/touchpoints", s.requireSession(s.handleTouchpointProvision))
	mux.Handle("GET /api/v1/touchpoints/export.csv", s.requireSession(s.handleTouchpointExportCSV))
	mux.Handle("GET /api/v1/touchpoints/{id}", s.requireSession(s.handleTouchpointGet))
	mux.Handle("POST /api/v1/touchpoints/{id}/rebind-preview", s.requireSession(s.handleTouchpointRebindPreview))
	mux.Handle("POST /api/v1/touchpoints/{id}/rebind", s.requireSession(s.handleTouchpointRebind))
	mux.Handle("POST /api/v1/touchpoints/{id}/status", s.requireSession(s.handleTouchpointStatus))
	mux.Handle("POST /api/v1/touchpoints/{id}/stage", s.requireSession(s.handleTouchpointStage))
	mux.Handle("GET /api/v1/touchpoints/{id}/qrcode", s.requireSession(s.handleTouchpointQRCode))

	// 公共稳定入口解析(FEATURE_TOUCHPOINT_ENTRY 默认 off;off = 路由不注册且
	// handler gate 再答统一 404 —— 双保险,同 FEATURE_DASHBOARD 写法)。GET-only,
	// 白名单 {state}/{state,code};available 交既有 /c/{code} 呈现(1896 零重复),
	// 旧 /c 链接语义与权限零变化。
	if s.Cfg.FeatureTouchpointEntry {
		mux.Handle("GET /api/v1/public/touchpoints/{code}", http.HandlerFunc(s.handlePublicTouchpoint))
	}

	mux.Handle("GET /api/v1/admin/members", s.requireSession(s.handleMemberList))
	mux.Handle("POST /api/v1/admin/members", s.requireSession(s.handleMemberCreate))
	mux.Handle("PATCH /api/v1/admin/members/{id}", s.requireSession(s.handleMemberPatch))

	// public guest surface (公共活动页): no session. Reads stay on the whitelist.
	// A benefit claim records an activity fact only. It does not create a platform
	// user, show an enterprise balance, or debit a wallet.
	mux.Handle("GET /api/v1/public/links/{code}", http.HandlerFunc(s.handlePublicLink))
	mux.Handle("POST /api/v1/public/links/{code}/benefit-claims", http.HandlerFunc(s.handlePublicBenefitClaim))
	mux.Handle("GET /api/v1/account-separation", s.requireSession(s.handleAccountSeparation))
	mux.Handle("POST /api/v1/account-separation/charges", s.requireSession(s.handleAccountCharge))
	mux.Handle("POST /api/v1/account-separation/recharge", s.requireSession(s.handleAccountRecharge))

	// public lead-capture surface (HUI-1747): the ONLY guest-write surface.
	// Gated by FEATURE_LEADS_CAPTURE (off -> uniform 404, surface invisible).
	mux.Handle("GET /api/v1/public/links/{code}/lead-form", http.HandlerFunc(s.handlePublicLeadForm))
	mux.Handle("POST /api/v1/public/links/{code}/lead-submissions", http.HandlerFunc(s.handlePublicLeadSubmit))
	mux.Handle("POST /api/v1/public/links/{code}/lead-status", http.HandlerFunc(s.handlePublicLeadStatus))
	mux.Handle("POST /api/v1/public/links/{code}/lead-revocations", http.HandlerFunc(s.handlePublicLeadRevoke))
	mux.Handle("POST /api/v1/public/links/{code}/view-events", http.HandlerFunc(s.handlePublicViewEvent))

	// HUI-1670 customer preview / manual publish. No platform outbound.
	mux.Handle("GET /api/v1/public/links/{code}/publish-capabilities", http.HandlerFunc(s.handlePublicPublishCapabilities))
	mux.Handle("POST /api/v1/public/links/{code}/publish-attempts", http.HandlerFunc(s.handlePublicPublishPreview))
	mux.Handle("POST /api/v1/public/links/{code}/publish-attempts/{id}/export", http.HandlerFunc(s.handlePublicPublishExport))
	mux.Handle("POST /api/v1/public/links/{code}/publish-attempts/{id}/confirm", http.HandlerFunc(s.handlePublicPublishConfirm))
	mux.Handle("POST /api/v1/public/links/{code}/publish-attempts/{id}/self-report", http.HandlerFunc(s.handlePublicPublishSelfReport))
	mux.Handle("POST /api/v1/public/links/{code}/publish-attempts/{id}/publish", http.HandlerFunc(s.handlePublicPublishRequest))
	mux.Handle("POST /api/v1/public/links/{code}/publish-attempts/{id}/open-editor", http.HandlerFunc(s.handlePublicPublishOpenEditor))
	mux.Handle("PATCH /api/v1/public/links/{code}/publish-attempts/{id}", http.HandlerFunc(s.handlePublicPublishRevise))
	mux.Handle("POST /api/v1/publish-adapters", s.requireSession(s.handlePublishAdapterNote))

	// HUI-1671 publish reward. Real coupons and fee charges stay off.
	mux.Handle("GET /api/v1/public/links/{code}/publish-reward", http.HandlerFunc(s.handlePublicPublishReward))
	mux.Handle("POST /api/v1/public/links/{code}/publish-attempts/{id}/reward-claim", http.HandlerFunc(s.handlePublicRewardClaim))
	mux.Handle("POST /api/v1/public/links/{code}/publish-attempts/{id}/reward-proof", http.HandlerFunc(s.handlePublicRewardProof))
	mux.Handle("PUT /api/v1/campaigns/{id}/publish-reward", s.requireSession(s.handlePublishRewardPut))

	// HUI-1672 extra jumps. Guests only see configured public https targets.
	mux.Handle("GET /api/v1/public/links/{code}/extra-jumps", http.HandlerFunc(s.handlePublicExtraJumps))
	mux.Handle("POST /api/v1/public/links/{code}/extra-jumps/{kind}/clicks", http.HandlerFunc(s.handlePublicExtraJumpClick))
	mux.Handle("POST /api/v1/public/links/{code}/returns/clicks", http.HandlerFunc(s.handlePublicReturnClick))
	mux.Handle("GET /api/v1/campaigns/{id}/extra-jumps", s.requireSession(s.handleExtraJumpsGet))
	mux.Handle("PUT /api/v1/campaigns/{id}/extra-jumps", s.requireSession(s.handleExtraJumpsPut))

	// HUI-1673 private domain. WeCom and community links stay clicks until a callback exists.
	mux.Handle("GET /api/v1/public/links/{code}/private-domain", http.HandlerFunc(s.handlePublicPrivateDomain))
	mux.Handle("POST /api/v1/public/links/{code}/private-domain/{kind}/clicks", http.HandlerFunc(s.handlePublicPrivateDomainClick))
	mux.Handle("PUT /api/v1/campaigns/{id}/private-domain", s.requireSession(s.handlePrivateDomainPut))
	mux.Handle("POST /api/v1/campaigns/{id}/publish-reward/proofs/{proofId}/review", s.requireSession(s.handlePublishRewardReview))

	// admin lead surface (HUI-1747): merchant visibility of their own leads.
	mux.Handle("POST /api/v1/campaigns/{id}/lead-form", s.requireSession(s.handleLeadFormUpsert))
	mux.Handle("GET /api/v1/campaigns/{id}/leads", s.requireSession(s.handleLeadList))
	mux.Handle("GET /api/v1/campaigns/{id}/lead-stats", s.requireSession(s.handleLeadStats))
	mux.Handle("GET /api/v1/campaigns/{id}/leads/{ref}/audit", s.requireSession(s.handleLeadAudit))

	// dashboard read surface (HUI-1677 FEAT-0178): gated by FEATURE_DASHBOARD
	// (default off; off = route not registered AND handler gate answers uniform
	// 404 — an invisible surface). Read-only aggregation, no write paths.
	// /operations(+ /operations-summary) 是 2026-10-14 经营数据增量(HUI-1677):
	// 同一开关,不新建开关名。
	if s.Cfg.FeatureDashboard {
		mux.Handle("GET /api/v1/dashboard", s.requireSession(s.handleDashboard))
		mux.Handle("GET /api/v1/dashboard/operations", s.requireSession(s.handleDashboardOperations))
		mux.Handle("GET /api/v1/dashboard/operations-summary", s.requireSession(s.handleDashboardOperationsSummary))
	}

	// campaign rules (HUI-1676 FEAT-0177): 登记制开关 FEATURE_CAMPAIGN_RULES
	// (默认 off;off = 路由不注册且 handler gate 再答统一 404 —— 与
	// FEATURE_DASHBOARD 的 off 双保险同一写法)。规则读写仅 org_owner
	// (authz.manage_campaign_rules);确定性评估在服务端单点完成,BFF 零业务判断。
	if s.Cfg.FeatureCampaignRules {
		mux.Handle("GET /api/v1/campaigns/{id}/rules", s.requireSession(s.handleCampaignRulesGet))
		mux.Handle("PUT /api/v1/campaigns/{id}/rules", s.requireSession(s.handleCampaignRulesPut))
		mux.Handle("DELETE /api/v1/campaigns/{id}/rules", s.requireSession(s.handleCampaignRulesDelete))
		mux.Handle("GET /api/v1/campaigns/{id}/rules/revisions", s.requireSession(s.handleCampaignRulesRevisions))

		// publish config (HUI-1676 增量): 商家配置与真实启用 —— 同一开关、
		// 同一授权(manage_campaign_rules)。启用前检查在服务端单点完成
		// (campaignenable.Check),拒绝带机器原因码;暂停只动配置行,
		// 参与/奖励零触碰。
		mux.Handle("GET /api/v1/campaigns/{id}/publish-config", s.requireSession(s.handlePublishConfigGet))
		mux.Handle("PUT /api/v1/campaigns/{id}/publish-config", s.requireSession(s.handlePublishConfigPut))
		mux.Handle("POST /api/v1/campaigns/{id}/publish-config/enable", s.requireSession(s.handlePublishConfigEnable))
		mux.Handle("POST /api/v1/campaigns/{id}/publish-config/pause", s.requireSession(s.handlePublishConfigPause))
		mux.Handle("GET /api/v1/campaigns/{id}/publish-config/snapshot", s.requireSession(s.handlePublishConfigSnapshot))
		mux.Handle("GET /api/v1/campaigns/{id}/publish-config/revisions", s.requireSession(s.handlePublishConfigRevisions))
	}

	// asset library (HUI-1666 FEAT-0167 商家素材库 v1): 登记制开关
	// FEATURE_ASSET_LIB(默认 off;off = 路由不注册且 handler gate 再答统一
	// 404 —— 同一双保险写法,面板不可见)。管理动作(登记/导入素材、池增删改、
	// 池内引用增删、候选标记)仅 org_owner(authz.manage_asset_lib);选择/调取
	// 按既有业务角色(ActionCreate/ActionReadList)。v1 不建物理文件仓库:
	// 接口只收元数据+指纹;物理授权资产版本面(HUI-1732)未建,grant_ref 为
	// 不透明声明引用。
	if s.Cfg.FeatureAssetLib {
		mux.Handle("POST /api/v1/assets", s.requireSession(s.handleLibAssetRegister))
		mux.Handle("POST /api/v1/assets/import", s.requireSession(s.handleLibAssetImport))
		mux.Handle("GET /api/v1/assets", s.requireSession(s.handleLibAssetList))
		mux.Handle("GET /api/v1/assets/{id}", s.requireSession(s.handleLibAssetGet))
		mux.Handle("POST /api/v1/assets/{id}/candidate", s.requireSession(s.handleLibAssetCandidate))

		mux.Handle("POST /api/v1/asset-pools", s.requireSession(s.handleLibPoolCreate))
		mux.Handle("GET /api/v1/asset-pools", s.requireSession(s.handleLibPoolList))
		mux.Handle("GET /api/v1/asset-pools/{id}", s.requireSession(s.handleLibPoolGet))
		mux.Handle("PATCH /api/v1/asset-pools/{id}", s.requireSession(s.handleLibPoolPatch))
		mux.Handle("DELETE /api/v1/asset-pools/{id}", s.requireSession(s.handleLibPoolDelete))
		mux.Handle("POST /api/v1/asset-pools/{id}/items", s.requireSession(s.handleLibPoolItemAdd))
		mux.Handle("GET /api/v1/asset-pools/{id}/items", s.requireSession(s.handleLibPoolItemList))
		mux.Handle("DELETE /api/v1/asset-pools/{id}/items/{itemId}", s.requireSession(s.handleLibPoolItemRemove))
		mux.Handle("POST /api/v1/asset-pools/{id}/draw", s.requireSession(s.handleLibPoolDraw))
		mux.Handle("GET /api/v1/asset-pools/{id}/selections", s.requireSession(s.handleLibPoolSelections))
	}

	// video templates (HUI-1669 FEAT-0170 视频模板管理): 登记制开关
	// FEATURE_VIDEO_TEMPLATES(默认 off;off = 全部路由不注册且 handler gate
	// 再答统一 404 —— 同一双保险写法,面板不可见)。管理动作(模板 CRUD/
	// 发布/版本/分配)仅 org_owner(authz.manage_video_templates);查询按
	// 既有业务角色(本店分配查询按 store 作用域)。模板=素材引用集的组织
	// 单元,零物理存储;视频合成/渲染执行不做(接口零生成/渲染触发字段)。
	if s.Cfg.FeatureVideoTemplates {
		mux.Handle("POST /api/v1/video-templates", s.requireSession(s.handleVideoTemplateCreate))
		mux.Handle("GET /api/v1/video-templates", s.requireSession(s.handleVideoTemplateList))
		mux.Handle("GET /api/v1/video-templates/{id}", s.requireSession(s.handleVideoTemplateGet))
		mux.Handle("PATCH /api/v1/video-templates/{id}", s.requireSession(s.handleVideoTemplatePatch))
		mux.Handle("DELETE /api/v1/video-templates/{id}", s.requireSession(s.handleVideoTemplateDelete))
		mux.Handle("GET /api/v1/video-templates/{id}/versions", s.requireSession(s.handleVideoTemplateVersionList))
		mux.Handle("POST /api/v1/video-templates/{id}/versions", s.requireSession(s.handleVideoTemplateVersionCreate))
		mux.Handle("GET /api/v1/video-templates/{id}/versions/{version}", s.requireSession(s.handleVideoTemplateVersionGet))
		mux.Handle("PUT /api/v1/video-templates/{id}/versions/{version}", s.requireSession(s.handleVideoTemplateVersionPut))
		mux.Handle("POST /api/v1/video-templates/{id}/publish", s.requireSession(s.handleVideoTemplatePublish))
		mux.Handle("POST /api/v1/video-templates/{id}/assignments", s.requireSession(s.handleVideoTemplateAssign))
		mux.Handle("GET /api/v1/video-templates/{id}/assignments", s.requireSession(s.handleVideoTemplateAssignmentsByTemplate))
		mux.Handle("DELETE /api/v1/video-templates/{id}/assignments/{storeId}", s.requireSession(s.handleVideoTemplateUnassign))
		mux.Handle("GET /api/v1/stores/{id}/video-templates", s.requireSession(s.handleVideoTemplatesByStore))
	}

	// agency (HUI-1675 FEAT-0176 代理与子账号,单级代理 v1): 登记制开关
	// FEATURE_AGENCY(默认 off;off = 全部路由不注册且 handler gate 再答统一
	// 404 —— 同一双保险写法,面板不可见)。建立/解除代管限平台/owner 类角色
	// (authz.manage_agency,org_owner 专属);开子账号与留痕查询走
	// authz.manage_agency_subaccounts(org_owner 恒可;agent 仅当激活代管关系,
	// 由 authz 按 AgencyActive 单点裁决)。代理数据可见性 = 既有租户成员行门槛
	// + 激活代管关系门槛,双 fail-closed;解除代管立即收窄。子账号 = 既有成员
	// 角色(staff/store_manager),不发明第二套身份/权限体系。不复制租户、
	// 不动 tenants 语义;多级代理(代理下再挂代理)deferred。
	if s.Cfg.FeatureAgency {
		mux.Handle("POST /api/v1/agency/relations", s.requireSession(s.handleAgencyRelationCreate))
		mux.Handle("GET /api/v1/agency/relations", s.requireSession(s.handleAgencyRelationList))
		mux.Handle("DELETE /api/v1/agency/relations/{id}", s.requireSession(s.handleAgencyRelationRevoke))
		mux.Handle("POST /api/v1/agency/sub-accounts", s.requireSession(s.handleAgencySubAccountCreate))
		mux.Handle("GET /api/v1/agency/sub-accounts", s.requireSession(s.handleAgencySubAccountList))
	}

	// HUI-2053 brand shell + tenant lifecycle/export. Off = routes absent.
	// These routes do not write the brand registry and do not treat a host
	// as a tenant or a payer.
	if s.Cfg.FeatureBrand {
		mux.Handle("GET /api/v1/public/brand-shell", http.HandlerFunc(s.handlePublicBrandShell))
		mux.Handle("POST /api/v1/tenant-lifecycle", s.requireSession(s.handleTenantLifecycle))
		mux.Handle("POST /api/v1/tenant-exports", s.requireSession(s.handleTenantExportCreate))
		mux.Handle("GET /api/v1/tenant-exports/{id}", s.requireSession(s.handleTenantExportGet))
		mux.Handle("POST /api/v1/tenant-exports/{id}/revoke", s.requireSession(s.handleTenantExportRevoke))
	}

	// HUI-3553 顾客侧内容快照读(1670/1896 消费):登记制开关
	// FEATURE_CONTENT_PACKS(默认 off;off = 路由不注册 + handler gate 再答
	// 统一 404 —— 双保险同款)。GET-only 白名单字段;blocked/授权过期版本
	// 拒新分发;访客多次碰触零生成通道(结构保证)。
	if s.Cfg.FeatureContentPacks {
		mux.Handle("GET /api/v1/public/links/{code}/content-choices", http.HandlerFunc(s.handlePublicContentChoices))
		mux.Handle("GET /api/v1/public/links/{code}/content-snapshot", http.HandlerFunc(s.handlePublicContentSnapshot))
	}
	// HUI-1671 B3 真实奖励交付: /work 管理面(无开关)、受信预留(internal token)、
	// 公共 Summary(FEATURE_REWARD_OPS 默认 off)注册段集中在一个方法里。
	s.registerRewardOpsRoutes(mux)

	// HUI-3546 participation & cross-device resume. 登记制开关
	// FEATURE_PARTICIPATION(默认 off;off = 全部路由不注册且 handler gate
	// 再答统一 404 —— 双保险,同 FEATURE_DASHBOARD 写法)。顾客面无会话概念:
	// 首方 visitor cookie 只做同浏览器续接,跨端走短时/单用途/可撤销交接
	// 凭据(码 ≠ 发奖授权);店员面走 requireSession + 既有 authz 门店作用域。
	// 权益预留只经 BenefitReserver 受信端口,未确认绝不返回"已保留"。
	if s.Cfg.FeatureParticipation {
		mux.Handle("POST /api/v1/participations", http.HandlerFunc(s.handleParticipationStart))
		mux.Handle("POST /api/v1/participations/lookup", http.HandlerFunc(s.handleParticipationLookup))
		mux.Handle("POST /api/v1/participations/handoff/redeem", http.HandlerFunc(s.handleParticipationRedeem))
		mux.Handle("GET /api/v1/participations/{id}", http.HandlerFunc(s.handleParticipationGet))
		mux.Handle("PATCH /api/v1/participations/{id}/content", http.HandlerFunc(s.handleParticipationContent))
		mux.Handle("POST /api/v1/participations/{id}/cancel", http.HandlerFunc(s.handleParticipationCancel))
		mux.Handle("GET /api/v1/participations/{id}/attempts", http.HandlerFunc(s.handleParticipationAttempts))
		mux.Handle("POST /api/v1/participations/{id}/attempts", http.HandlerFunc(s.handleParticipationAttemptCreate))
		mux.Handle("POST /api/v1/participations/{id}/handoff", http.HandlerFunc(s.handleParticipationHandoff))
		mux.Handle("DELETE /api/v1/participations/{id}/handoff/{credentialId}", http.HandlerFunc(s.handleParticipationHandoffRevoke))
		mux.Handle("GET /api/v1/participations/{id}/staff-view", s.requireSession(s.handleParticipationStaffView))
		mux.Handle("POST /api/v1/participations/staff/redeem", s.requireSession(s.handleParticipationStaffRedeem))
		// HUI-3562 店员台:扫/手输凭据开参与卡(不消费,重复扫码幂等)。
		mux.Handle("POST /api/v1/participations/staff/peek", s.requireSession(s.handleParticipationStaffPeek))
	}

	// channel admission (HUI-3535 渠道准入与承接清单): 登记制开关
	// FEATURE_CHANNEL_ADMISSION(默认 off;off = 路由不注册且 handler gate
	// 再答统一 404 —— 双保险同款;种子亦不装载,现行为逐字节不变)。on =
	// 启动装载 vendored 种子真值(docs/contracts/channel-admission/v1),两个
	// GET 只读端点:商家面(HUI-1676 消费,可选渠道+缺项原因)与顾客/交接面
	// (HUI-1670/1896 消费,当前可执行动作+缺字段提示,租户取自短码链路)。
	// 服务端强制闸是 channeladmission.Gate 纯函数:1670 交接前必须过闸,
	// 前端隐藏按钮不算数;unknown 永不等价 allowed。
	if s.Cfg.FeatureChannelAdmission {
		mux.Handle("GET /api/v1/channel-admissions", s.requireSession(s.handleChannelAdmissionList))
		mux.Handle("GET /api/v1/public/links/{code}/channel-admission", http.HandlerFunc(s.handlePublicChannelAdmission))
	}

	// publish handoff (HUI-1670 顾客发布交接): 登记制开关 FEATURE_PUBLISH_HANDOFF
	// (默认 off;off = 路由不注册 + handler gate 再答统一 404 —— 双保险同款)。
	// on = 顾客面(过 3535 闸 + contentpack 受控媒体 + 九态证据状态机)、店员
	// 人工事实面(requireSession + authz 门店作用域;永不转官方)与官方回执
	// 回调(服务端验签+五绑定+重放去重;密钥未配置一律拒绝 = NOT_VERIFIED)。
	// 路由集中在 publish-handoff / publish-receipts 前缀(BFF catch-all 透传)。
	if s.Cfg.FeaturePublishHandoff {
		mux.Handle("GET /api/v1/public/links/{code}/publish-handoff/routes", http.HandlerFunc(s.handlePublicPublishHandoffRoutes))
		mux.Handle("POST /api/v1/public/links/{code}/publish-handoff", http.HandlerFunc(s.handlePublicPublishHandoffStart))
		mux.Handle("GET /api/v1/public/links/{code}/publish-handoff/{id}", http.HandlerFunc(s.handlePublicPublishHandoffGet))
		mux.Handle("POST /api/v1/public/links/{code}/publish-handoff/{id}/cancel", http.HandlerFunc(s.handlePublicPublishHandoffCancel))
		mux.Handle("POST /api/v1/public/links/{code}/publish-handoff/{id}/done", http.HandlerFunc(s.handlePublicPublishHandoffDone))
		mux.Handle("POST /api/v1/public/links/{code}/publish-handoff/{id}/editor-opened", http.HandlerFunc(s.handlePublicPublishHandoffEditorOpened))
		mux.Handle("POST /api/v1/public/publish-receipts/{id}", http.HandlerFunc(s.handlePublicPublishReceipt))
		mux.Handle("POST /api/v1/publish-handoffs/{id}/staff-events", s.requireSession(s.handlePublishHandoffStaffEvent))
	}

	return s.withRequestLog(mux)
}

// ---- middleware -------------------------------------------------------------

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) withRequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		c := callerFrom(r)
		who := "guest"
		if c != nil {
			who = redact.MaskEmail(c.Principal.Email) + " principal=" + c.Principal.ID
		}
		s.Log.Printf("%s %s -> %d (%s) %s", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond), who)
	})
}

// requireInternal enforces the BFF->server shared secret.
func (s *Server) requireInternal(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Cfg.InternalToken == "" || r.Header.Get(internalTokenHeader) != s.Cfg.InternalToken {
			fail(w, http.StatusUnauthorized, "unauthorized", "missing or wrong internal token")
			return
		}
		next(w, r)
	})
}

// requireSession chains: internal token -> config gate -> identity resolve ->
// tenant membership resolve. It is the single identity path; there is no
// alternate or stub mode.
func (s *Server) requireSession(next http.HandlerFunc) http.Handler {
	return s.requireIdentity(next, true)
}

// requireServiceProducer gates the queue-view producer seam with DUAL AUTH
// (HUI-3553 W5):
//
//   - request carries X-Service-ID -> trusted service producer path: per-service
//     token from TOUCH_SERVICE_TOKENS (constant-time compare) + queue_producer
//     role; NO session, NO shared internal token, NO X-Tenant-ID (tenant is
//     resolved from the handoff row by the handler). Empty/misconfigured
//     registry fails closed (503). Service path takes priority when both
//     service and session credentials are present.
//   - no X-Service-ID -> delegate to the legacy requireSession path with its
//     exact behaviour (internal token -> config gate -> session -> tenant
//     member -> manage_content_packs).
func (s *Server) requireServiceProducer(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serviceID := strings.TrimSpace(r.Header.Get(serviceIDHeader))
		if serviceID == "" {
			// Dual auth transition: old callers keep the old path byte-for-byte.
			s.requireSession(next).ServeHTTP(w, r)
			return
		}
		if len(s.Cfg.ServiceTokens) == 0 {
			s.auditServiceProducer("unavailable", serviceID, "registry_not_configured")
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error":   "service_producer_unavailable",
				"message": "trusted service producer registry is not configured; refusing to act (fail-closed)",
			})
			return
		}
		want, registered := s.Cfg.ServiceTokens[serviceID]
		got := strings.TrimSpace(r.Header.Get(serviceTokenHeader))
		if !registered || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			// Uniform refusal: never disclose whether the service id exists.
			s.auditServiceProducer("rejected", serviceID, "bad_service_identity")
			fail(w, http.StatusUnauthorized, "unauthorized", "missing or wrong service identity")
			return
		}
		if s.Cfg.ServiceProducerRole(serviceID) != serviceProducerRoleQueue {
			s.auditServiceProducer("rejected", serviceID, "role_not_queue_producer")
			fail(w, http.StatusForbidden, "forbidden", "service identity lacks the queue_producer role")
			return
		}
		s.auditServiceProducer("accepted", serviceID, "")
		ctx := context.WithValue(r.Context(), serviceProducerKey, &serviceProducer{ServiceID: serviceID})
		next(w, r.WithContext(ctx))
	})
}

// auditServiceProducer logs the trusted-service auth decision. It never logs
// the service token or request body; the service id appears only because it is
// the caller's declared identity (not a secret).
func (s *Server) auditServiceProducer(result, serviceID, reason string) {
	if s == nil || s.Log == nil {
		return
	}
	if reason != "" {
		s.Log.Printf("mx_ops_queue_view_producer_auth service=%s result=%s reason=%s", serviceID, result, reason)
		return
	}
	s.Log.Printf("mx_ops_queue_view_producer_auth service=%s result=%s", serviceID, result)
}

// requirePrincipal resolves the platform session without a hand-filled tenant.
// Membership lists are read from the principal, not from X-Tenant-ID.
func (s *Server) requirePrincipal(next http.HandlerFunc) http.Handler {
	return s.requireIdentity(next, false)
}

func (s *Server) requireIdentity(next http.HandlerFunc, withTenant bool) http.Handler {
	return s.requireInternal(func(w http.ResponseWriter, r *http.Request) {
		if problems := s.Cfg.Gate(); len(problems) > 0 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error":   "config_gate",
				"message": "server platform integration is not configured; refusing to act (fail-closed)",
				"detail":  problems,
			})
			return
		}

		sessionToken := sessionTokenFromRequest(r, s.Cfg.SessionCookie)
		if sessionToken == "" {
			fail(w, http.StatusUnauthorized, "unauthenticated", "no session")
			return
		}
		principal, err := s.ID.ResolveSession(r.Context(), sessionToken)
		if err != nil {
			switch {
			case errors.Is(err, identity.ErrUnauthenticated):
				fail(w, http.StatusUnauthorized, "unauthenticated", "session rejected by identity")
			default:
				// transport/config failure: explicit 503, never fake a session
				writeJSON(w, http.StatusServiceUnavailable, map[string]any{
					"error":   "identity_unavailable",
					"message": "platform identity could not be reached; refusing to act (fail-closed)",
				})
			}
			return
		}

		if !withTenant {
			c := &caller{Principal: principal}
			ctx := context.WithValue(r.Context(), callerKey, c)
			next(w, r.WithContext(ctx))
			return
		}

		tenantID := strings.TrimSpace(r.Header.Get(tenantHeader))
		if tenantID == "" {
			fail(w, http.StatusBadRequest, "tenant_required", "header "+tenantHeader+" selects the workspace tenant")
			return
		}

		member, err := s.St.GetMemberByPrincipal(tenantID, principal.ID)
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, store.ErrNotFound) {
			// 身份纪律:principal 无成员行 -> 拒绝,绝不自动入租户/开户
			fail(w, http.StatusForbidden, authz.ReasonNotMember, "principal is not a member of this tenant")
			return
		}
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "member lookup failed")
			return
		}

		mutating := r.Method != http.MethodGet && r.Method != http.MethodHead
		gate, ok := s.attachBrand(w, r, tenantID, mutating)
		if !ok {
			return
		}
		c := &caller{Principal: principal, Member: &member, Brand: gate}
		if mutating && gate != nil && s.blockTenantMutation(w, r, gate.Tenant) {
			return
		}
		ctx := context.WithValue(r.Context(), callerKey, c)
		ctx = context.WithValue(ctx, brandKey, gate)
		next(w, r.WithContext(ctx))
	})
}

func sessionTokenFromRequest(r *http.Request, cookieName string) string {
	if h := r.Header.Get("X-Session-Token"); h != "" {
		return h
	}
	if ck, err := r.Cookie(cookieName); err == nil {
		return ck.Value
	}
	return ""
}

// requireAction wraps a handler with an authz decision for a specific action.
func (s *Server) requireAction(c *caller, action authz.Action, rec authz.RecordScope, w http.ResponseWriter) bool {
	d := authz.Authorize(s.authzMember(c), action, rec)
	if d.Allowed {
		return true
	}
	fail(w, http.StatusForbidden, d.Reason, "action not allowed for this member")
	return false
}
