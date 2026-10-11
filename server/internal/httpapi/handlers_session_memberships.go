package httpapi

import "net/http"

// handleSessionMemberships lists the signed-in principal's enabled memberships.
// The tenant header and query are ignored: a hand-filled id is not a selector.
func (s *Server) handleSessionMemberships(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if c == nil || c.Principal.ID == "" {
		fail(w, http.StatusUnauthorized, "unauthenticated", "no session")
		return
	}
	rows, err := s.St.ListMembershipsByPrincipal(c.Principal.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "membership lookup failed")
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, m := range rows {
		name := m.DisplayName
		if t, terr := s.St.GetTenant(m.TenantID); terr == nil && t.Name != "" {
			name = t.Name
		}
		if name == "" {
			name = "当前组织"
		}
		items = append(items, map[string]any{
			"tenant_id":    m.TenantID,
			"display_name": name,
			"role":         m.Role,
			"enabled":      true,
			"source":       "membership",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
