// Package matrixconsume is the Touch-side consumer of a matrix draft and the
// public permission read. A recorded draft is not an outbound send. Customer
// leads and UGC are different buttons and are not written here.
package matrixconsume

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/matrixhandoff"
)

const (
	SupplyReady   = "ready"
	SupplyMissing = "missing"

	ReasonPermissionSupply = "permission_supply_missing"
	ReasonMatrixSupply     = "matrix_supply_missing"
	ReasonAckNotDraft      = "matrix_ack_not_draft"
	ReasonAuthorityMissing = "matrix_draft_authority_missing"
	ReasonCrossTenant      = "cross_tenant"
	ReasonForbidden        = "forbidden"
	ReasonDraftRecorded    = "draft_recorded"
)

var (
	ErrNotDraft    = errors.New("matrixconsume: ack is not a draft")
	ErrCrossTenant = errors.New("matrixconsume: actor tenant does not own the activity")
)

// Authority carries the payer and license facts the real matrix draft API
// requires on every create. They come from the permission authority, not from
// Touch's own data model, so Touch never invents them. A zero LicenseExpiresAt
// means the permission read did not declare one.
type Authority struct {
	PayerID          string
	LicenseRef       string
	LicenseExpiresAt time.Time
}

// Complete reports whether the facts are complete enough to post. The draft
// API validates payer and license ids and honours an expiry, so a read that
// lacks any of them is not a postable permission.
func (a Authority) Complete() bool {
	return strings.TrimSpace(a.PayerID) != "" && strings.TrimSpace(a.LicenseRef) != "" && !a.LicenseExpiresAt.IsZero()
}

// Permission is one public-permission read. Supply "missing" means the
// permission API is not configured or did not answer; it is not a grant.
// Authority is the optional payer/license block: a permission that is allowed
// without complete authority still refuses the post (fail closed).
type Permission struct {
	Allowed   bool
	Supply    string
	Reason    string
	Authority Authority
}

// DraftError is a precise refusal from the matrix draft API. Reason is the
// button code: "matrix_" plus the matrix error code, passed through verbatim.
type DraftError struct{ Reason string }

// Error returns the reason as the message.
func (e *DraftError) Error() string {
	if e == nil {
		return ""
	}
	return "matrixconsume: " + e.Reason
}

// PermissionGate is the AG01 public permission port. A nil gate is missing supply.
type PermissionGate interface {
	Decide(ctx context.Context, principalID, tenantID, brandID string) (Permission, error)
}

// PermissionFunc adapts a function to PermissionGate.
type PermissionFunc func(ctx context.Context, principalID, tenantID, brandID string) (Permission, error)

// Decide calls the function.
func (f PermissionFunc) Decide(ctx context.Context, principalID, tenantID, brandID string) (Permission, error) {
	if f == nil {
		return Permission{Supply: SupplyMissing, Reason: ReasonPermissionSupply}, nil
	}
	return f(ctx, principalID, tenantID, brandID)
}

// DraftAck is the matrix draft receipt. Anything other than status draft with
// executed false is not a completed send and is not stored as one.
type DraftAck struct {
	PlanID   string
	Status   string
	Executed bool
}

// DraftSink is the real matrix draft API. Available false means that supply
// is missing; only the matrix button is disabled.
type DraftSink interface {
	Available() bool
	PutDraft(ctx context.Context, doc matrixhandoff.Draft, auth Authority) (DraftAck, error)
}

// Actor is the signed-in member. The tenant comes from membership resolution,
// not from a hand-filled request field.
type Actor struct {
	PrincipalID string
	TenantID    string
	BrandID     string
	Role        string
}

// Button is one surface control. Lead and UGC are echoed unchanged.
type Button struct {
	Enabled bool
	Reason  string
}

// Surfaces is the state of the buttons this call must not rewrite.
type Surfaces struct {
	LeadEnabled bool
	UGCEnabled  bool
}

// Outcome is the consumer result. OutboundComplete is never set for a draft.
type Outcome struct {
	MatrixButton     Button
	LeadButton       Button
	UGCButton        Button
	Draft            matrixhandoff.Draft
	Copy             string
	OutboundComplete bool
	// ActivityVersion is the exact Touch campaign version handed off. The
	// browser displays it so a returned candidate can be tied to its source.
	ActivityVersion int
}

// Consumer schedules a merchant matrix draft.
type Consumer struct {
	gate     PermissionGate
	sink     DraftSink
	effects  *matrixhandoff.EffectSink
	handoff  *matrixhandoff.Service
	plans    *planClient
	recordMu sync.Mutex
}

// New returns a consumer. Nil dependencies are missing supply, not success.
// Drafts stay in the matrixhandoff service for the life of this consumer.
func New(gate PermissionGate, sink DraftSink, effects *matrixhandoff.EffectSink) *Consumer {
	if effects == nil {
		effects = &matrixhandoff.EffectSink{}
	}
	plans := &planClient{}
	return &Consumer{
		gate:    gate,
		sink:    sink,
		effects: effects,
		plans:   plans,
		handoff: matrixhandoff.New(plans, effects),
	}
}

// Drafts returns the drafts this process has kept. The slice is a copy.
func (c *Consumer) Drafts() []matrixhandoff.Draft {
	if c == nil || c.handoff == nil {
		return nil
	}
	return c.handoff.List()
}

// Schedule records a matrix draft or disables only the matrix button.
// Customer lead and UGC buttons are returned as given. Customer effect
// counters are not incremented.
func (c *Consumer) Schedule(ctx context.Context, now time.Time, actor Actor, req matrixhandoff.Request, surfaces Surfaces) (Outcome, error) {
	out := Outcome{
		LeadButton: Button{Enabled: surfaces.LeadEnabled},
		UGCButton:  Button{Enabled: surfaces.UGCEnabled},
	}
	if strings.TrimSpace(actor.TenantID) == "" || actor.TenantID != strings.TrimSpace(req.TenantID) || actor.BrandID != strings.TrimSpace(req.BrandID) {
		out.MatrixButton = Button{Enabled: false, Reason: ReasonCrossTenant}
		return out, ErrCrossTenant
	}
	if actor.Role != "org_owner" && actor.Role != matrixhandoff.RoleBrandPublisher {
		out.MatrixButton = Button{Enabled: false, Reason: ReasonForbidden}
		return out, errors.New("matrixconsume: role cannot schedule a brand draft")
	}
	perm, err := c.decide(ctx, actor)
	if err != nil || perm.Supply != SupplyReady {
		out.MatrixButton = Button{Enabled: false, Reason: ReasonPermissionSupply}
		return out, nil
	}
	if !perm.Allowed {
		reason := perm.Reason
		if reason == "" {
			reason = ReasonForbidden
		}
		out.MatrixButton = Button{Enabled: false, Reason: reason}
		return out, nil
	}
	// An allowed permission without the payer/license authority is not a
	// postable permission. The matrix is never dialed on a gap; the reason
	// names the missing authority instead of pretending the matrix refused.
	if !perm.Authority.Complete() {
		out.MatrixButton = Button{Enabled: false, Reason: ReasonAuthorityMissing}
		return out, nil
	}
	// Permission is decided above on every call. Only after it passes is the
	// draft already recorded for this identity reused, so a repeat click,
	// a timeout retry, or a re-login never posts a second matrix plan.
	if existing, ok := c.handoff.Existing(req); ok {
		existing.Executed = false
		existing.CustomerPublish = false
		out.Draft = existing
		out.Copy = existing.Copy
		out.OutboundComplete = false
		out.MatrixButton = Button{Enabled: true, Reason: ReasonDraftRecorded}
		return out, nil
	}
	if c.sink == nil || !c.sink.Available() {
		out.MatrixButton = Button{Enabled: false, Reason: ReasonMatrixSupply}
		return out, nil
	}
	preview := previewDraft(req)
	ack, err := c.sink.PutDraft(ctx, preview, perm.Authority)
	if err != nil {
		var refuse *DraftError
		if errors.As(err, &refuse) && refuse != nil && refuse.Reason != "" {
			out.MatrixButton = Button{Enabled: false, Reason: refuse.Reason}
			return out, nil
		}
		out.MatrixButton = Button{Enabled: false, Reason: ReasonMatrixSupply}
		return out, nil
	}
	if ack.Executed || ack.Status != matrixhandoff.StatusDraft || strings.TrimSpace(ack.PlanID) == "" {
		out.MatrixButton = Button{Enabled: false, Reason: ReasonAckNotDraft}
		return out, ErrNotDraft
	}
	recorded, err := c.remember(now, actor, req, ack.PlanID)
	if err != nil {
		out.MatrixButton = Button{Enabled: false, Reason: matrixhandoff.ReasonOf(err)}
		if out.MatrixButton.Reason == "" {
			out.MatrixButton.Reason = ReasonMatrixSupply
		}
		return out, err
	}
	recorded.Draft.Executed = false
	recorded.Draft.CustomerPublish = false
	out.Draft = recorded.Draft
	out.Copy = recorded.Draft.Copy
	out.OutboundComplete = false
	out.MatrixButton = Button{Enabled: true, Reason: ReasonDraftRecorded}
	return out, nil
}

func (c *Consumer) decide(ctx context.Context, actor Actor) (Permission, error) {
	if c == nil || c.gate == nil {
		return Permission{Supply: SupplyMissing, Reason: ReasonPermissionSupply}, nil
	}
	return c.gate.Decide(ctx, actor.PrincipalID, actor.TenantID, actor.BrandID)
}

// remember keeps the full draft on the process-lifetime handoff service.
// The HTTP sink already posted this document. The handoff client only stamps
// the plan id the sink returned; it does not post a second, thinner body.
// Role is rewritten to brand_publisher only after the caller passed the role
// gate. A disabled member never reaches this function.
func (c *Consumer) remember(now time.Time, actor Actor, req matrixhandoff.Request, planID string) (matrixhandoff.Result, error) {
	if c == nil || c.handoff == nil || c.plans == nil {
		return matrixhandoff.Result{}, errors.New("matrixconsume: draft store missing")
	}
	c.recordMu.Lock()
	defer c.recordMu.Unlock()
	c.plans.set(planID)
	return c.handoff.CreateDraft(now, matrixhandoff.Actor{
		TenantID: actor.TenantID,
		BrandID:  actor.BrandID,
		Role:     matrixhandoff.RoleBrandPublisher,
	}, req)
}

func previewDraft(req matrixhandoff.Request) matrixhandoff.Draft {
	ids := make([]string, 0, len(req.Assets))
	hashes := make([]string, 0, len(req.Assets))
	grant := ""
	for _, asset := range req.Assets {
		ids = append(ids, strings.TrimSpace(asset.ID))
		hashes = append(hashes, strings.TrimSpace(asset.Hash))
		if grant == "" {
			grant = strings.TrimSpace(asset.GrantRef)
		}
	}
	version := req.ActivityVersion
	if version < 1 {
		version = 1
	}
	return matrixhandoff.Draft{
		Activity: matrixhandoff.TouchActivity{
			TenantID:    strings.TrimSpace(req.TenantID),
			BrandID:     strings.TrimSpace(req.BrandID),
			StoreID:     strings.TrimSpace(req.StoreID),
			ActivityID:  strings.TrimSpace(req.ActivityID),
			Version:     version,
			AssetIDs:    ids,
			AssetHashes: hashes,
			GrantRef:    grant,
			OfferText:   strings.TrimSpace(req.OfferText),
			OfferExpiry: req.OfferExpiry.UTC(),
			Disclosure:  strings.TrimSpace(req.Disclosure),
		},
		ReturnLocationToken: strings.TrimSpace(req.ReturnLocationToken),
		ActivityStatus:      matrixhandoff.ActivityOpen,
		Status:              matrixhandoff.StatusDraft,
		Copy:                matrixhandoff.CopyDraft,
	}
}

// planClient stamps a plan id onto a draft the HTTP sink already accepted.
// It does not dial and does not replace the posted document.
type planClient struct {
	mu sync.Mutex
	id string
}

func (p *planClient) set(id string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.id = id
	p.mu.Unlock()
}

func (p *planClient) RequiresVideo() bool { return true }
func (p *planClient) LiveMatrix() bool    { return false }
func (p *planClient) PutDraft(matrixhandoff.Draft) (matrixhandoff.MatrixPlanRef, error) {
	if p == nil {
		return matrixhandoff.MatrixPlanRef{}, errors.New("matrixconsume: plan id missing")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if strings.TrimSpace(p.id) == "" {
		return matrixhandoff.MatrixPlanRef{}, errors.New("matrixconsume: plan id missing")
	}
	return matrixhandoff.MatrixPlanRef{ID: p.id}, nil
}
func (p *planClient) RecordMerchant(matrixhandoff.MatrixPlanRef) error { return nil }
