package httpapi

import (
	"encoding/json"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"
)

// Only exact, deliberate probes by an owner. The limiter is keyed by server +
// resolved principal, shared across tenants so switching tenants cannot evade it.
type memberLookupLimiter struct {
	sync.Mutex
	rows map[string][]time.Time
}

func (s *Server) handleMemberLookup(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, "manage_members", authzScope(c), w) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var in struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&in); err != nil {
		fail(w, 400, "bad_request", "请输入对方完整的登录邮箱。")
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || !strings.Contains(email, ".") || len(email) > 254 {
		fail(w, 400, "invalid_email", "请输入对方完整的登录邮箱，不支持搜索或批量查询。")
		return
	}
	now := time.Now()
	s.memberLookups.Lock()
	rows := s.memberLookups.rows
	if rows == nil {
		rows = make(map[string][]time.Time)
		s.memberLookups.rows = rows
	}
	for principal, times := range rows {
		if len(times) == 0 || now.Sub(times[len(times)-1]) >= 10*time.Minute {
			delete(rows, principal)
		}
	}
	hits := rows[c.Member.PrincipalRef]
	fresh := hits[:0]
	for _, at := range hits {
		if now.Sub(at) < 10*time.Minute {
			fresh = append(fresh, at)
		}
	}
	limited := len(fresh) >= 10
	if !limited {
		fresh = append(fresh, now)
	}
	rows[c.Member.PrincipalRef] = fresh
	s.memberLookups.Unlock()
	if limited {
		w.Header().Set("Retry-After", "600")
		fail(w, 429, "lookup_rate_limited", "人员查询过于频繁，请十分钟后重试。")
		return
	}
	if s.ID == nil || strings.TrimSpace(s.Cfg.IdentityBaseURL) == "" || strings.TrimSpace(s.Cfg.IdentityToken) == "" {
		fail(w, 503, "identity_unavailable", "人员服务暂不可用，请稍后重试或联系平台恢复公共身份连接。无需索取账号编号。")
		return
	}
	person, err := s.ID.UserByEmail(r.Context(), email)
	if err != nil {
		fail(w, 503, "identity_unavailable", "人员服务暂不可用，请稍后重试或联系平台恢复公共身份连接。无需索取账号编号。")
		return
	}
	if !person.Exists || person.Status != "active" || !strings.HasPrefix(person.UserID, "usr_") {
		fail(w, 404, "person_unavailable", "暂不能选择该人员。请对方先完成公共账号注册并确认账号可用，再按完整邮箱重试。")
		return
	}
	// account_id/status and unrelated directory entries never leave the BFF.
	writeJSON(w, http.StatusOK, map[string]any{"principal_ref": person.UserID, "display_name": email})
}
