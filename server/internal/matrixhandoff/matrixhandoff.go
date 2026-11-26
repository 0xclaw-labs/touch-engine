// Package matrixhandoff stores a Touch activity draft for an independent
// matrix publish plan.
//
// A Touch activity and a matrix plan are different objects. The document keeps
// a plan reference, never a copy of the account pool and never a social
// secret. Creating a draft does not execute a publish.
//
// The fixture client is not a live matrix. This package does not transcode,
// charge, or call a cutter. Service, billing, production, and human adoption
// stay unverified. Customer rewards, customer posts, and leads are out of scope.
package matrixhandoff

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ActivityOpen = "open"

	ScopeCampaign = "campaign"
	ScopeStore    = "store"

	StatusDraft         = "draft"
	StatusNeedsVideo    = "needs_video"
	StatusApproved      = "approved"
	StatusMerchantNoted = "merchant_noted"
	StatusSuperseded    = "superseded"

	// Layered merchant plan states (strictly separated from customer publish).
	PlanStateDraftSaved      = "draft_saved"
	PlanStatePendingApproval = "pending_approval"
	PlanStateScheduled       = "scheduled"
	PlanStatePublished       = "published"
	PlanStateUnknown         = "unknown"

	RoleBrandPublisher = "brand_publisher"
	RoleOperatorAdmin  = "operator_admin"
	RoleDelegate       = "delegate"

	PaymentScopeMerchantQuota    = "merchant_quota"
	PaymentScopeDelegatePersonal = "delegate_personal_payment"

	ReasonOperatorNotPublisher     = "operator_admin_is_not_brand_publisher"
	ReasonDelegateRevoked          = "delegate_revoked"
	ReasonDelegatePaymentForbidden = "delegate_personal_payment_forbidden"
	ReasonCustomerPathForbidden    = "customer_path_cannot_create_merchant_plan"
	ReasonCrossTenant              = "cross_tenant"
	ReasonCrossBrand               = "cross_brand"
	ReasonForbidden                = "forbidden"
	ReasonOfferExpired             = "offer_expired"
	ReasonAssetExpired             = "asset_expired"
	ReasonAssetRevoked             = "asset_revoked"
	ReasonWrongContentType         = "wrong_content_type"
	ReasonInvalid                  = "invalid_request"
	ReasonNeedsVideo               = "needs_video"
	ReasonNotFound                 = "not_found"
	ReasonNotApproved              = "not_approved"
	ReasonPlanRefMissing           = "plan_ref_missing"
	ReasonPlanIDMismatch           = "plan_id_mismatch"
	ReasonStaleReceipt             = "stale_receipt_ignored"
	ReasonNoOfficialAuthority      = "no_official_publish_authority"

	CopyDraft           = "已记下商家矩阵草稿。活动仍开放，这里没有执行。"
	CopyNeedsVideo      = "缺少视频素材，不能排期。"
	CopyApproved        = "已确认这一版草稿。先前已批准的计划内容保持原样。"
	CopyMerchant        = "商家计划已分开记下。没有顾客奖励、顾客发布或线索。"
	CopyPendingApproval = "商家计划等待审批确认。尚未外发，无顾客奖励或线索。"
	CopyScheduled       = "商家计划已进入排期队列。尚未实际发出，无顾客奖励或线索。"
	CopyPublished       = "已收到经核验的商家账号状态回执。与顾客发帖和奖励完全隔离。"
	CopyUnknown         = "商家计划外部状态暂未确认，已如实记为未知，不伪造结果。"

	VerificationService    = "NOT_VERIFIED"
	VerificationBilling    = "NOT_VERIFIED"
	VerificationProduction = "NOT_AUTHORIZED"
	VerificationHuman      = "UNKNOWN"
)

// Asset is one selected activity or store content file. Kind "video" is the
// only kind a video-only fixture can hand off. GrantRef is the asset's own
// authorization reference; the real matrix draft API requires it per asset.
// DeclaredContentType is optional caller-declared media type; if it claims
// "video" while Kind is not "video" (e.g. an image masquerading as video),
// the handoff fails closed with ReasonWrongContentType without transcoding.
type Asset struct {
	ID                  string
	Hash                string
	Kind                string
	GrantRef            string
	ExpiresAt           time.Time
	Revoked             bool
	DeclaredContentType string
}

// Request is the selected Touch activity (or campaign-less store content) plus
// fields this package refuses to store. AccountPool, SocialSecret, and
// ActivityStatus are ignored.
type Request struct {
	TenantID              string
	BrandID               string
	StoreID               string
	ScopeKind             string // "" or ScopeCampaign (default), or ScopeStore (no campaign required)
	ActivityID            string
	ActivityVersion       int
	ContentPackID         string
	ContentVersionID      string
	Assets                []Asset
	OfferText             string
	OfferExpiry           time.Time
	Disclosure            string
	ReturnLocationToken   string
	ReturnURL             string
	IdempotencyKey        string
	ActingAsPersonalPayer bool
	AccountPool           []string
	SocialSecret          string
	ActivityStatus        string
}

// Actor is the caller. Operator admin is not brand publish authority, customer
// identities cannot create merchant publish plans, and merchant quota cannot
// be escalated into a delegate's personal payment authority.
type Actor struct {
	PrincipalID            string
	TenantID               string
	BrandID                string
	Role                   string
	Revoked                bool
	PaymentScope           string
	OfficialPublishGranted bool
	IsCustomer             bool
}

// PlanMetrics reports observed post-publish metrics. Unknown metrics MUST be
// nil (JSON null), never fabricated as 0.
type PlanMetrics struct {
	Reach *int64 `json:"reach"`
	Likes *int64 `json:"likes"`
	Leads *int64 `json:"leads"`
}

// TouchActivity is the campaign (or store content) reference being handed off.
// It is not a matrix plan. GrantRef keeps the selected asset's authorization
// reference with the record so the wire document and the source-side record
// stay one fact.
type TouchActivity struct {
	TenantID         string    `json:"tenant_id"`
	BrandID          string    `json:"brand_id"`
	StoreID          string    `json:"store_id"`
	ActivityID       string    `json:"activity_id"`
	ContentPackID    string    `json:"content_pack_id,omitempty"`
	ContentVersionID string    `json:"content_version_id,omitempty"`
	Version          int       `json:"version"`
	AssetIDs         []string  `json:"asset_ids"`
	AssetHashes      []string  `json:"asset_hashes"`
	GrantRef         string    `json:"grant_ref,omitempty"`
	OfferText        string    `json:"offer_text"`
	OfferExpiry      time.Time `json:"offer_expiry"`
	Disclosure       string    `json:"disclosure"`
}

// MatrixPlanRef points at a matrix plan. It does not carry accounts or secrets.
type MatrixPlanRef struct {
	ID string `json:"id"`
}

// Draft is the handoff document. ActivityStatus stays open.
type Draft struct {
	ID                       string        `json:"id"`
	Version                  int           `json:"draft_version"`
	ScopeKind                string        `json:"scope_kind,omitempty"`
	Activity                 TouchActivity `json:"activity"`
	Plan                     MatrixPlanRef `json:"plan_ref"`
	PlanState                string        `json:"plan_state,omitempty"`
	ReturnLocationToken      string        `json:"return_location_token"`
	ReturnURL                string        `json:"return_url,omitempty"`
	IdempotencyKey           string        `json:"idempotency_key,omitempty"`
	ActivityStatus           string        `json:"activity_status"`
	Status                   string        `json:"status"`
	Copy                     string        `json:"copy"`
	Metrics                  PlanMetrics   `json:"metrics"`
	OfficialAuthorityGranted bool          `json:"official_authority_granted"`
	ReceiptSequence          int64         `json:"receipt_sequence,omitempty"`
	Approved                 bool          `json:"approved"`
	Superseded               bool          `json:"superseded"`
	MerchantNoted            bool          `json:"merchant_noted"`
	CustomerPublish          bool          `json:"customer_publish"`
	CustomerEvidenceEligible bool          `json:"customer_evidence_eligible"`
	CustomerRewardEligible   bool          `json:"customer_reward_eligible"`
	Executed                 bool          `json:"executed"`
	Charged                  bool          `json:"charged"`
	Transcoded               bool          `json:"transcoded"`
	AICutCalled              bool          `json:"aicut_called"`
}

// Receipt is an incoming status update from the external matrix service for an
// existing merchant publish plan. Late/stale receipts never overwrite a newer
// adopted version or regress sequence ordering, and merchant receipts can never
// become customer publish evidence or trigger customer rewards.
type Receipt struct {
	PlanID           string
	TargetState      string // draft_saved | pending_approval | scheduled | published | unknown
	ActivityVersion  int
	Sequence         int64
	OfficialVerified bool
	Reach            *int64
	Likes            *int64
	Leads            *int64
}

// ReceiptOutcome reports how ApplyReceipt handled an incoming matrix receipt.
type ReceiptOutcome struct {
	Draft                    Draft
	Applied                  bool
	Ignored                  bool
	Reason                   string
	CustomerEvidenceEligible bool
	CustomerRewardEligible   bool
}

// Result is one handoff answer. Success stays false: a draft, a missing video,
// and a fixture note are not a live publish.
type Result struct {
	Draft   Draft
	Success bool
}

// EffectSink counts customer side effects. Merchant handoff must not call it.
type EffectSink struct {
	RewardWrites int
	LeadWrites   int
	UGCWrites    int
	GrantCalls   int
}

// GrantCustomerReward records one customer reward write.
func (e *EffectSink) GrantCustomerReward(_ string) {
	if e == nil {
		return
	}
	e.GrantCalls++
	e.RewardWrites++
}

// WriteLead records one lead write.
func (e *EffectSink) WriteLead(_ string) {
	if e == nil {
		return
	}
	e.LeadWrites++
}

// WriteCustomerUGC records one customer post.
func (e *EffectSink) WriteCustomerUGC(_ string) {
	if e == nil {
		return
	}
	e.UGCWrites++
}

// Client is the matrix boundary. The fixture is not a live matrix.
type Client interface {
	RequiresVideo() bool
	LiveMatrix() bool
	PutDraft(Draft) (MatrixPlanRef, error)
	RecordMerchant(MatrixPlanRef) error
}

// FixtureClient returns plan references and errors. It does not publish.
type FixtureClient struct {
	VideoOnly bool
	Err       error
	Puts      int
	Records   int
}

// RequiresVideo reports whether this fixture refuses a handoff without video.
func (f *FixtureClient) RequiresVideo() bool { return f != nil && f.VideoOnly }

// LiveMatrix is always false for the fixture.
func (f *FixtureClient) LiveMatrix() bool { return false }

// PutDraft allocates a plan reference or returns the fixture error.
func (f *FixtureClient) PutDraft(doc Draft) (MatrixPlanRef, error) {
	if f == nil {
		return MatrixPlanRef{}, errors.New("matrix client missing")
	}
	f.Puts++
	if f.Err != nil {
		return MatrixPlanRef{}, f.Err
	}
	if doc.ID == "" {
		return MatrixPlanRef{}, errors.New("missing draft")
	}
	return MatrixPlanRef{ID: "planref:" + doc.ID}, nil
}

// RecordMerchant notes a merchant plan reference. It does not publish.
func (f *FixtureClient) RecordMerchant(ref MatrixPlanRef) error {
	if f == nil {
		return errors.New("matrix client missing")
	}
	f.Records++
	if ref.ID == "" {
		return errors.New("missing plan ref")
	}
	if f.Err != nil {
		return f.Err
	}
	return nil
}

type handoffError struct{ Reason string }

func (e *handoffError) Error() string {
	if e == nil {
		return ""
	}
	return e.Reason
}

var (
	errOperator           = &handoffError{Reason: ReasonOperatorNotPublisher}
	errDelegateRevoked    = &handoffError{Reason: ReasonDelegateRevoked}
	errDelegatePayment    = &handoffError{Reason: ReasonDelegatePaymentForbidden}
	errCustomerForbidden  = &handoffError{Reason: ReasonCustomerPathForbidden}
	errCrossTenant        = &handoffError{Reason: ReasonCrossTenant}
	errCrossBrand         = &handoffError{Reason: ReasonCrossBrand}
	errForbidden          = &handoffError{Reason: ReasonForbidden}
	errExpired            = &handoffError{Reason: ReasonOfferExpired}
	errAssetExpired       = &handoffError{Reason: ReasonAssetExpired}
	errAssetRevoked       = &handoffError{Reason: ReasonAssetRevoked}
	errWrongContentType   = &handoffError{Reason: ReasonWrongContentType}
	errInvalid            = &handoffError{Reason: ReasonInvalid}
	errNeedsVideo         = &handoffError{Reason: ReasonNeedsVideo}
	errNotFound           = &handoffError{Reason: ReasonNotFound}
	errNotApproved        = &handoffError{Reason: ReasonNotApproved}
	errPlanRef            = &handoffError{Reason: ReasonPlanRefMissing}
	errPlanIDMismatch     = &handoffError{Reason: ReasonPlanIDMismatch}
	errNoOfficialAuth     = &handoffError{Reason: ReasonNoOfficialAuthority}
)

// ReasonOf returns a machine reason when err came from this package.
func ReasonOf(err error) string {
	var h *handoffError
	if errors.As(err, &h) && h != nil {
		return h.Reason
	}
	return ""
}

// Service stores handoff drafts for one process. It is not a live matrix.
type Service struct {
	mu         sync.Mutex
	client     Client
	effects    *EffectSink
	docs       map[string]Draft
	byKey      map[string]string
	charges    int
	transcodes int
	aicut      int
}

// New returns a service. A nil client uses a fixture that is not live.
func New(client Client, effects *EffectSink) *Service {
	if client == nil {
		client = &FixtureClient{}
	}
	return &Service{
		client:  client,
		effects: effects,
		docs:    map[string]Draft{},
		byKey:   map[string]string{},
	}
}

// LiveMatrix reports the client claim. The fixture returns false.
func (s *Service) LiveMatrix() bool {
	if s == nil || s.client == nil {
		return false
	}
	return s.client.LiveMatrix()
}

// ChargeCount is the number of charges. This package never charges.
func (s *Service) ChargeCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.charges
}

// TranscodeCount is the number of transcodes. This package never transcodes.
func (s *Service) TranscodeCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transcodes
}

// AICutCount is the number of cutter calls. This package never makes one.
func (s *Service) AICutCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.aicut
}

// RewardWrites is the customer reward write count visible to the service.
func (s *Service) RewardWrites() int {
	return s.effectCount(func(e *EffectSink) int { return e.RewardWrites })
}

// LeadWrites is the lead write count visible to the service.
func (s *Service) LeadWrites() int {
	return s.effectCount(func(e *EffectSink) int { return e.LeadWrites })
}

// UGCWrites is the customer post write count visible to the service.
func (s *Service) UGCWrites() int {
	return s.effectCount(func(e *EffectSink) int { return e.UGCWrites })
}

// GrantCustomerRewardCalls is how many times a customer reward was requested.
func (s *Service) GrantCustomerRewardCalls() int {
	return s.effectCount(func(e *EffectSink) int { return e.GrantCalls })
}

func (s *Service) effectCount(read func(*EffectSink) int) int {
	if s == nil || s.effects == nil || read == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return read(s.effects)
}

// Get returns one stored draft.
func (s *Service) Get(id string) (Draft, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.docs[id]
	return doc, ok
}

// GetForTenant returns one stored draft only when it belongs to tenantID.
func (s *Service) GetForTenant(tenantID, id string) (Draft, bool) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return Draft{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.docs[id]
	if !ok || doc.Activity.TenantID != tenantID {
		return Draft{}, false
	}
	return doc, true
}

// ListByScope returns drafts strictly scoped to tenantID and optionally filtered
// by storeID and/or activityID.
func (s *Service) ListByScope(tenantID, storeID, activityID string) []Draft {
	tenantID = strings.TrimSpace(tenantID)
	storeID = strings.TrimSpace(storeID)
	activityID = strings.TrimSpace(activityID)
	if tenantID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Draft, 0)
	for _, doc := range s.docs {
		if doc.Activity.TenantID != tenantID {
			continue
		}
		if storeID != "" && doc.Activity.StoreID != storeID {
			continue
		}
		if activityID != "" && doc.Activity.ActivityID != activityID {
			continue
		}
		out = append(out, doc)
	}
	sortDrafts(out)
	return out
}

// List returns the stored drafts.
func (s *Service) List() []Draft {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Draft, 0, len(s.docs))
	for _, doc := range s.docs {
		out = append(out, doc)
	}
	sortDrafts(out)
	return out
}

func sortDrafts(out []Draft) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].Activity.ActivityID != out[j].Activity.ActivityID {
			return out[i].Activity.ActivityID < out[j].Activity.ActivityID
		}
		if out[i].Activity.StoreID != out[j].Activity.StoreID {
			return out[i].Activity.StoreID < out[j].Activity.StoreID
		}
		if out[i].Version != out[j].Version {
			return out[i].Version < out[j].Version
		}
		return out[i].ID < out[j].ID
	})
}

// Existing returns the draft already recorded for the same identity (tenant,
// brand, scope/activity, version, assets, expiry or explicit idempotency key)
// when it holds a matrix plan id. It does not call the matrix and also recovers
// already-approved/noted drafts so retries or re-logins never create duplicates.
func (s *Service) Existing(req Request) (Draft, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byKey[identityKey(req)]
	if !ok {
		return Draft{}, false
	}
	doc, ok := s.docs[id]
	if !ok || doc.Status == StatusNeedsVideo || strings.TrimSpace(doc.Plan.ID) == "" {
		return Draft{}, false
	}
	return doc, true
}

// CreateDraft stores a draft or returns needs_video. It does not publish.
func (s *Service) CreateDraft(now time.Time, actor Actor, req Request) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := authorize(actor, req.TenantID, req.BrandID, req.ActingAsPersonalPayer); err != nil {
		return Result{}, err
	}
	if err := validate(req); err != nil {
		return Result{}, err
	}
	if !req.OfferExpiry.After(now) {
		return Result{}, errExpired
	}
	normalized := normalizeAssets(req.Assets)
	if err := checkAssetIntegrity(now, normalized); err != nil {
		return Result{}, err
	}
	if s.client.RequiresVideo() && !hasVideo(normalized) {
		return s.save(req, StatusNeedsVideo, "", CopyNeedsVideo, false)
	}
	if err := validateAssets(normalized); err != nil {
		return Result{}, err
	}
	return s.save(req, StatusDraft, PlanStateDraftSaved, CopyDraft, true)
}

// Confirm approves one draft. A newer confirmation ends older approvals for
// the same scope without rewriting their stored activity content.
func (s *Service) Confirm(now time.Time, actor Actor, id string) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.docs[id]
	if !ok {
		return Result{}, errNotFound
	}
	if err := authorize(actor, doc.Activity.TenantID, doc.Activity.BrandID, false); err != nil {
		return Result{Draft: doc}, err
	}
	if doc.Status == StatusNeedsVideo {
		doc.ActivityStatus = ActivityOpen
		s.docs[id] = doc
		return Result{Draft: doc}, errNeedsVideo
	}
	if !doc.Activity.OfferExpiry.After(now) {
		doc.ActivityStatus = ActivityOpen
		s.docs[id] = doc
		return Result{Draft: doc}, errExpired
	}
	if doc.Plan.ID == "" {
		doc.ActivityStatus = ActivityOpen
		s.docs[id] = doc
		return Result{Draft: doc}, errPlanRef
	}
	doc.Approved = true
	doc.Superseded = false
	doc.Executed = false
	doc.CustomerPublish = false
	doc.CustomerEvidenceEligible = false
	doc.CustomerRewardEligible = false
	doc.Charged = false
	doc.Transcoded = false
	doc.AICutCalled = false
	doc.ActivityStatus = ActivityOpen
	if doc.Status != StatusMerchantNoted {
		doc.Status = StatusApproved
		doc.Copy = CopyApproved
	}
	if doc.PlanState == "" || doc.PlanState == PlanStateDraftSaved {
		doc.PlanState = PlanStatePendingApproval
	}
	s.docs[doc.ID] = doc
	s.supersedeOthers(doc)
	return Result{Draft: s.docs[doc.ID]}, nil
}

// RecordMerchantPublish notes a merchant plan. It does not write a customer
// reward, a customer post, or a lead.
func (s *Service) RecordMerchantPublish(now time.Time, actor Actor, id string) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.docs[id]
	if !ok {
		return Result{}, errNotFound
	}
	if err := authorize(actor, doc.Activity.TenantID, doc.Activity.BrandID, false); err != nil {
		return Result{Draft: doc}, err
	}
	if !doc.Activity.OfferExpiry.After(now) {
		doc.ActivityStatus = ActivityOpen
		s.docs[id] = doc
		return Result{Draft: doc}, errExpired
	}
	if doc.Status == StatusNeedsVideo {
		doc.ActivityStatus = ActivityOpen
		s.docs[id] = doc
		return Result{Draft: doc}, errNeedsVideo
	}
	if !doc.Approved || doc.Superseded {
		return Result{Draft: doc}, errNotApproved
	}
	if doc.Plan.ID == "" {
		doc.ActivityStatus = ActivityOpen
		s.docs[id] = doc
		return Result{Draft: doc}, errPlanRef
	}
	if err := s.client.RecordMerchant(doc.Plan); err != nil {
		doc.ActivityStatus = ActivityOpen
		s.docs[id] = doc
		return Result{Draft: doc}, err
	}
	doc.MerchantNoted = true
	doc.CustomerPublish = false
	doc.CustomerEvidenceEligible = false
	doc.CustomerRewardEligible = false
	doc.Executed = false
	doc.Charged = false
	doc.Transcoded = false
	doc.AICutCalled = false
	doc.Approved = true
	doc.ActivityStatus = ActivityOpen
	doc.Status = StatusMerchantNoted
	doc.Copy = CopyMerchant
	if doc.PlanState == "" || doc.PlanState == PlanStateDraftSaved || doc.PlanState == PlanStatePendingApproval {
		doc.PlanState = PlanStateScheduled
	}
	s.docs[id] = doc
	return Result{Draft: doc}, nil
}

// ApplyReceipt reconciles a matrix plan status receipt into the local draft
// reference while strictly enforcing:
//   - tenant/brand/role isolation (fail-closed);
//   - stale/late receipt protection (never overwrites a superseded plan, a newer
//     adopted/approved version, or a higher receipt sequence);
//   - strict layered plan states (draft_saved / pending_approval / scheduled /
//     published / unknown); without official authority, a plan never upgrades to
//     published;
//   - honest unknown metrics (nil -> JSON null, never fabricated as 0);
//   - absolute customer isolation (never eligible as customer publish evidence
//     and never triggers customer UGC rewards or leads).
func (s *Service) ApplyReceipt(now time.Time, actor Actor, id string, receipt Receipt) (ReceiptOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.docs[id]
	if !ok {
		return ReceiptOutcome{}, errNotFound
	}
	if err := authorize(actor, doc.Activity.TenantID, doc.Activity.BrandID, false); err != nil {
		return ReceiptOutcome{Draft: doc}, err
	}
	if doc.Plan.ID == "" {
		return ReceiptOutcome{Draft: doc}, errPlanRef
	}
	if strings.TrimSpace(receipt.PlanID) != "" && strings.TrimSpace(receipt.PlanID) != doc.Plan.ID {
		return ReceiptOutcome{Draft: doc}, errPlanIDMismatch
	}
	targetState := strings.TrimSpace(receipt.TargetState)
	if !isValidPlanState(targetState) {
		return ReceiptOutcome{Draft: doc}, errInvalid
	}
	// Late/stale receipt guard 1: never overwrite a superseded draft or an older
	// version when a newer approved version already exists for this scope.
	if doc.Superseded || s.hasNewerApproved(doc) {
		return ReceiptOutcome{
			Draft:                    doc,
			Applied:                  false,
			Ignored:                  true,
			Reason:                   ReasonStaleReceipt,
			CustomerEvidenceEligible: false,
			CustomerRewardEligible:   false,
		}, nil
	}
	// Late/stale receipt guard 2: receipt targeting an older activity version.
	if receipt.ActivityVersion > 0 && receipt.ActivityVersion < doc.Activity.Version {
		return ReceiptOutcome{
			Draft:                    doc,
			Applied:                  false,
			Ignored:                  true,
			Reason:                   ReasonStaleReceipt,
			CustomerEvidenceEligible: false,
			CustomerRewardEligible:   false,
		}, nil
	}
	// Late/stale receipt guard 3: out-of-order / duplicate older sequence number,
	// or unsequenced late event after a sequenced receipt has already been applied.
	if doc.ReceiptSequence > 0 && (receipt.Sequence == 0 || receipt.Sequence <= doc.ReceiptSequence) {
		return ReceiptOutcome{
			Draft:                    doc,
			Applied:                  false,
			Ignored:                  true,
			Reason:                   ReasonStaleReceipt,
			CustomerEvidenceEligible: false,
			CustomerRewardEligible:   false,
		}, nil
	}
	// Late/stale receipt guard 4: never regress a verified published plan back to
	// an earlier pre-publish state (draft_saved / pending_approval / scheduled).
	if doc.PlanState == PlanStatePublished &&
		(targetState == PlanStateDraftSaved || targetState == PlanStatePendingApproval || targetState == PlanStateScheduled) {
		return ReceiptOutcome{
			Draft:                    doc,
			Applied:                  false,
			Ignored:                  true,
			Reason:                   ReasonStaleReceipt,
			CustomerEvidenceEligible: false,
			CustomerRewardEligible:   false,
		}, nil
	}
	// Strict authority guard: without official authority & verification, never
	// claim published — remain honestly on the restricted draft/plan state.
	if targetState == PlanStatePublished {
		if !doc.Activity.OfferExpiry.After(now) {
			return ReceiptOutcome{Draft: doc}, errExpired
		}
		if !actor.OfficialPublishGranted || !receipt.OfficialVerified {
			doc.OfficialAuthorityGranted = false
			doc.ActivityStatus = ActivityOpen
			doc.CustomerPublish = false
			doc.CustomerEvidenceEligible = false
			doc.CustomerRewardEligible = false
			s.docs[id] = doc
			return ReceiptOutcome{
				Draft:                    doc,
				Applied:                  false,
				Ignored:                  true,
				Reason:                   ReasonNoOfficialAuthority,
				CustomerEvidenceEligible: false,
				CustomerRewardEligible:   false,
			}, errNoOfficialAuth
		}
		doc.OfficialAuthorityGranted = true
		doc.MerchantNoted = true
		doc.Status = StatusMerchantNoted
		doc.Copy = CopyPublished
	} else {
		switch targetState {
		case PlanStateDraftSaved:
			doc.Copy = CopyDraft
		case PlanStatePendingApproval:
			doc.Copy = CopyPendingApproval
		case PlanStateScheduled:
			doc.Copy = CopyScheduled
		case PlanStateUnknown:
			doc.Copy = CopyUnknown
		}
	}
	doc.PlanState = targetState
	if receipt.Sequence > 0 {
		doc.ReceiptSequence = receipt.Sequence
	}
	// Metrics stay nil (JSON null) unless explicitly reported as non-nil values.
	if receipt.Reach != nil {
		v := *receipt.Reach
		doc.Metrics.Reach = &v
	}
	if receipt.Likes != nil {
		v := *receipt.Likes
		doc.Metrics.Likes = &v
	}
	if receipt.Leads != nil {
		v := *receipt.Leads
		doc.Metrics.Leads = &v
	}
	// Hard customer-separation invariants.
	doc.CustomerPublish = false
	doc.CustomerEvidenceEligible = false
	doc.CustomerRewardEligible = false
	doc.Executed = false
	doc.Charged = false
	doc.Transcoded = false
	doc.AICutCalled = false
	doc.ActivityStatus = ActivityOpen
	s.docs[id] = doc
	return ReceiptOutcome{
		Draft:                    doc,
		Applied:                  true,
		Ignored:                  false,
		CustomerEvidenceEligible: false,
		CustomerRewardEligible:   false,
	}, nil
}

func isValidPlanState(state string) bool {
	switch state {
	case PlanStateDraftSaved, PlanStatePendingApproval, PlanStateScheduled, PlanStatePublished, PlanStateUnknown:
		return true
	default:
		return false
	}
}

func (s *Service) hasNewerApproved(target Draft) bool {
	for id, other := range s.docs {
		if id == target.ID || !other.Approved {
			continue
		}
		if sameScope(other, target) && other.Version > target.Version {
			return true
		}
	}
	return false
}

// ApplyClientError returns a matrix failure and leaves the activity open.
func (s *Service) ApplyClientError(id string, clientErr error) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.docs[id]
	if !ok {
		return Result{}, errNotFound
	}
	doc.ActivityStatus = ActivityOpen
	s.docs[id] = doc
	return Result{Draft: doc}, clientErr
}

func (s *Service) save(req Request, status, planState, copy string, callClient bool) (Result, error) {
	key := identityKey(req)
	if id, ok := s.byKey[key]; ok {
		doc := s.docs[id]
		if callClient && doc.Status == StatusDraft && doc.Plan.ID == "" {
			ref, err := s.client.PutDraft(doc)
			if err != nil {
				doc.ActivityStatus = ActivityOpen
				s.docs[id] = doc
				return Result{Draft: doc}, err
			}
			doc.Plan = ref
			if doc.PlanState == "" {
				doc.PlanState = PlanStateDraftSaved
			}
			doc.ActivityStatus = ActivityOpen
			s.docs[id] = doc
		}
		doc = s.docs[id]
		doc.ActivityStatus = ActivityOpen
		s.docs[id] = doc
		return Result{Draft: doc}, nil
	}
	doc := build(req, draftID(key), s.nextVersion(req), status, planState, copy)
	s.docs[doc.ID] = doc
	s.byKey[key] = doc.ID
	if !callClient {
		return Result{Draft: doc}, nil
	}
	ref, err := s.client.PutDraft(doc)
	if err != nil {
		doc.ActivityStatus = ActivityOpen
		s.docs[doc.ID] = doc
		return Result{Draft: doc}, err
	}
	doc.Plan = ref
	doc.PlanState = PlanStateDraftSaved
	doc.ActivityStatus = ActivityOpen
	s.docs[doc.ID] = doc
	return Result{Draft: doc}, nil
}

func sameScope(a, b Draft) bool {
	if a.Activity.TenantID != b.Activity.TenantID || a.Activity.BrandID != b.Activity.BrandID {
		return false
	}
	if a.ScopeKind == ScopeStore || b.ScopeKind == ScopeStore {
		return a.ScopeKind == b.ScopeKind &&
			a.Activity.StoreID == b.Activity.StoreID &&
			a.Activity.ContentPackID == b.Activity.ContentPackID
	}
	return a.Activity.ActivityID == b.Activity.ActivityID
}

func (s *Service) supersedeOthers(target Draft) {
	for id, other := range s.docs {
		if id == target.ID || !other.Approved {
			continue
		}
		if !sameScope(other, target) {
			continue
		}
		other.Approved = false
		other.Superseded = true
		other.Status = StatusSuperseded
		other.ActivityStatus = ActivityOpen
		s.docs[id] = other
	}
}

func (s *Service) nextVersion(req Request) int {
	scope := normalizeScope(req.ScopeKind)
	tenantID := strings.TrimSpace(req.TenantID)
	brandID := strings.TrimSpace(req.BrandID)
	storeID := strings.TrimSpace(req.StoreID)
	activityID := strings.TrimSpace(req.ActivityID)
	packID := strings.TrimSpace(req.ContentPackID)
	max := 0
	for _, doc := range s.docs {
		if doc.Activity.TenantID != tenantID || doc.Activity.BrandID != brandID {
			continue
		}
		if scope == ScopeStore {
			if normalizeScope(doc.ScopeKind) == ScopeStore && doc.Activity.StoreID == storeID && doc.Activity.ContentPackID == packID && doc.Version > max {
				max = doc.Version
			}
			continue
		}
		if normalizeScope(doc.ScopeKind) == ScopeCampaign && doc.Activity.ActivityID == activityID && doc.Version > max {
			max = doc.Version
		}
	}
	return max + 1
}

func normalizeScope(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), ScopeStore) {
		return ScopeStore
	}
	return ScopeCampaign
}

func authorize(actor Actor, tenantID, brandID string, actingAsPersonalPayer bool) error {
	role := strings.TrimSpace(actor.Role)
	if actor.IsCustomer || role == "customer" || role == "visitor" || role == "guest" {
		return errCustomerForbidden
	}
	tenantID = strings.TrimSpace(tenantID)
	brandID = strings.TrimSpace(brandID)
	if strings.TrimSpace(actor.TenantID) == "" || strings.TrimSpace(actor.TenantID) != tenantID {
		return errCrossTenant
	}
	if strings.TrimSpace(actor.BrandID) == "" || strings.TrimSpace(actor.BrandID) != brandID {
		return errCrossBrand
	}
	switch role {
	case RoleBrandPublisher:
		if actingAsPersonalPayer || strings.TrimSpace(actor.PaymentScope) == PaymentScopeDelegatePersonal {
			return errDelegatePayment
		}
		return nil
	case RoleDelegate:
		if actor.Revoked {
			return errDelegateRevoked
		}
		if actingAsPersonalPayer || strings.TrimSpace(actor.PaymentScope) == PaymentScopeDelegatePersonal {
			return errDelegatePayment
		}
		return nil
	case RoleOperatorAdmin:
		return errOperator
	default:
		return errForbidden
	}
}

func validate(req Request) error {
	scope := normalizeScope(req.ScopeKind)
	if strings.TrimSpace(req.TenantID) == "" || strings.TrimSpace(req.BrandID) == "" || strings.TrimSpace(req.StoreID) == "" || req.ActivityVersion < 1 {
		return errInvalid
	}
	if scope == ScopeCampaign && strings.TrimSpace(req.ActivityID) == "" {
		return errInvalid
	}
	if scope == ScopeStore && strings.TrimSpace(req.ContentPackID) == "" && len(req.Assets) == 0 {
		return errInvalid
	}
	if strings.TrimSpace(req.OfferText) == "" || strings.TrimSpace(req.Disclosure) == "" || strings.TrimSpace(req.ReturnLocationToken) == "" || req.OfferExpiry.IsZero() {
		return errInvalid
	}
	return nil
}

func checkAssetIntegrity(now time.Time, assets []Asset) error {
	for _, asset := range assets {
		if asset.Revoked {
			return errAssetRevoked
		}
		if !asset.ExpiresAt.IsZero() && !asset.ExpiresAt.After(now) {
			return errAssetExpired
		}
		if asset.DeclaredContentType != "" {
			declared := strings.ToLower(strings.TrimSpace(asset.DeclaredContentType))
			if declared != asset.Kind {
				return errWrongContentType
			}
		}
	}
	return nil
}

func validateAssets(assets []Asset) error {
	if len(assets) == 0 {
		return errInvalid
	}
	for _, asset := range assets {
		if asset.ID == "" || asset.Hash == "" || asset.Kind == "" {
			return errInvalid
		}
	}
	return nil
}

func hasVideo(assets []Asset) bool {
	for _, asset := range assets {
		if strings.ToLower(strings.TrimSpace(asset.Kind)) == "video" {
			return true
		}
	}
	return false
}

func normalizeAssets(in []Asset) []Asset {
	out := make([]Asset, len(in))
	copy(out, in)
	for i := range out {
		out[i].ID = strings.TrimSpace(out[i].ID)
		out[i].Hash = strings.TrimSpace(out[i].Hash)
		out[i].Kind = strings.ToLower(strings.TrimSpace(out[i].Kind))
		out[i].DeclaredContentType = strings.ToLower(strings.TrimSpace(out[i].DeclaredContentType))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Hash != out[j].Hash {
			return out[i].Hash < out[j].Hash
		}
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

func identityKey(req Request) string {
	scope := normalizeScope(req.ScopeKind)
	if key := strings.TrimSpace(req.IdempotencyKey); key != "" {
		return strings.Join([]string{
			"idem",
			scope,
			strings.TrimSpace(req.TenantID),
			strings.TrimSpace(req.BrandID),
			strings.TrimSpace(req.StoreID),
			strings.TrimSpace(req.ActivityID),
			key,
		}, "\x00")
	}
	assets := normalizeAssets(req.Assets)
	parts := make([]string, len(assets))
	for i, asset := range assets {
		parts[i] = asset.Kind + ":" + asset.Hash
	}
	if scope == ScopeStore {
		return strings.Join([]string{
			"store",
			strings.TrimSpace(req.TenantID),
			strings.TrimSpace(req.BrandID),
			strings.TrimSpace(req.StoreID),
			strings.TrimSpace(req.ContentPackID),
			strings.TrimSpace(req.ContentVersionID),
			strconv.Itoa(req.ActivityVersion),
			strings.Join(parts, ","),
			strconv.FormatInt(req.OfferExpiry.UTC().UnixNano(), 10),
		}, "\x00")
	}
	return strings.Join([]string{
		strings.TrimSpace(req.TenantID),
		strings.TrimSpace(req.BrandID),
		strings.TrimSpace(req.ActivityID),
		strconv.Itoa(req.ActivityVersion),
		strings.Join(parts, ","),
		strconv.FormatInt(req.OfferExpiry.UTC().UnixNano(), 10),
	}, "\x00")
}

func draftID(key string) string {
	sum := sha256.Sum256([]byte("matrixhandoff:" + key))
	return "mh_" + hex.EncodeToString(sum[:12])
}

func build(req Request, id string, version int, status, planState, copy string) Draft {
	assets := normalizeAssets(req.Assets)
	ids := make([]string, 0, len(assets))
	hashes := make([]string, 0, len(assets))
	grant := ""
	for _, asset := range assets {
		ids = append(ids, asset.ID)
		hashes = append(hashes, asset.Hash)
		if grant == "" {
			grant = strings.TrimSpace(asset.GrantRef)
		}
	}
	scope := normalizeScope(req.ScopeKind)
	returnURL := strings.TrimSpace(req.ReturnURL)
	if returnURL == "" {
		if scope == ScopeStore {
			returnURL = "/work/stores/" + strings.TrimSpace(req.StoreID) + "/content"
		} else if strings.TrimSpace(req.ActivityID) != "" {
			returnURL = "/work/campaigns/" + strings.TrimSpace(req.ActivityID)
		}
	}
	return Draft{
		ID:        id,
		Version:   version,
		ScopeKind: scope,
		Activity: TouchActivity{
			TenantID:         strings.TrimSpace(req.TenantID),
			BrandID:          strings.TrimSpace(req.BrandID),
			StoreID:          strings.TrimSpace(req.StoreID),
			ActivityID:       strings.TrimSpace(req.ActivityID),
			ContentPackID:    strings.TrimSpace(req.ContentPackID),
			ContentVersionID: strings.TrimSpace(req.ContentVersionID),
			Version:          req.ActivityVersion,
			AssetIDs:         ids,
			AssetHashes:      hashes,
			GrantRef:         grant,
			OfferText:        strings.TrimSpace(req.OfferText),
			OfferExpiry:      req.OfferExpiry.UTC(),
			Disclosure:       strings.TrimSpace(req.Disclosure),
		},
		PlanState:                planState,
		ReturnLocationToken:      strings.TrimSpace(req.ReturnLocationToken),
		ReturnURL:                returnURL,
		IdempotencyKey:           strings.TrimSpace(req.IdempotencyKey),
		ActivityStatus:           ActivityOpen,
		Status:                   status,
		Copy:                     copy,
		Metrics:                  PlanMetrics{},
		OfficialAuthorityGranted: false,
		CustomerPublish:          false,
		CustomerEvidenceEligible: false,
		CustomerRewardEligible:   false,
	}
}
