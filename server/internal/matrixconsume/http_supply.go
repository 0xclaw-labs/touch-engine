package matrixconsume

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/matrixhandoff"
)

// httpPermission reads AG01's public permission API.
// An empty base URL is missing supply and does not open a connection.
type httpPermission struct {
	base   string
	client *http.Client
}

// NewHTTPPermission returns a permission gate. An empty base is missing supply.
func NewHTTPPermission(base string, client *http.Client) PermissionGate {
	return &httpPermission{base: strings.TrimRight(strings.TrimSpace(base), "/"), client: client}
}

func (h *httpPermission) Decide(ctx context.Context, principalID, tenantID, brandID string) (Permission, error) {
	if h == nil || h.base == "" {
		return Permission{Supply: SupplyMissing, Reason: ReasonPermissionSupply}, nil
	}
	u, err := url.Parse(h.base + "/permissions")
	if err != nil {
		return Permission{Supply: SupplyMissing, Reason: ReasonPermissionSupply}, nil
	}
	q := u.Query()
	q.Set("action", "brand_matrix_draft")
	q.Set("tenant_id", tenantID)
	q.Set("brand_id", brandID)
	q.Set("principal_id", principalID)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Permission{Supply: SupplyMissing, Reason: ReasonPermissionSupply}, nil
	}
	res, err := h.http().Do(req)
	if err != nil {
		log.Printf("matrixconsume: dial permission %s error=%v", u.String(), err)
		return Permission{Supply: SupplyMissing, Reason: ReasonPermissionSupply}, nil
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		log.Printf("matrixconsume: dial permission %s status=%d", u.String(), res.StatusCode)
		return Permission{Supply: SupplyMissing, Reason: ReasonPermissionSupply}, nil
	}
	var parsed struct {
		Allowed bool   `json:"allowed"`
		Supply  string `json:"supply"`
		Reason  string `json:"reason"`
		// Authority facts are optional and additive: an older permission face
		// that omits them leaves Authority zero, which the consumer treats as
		// a post-blocking gap, never as invented values.
		PayerID          string `json:"payer_id"`
		LicenseRef       string `json:"license_ref"`
		LicenseExpiresAt string `json:"license_expires_at"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Supply == "" {
		log.Printf("matrixconsume: dial permission %s status=%d supply=missing", u.String(), res.StatusCode)
		return Permission{Supply: SupplyMissing, Reason: ReasonPermissionSupply}, nil
	}
	auth := Authority{PayerID: parsed.PayerID, LicenseRef: parsed.LicenseRef}
	if t, err := time.Parse(time.RFC3339, parsed.LicenseExpiresAt); err == nil {
		auth.LicenseExpiresAt = t
	}
	log.Printf("matrixconsume: dial permission %s status=%d supply=%s authority=%t", u.String(), res.StatusCode, parsed.Supply, auth.Complete())
	return Permission{Allowed: parsed.Allowed, Supply: parsed.Supply, Reason: parsed.Reason, Authority: auth}, nil
}

func (h *httpPermission) http() *http.Client {
	if h.client != nil {
		return h.client
	}
	return http.DefaultClient
}

// httpDraft posts a matrix draft to the real draft API. An empty base is
// missing supply. The wire contract is the one at
// bianjiefilm/public-ai@6d1aadea internal/matrixdraft: POST /v1/matrix-drafts
// with X-Matrix-Tenant and the matrix-draft-request/v1 field set.
type httpDraft struct {
	base   string
	client *http.Client
}

// NewHTTPDraftSink returns a draft sink. An empty base does not open a connection.
func NewHTTPDraftSink(base string, client *http.Client) DraftSink {
	return &httpDraft{base: strings.TrimRight(strings.TrimSpace(base), "/"), client: client}
}

func (h *httpDraft) Available() bool {
	return h != nil && h.base != ""
}

// matrixDraftRequest is the exact wire document. The real server decodes with
// DisallowUnknownFields, so no extra field may ride along.
type matrixDraftRequest struct {
	SchemaVersion    string           `json:"schema_version"`
	SourceApp        string           `json:"source_app"`
	SourceProjectID  string           `json:"source_project_id"`
	CampaignID       string           `json:"campaign_id"`
	Revision         int              `json:"revision"`
	AssetID          string           `json:"asset_id"`
	AssetHash        string           `json:"asset_hash"`
	ContentType      string           `json:"content_type"`
	GrantRef         string           `json:"grant_ref"`
	TenantID         string           `json:"tenant_id"`
	BrandID          string           `json:"brand_id"`
	PayerID          string           `json:"payer_id"`
	LicenseRef       string           `json:"license_ref"`
	LicenseExpiresAt *string          `json:"license_expires_at,omitempty"`
	ReturnTarget     matrixReturnPath `json:"return_target"`
}

type matrixReturnPath struct {
	App  string `json:"app"`
	Path string `json:"path"`
}

func (h *httpDraft) PutDraft(ctx context.Context, doc matrixhandoff.Draft, auth Authority) (DraftAck, error) {
	if !h.Available() {
		return DraftAck{}, errors.New("matrixconsume: matrix supply missing")
	}
	payload, err := json.Marshal(wireRequest(doc, auth))
	if err != nil {
		return DraftAck{}, err
	}
	target := h.base + "/v1/matrix-drafts"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return DraftAck{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Matrix-Tenant", doc.Activity.TenantID)
	client := h.client
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		log.Printf("matrixconsume: dial draft %s error=%v", target, err)
		return DraftAck{}, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusCreated {
		log.Printf("matrixconsume: dial draft %s status=%d body=%s", target, res.StatusCode, truncate(raw))
		return DraftAck{}, draftRefusal(raw)
	}
	var ack struct {
		DraftID          string `json:"draft_id"`
		Status           string `json:"status"`
		Published        bool   `json:"published"`
		Charged          bool   `json:"charged"`
		IdempotentReplay bool   `json:"idempotent_replay"`
	}
	if err := json.Unmarshal(raw, &ack); err != nil {
		return DraftAck{}, err
	}
	log.Printf("matrixconsume: dial draft %s status=%d draft_id=%s draft_status=%s published=%t charged=%t replay=%t",
		target, res.StatusCode, ack.DraftID, ack.Status, ack.Published, ack.Charged, ack.IdempotentReplay)
	return DraftAck{PlanID: ack.DraftID, Status: ack.Status, Executed: ack.Published || ack.Charged}, nil
}

// draftRefusal maps a matrix error document to a precise button reason. An
// unreadable body stays a generic supply failure; it never becomes success.
func draftRefusal(raw []byte) error {
	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || strings.TrimSpace(parsed.Error.Code) == "" {
		return errors.New("matrixconsume: matrix supply missing")
	}
	return &DraftError{Reason: "matrix_" + strings.TrimSpace(parsed.Error.Code)}
}

func truncate(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// wireRequest builds the matrix-draft-request/v1 document. source_project_id
// and campaign_id are both the Touch activity id. revision is the integer
// campaign-asset version. The return path points back at the campaign page in
// this app; the local record keeps its own ReturnLocationToken untouched.
func wireRequest(doc matrixhandoff.Draft, auth Authority) matrixDraftRequest {
	version := doc.Activity.Version
	if version < 1 {
		version = 1
	}
	sha := ""
	assetID := ""
	grant := ""
	if len(doc.Activity.AssetHashes) > 0 {
		sha = doc.Activity.AssetHashes[0]
	}
	if len(doc.Activity.AssetIDs) > 0 {
		assetID = doc.Activity.AssetIDs[0]
	}
	if strings.TrimSpace(doc.Activity.GrantRef) != "" {
		grant = strings.TrimSpace(doc.Activity.GrantRef)
	}
	req := matrixDraftRequest{
		SchemaVersion:   "matrix-draft-request/v1",
		SourceApp:       "touch",
		SourceProjectID: doc.Activity.ActivityID,
		CampaignID:      doc.Activity.ActivityID,
		Revision:        version,
		AssetID:         assetID,
		AssetHash:       sha,
		ContentType:     "activity",
		GrantRef:        grant,
		TenantID:        doc.Activity.TenantID,
		BrandID:         doc.Activity.BrandID,
		PayerID:         strings.TrimSpace(auth.PayerID),
		LicenseRef:      strings.TrimSpace(auth.LicenseRef),
		ReturnTarget:    matrixReturnPath{App: "touch", Path: "/work/campaigns/" + doc.Activity.ActivityID},
	}
	if !auth.LicenseExpiresAt.IsZero() {
		u := auth.LicenseExpiresAt.UTC().Format(time.RFC3339)
		req.LicenseExpiresAt = &u
	}
	return req
}
