// Package participation is HUI-3546 碰一碰门店交付 A2: the authoritative
// record of a customer participation (参与), its publish attempts (尝试), and
// the controlled handoff credentials (交接凭据) that carry the SAME
// participation across H5 → platform mini-program → staff.
//
// 纪律(票面红线):
//   - 浏览不建参与:只有顾客显式开始才落行;同一幂等键重放回原记录;
//   - 内容预览可换选;真正交接时冻结该 attempt 的内容包版本与规则版本;
//     换稿/换平台 = 新 attempt,但不新增 benefit_slot(一参与一槽);
//   - 状态语义分开:pending/in_progress/awaiting_review/fulfilled/canceled,
//     禁单一 success 布尔;fulfilled 只经 MarkFulfilled(G3 受信接线)到达,
//     晚到失败/回执乱序不静默改写已兑现;
//   - 权益预留/发放/核销的权威归 HUI-1671:本包只持 BenefitReserver 受信端口,
//     未确认预留绝不表述为"已保留",不建第二份库存;
//   - 交接凭据短时/单用途/可撤销,128-bit 随机 + 校验位防枚举,库内只存
//     SHA-256 哈希,明文永不落库、永不进日志;码 ≠ 发奖授权;
//   - 参与档案与凭据不进公共日志;visitor_id 是首方随机续接键,
//     不是手机号、不是平台 open_id,不建消费者主库。
//
// 本包是叶子包:参与内部表只由本包(即 G2)修改;存储自持 SQL,
// httpapi 仅经 Service 消费。
package participation

import (
	"errors"
	"time"
)

// Status is the participation lifecycle. The five states are the contract:
// no boolean collapses canceled / awaiting_review / fulfilled.
type Status string

const (
	StatusPending        Status = "pending"         // 已开始,权益预留未确认
	StatusInProgress     Status = "in_progress"     // 首次交接已发出
	StatusAwaitingReview Status = "awaiting_review" // 店员/官方核验中
	StatusFulfilled      Status = "fulfilled"       // 已兑现(仅 G3 受信接线)
	StatusCanceled       Status = "canceled"        // 顾客或店员取消(终态)
)

// AttemptStatus is the attempt lifecycle. Publish evidence states
// (published / official receipts) belong to HUI-1670/1671 domains and are
// deliberately absent here.
type AttemptStatus string

const (
	AttemptPrepared  AttemptStatus = "prepared"  // 已备好,尚未交接(版本未冻结)
	AttemptHandedOff AttemptStatus = "handed_off" // 已交接,内容与规则已冻结
	AttemptCompleted AttemptStatus = "completed"  // 该次尝试闭环(顾客回传完成)
	AttemptCanceled  AttemptStatus = "canceled"
)

// Source is where the customer came from. A copied short code or NFC tag is
// NOT an in-store proof; the source is recorded for counting only and must
// never gate a high-value reward by itself.
type Source string

const (
	SourceNFC      Source = "nfc"
	SourceQR       Source = "qr"
	SourceShortURL Source = "shorturl"
	SourceManual   Source = "manual"
)

// HandoffMethod is how an attempt leaves the H5 page.
type HandoffMethod string

const (
	MethodH5                HandoffMethod = "h5"                  // 同浏览器/站内续接
	MethodPlatformMini      HandoffMethod = "platform_miniprogram" // 去平台小程序发布
	MethodStaffView         HandoffMethod = "staff_view"           // 店员目测核验
)

// Purpose of a handoff credential.
type Purpose string

const (
	PurposeCustomerResume Purpose = "customer_resume" // 顾客跨端续接(换绑 visitor)
	PurposeStaffVerify    Purpose = "staff_verify"     // 店员扫码核验(需商家会话)
)

// Reservation states. Only confirmed means G3 acknowledged the reservation;
// pending/failed must never be rendered as 已保留.
type ReservationState string

const (
	ReservationNotRequested ReservationState = "not_requested"
	ReservationPending      ReservationState = "pending"
	ReservationConfirmed    ReservationState = "confirmed"
	ReservationFailed       ReservationState = "failed"
)

// Sentinel errors (stable for tests/handlers).
var (
	ErrNotFound             = errors.New("participation: not found")
	ErrIdempotencyConflict  = errors.New("participation: idempotency key conflict")
	ErrFrozen               = errors.New("participation: versions frozen at handoff")
	ErrInvalidSource        = errors.New("participation: invalid touchpoint source")
	ErrInvalidTransition    = errors.New("participation: illegal status transition")
	ErrCredentialExpired    = errors.New("participation: handoff credential expired")
	ErrCredentialUsed       = errors.New("participation: handoff credential already used")
	ErrCredentialRevoked    = errors.New("participation: handoff credential revoked")
	ErrTargetMismatch       = errors.New("participation: handoff target mismatch")
	ErrNotEntitled          = errors.New("participation: not entitled to read this participation")
)

// Window is the participation validity window (RFC3339 strings, campaign-
// aligned). An expired window closes further handoffs; reading stays allowed
// so a customer can still see what happened.
type Window struct {
	StartsAt  string
	ExpiresAt string
}

// Expired reports whether the window has closed at time now.
func (w Window) Expired(now time.Time) bool {
	if w.ExpiresAt == "" {
		return false
	}
	end, err := time.Parse(time.RFC3339, w.ExpiresAt)
	if err != nil {
		return true // unparsable expiry fails closed
	}
	return now.After(end)
}

// transitions is the legal state machine. pending→fulfilled directly is
// deliberately illegal (a reservation is not a fulfillment).
var transitions = map[Status]map[Status]bool{
	StatusPending: {
		StatusInProgress:     true,
		StatusAwaitingReview: true, // 店员核验可先于发布交接(目测场景)
		StatusCanceled:       true,
	},
	StatusInProgress: {
		StatusAwaitingReview: true,
		StatusCanceled:       true,
	},
	StatusAwaitingReview: {
		StatusFulfilled: true,
		StatusCanceled:  true,
	},
	StatusFulfilled: {},
	StatusCanceled:  {},
}

// CanTransition reports whether from→to is a legal participation transition.
// Unknown statuses fail closed.
func CanTransition(from, to Status) bool {
	m, ok := transitions[from]
	if !ok {
		return false
	}
	return m[to]
}

// ValidSource reports whether s is a known touchpoint source.
func ValidSource(s Source) bool {
	switch s {
	case SourceNFC, SourceQR, SourceShortURL, SourceManual:
		return true
	}
	return false
}

// Participation is the authoritative record of one customer joining one
// campaign. It is tenant/store/campaign bound from birth; nothing in it is
// derived from a phone number or a platform identity.
type Participation struct {
	ID                  string
	TenantID            string
	StoreID             string
	CampaignID          string
	LinkCode            string
	Source              Source
	RuleVersion         int
	ContentPackVersion  string
	VisitorID           string
	Status              Status
	IdempotencyKey      string
	BenefitSlot         string
	ReservationRef      string
	ReservationState    ReservationState
	FulfillmentRef      string
	Window              Window
	CreatedAt           string
	UpdatedAt           string
}

// Attempt is one distinguishable publish/handoff try. Its content and rule
// versions freeze at handoff time; later edits open a new attempt instead of
// rewriting history.
type Attempt struct {
	ID                 string
	TenantID           string
	ParticipationID    string
	Platform           string
	HandoffMethod      HandoffMethod
	ContentPackVersion string
	RuleVersion        int
	Status             AttemptStatus
	CreatedAt          string
	UpdatedAt          string
}

// Credential is the safe-to-expose view of a handoff credential. The token
// itself only ever exists in the response of the call that created it.
type Credential struct {
	ID              string
	ParticipationID string
	Purpose         Purpose
	TargetScope     string
	ExpiresAt       string
	UsedAt          string
	RevokedAt       string
	CreatedAt       string
}
