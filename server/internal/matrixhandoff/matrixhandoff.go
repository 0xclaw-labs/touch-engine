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

	StatusDraft         = "draft"
	StatusNeedsVideo    = "needs_video"
	StatusApproved      = "approved"
	StatusMerchantNoted = "merchant_noted"
	StatusSuperseded    = "superseded"

	RoleBrandPublisher = "brand_publisher"
	RoleOperatorAdmin  = "operator_admin"
	RoleDelegate       = "delegate"

	ReasonOperatorNotPublisher = "operator_admin_is_not_brand_publisher"
	ReasonDelegateRevoked      = "delegate_revoked"
	ReasonCrossTenant          = "cross_tenant"
	ReasonCrossBrand           = "cross_brand"
	ReasonForbidden            = "forbidden"
	ReasonOfferExpired         = "offer_expired"
	ReasonInvalid              = "invalid_request"
	ReasonNeedsVideo           = "needs_video"
	ReasonNotFound             = "not_found"
	ReasonNotApproved          = "not_approved"
	ReasonPlanRefMissing       = "plan_ref_missing"

	CopyDraft      = "已记下商家矩阵草稿。活动仍开放，这里没有执行。"
	CopyNeedsVideo = "缺少视频素材，不能排期。"
	CopyApproved   = "已确认这一版草稿。先前已批准的计划内容保持原样。"
	CopyMerchant   = "商家计划已分开记下。没有顾客奖励、顾客发布或线索。"

	VerificationService    = "NOT_VERIFIED"
	VerificationBilling    = "NOT_VERIFIED"
	VerificationProduction = "NOT_AUTHORIZED"
	VerificationHuman      = "UNKNOWN"
)

// Asset is one selected activity file. Kind "video" is the only kind a
// video-only fixture can hand off. GrantRef is the asset's own authorization
// reference; the real matrix draft API requires it per asset.
type Asset struct {
	ID       string
	Hash     string
	Kind     string
	GrantRef string
}

// Request is the selected Touch activity plus fields this package refuses to
// store. AccountPool, SocialSecret, and ActivityStatus are ignored.
type Request struct {
	TenantID            string
	BrandID             string
	StoreID             string
	ActivityID          string
	ActivityVersion     int
	Assets              []Asset
	OfferText           string
	OfferExpiry         time.Time
	Disclosure          string
	ReturnLocationToken string
	AccountPool         []string
	SocialSecret        string
	ActivityStatus      string
}

// Actor is the caller. Operator admin is not brand publish authority.
type Actor struct {
	TenantID string
	BrandID  string
	Role     string
	Revoked  bool
}

// TouchActivity is the campaign being handed off. It is not a matrix plan.
// GrantRef keeps the selected asset's authorization reference with the record
// so the wire document and the source-side record stay one fact.
type TouchActivity struct {
	TenantID    string    `json:"tenant_id"`
	BrandID     string    `json:"brand_id"`
	StoreID     string    `json:"store_id"`
	ActivityID  string    `json:"activity_id"`
	Version     int       `json:"version"`
	AssetIDs    []string  `json:"asset_ids"`
	AssetHashes []string  `json:"asset_hashes"`
	GrantRef    string    `json:"grant_ref,omitempty"`
	OfferText   string    `json:"offer_text"`
	OfferExpiry time.Time `json:"offer_expiry"`
	Disclosure  string    `json:"disclosure"`
}

// MatrixPlanRef points at a matrix plan. It does not carry accounts or secrets.
type MatrixPlanRef struct {
	ID string `json:"id"`
}

// Draft is the handoff document. ActivityStatus stays open.
type Draft struct {
	ID                  string        `json:"id"`
	Version             int           `json:"draft_version"`
	Activity            TouchActivity `json:"activity"`
	Plan                MatrixPlanRef `json:"plan_ref"`
	ReturnLocationToken string        `json:"return_location_token"`
	ActivityStatus      string        `json:"activity_status"`
	Status              string        `json:"status"`
	Copy                string        `json:"copy"`
	Approved            bool          `json:"approved"`
	Superseded          bool          `json:"superseded"`
	MerchantNoted       bool          `json:"merchant_noted"`
	CustomerPublish     bool          `json:"customer_publish"`
	Executed            bool          `json:"executed"`
	Charged             bool          `json:"charged"`
	Transcoded          bool          `json:"transcoded"`
	AICutCalled         bool          `json:"aicut_called"`
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
	errOperator        = &handoffError{Reason: ReasonOperatorNotPublisher}
	errDelegateRevoked = &handoffError{Reason: ReasonDelegateRevoked}
	errCrossTenant     = &handoffError{Reason: ReasonCrossTenant}
	errCrossBrand      = &handoffError{Reason: ReasonCrossBrand}
	errForbidden       = &handoffError{Reason: ReasonForbidden}
	errExpired         = &handoffError{Reason: ReasonOfferExpired}
	errInvalid         = &handoffError{Reason: ReasonInvalid}
	errNeedsVideo      = &handoffError{Reason: ReasonNeedsVideo}
	errNotFound        = &handoffError{Reason: ReasonNotFound}
	errNotApproved     = &handoffError{Reason: ReasonNotApproved}
	errPlanRef         = &handoffError{Reason: ReasonPlanRefMissing}
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

// List returns the stored drafts.
func (s *Service) List() []Draft {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Draft, 0, len(s.docs))
	for _, doc := range s.docs {
		out = append(out, doc)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Activity.ActivityID != out[j].Activity.ActivityID {
			return out[i].Activity.ActivityID < out[j].Activity.ActivityID
		}
		if out[i].Version != out[j].Version {
			return out[i].Version < out[j].Version
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Existing returns the draft already recorded for the same identity (tenant,
// brand, activity, version, assets, expiry) when it holds a matrix plan id and
// is still a draft. It does not call the matrix.
func (s *Service) Existing(req Request) (Draft, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byKey[identityKey(req)]
	if !ok {
		return Draft{}, false
	}
	doc, ok := s.docs[id]
	if !ok || doc.Status != StatusDraft || strings.TrimSpace(doc.Plan.ID) == "" {
		return Draft{}, false
	}
	return doc, true
}

// CreateDraft stores a draft or returns needs_video. It does not publish.
func (s *Service) CreateDraft(now time.Time, actor Actor, req Request) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := authorize(actor, req.TenantID, req.BrandID); err != nil {
		return Result{}, err
	}
	if err := validate(req); err != nil {
		return Result{}, err
	}
	if !req.OfferExpiry.After(now) {
		return Result{}, errExpired
	}
	if s.client.RequiresVideo() && !hasVideo(req.Assets) {
		return s.save(req, StatusNeedsVideo, CopyNeedsVideo, false)
	}
	if err := validateAssets(normalizeAssets(req.Assets)); err != nil {
		return Result{}, err
	}
	return s.save(req, StatusDraft, CopyDraft, true)
}

// Confirm approves one draft. A newer confirmation ends older approvals for
// the same activity without rewriting their stored activity content.
func (s *Service) Confirm(now time.Time, actor Actor, id string) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.docs[id]
	if !ok {
		return Result{}, errNotFound
	}
	if err := authorize(actor, doc.Activity.TenantID, doc.Activity.BrandID); err != nil {
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
	doc.Charged = false
	doc.Transcoded = false
	doc.AICutCalled = false
	doc.ActivityStatus = ActivityOpen
	if doc.Status != StatusMerchantNoted {
		doc.Status = StatusApproved
		doc.Copy = CopyApproved
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
	if err := authorize(actor, doc.Activity.TenantID, doc.Activity.BrandID); err != nil {
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
	doc.Executed = false
	doc.Charged = false
	doc.Transcoded = false
	doc.AICutCalled = false
	doc.Approved = true
	doc.ActivityStatus = ActivityOpen
	doc.Status = StatusMerchantNoted
	doc.Copy = CopyMerchant
	s.docs[id] = doc
	return Result{Draft: doc}, nil
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

func (s *Service) save(req Request, status, copy string, callClient bool) (Result, error) {
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
			doc.ActivityStatus = ActivityOpen
			s.docs[id] = doc
		}
		doc = s.docs[id]
		doc.ActivityStatus = ActivityOpen
		s.docs[id] = doc
		return Result{Draft: doc}, nil
	}
	doc := build(req, draftID(key), s.nextVersion(req.TenantID, req.BrandID, req.ActivityID), status, copy)
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
	doc.ActivityStatus = ActivityOpen
	s.docs[doc.ID] = doc
	return Result{Draft: doc}, nil
}

func (s *Service) supersedeOthers(target Draft) {
	for id, other := range s.docs {
		if id == target.ID || !other.Approved {
			continue
		}
		if other.Activity.TenantID != target.Activity.TenantID || other.Activity.BrandID != target.Activity.BrandID || other.Activity.ActivityID != target.Activity.ActivityID {
			continue
		}
		other.Approved = false
		other.Superseded = true
		other.Status = StatusSuperseded
		other.ActivityStatus = ActivityOpen
		s.docs[id] = other
	}
}

func (s *Service) nextVersion(tenantID, brandID, activityID string) int {
	max := 0
	for _, doc := range s.docs {
		if doc.Activity.TenantID == tenantID && doc.Activity.BrandID == brandID && doc.Activity.ActivityID == activityID && doc.Version > max {
			max = doc.Version
		}
	}
	return max + 1
}

func authorize(actor Actor, tenantID, brandID string) error {
	tenantID = strings.TrimSpace(tenantID)
	brandID = strings.TrimSpace(brandID)
	if strings.TrimSpace(actor.TenantID) == "" || strings.TrimSpace(actor.TenantID) != tenantID {
		return errCrossTenant
	}
	if strings.TrimSpace(actor.BrandID) == "" || strings.TrimSpace(actor.BrandID) != brandID {
		return errCrossBrand
	}
	switch actor.Role {
	case RoleBrandPublisher:
		return nil
	case RoleDelegate:
		if actor.Revoked {
			return errDelegateRevoked
		}
		return nil
	case RoleOperatorAdmin:
		return errOperator
	default:
		return errForbidden
	}
}

func validate(req Request) error {
	if strings.TrimSpace(req.TenantID) == "" || strings.TrimSpace(req.BrandID) == "" || strings.TrimSpace(req.StoreID) == "" || strings.TrimSpace(req.ActivityID) == "" || req.ActivityVersion < 1 {
		return errInvalid
	}
	if strings.TrimSpace(req.OfferText) == "" || strings.TrimSpace(req.Disclosure) == "" || strings.TrimSpace(req.ReturnLocationToken) == "" || req.OfferExpiry.IsZero() {
		return errInvalid
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
	for _, asset := range normalizeAssets(assets) {
		if asset.Kind == "video" {
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
	assets := normalizeAssets(req.Assets)
	parts := make([]string, len(assets))
	for i, asset := range assets {
		parts[i] = asset.Kind + ":" + asset.Hash
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

func build(req Request, id string, version int, status, copy string) Draft {
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
	return Draft{
		ID:      id,
		Version: version,
		Activity: TouchActivity{
			TenantID:    strings.TrimSpace(req.TenantID),
			BrandID:     strings.TrimSpace(req.BrandID),
			StoreID:     strings.TrimSpace(req.StoreID),
			ActivityID:  strings.TrimSpace(req.ActivityID),
			Version:     req.ActivityVersion,
			AssetIDs:    ids,
			AssetHashes: hashes,
			GrantRef:    grant,
			OfferText:   strings.TrimSpace(req.OfferText),
			OfferExpiry: req.OfferExpiry.UTC(),
			Disclosure:  strings.TrimSpace(req.Disclosure),
		},
		ReturnLocationToken: strings.TrimSpace(req.ReturnLocationToken),
		ActivityStatus:      ActivityOpen,
		Status:              status,
		Copy:                copy,
	}
}
