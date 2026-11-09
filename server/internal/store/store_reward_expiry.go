package store

// HUI-1671: 权益到期事实快照、顾客展示码(可重签)、访客本人券查询、
// 核销边界的到期/外部供给守卫。
//
// 纪律:
//   - 六种到期字段落库(reward_expiry_facts),查询时逐项给原因,永不复用
//     单一 expired 字段;
//   - 已发未过期权益: 订阅到期/活动结束/渠道关闭/参与截止只停新承诺,
//     核销只看券本身有效期(T16);
//   - 展示码只存哈希,短时,可重签;码只定位查验,不能换取顾客身份;
//   - participations 表是 3546 的权威,这里只读(SELECT visitor_id)。

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/rewardops"
)

// ErrRewardExpired: 券本身有效期已过(核销/重签拒绝;查询仍可)。
var ErrRewardExpired = errors.New("store: benefit validity passed")

// RewardExpiryFacts is the six-deadline snapshot taken when a benefit is
// reserved or issued. ”/0 = no deadline (not judged).
type RewardExpiryFacts struct {
	BenefitID          string `json:"benefit_id"`
	TenantID           string `json:"tenant_id"`
	SubscriptionUntil  string `json:"subscription_until,omitempty"`
	CampaignUntil      string `json:"campaign_until,omitempty"`
	ChannelChecked     bool   `json:"channel_checked"`
	ChannelOpen        bool   `json:"channel_open"`
	ParticipationUntil string `json:"participation_until,omitempty"`
	ReservationUntil   string `json:"reservation_until,omitempty"`
	BenefitUntil       string `json:"benefit_until,omitempty"`
	JudgedAt           string `json:"judged_at"`
	UpdatedAt          string `json:"updated_at"`
}

func (f RewardExpiryFacts) deadlines() rewardops.GrantDeadlines {
	return rewardops.GrantDeadlines{
		SubscriptionUntil:  f.SubscriptionUntil,
		CampaignUntil:      f.CampaignUntil,
		ParticipationUntil: f.ParticipationUntil,
		ReservationUntil:   f.ReservationUntil,
		BenefitUntil:       f.BenefitUntil,
		ChannelChecked:     f.ChannelChecked,
		ChannelOpen:        f.ChannelOpen,
	}
}

const rewardExpiryFactsCols = `benefit_id,tenant_id,subscription_until,campaign_until,channel_checked,channel_open,participation_until,reservation_until,benefit_until,judged_at,updated_at`

func scanRewardExpiryFacts(sc interface{ Scan(...any) error }) (RewardExpiryFacts, error) {
	var f RewardExpiryFacts
	var sub, camp, part, res, ben sql.NullString
	err := sc.Scan(&f.BenefitID, &f.TenantID, &sub, &camp, &f.ChannelChecked, &f.ChannelOpen,
		&part, &res, &ben, &f.JudgedAt, &f.UpdatedAt)
	if err != nil {
		return RewardExpiryFacts{}, err
	}
	f.SubscriptionUntil, f.CampaignUntil, f.ParticipationUntil = sub.String, camp.String, part.String
	f.ReservationUntil, f.BenefitUntil = res.String, ben.String
	return f, nil
}

// upsertExpiryFactsExec writes the snapshot through either the ledger
// transaction or the bare DB (same SQL, same columns).
func upsertExpiryFactsExec(run interface {
	Exec(query string, args ...any) (sql.Result, error)
}, benefitID, tenantID string, dl rewardops.GrantDeadlines, nowTS string) error {
	if strings.TrimSpace(benefitID) == "" {
		return fmt.Errorf("%w: missing benefit id", ErrRewardInvalid)
	}
	_, err := run.Exec(
		`INSERT INTO reward_expiry_facts(`+rewardExpiryFactsCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(benefit_id) DO UPDATE SET
		   subscription_until=excluded.subscription_until, campaign_until=excluded.campaign_until,
		   channel_checked=excluded.channel_checked, channel_open=excluded.channel_open,
		   participation_until=excluded.participation_until, reservation_until=excluded.reservation_until,
		   benefit_until=excluded.benefit_until, judged_at=excluded.judged_at, updated_at=excluded.updated_at`,
		benefitID, tenantID, dl.SubscriptionUntil, dl.CampaignUntil, dl.ChannelChecked, dl.ChannelOpen,
		dl.ParticipationUntil, dl.ReservationUntil, dl.BenefitUntil, nowTS, nowTS)
	return err
}

// saveExpiryFactsInTx upserts the six-deadline snapshot inside the ledger
// transaction, filling unknown deadlines from the batch/campaign the store
// can see.
func saveExpiryFactsInTx(tx *sql.Tx, benefitID, tenantID string, inv RewardInventory, camp Campaign, dl *rewardops.GrantDeadlines, reservationUntil, nowTS string) error {
	if dl == nil {
		dl = &rewardops.GrantDeadlines{}
	}
	filled := rewardops.GrantDeadlines{
		SubscriptionUntil:  dl.SubscriptionUntil,
		CampaignUntil:      firstNonEmpty(dl.CampaignUntil, camp.EndsAt),
		ChannelChecked:     dl.ChannelChecked,
		ChannelOpen:        dl.ChannelOpen,
		ParticipationUntil: dl.ParticipationUntil,
		ReservationUntil:   firstNonEmpty(dl.ReservationUntil, reservationUntil),
		BenefitUntil:       firstNonEmpty(dl.BenefitUntil, inv.ValidUntil),
	}
	return upsertExpiryFactsExec(tx, benefitID, tenantID, filled, nowTS)
}

// SaveExpiryFactsForBenefit upserts the snapshot for an existing benefit row.
func (s *Store) SaveExpiryFactsForBenefit(benefitID, tenantID string, dl rewardops.GrantDeadlines, nowTS string) error {
	return upsertExpiryFactsExec(s.DB, benefitID, tenantID, dl, nowTS)
}

// GetRewardExpiryFacts loads the stored snapshot (ErrNotFound when absent).
func (s *Store) GetRewardExpiryFacts(benefitID, tenantID string) (RewardExpiryFacts, error) {
	row := s.DB.QueryRow(
		`SELECT `+rewardExpiryFactsCols+` FROM reward_expiry_facts WHERE benefit_id=? AND tenant_id=?`,
		benefitID, tenantID)
	f, err := scanRewardExpiryFacts(row)
	if errors.Is(err, sql.ErrNoRows) {
		return RewardExpiryFacts{}, ErrNotFound
	}
	return f, err
}

// benefitValidityUntil returns the effective benefit validity deadline:
// the stored snapshot wins; legacy rows fall back to the batch's ValidUntil.
func (s *Store) benefitValidityUntil(tx *sql.Tx, tenantID string, ben RewardBenefit) (string, error) {
	q := `SELECT benefit_id,tenant_id,subscription_until,campaign_until,channel_checked,channel_open,participation_until,reservation_until,benefit_until,judged_at,updated_at
	      FROM reward_expiry_facts WHERE benefit_id=? AND tenant_id=?`
	var f RewardExpiryFacts
	var sub, camp, part, res, benUntil, judged, updated sql.NullString
	var err error
	if tx != nil {
		err = tx.QueryRow(q, ben.ID, tenantID).Scan(&f.BenefitID, &f.TenantID, &sub, &camp, &f.ChannelChecked, &f.ChannelOpen, &part, &res, &benUntil, &judged, &updated)
	} else {
		err = s.DB.QueryRow(q, ben.ID, tenantID).Scan(&f.BenefitID, &f.TenantID, &sub, &camp, &f.ChannelChecked, &f.ChannelOpen, &part, &res, &benUntil, &judged, &updated)
	}
	if err == nil {
		f.BenefitID, f.TenantID = ben.ID, tenantID
		f.SubscriptionUntil, f.CampaignUntil, f.ParticipationUntil = sub.String, camp.String, part.String
		f.ReservationUntil, f.BenefitUntil = res.String, benUntil.String
		f.JudgedAt, f.UpdatedAt = judged.String, updated.String
		return f.BenefitUntil, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		inv, ierr := s.getInventoryInTx(tx, tenantID, ben.InventoryID)
		if ierr != nil {
			return "", ierr
		}
		return inv.ValidUntil, nil
	}
	return "", err
}

func (s *Store) getInventoryInTx(tx *sql.Tx, tenantID, inventoryID string) (RewardInventory, error) {
	q := `SELECT ` + rewardInventoryCols + ` FROM reward_inventory WHERE id=? AND tenant_id=?`
	var row *sql.Row
	if tx != nil {
		row = tx.QueryRow(q, inventoryID, tenantID)
	} else {
		row = s.DB.QueryRow(q, inventoryID, tenantID)
	}
	occupied := 0
	inv, err := scanRewardInventory("", compositeScan{row, &occupied})
	if errors.Is(err, sql.ErrNoRows) {
		return RewardInventory{}, ErrNotFound
	}
	return inv, err
}

// validUntilPassed reports whether until is a set, already-passed deadline.
func validUntilPassed(until, nowTS string) bool {
	if strings.TrimSpace(until) == "" {
		return false
	}
	u, err := time.Parse(time.RFC3339, until)
	if err != nil {
		return true // unparseable = conservatively passed
	}
	n, err := time.Parse(time.RFC3339Nano, nowTS)
	if err != nil {
		return true
	}
	return n.After(u)
}

// redeemGuardInTx refuses redemptions the local ledger has no right to make:
// a benefit whose own validity has passed, and a benefit bound to an external
// platform offer (external facts only come from the authorized adapter).
func (s *Store) redeemGuardInTx(tx *sql.Tx, tenantID string, ben RewardBenefit, nowTS string) error {
	if ben.Status != RewardStatusIssued {
		return nil // state machine handles other statuses with its own reasons
	}
	until, err := s.benefitValidityUntil(tx, tenantID, ben)
	if err != nil {
		return err
	}
	if validUntilPassed(until, nowTS) {
		return ErrRewardExpired
	}
	offer, oerr := s.benefitOffer(tx, tenantID, ben.InventoryID)
	if oerr == nil && offer.IsExternal() {
		return ErrExternalRedeemForbidden
	}
	return nil
}

// ---- 顾客展示码(短时,可重签) --------------------------------------------------

const displayCodeTTL = 10 * time.Minute

// rewardVisitorForBenefit reads the owning visitor of a benefit's
// participation (participations 表只读,3546 权威). ErrNotFound when the
// participation row does not exist.
func (s *Store) rewardVisitorForBenefit(tenantID, participationID string) (string, error) {
	var visitor string
	err := s.DB.QueryRow(
		`SELECT visitor_id FROM participations WHERE id=? AND tenant_id=?`,
		participationID, tenantID).Scan(&visitor)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return visitor, err
}

// RewardDisplayCode is the minted short-lived code. Token exists only here
// (one response); only its hash is stored, never logged.
type RewardDisplayCode struct {
	ID        string `json:"id"`
	BenefitID string `json:"benefit_id"`
	ExpiresAt string `json:"expires_at"`
	Token     string `json:"token"`
}

func hashDisplayToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// MintRewardDisplayCode re-signs a short-lived staff_verify display code for
// an issued benefit the visitor owns. Old codes are not invalidated (each has
// its own TTL; an expired code is refused at lookup) — 重签是补发,不是改账。
func (s *Store) MintRewardDisplayCode(tenantID, benefitID, visitorID, nowTS string) (RewardDisplayCode, error) {
	out := RewardDisplayCode{}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(benefitID) == "" || strings.TrimSpace(visitorID) == "" {
		return out, fmt.Errorf("%w: missing fields", ErrRewardInvalid)
	}
	if _, err := time.Parse(time.RFC3339Nano, nowTS); err != nil {
		return out, fmt.Errorf("%w: bad now", ErrRewardInvalid)
	}
	row := s.DB.QueryRow(`SELECT `+rewardBenefitCols+` FROM reward_benefits WHERE id=? AND tenant_id=?`, benefitID, tenantID)
	ben, err := scanRewardBenefit(row)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	owner, err := s.rewardVisitorForBenefit(tenantID, ben.ParticipationID)
	if err != nil {
		return out, ErrNotFound // no participation = no ownership proof
	}
	if owner != strings.TrimSpace(visitorID) {
		// 伪造/跨访客请求:不泄露存在性,答 not_found。
		return out, ErrNotFound
	}
	if ben.Status != RewardStatusIssued {
		return out, fmt.Errorf("%w: benefit not in issued state", ErrRewardConflict)
	}
	// 券本身到期与否独立判定(T16): 其它五种到期不拦重签。
	until, err := s.benefitValidityUntil(nil, tenantID, ben)
	if err != nil {
		return out, err
	}
	if validUntilPassed(until, nowTS) {
		return out, ErrRewardExpired
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return out, err
	}
	token := hex.EncodeToString(raw[:])
	expiry, err := addSeconds(nowTS, int(displayCodeTTL.Seconds()))
	if err != nil {
		return out, err
	}
	code := RewardDisplayCode{ID: newID("rdc_"), BenefitID: ben.ID, ExpiresAt: expiry, Token: token}
	if _, err := s.DB.Exec(
		`INSERT INTO reward_display_codes(id,tenant_id,benefit_id,token_hash,purpose,created_by_visitor,expires_at,used_at,revoked_at,created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?)`,
		code.ID, tenantID, ben.ID, hashDisplayToken(token), "staff_verify_display", visitorID, expiry, "", "", nowTS); err != nil {
		return out, err
	}
	return code, nil
}

// ResolveRewardDisplayCode locates a benefit by a display code the customer
// shows at the counter. Expired/revoked codes are refused (过期旧码本身拒绝).
// The answer is the benefit only — no customer identity rides back.
func (s *Store) ResolveRewardDisplayCode(tenantID, token, nowTS string) (RewardBenefit, error) {
	t := strings.TrimSpace(token)
	if t == "" {
		return RewardBenefit{}, ErrNotFound
	}
	var benefitID string
	var expires string
	err := s.DB.QueryRow(
		`SELECT benefit_id, expires_at FROM reward_display_codes
		 WHERE tenant_id=? AND token_hash=? AND revoked_at=''`,
		tenantID, hashDisplayToken(t)).Scan(&benefitID, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return RewardBenefit{}, ErrNotFound
	}
	if err != nil {
		return RewardBenefit{}, err
	}
	exp, perr := time.Parse(time.RFC3339, expires)
	n, nerr := time.Parse(time.RFC3339Nano, nowTS)
	if perr != nil || nerr != nil || !n.Before(exp) {
		return RewardBenefit{}, ErrNotFound // expired code: same shape as unknown
	}
	row := s.DB.QueryRow(`SELECT `+rewardBenefitCols+` FROM reward_benefits WHERE id=? AND tenant_id=?`, benefitID, tenantID)
	ben, err := scanRewardBenefit(row)
	if errors.Is(err, sql.ErrNoRows) {
		return RewardBenefit{}, ErrNotFound
	}
	return ben, err
}

// ---- 访客本人券查询(不经新参与 Gate) ------------------------------------------

// VisitorBenefitView is one row of the customer's own benefit list: status,
// their own coupon code, face value, and the per-deadline expiry reasons.
type VisitorBenefitView struct {
	Benefit        RewardBenefit `json:"benefit"`
	ExpiryReasons  []string      `json:"expiry_reasons"`
	RedeemAllowed  bool          `json:"redeem_allowed"`
	RedeemBlockers []string      `json:"redeem_blockers,omitempty"`
	IsExternal     bool          `json:"is_external"`
	ExternalState  string        `json:"external_state,omitempty"` // issued|pending_unknown|failed
}

// ListRewardBenefitsForVisitor returns the visitor's own issued/redeemed
// benefits across all tenants their participations live in. 查询路径不经过
// resolveAvailableLink:活动结束/订阅到期后旧券仍可查(T16)。
// 单连接 SQLite: 先完整收集行,再逐行补到期判定(不开嵌套迭代器)。
func (s *Store) ListRewardBenefitsForVisitor(visitorID, nowTS string) ([]VisitorBenefitView, error) {
	v := strings.TrimSpace(visitorID)
	if v == "" {
		return nil, nil
	}
	bens := []RewardBenefit{}
	{
		rows, err := s.DB.Query(
			`SELECT `+rewardBenefitColsPrefix()+` FROM reward_benefits b
			 JOIN participations p ON p.id = b.participation_id AND p.tenant_id = b.tenant_id
			 WHERE p.visitor_id=? AND b.status IN ('issued','redeemed')
			 ORDER BY b.created_at DESC, b.id DESC`, v)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			ben, err := scanRewardBenefit(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			bens = append(bens, ben)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	out := []VisitorBenefitView{}
	for _, ben := range bens {
		view := VisitorBenefitView{Benefit: ben}
		dl, derr := s.deadlinesForBenefit(ben.TenantID, ben)
		if derr == nil {
			verdict := rewardops.AdjudicateExpiry(rewardops.ExpiryOpQuery, dl, nowTS)
			view.ExpiryReasons = verdict.Reasons
			rv := rewardops.AdjudicateExpiry(rewardops.ExpiryOpRedeem, dl, nowTS)
			view.RedeemAllowed = rv.Allowed
			if !rv.Allowed {
				view.RedeemBlockers = rv.Reasons // blockers only when actually blocked
			}
		} else {
			view.ExpiryReasons = []string{}
			view.RedeemAllowed = false
			view.RedeemBlockers = []string{"unknown"}
		}
		view.IsExternal = s.BenefitIsExternal(ben.TenantID, ben)
		if view.IsExternal {
			view.RedeemAllowed = false
			view.RedeemBlockers = append(view.RedeemBlockers, "external_redeem_local_forbidden")
			if e, eerr := s.latestExternalIssuanceForBenefit(ben.TenantID, ben.ID); eerr == nil {
				view.ExternalState = e.State
			}
		}
		out = append(out, view)
	}
	return out, nil
}

func rewardBenefitColsPrefix() string {
	// rewardBenefitCols is a bare column list; qualify it for the join.
	cols := strings.Split(rewardBenefitCols, ",")
	for i, c := range cols {
		cols[i] = "b." + c
	}
	return strings.Join(cols, ",")
}

// deadlinesForBenefit assembles the six deadlines for one benefit: stored
// snapshot first, live fallback for legacy rows.
func (s *Store) deadlinesForBenefit(tenantID string, ben RewardBenefit) (rewardops.GrantDeadlines, error) {
	if f, err := s.GetRewardExpiryFacts(ben.ID, tenantID); err == nil {
		return f.deadlines(), nil
	}
	dl := rewardops.GrantDeadlines{ReservationUntil: ben.ReservedUntil}
	if camp, cerr := s.GetCampaign(ben.CampaignID, tenantID); cerr == nil {
		dl.CampaignUntil = camp.EndsAt
	}
	if inv, ierr := s.getInventoryInTx(nil, tenantID, ben.InventoryID); ierr == nil {
		dl.BenefitUntil = inv.ValidUntil
	}
	return dl, nil
}

func (s *Store) latestExternalIssuanceForBenefit(tenantID, benefitID string) (ExternalIssuance, error) {
	row := s.DB.QueryRow(
		`SELECT `+externalIssuanceCols+` FROM reward_external_issuances
		 WHERE tenant_id=? AND benefit_id=? ORDER BY created_at DESC, id DESC LIMIT 1`,
		tenantID, benefitID)
	e, err := scanExternalIssuance(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ExternalIssuance{}, ErrNotFound
	}
	return e, err
}
