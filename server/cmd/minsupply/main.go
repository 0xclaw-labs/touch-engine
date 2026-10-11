// Command minsupply is a loopback stand-in for the public permission read
// and the matrix draft sink. Drafts stay in this process's memory.
// Responses stay drafts: status is draft and executed is false.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const listenAddr = "127.0.0.1:18741"

type permissionResponse struct {
	Allowed bool   `json:"allowed"`
	Supply  string `json:"supply"`
	Reason  string `json:"reason"`
}

type draftAck struct {
	PlanID   string `json:"plan_id"`
	Status   string `json:"status"`
	Executed bool   `json:"executed"`
}

type draftRecord struct {
	PlanID string
	Body   map[string]any
}

type supply struct {
	mu    sync.Mutex
	saved []draftRecord
}

func main() {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("minsupply: listen: %v", err)
	}
	mux := http.NewServeMux()
	svc := &supply{}
	mux.HandleFunc("GET /permissions", svc.permissions)
	mux.HandleFunc("POST /drafts", svc.drafts)
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("minsupply: listening on %s", ln.Addr().String())
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("minsupply: serve: %v", err)
	}
}

func (s *supply) permissions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	action := strings.TrimSpace(q.Get("action"))
	tenantID := strings.TrimSpace(q.Get("tenant_id"))
	brandID := strings.TrimSpace(q.Get("brand_id"))
	principalID := strings.TrimSpace(q.Get("principal_id"))
	resp := permissionResponse{Allowed: false, Supply: "missing", Reason: "missing_query"}
	switch {
	case action == "brand_matrix_draft" && tenantID != "" && brandID != "" && principalID != "":
		resp = permissionResponse{Allowed: true, Supply: "ready", Reason: "ok"}
	case action != "" && action != "brand_matrix_draft":
		resp.Reason = "unsupported_action"
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *supply) drafts(w http.ResponseWriter, r *http.Request) {
	media := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if !strings.HasPrefix(media, "application/json") {
		writeJSON(w, http.StatusUnsupportedMediaType, permissionResponse{
			Allowed: false, Supply: "missing", Reason: "content_type",
		})
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, permissionResponse{
			Allowed: false, Supply: "missing", Reason: "read_body",
		})
		return
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil || body == nil {
		writeJSON(w, http.StatusBadRequest, permissionResponse{
			Allowed: false, Supply: "missing", Reason: "invalid_json",
		})
		return
	}
	planID, err := newPlanID()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, permissionResponse{
			Allowed: false, Supply: "missing", Reason: "plan_id",
		})
		return
	}
	s.mu.Lock()
	s.saved = append(s.saved, draftRecord{PlanID: planID, Body: body})
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, draftAck{
		PlanID:   planID,
		Status:   "draft",
		Executed: false,
	})
}

func newPlanID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return "plan_" + hex.EncodeToString(buf[:]), nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	payload, err := json.Marshal(v)
	if err != nil {
		http.Error(w, `{"allowed":false,"supply":"missing","reason":"encode"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}
