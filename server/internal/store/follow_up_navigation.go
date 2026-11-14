package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"time"
)

type FollowUpNavigation struct {
	HandoffID, Kind, PrincipalID, SourceTenantID, SourceRef string
	SourceRevision                                          int
	StoreID, CRMTenantID, LeadID, OriginHandoffID, VisitRef string
	ExpiresAt, UsedAt                                       int64
}

const followNavCols = `handoff_id,kind,principal_id,source_tenant_id,source_ref,source_revision,store_id,crm_tenant_id,lead_id,origin_handoff_id,COALESCE(visit_ref,''),expires_at,used_at`

func NewFollowUpVisitRef() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return "fuv_" + hex.EncodeToString(b)
}
func (s *Store) InsertFollowUpNavigation(n FollowUpNavigation) error {
	var visit any
	if n.VisitRef != "" {
		visit = n.VisitRef
	}
	_, err := s.DB.Exec(`INSERT INTO follow_up_navigation_contexts(handoff_id,kind,principal_id,source_tenant_id,source_ref,source_revision,store_id,crm_tenant_id,lead_id,origin_handoff_id,visit_ref,expires_at,used_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,0)`, n.HandoffID, n.Kind, n.PrincipalID, n.SourceTenantID, n.SourceRef, n.SourceRevision, n.StoreID, n.CRMTenantID, n.LeadID, n.OriginHandoffID, visit, n.ExpiresAt)
	return err
}
func scanFollowUpNavigation(row interface{ Scan(...any) error }) (FollowUpNavigation, error) {
	var n FollowUpNavigation
	err := row.Scan(&n.HandoffID, &n.Kind, &n.PrincipalID, &n.SourceTenantID, &n.SourceRef, &n.SourceRevision, &n.StoreID, &n.CRMTenantID, &n.LeadID, &n.OriginHandoffID, &n.VisitRef, &n.ExpiresAt, &n.UsedAt)
	return n, err
}
func (s *Store) GetFollowUpNavigation(h string) (FollowUpNavigation, error) {
	return scanFollowUpNavigation(s.DB.QueryRow(`SELECT `+followNavCols+` FROM follow_up_navigation_contexts WHERE handoff_id=?`, h))
}
func (s *Store) GetFollowUpVisit(v string) (FollowUpNavigation, error) {
	return scanFollowUpNavigation(s.DB.QueryRow(`SELECT `+followNavCols+` FROM follow_up_navigation_contexts WHERE visit_ref=? AND used_at>0`, v))
}
func (s *Store) ConsumeFollowUpNavigation(h, principal string, at time.Time) error {
	r, err := s.DB.Exec(`UPDATE follow_up_navigation_contexts SET used_at=? WHERE handoff_id=? AND principal_id=? AND used_at=0 AND expires_at>?`, at.Unix(), h, principal, at.Unix())
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	return nil
}
