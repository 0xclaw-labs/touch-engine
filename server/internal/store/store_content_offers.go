package store

// SaveProductionOffer stores only signed authority context so task retries can
// resume the same usage/hold after logout or transport loss. No money is minted.
func (s *Store) SaveProductionOffer(tenant, quoteRef, token string) error {
	_, err := s.DB.Exec(`INSERT INTO content_production_offers(tenant_id,quote_ref,offer_token) VALUES(?,?,?) ON CONFLICT(tenant_id,quote_ref) DO NOTHING`, tenant, quoteRef, token)
	return err
}
func (s *Store) ProductionOfferToken(tenant, quoteRef string) (string, error) {
	var token string
	err := s.DB.QueryRow(`SELECT offer_token FROM content_production_offers WHERE tenant_id=? AND quote_ref=?`, tenant, quoteRef).Scan(&token)
	return token, err
}

func (s *Store) SaveProductionHold(tenant, quoteRef, holdID string) error {
	_, err := s.DB.Exec(`UPDATE content_production_offers SET hold_id=? WHERE tenant_id=? AND quote_ref=? AND (hold_id='' OR hold_id=?)`, holdID, tenant, quoteRef, holdID)
	return err
}
func (s *Store) ProductionHoldRef(tenant, quoteRef string) (string, error) {
	var holdID string
	err := s.DB.QueryRow(`SELECT hold_id FROM content_production_offers WHERE tenant_id=? AND quote_ref=?`, tenant, quoteRef).Scan(&holdID)
	return holdID, err
}
