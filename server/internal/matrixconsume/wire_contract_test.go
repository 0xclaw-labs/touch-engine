package matrixconsume

// The tests in this file pin the Touch client to the real matrix draft API
// contract. The reference is bianjiefilm/public-ai@6d1aadea
// internal/matrixdraft (http.go create handler, store.go validate/Create,
// consume_wire_test.go). wireDraftServer below is an engineering-local
// fixture that reproduces that contract: same path, same tenant header, the
// exact request field set decoded with DisallowUnknownFields, the same error
// codes and statuses, and the same field-identity idempotent replay. It is
// not the real service; the real dual-process evidence lives under
// docs/audits/hui-2390/w2/.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/matrixhandoff"
)

const wireRef = "bianjiefilm/public-ai@6d1aadea internal/matrixdraft"

// createBody mirrors the real request schema at the pinned SHA. Unknown
// fields are refused by the real server; this fixture decodes the same way.
type createBody struct {
	SchemaVersion    string  `json:"schema_version"`
	SourceApp        string  `json:"source_app"`
	SourceProjectID  string  `json:"source_project_id"`
	CampaignID       string  `json:"campaign_id"`
	Revision         int     `json:"revision"`
	AssetID          string  `json:"asset_id"`
	AssetHash        string  `json:"asset_hash"`
	ContentType      string  `json:"content_type"`
	GrantRef         string  `json:"grant_ref"`
	TenantID         string  `json:"tenant_id"`
	BrandID          string  `json:"brand_id"`
	PayerID          string  `json:"payer_id"`
	LicenseRef       string  `json:"license_ref"`
	LicenseExpiresAt *string `json:"license_expires_at"`
	ReturnTarget     struct {
		App  string `json:"app"`
		Path string `json:"path"`
	} `json:"return_target"`
}

type wireRow struct {
	draftID     string
	fingerprint string
}

// wireDraftServer reproduces the real create endpoint for one tenant.
type wireDraftServer struct {
	mu          sync.Mutex
	grant       string // active, revoked, expired, unknown
	licensePast bool
	storeFailed bool
	dropFirst   bool
	rows        map[string]wireRow
	puts        int
	lastBody    createBody
	lastTenant  string
	lastRaw     []byte
}

func newWireDraftServer(grant string) *wireDraftServer {
	return &wireDraftServer{grant: grant, rows: map[string]wireRow{}}
}

func (s *wireDraftServer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/matrix-drafts" || r.Method != http.MethodPost {
			writeWireErr(w, http.StatusNotFound, "not_found")
			return
		}
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		var body createBody
		if err := dec.Decode(&body); err != nil {
			writeWireErr(w, http.StatusBadRequest, "bad_request")
			return
		}
		var extra any
		if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
			writeWireErr(w, http.StatusBadRequest, "bad_request")
			return
		}
		if body.SchemaVersion != "matrix-draft-request/v1" {
			writeWireErr(w, http.StatusBadRequest, "bad_request")
			return
		}
		tenant := r.Header.Get("X-Matrix-Tenant")
		s.mu.Lock()
		defer s.mu.Unlock()
		s.puts++
		s.lastBody = body
		s.lastTenant = tenant
		s.lastRaw = raw
		if tenant == "" || tenant != body.TenantID {
			writeWireErr(w, http.StatusForbidden, "wrong_tenant")
			return
		}
		switch s.grant {
		case "revoked":
			writeWireErr(w, http.StatusForbidden, "grant_revoked")
			return
		case "expired":
			writeWireErr(w, http.StatusForbidden, "grant_expired")
			return
		case "unknown":
			writeWireErr(w, http.StatusForbidden, "grant_unknown")
			return
		}
		if s.licensePast {
			writeWireErr(w, http.StatusForbidden, "license_expired")
			return
		}
		// Field-identity key and fingerprint, same identity as the real store.
		key := strings.Join([]string{body.SourceApp, body.SourceProjectID, body.CampaignID,
			jsonInt(body.Revision), body.AssetHash, body.TenantID}, "\x00")
		fp := strings.Join([]string{body.AssetID, body.ContentType, body.GrantRef, body.BrandID,
			body.PayerID, body.LicenseRef, deref(body.LicenseExpiresAt), body.ReturnTarget.App, body.ReturnTarget.Path}, "\x00")
		if row, ok := s.rows[key]; ok {
			if row.fingerprint != fp {
				writeWireErr(w, http.StatusConflict, "idempotency_conflict")
				return
			}
			writeDraft(w, http.StatusOK, row.draftID, true)
			return
		}
		if s.storeFailed {
			writeWireErr(w, http.StatusInternalServerError, "store_failed")
			return
		}
		id := "md_" + strings.Repeat("0", 8) + jsonInt(s.puts)
		s.rows[key] = wireRow{draftID: id, fingerprint: fp}
		if s.dropFirst {
			// The row exists but the response is lost: the client sees a
			// broken, non-JSON answer, exactly like a cut connection.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("<html>gateway timeout</html>"))
			return
		}
		writeDraft(w, http.StatusCreated, id, false)
	})
}

func writeDraft(w http.ResponseWriter, status int, id string, replay bool) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"schema_version": "matrix-draft/v1", "draft_id": id, "source_app": "touch",
		"status": "draft", "published": false, "charged": false, "idempotent_replay": replay,
	})
}

func writeWireErr(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": code}})
}

func jsonInt(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// wireConsumer serves perm as the /permissions answer and routes the draft
// create to srv, so each test controls both faces of the same process.
func wireConsumer(t *testing.T, srv *wireDraftServer, perm string) *Consumer {
	t.Helper()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/permissions" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(perm))
			return
		}
		srv.handler(t).ServeHTTP(w, r)
	}))
	t.Cleanup(api.Close)
	return New(NewHTTPPermission(api.URL, api.Client()), NewHTTPDraftSink(api.URL, api.Client()), nil)
}

const permJSON = `{"allowed":true,"supply":"ready","reason":"ok","payer_id":"pay_oper","license_ref":"lic_v1","license_expires_at":"2027-01-02T03:04:05Z"}`

func wireScheduleRequest() matrixhandoff.Request {
	return matrixhandoff.Request{
		TenantID: "ten", BrandID: "brd", StoreID: "sto", ActivityID: "cmp_9", ActivityVersion: 3,
		Assets:    []matrixhandoff.Asset{{ID: "ast_1", Hash: strings.Repeat("ab", 32), Kind: "video", GrantRef: "grant_v1"}},
		OfferText: "到店有礼", OfferExpiry: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		Disclosure: "这是商家活动草稿，不是已经发出的内容。", ReturnLocationToken: "return:cmp_9",
	}
}

func wireSchedule(t *testing.T, c *Consumer) (Outcome, error) {
	t.Helper()
	return c.Schedule(context.Background(), time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC),
		Actor{PrincipalID: "usr", TenantID: "ten", BrandID: "brd", Role: "org_owner"},
		wireScheduleRequest(), Surfaces{LeadEnabled: true, UGCEnabled: true})
}

// The happy path posts the exact real wire document and records one draft
// from a 201 answer whose published and charged are false.
func TestWireHappyPathPostsRealContract(t *testing.T) {
	srv := newWireDraftServer("active")
	c := wireConsumer(t, srv, permJSON)
	out, err := wireSchedule(t, c)
	if err != nil {
		t.Fatal(err)
	}
	if srv.puts != 1 {
		t.Fatalf("puts = %d", srv.puts)
	}
	body := srv.lastBody
	if body.SchemaVersion != "matrix-draft-request/v1" || body.SourceApp != "touch" || body.ContentType != "activity" {
		t.Fatalf("schema = %+v (%s)", body, wireRef)
	}
	if body.SourceProjectID != "cmp_9" || body.CampaignID != "cmp_9" || body.Revision != 3 {
		t.Fatalf("project = %+v", body)
	}
	if body.AssetID != "ast_1" || body.AssetHash != strings.Repeat("ab", 32) || body.GrantRef != "grant_v1" {
		t.Fatalf("asset = %+v", body)
	}
	if body.TenantID != "ten" || body.BrandID != "brd" || srv.lastTenant != "ten" {
		t.Fatalf("tenant = body %q header %q", body.TenantID, srv.lastTenant)
	}
	if body.PayerID != "pay_oper" || body.LicenseRef != "lic_v1" {
		t.Fatalf("authority = %+v", body)
	}
	if body.LicenseExpiresAt == nil || *body.LicenseExpiresAt != "2027-01-02T03:04:05Z" {
		t.Fatalf("license expiry = %v", body.LicenseExpiresAt)
	}
	if body.ReturnTarget.App != "touch" || body.ReturnTarget.Path != "/work/campaigns/cmp_9" {
		t.Fatalf("return = %+v", body.ReturnTarget)
	}
	if !out.MatrixButton.Enabled || out.OutboundComplete || out.Draft.Executed || out.Draft.CustomerPublish {
		t.Fatalf("outcome = %+v", out)
	}
	if out.Draft.Status != "draft" || out.Draft.Plan.ID == "" || !strings.HasPrefix(out.Draft.Plan.ID, "md_") {
		t.Fatalf("draft = %+v", out.Draft)
	}
	kept := c.Drafts()
	if len(kept) != 1 || kept[0].Plan.ID != out.Draft.Plan.ID {
		t.Fatalf("kept = %+v", kept)
	}
	if kept[0].Activity.GrantRef != "grant_v1" {
		t.Fatalf("record grant ref = %q", kept[0].Activity.GrantRef)
	}
}

// A response that is lost after the matrix stored the row must leave exactly
// one matrix draft and, on retry, exactly one Touch record. The replay rides
// on field identity, so no Idempotency-Key contract is needed.
func TestWireResponseLossRetryKeepsOneMatrixDraft(t *testing.T) {
	srv := newWireDraftServer("active")
	srv.dropFirst = true
	c := wireConsumer(t, srv, permJSON)

	first, err := wireSchedule(t, c)
	if err != nil {
		t.Fatal(err)
	}
	if first.MatrixButton.Enabled || first.MatrixButton.Reason != ReasonMatrixSupply || len(c.Drafts()) != 0 {
		t.Fatalf("lost response = %+v drafts %d", first.MatrixButton, len(c.Drafts()))
	}
	if srv.puts != 1 {
		t.Fatalf("puts after loss = %d", srv.puts)
	}

	second, err := wireSchedule(t, c)
	if err != nil {
		t.Fatal(err)
	}
	if srv.puts != 2 {
		t.Fatalf("puts after retry = %d", srv.puts)
	}
	if len(srv.rows) != 1 {
		t.Fatalf("matrix rows = %d; want exactly one draft on the matrix", len(srv.rows))
	}
	if !second.MatrixButton.Enabled || len(c.Drafts()) != 1 {
		t.Fatalf("retry = %+v drafts %d", second.MatrixButton, len(c.Drafts()))
	}
	replayed := second.Draft.Plan.ID
	stored := ""
	for _, row := range srv.rows {
		stored = row.draftID
	}
	if replayed != stored || !strings.HasPrefix(replayed, "md_") {
		t.Fatalf("replay id %q vs stored %q", replayed, stored)
	}
}

// Every precise matrix refusal disables only the matrix button with the
// matrix_-prefixed reason, records nothing, and leaves lead and UGC alone.
func TestWirePreciseErrorsDisableMatrixButtonOnly(t *testing.T) {
	cases := []struct {
		status int
		code   string
	}{
		{http.StatusForbidden, "grant_revoked"},
		{http.StatusForbidden, "grant_expired"},
		{http.StatusForbidden, "grant_unknown"},
		{http.StatusForbidden, "license_expired"},
		{http.StatusForbidden, "wrong_tenant"},
		{http.StatusConflict, "wrong_type"},
		{http.StatusConflict, "old_version"},
		{http.StatusConflict, "hash_conflict"},
		{http.StatusConflict, "idempotency_conflict"},
		{http.StatusBadRequest, "bad_request"},
		{http.StatusInternalServerError, "store_failed"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/permissions" {
					_, _ = w.Write([]byte(permJSON))
					return
				}
				writeWireErr(w, tc.status, tc.code)
			}))
			defer api.Close()
			c := New(NewHTTPPermission(api.URL, api.Client()), NewHTTPDraftSink(api.URL, api.Client()), nil)
			out, err := wireSchedule(t, c)
			if err != nil {
				// Refusals are button states; only the ack-not-draft claim
				// is an error, and an error status is never that.
				t.Fatal(err)
			}
			if out.MatrixButton.Enabled || out.MatrixButton.Reason != "matrix_"+tc.code {
				t.Fatalf("reason = %q; want matrix_%s", out.MatrixButton.Reason, tc.code)
			}
			if len(c.Drafts()) != 0 || out.OutboundComplete || out.Draft.ID != "" {
				t.Fatalf("refusal recorded a draft: %+v", out)
			}
			if !out.LeadButton.Enabled || !out.UGCButton.Enabled {
				t.Fatalf("other surfaces changed: %+v", out)
			}
		})
	}
}

// A granted permission without the full authority facts must not reach the
// matrix: the button is disabled with a precise reason and nothing is posted.
func TestWireAuthorityMissingBlocksPost(t *testing.T) {
	cases := []struct {
		name string
		perm string
	}{
		{"no_payer", `{"allowed":true,"supply":"ready","license_ref":"lic_v1","license_expires_at":"2027-01-02T03:04:05Z"}`},
		{"no_license", `{"allowed":true,"supply":"ready","payer_id":"pay_oper","license_expires_at":"2027-01-02T03:04:05Z"}`},
		{"no_expiry", `{"allowed":true,"supply":"ready","payer_id":"pay_oper","license_ref":"lic_v1"}`},
		{"malformed_expiry", `{"allowed":true,"supply":"ready","payer_id":"pay_oper","license_ref":"lic_v1","license_expires_at":"not-a-time"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newWireDraftServer("active")
			c := wireConsumer(t, srv, tc.perm)
			out, err := wireSchedule(t, c)
			if err != nil {
				t.Fatal(err)
			}
			if srv.puts != 0 {
				t.Fatalf("authority gap still posted %d drafts", srv.puts)
			}
			if out.MatrixButton.Enabled || out.MatrixButton.Reason != ReasonAuthorityMissing {
				t.Fatalf("button = %+v", out.MatrixButton)
			}
			if len(c.Drafts()) != 0 || out.OutboundComplete {
				t.Fatalf("outcome = %+v", out)
			}
		})
	}
}

// An unreachable matrix stays a missing supply: the reason does not pretend
// to be a matrix answer.
func TestWireUnreachableMatrixStaysSupplyMissing(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/permissions" {
			_, _ = w.Write([]byte(permJSON))
			return
		}
		http.NotFound(w, r) // a matrix process that does not know this route
	}))
	defer api.Close()
	c := New(NewHTTPPermission(api.URL, api.Client()), NewHTTPDraftSink(api.URL, api.Client()), nil)
	out, err := wireSchedule(t, c)
	if err != nil {
		t.Fatal(err)
	}
	if out.MatrixButton.Enabled || out.MatrixButton.Reason != ReasonMatrixSupply || len(c.Drafts()) != 0 {
		t.Fatalf("unreachable = %+v", out)
	}
}

// The permission read parses the optional authority facts and refuses to
// invent them: a malformed expiry is absent, not guessed.
func TestHTTPPermissionParsesAuthority(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("action") != "brand_matrix_draft" {
			t.Errorf("query %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(permJSON))
	}))
	defer srv.Close()
	perm, err := NewHTTPPermission(srv.URL, srv.Client()).Decide(context.Background(), "usr", "ten", "brd")
	if err != nil || !perm.Allowed {
		t.Fatalf("perm = %+v %v", perm, err)
	}
	if perm.Authority.PayerID != "pay_oper" || perm.Authority.LicenseRef != "lic_v1" {
		t.Fatalf("authority = %+v", perm.Authority)
	}
	if !perm.Authority.LicenseExpiresAt.Equal(time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("expiry = %v", perm.Authority.LicenseExpiresAt)
	}
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"allowed":true,"supply":"ready","license_expires_at":"yesterday"}`))
	}))
	defer broken.Close()
	perm, err = NewHTTPPermission(broken.URL, broken.Client()).Decide(context.Background(), "usr", "ten", "brd")
	if err != nil || !perm.Allowed {
		t.Fatalf("broken perm = %+v %v", perm, err)
	}
	if !perm.Authority.LicenseExpiresAt.IsZero() {
		t.Fatalf("malformed expiry parsed as %v", perm.Authority.LicenseExpiresAt)
	}
}
