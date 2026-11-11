// store_touchpoint_deliveries.go: HUI-1663 实体 NFC 交付域持久层。
//
// 纪律(票面 + briefs/hui-1663.md 裁定书 + 4591 报告 §7):
//   - 七步交付状态机(付款→出库→绑定→写卡→回读→现场验收→交付)裁决在
//     internal/delivery 纯函数;付款步事实归 4591 订单履约投影(只读前置,
//     创建闸校验 payment 已 done),出库/写卡/回读/验收四步在本域事件事务内
//     同步推 4591 投影(ApplyHardwareStepInTx,幂等/不可跳跃纪律同源)。
//     绑定确认是本域事实(4591 报告:绑定归 1665/1663 域,在其侧校验)。
//   - 事件账本只追加:UNIQUE(tenant_id,source,event_id) 去重;同键同负载 =
//     幂等读回(零推进/零投影推送——物理事件重放不推进两次);同键异负载 =
//     IDEMPOTENCY_CONFLICT;canonical payload 判等与 4591 同构。
//   - 回读一致性核对 = delivery.CheckReadback(纯函数):matches=false 只记事实,
//     不推进状态、不推投影、不更新实测版本——回读不一致绝不显示「已验证」。
//   - stale 纪律:交付行记录绑定确认与最后实测时的触点 binding_revision;
//     触点换绑后 View.BindingStale 如实暴露,不得沿用旧「已验证」标识。
//   - 补换两分支:damaged_recoverable 同触点换载体(逻辑入口保留,旧触点不动);
//     lost_uncontrolled 新触点换码(旧触点停用,遗失卡不能再成为有效入口)。
//     旧行封存 superseded + 0131 审计行 + replaced 事实;新行独立重走全流程。
//     参与/权益零触碰(1665 语义:参与挂活动不挂卡,结构保证快照不挪)。
//   - 卡片不写任何凭证(B15):全表零 secret;回读核对只针对公开 /t/{code} URL。
//   - 全查询租户作用域、全参数化;交付域零 bumpEpoch(公共解析只关心别名
//     目标/状态;补换停用旧触点经 SetTouchpointStatus 自带 epoch 失效)。

package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/billing"
	"github.com/bianjiefilm/touch-engine/server/internal/delivery"
)

// Sentinel errors for the delivery domain (handler maps onto honest codes).
var (
	// ErrDeliveryBadInput: 结构性坏请求(订单类型不符/补换参数缺失等)。
	ErrDeliveryBadInput = errors.New("store: bad delivery input")
	// ErrPaymentRequired: 4591 订单投影的付款步事实缺失(不可跳跃首步)。
	ErrPaymentRequired = errors.New("store: hardware order payment fact missing")
	// ErrStoreMismatch: 触点/交付的门店归属不一致。
	ErrStoreMismatch = errors.New("store: delivery store mismatch")
	// ErrTouchpointDisabled: 绑定确认时触点已停用。
	ErrTouchpointDisabled = errors.New("store: touchpoint disabled")
	// ErrLiveDeliveryExists: 该触点已有一条活交付(补换先封存旧行)。
	ErrLiveDeliveryExists = errors.New("store: touchpoint already has a live delivery")
	// ErrDeliveryIdempotencyConflict: 同 (source,event_id) 异负载。
	ErrDeliveryIdempotencyConflict = errors.New("store: delivery_idempotency_conflict")
	// ErrReplacementBadInput: 补换输入与其原因类别矛盾(见 ReplaceTouchpointDelivery)。
	ErrReplacementBadInput = errors.New("store: bad replacement input")
)

// Replacement reason categories (0131 CHECK 白名单同值)。
const (
	ReplaceReasonDamaged = "damaged_recoverable"
	ReplaceReasonLost    = "lost_uncontrolled"
)

// TouchpointDelivery mirrors one touchpoint_deliveries row.
type TouchpointDelivery struct {
	ID                      string `json:"id"`
	TenantID                string `json:"tenant_id"`
	StoreID                 string `json:"store_id"`
	TouchpointID            string `json:"touchpoint_id"`
	HardwareOrderID         string `json:"hardware_order_id"`
	DeliveryRef             string `json:"delivery_ref,omitempty"`
	PhysicalUnitRef         string `json:"physical_unit_ref,omitempty"`
	TagBatch                string `json:"tag_batch,omitempty"`
	Status                  string `json:"status"`
	BoundBindingRevision    int64  `json:"bound_binding_revision"`
	VerifiedBindingRevision int64  `json:"verified_binding_revision"`
	WriteEvidenceRef        string `json:"write_evidence_ref,omitempty"`
	ReadbackEvidenceRef     string `json:"readback_evidence_ref,omitempty"`
	DeviceEvidenceRef       string `json:"device_evidence_ref,omitempty"`
	ReplacementOf           string `json:"replacement_of,omitempty"`
	CreatedBy               string `json:"created_by,omitempty"`
	CreatedAt               string `json:"created_at"`
	UpdatedAt               string `json:"updated_at"`
}

const deliveryCols = `id,tenant_id,store_id,touchpoint_id,hardware_order_id,delivery_ref,physical_unit_ref,tag_batch,
status,bound_binding_revision,verified_binding_revision,write_evidence_ref,readback_evidence_ref,device_evidence_ref,
replacement_of,created_by,created_at,updated_at`

func scanDelivery(sc interface{ Scan(...any) error }) (TouchpointDelivery, error) {
	var d TouchpointDelivery
	var deliveryRef, unitRef, tagBatch, writeEv, readbackEv, deviceEv, replacementOf, createdBy sql.NullString
	err := sc.Scan(&d.ID, &d.TenantID, &d.StoreID, &d.TouchpointID, &d.HardwareOrderID,
		&deliveryRef, &unitRef, &tagBatch, &d.Status, &d.BoundBindingRevision, &d.VerifiedBindingRevision,
		&writeEv, &readbackEv, &deviceEv, &replacementOf, &createdBy, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return TouchpointDelivery{}, err
	}
	d.DeliveryRef, d.PhysicalUnitRef, d.TagBatch = deliveryRef.String, unitRef.String, tagBatch.String
	d.WriteEvidenceRef, d.ReadbackEvidenceRef, d.DeviceEvidenceRef = writeEv.String, readbackEv.String, deviceEv.String
	d.ReplacementOf, d.CreatedBy = replacementOf.String, createdBy.String
	return d, nil
}

// TouchpointDeliveryEvent mirrors one append-only ledger row (0130)。
type TouchpointDeliveryEvent struct {
	ID                      string `json:"id"`
	TenantID                string `json:"tenant_id"`
	DeliveryID              string `json:"delivery_id"`
	EventType               string `json:"event_type"`
	Source                  string `json:"source"`
	EventID                 string `json:"event_id"`
	OperatorRef             string `json:"operator_ref,omitempty"`
	DeviceRef               string `json:"device_ref,omitempty"`
	EvidenceRef             string `json:"evidence_ref,omitempty"`
	ObservedBindingRevision int64  `json:"observed_binding_revision"`
	Payload                 string `json:"payload,omitempty"`
	OccurredAt              string `json:"occurred_at"`
	CreatedAt               string `json:"created_at"`
}

// DeliveryStepFact is one recorded step fact projected from the ledger.
type DeliveryStepFact struct {
	EventType               string `json:"event_type"`
	OperatorRef             string `json:"operator_ref,omitempty"`
	DeviceRef               string `json:"device_ref,omitempty"`
	EvidenceRef             string `json:"evidence_ref,omitempty"`
	ObservedBindingRevision int64  `json:"observed_binding_revision"`
	OccurredAt              string `json:"occurred_at"`
}

// TouchpointDeliveryView is the management projection: the row plus the order
// facts consumed from 4591 (read-only), the step facts, and the honest stale
// signal (换绑/换域后当前绑定版本 ≠ 实测版本)。
type TouchpointDeliveryView struct {
	TouchpointDelivery
	OrderRef               string                      `json:"order_ref,omitempty"`
	Payment                billing.HardwareStep        `json:"payment"`
	Steps                  map[string]DeliveryStepFact `json:"steps"`
	CurrentBindingRevision int64                       `json:"current_binding_revision"`
	BindingStale           bool                        `json:"binding_stale"`
	LastReadback           *DeliveryStepFact           `json:"last_readback,omitempty"`
	LastReadbackMatches    bool                        `json:"last_readback_matches"`
	// LastReadbackReason: 最近一次回读判定原因(ok/host_mismatch/code_mismatch/
	// empty_read);空 = 还没有回读事实(展示面据此不说「不一致」)。
	LastReadbackReason string `json:"last_readback_reason,omitempty"`
}

// NewTouchpointDelivery is the creation input. Store/touchpoint facts derive
// from the server; the client never supplies them (同 provision 纪律)。
type NewTouchpointDelivery struct {
	TenantID        string
	TouchpointID    string
	HardwareOrderID string
	DeliveryRef     string
	PhysicalUnitRef string
	TagBatch        string
	ReplacementOf   string
	CreatedBy       string
}

func (s *Store) GetTouchpointDelivery(tenantID, id string) (TouchpointDelivery, error) {
	d, err := scanDelivery(s.DB.QueryRow(
		`SELECT `+deliveryCols+` FROM touchpoint_deliveries WHERE tenant_id=? AND id=?`, tenantID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return TouchpointDelivery{}, ErrNotFound
	}
	return d, err
}

// paymentFactDone reads the 4591 projection payment step (只读消费,不重建)。
// 投影行缺失 = 该订单尚无任何履约事实 = 未付款(不是查询错误)。
func (s *Store) paymentFactDone(tenantID, orderID string) (bool, error) {
	f, err := s.GetHardwareFulfillment(tenantID, orderID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return f.Payment.State == billing.StepDone, nil
}

// CreateTouchpointDelivery opens one physical delivery lifecycle. Gates:
// touchpoint visible in tenant (404 masks cross-tenant), order is a hardware
// order of the same tenant whose 4591 payment fact is done (付款步前置,不可
// 跳跃的第一步), the touchpoint has a store root (门店交付语义;legacy 无店卡
// 不进交付域), and no live delivery exists for the touchpoint.
func (s *Store) CreateTouchpointDelivery(in NewTouchpointDelivery) (TouchpointDelivery, error) {
	tp, err := s.GetTouchpointView(in.TouchpointID, in.TenantID)
	if err != nil {
		return TouchpointDelivery{}, err // ErrNotFound masks missing AND cross-tenant
	}
	order, err := s.GetBillingOrder(in.TenantID, in.HardwareOrderID)
	if errors.Is(err, sql.ErrNoRows) {
		return TouchpointDelivery{}, ErrNotFound // 掩码:未知与跨租户订单同形
	}
	if err != nil {
		return TouchpointDelivery{}, err
	}
	if order.Kind != billing.KindHardware {
		return TouchpointDelivery{}, ErrDeliveryBadInput
	}
	paid, err := s.paymentFactDone(in.TenantID, order.ID)
	if err != nil {
		return TouchpointDelivery{}, err
	}
	if !paid {
		return TouchpointDelivery{}, ErrPaymentRequired
	}
	if strings.TrimSpace(tp.StoreID) == "" {
		return TouchpointDelivery{}, ErrStoreMismatch // 无归属门店的卡不进交付域
	}
	if in.ReplacementOf != "" {
		if _, err := s.GetTouchpointDelivery(in.TenantID, in.ReplacementOf); err != nil {
			return TouchpointDelivery{}, err
		}
	}
	if live, err := s.hasLiveDelivery(in.TenantID, in.TouchpointID); err != nil {
		return TouchpointDelivery{}, err
	} else if live {
		return TouchpointDelivery{}, ErrLiveDeliveryExists
	}
	d := TouchpointDelivery{
		ID: newID("dlv_"), TenantID: in.TenantID, StoreID: tp.StoreID,
		TouchpointID: in.TouchpointID, HardwareOrderID: order.ID,
		DeliveryRef:     strings.TrimSpace(in.DeliveryRef),
		PhysicalUnitRef: strings.TrimSpace(in.PhysicalUnitRef),
		TagBatch:        strings.TrimSpace(in.TagBatch),
		Status:          delivery.StatusAwaitingDispatch,
		ReplacementOf:   in.ReplacementOf, CreatedBy: in.CreatedBy,
		CreatedAt: now(), UpdatedAt: now(),
	}
	err = s.withAudit(func(tx *sql.Tx) error {
		// NOT NULL DEFAULT '' 列传原始串(nullable() 的 nil 会撞 NOT NULL);
		// replacement_of 是可空外键,空值必须保持 NULL。
		_, err := tx.Exec(
			`INSERT INTO touchpoint_deliveries(`+deliveryCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			d.ID, d.TenantID, d.StoreID, d.TouchpointID, d.HardwareOrderID,
			d.DeliveryRef, d.PhysicalUnitRef, d.TagBatch,
			d.Status, d.BoundBindingRevision, d.VerifiedBindingRevision,
			d.WriteEvidenceRef, d.ReadbackEvidenceRef, d.DeviceEvidenceRef,
			nullable(d.ReplacementOf), d.CreatedBy, d.CreatedAt, d.UpdatedAt)
		if err != nil {
			if isUniqueViolation(err, "touchpoint_id") { // 部分唯一索引兜底
				return ErrLiveDeliveryExists
			}
			return err
		}
		return nil
	})
	if err != nil {
		return TouchpointDelivery{}, err
	}
	return d, nil
}

func (s *Store) hasLiveDelivery(tenantID, touchpointID string) (bool, error) {
	var n int64
	err := s.DB.QueryRow(
		`SELECT COUNT(1) FROM touchpoint_deliveries WHERE tenant_id=? AND touchpoint_id=? AND status != 'superseded'`,
		tenantID, touchpointID).Scan(&n)
	return n > 0, err
}

// DeliveryEventInput is one append-only delivery fact. OperatorRef is the
// server-resolved session principal (client-supplied operators are ignored);
// ExpectedURL is server-built from PUBLIC_BASE_URL (readback only).
type DeliveryEventInput struct {
	TenantID    string
	DeliveryID  string
	EventType   string
	Source      string
	EventID     string
	OperatorRef string
	DeviceRef   string
	EvidenceRef string
	OccurredAt  string
	ReadURL     string // readback 专用:回读抄录内容
	ExpectedURL string // readback 专用:服务端构造的期望 URL
	Note        string
}

// DeliveryEventResult reports what one accepted fact did.
type DeliveryEventResult struct {
	Delivery        TouchpointDelivery
	Replayed        bool // 同键同负载幂等读回(零推进)
	Advanced        bool // 状态推进(回读不一致时为 false)
	ReadbackMatches bool
	ReadbackReason  string
}

// deliveryEventCanonical is the dedup payload subset compared on replay
// (note/derived fields are display-only, not part of fact identity)。
type deliveryEventCanonical struct {
	EventType   string `json:"event_type"`
	DeliveryID  string `json:"delivery_id"`
	DeviceRef   string `json:"device_ref,omitempty"`
	EvidenceRef string `json:"evidence_ref,omitempty"`
	ReadURL     string `json:"read_url,omitempty"`
	OccurredAt  string `json:"occurred_at"`
}

func deliveryEventPayload(in DeliveryEventInput, expectedURL string, matches bool, reason, note string) (string, string, error) {
	canon := deliveryEventCanonical{
		EventType: in.EventType, DeliveryID: in.DeliveryID, DeviceRef: in.DeviceRef,
		EvidenceRef: in.EvidenceRef, ReadURL: strings.TrimSpace(in.ReadURL), OccurredAt: in.OccurredAt,
	}
	canonJSON, err := json.Marshal(canon)
	if err != nil {
		return "", "", err
	}
	full, err := json.Marshal(map[string]any{
		"canonical": json.RawMessage(canonJSON),
		"note":      note,
		// readback 展示字段(派生,不入判等)
		"expected_url": expectedURL,
		"matches":      matches,
		"reason":       reason,
	})
	if err != nil {
		return "", "", err
	}
	return string(full), string(canonJSON), nil
}

// ApplyTouchpointDeliveryEvent records one fact and advances the machine.
// Single transaction: ledger insert + delivery row update + 4591 projection
// push (接线:订单投影与交付事实同事务一致;重放路径零推送)。
func (s *Store) ApplyTouchpointDeliveryEvent(in DeliveryEventInput, at string) (DeliveryEventResult, error) {
	var res DeliveryEventResult
	source := strings.TrimSpace(in.Source)
	if source == "" {
		source = "merchant"
	}
	eventID := strings.TrimSpace(in.EventID)
	if eventID == "" {
		return res, ErrDeliveryBadInput
	}
	if delivery.IsReplacementEvent(in.EventType) {
		return res, ErrDeliveryBadInput // replaced 事实只由补换流程内部落账
	}
	occurredAt := strings.TrimSpace(in.OccurredAt)
	if occurredAt == "" {
		occurredAt = now()
	}
	in.OccurredAt = occurredAt
	in.Source = source
	in.EventID = eventID

	cur, err := s.GetTouchpointDelivery(in.TenantID, in.DeliveryID)
	if err != nil {
		return res, err
	}
	if !delivery.IsStepEvent(in.EventType) {
		return res, delivery.ErrUnknownStep
	}

	// 幂等闸先于封存判定:历史事实(含被封存交付上的)重放 = 幂等读回;
	// 只有「新」事实打到 superseded 交付才拒(delivery_closed)。
	if existing, ok, err := s.findDeliveryEvent(in.TenantID, source, eventID); err != nil {
		return res, err
	} else if ok {
		// 存储的是全 payload {"canonical":…,note,expected_url,matches};判等只取
		// canonical 子集(note/派生展示字段不入事实同一性)。
		var storedWrap struct {
			Canonical deliveryEventCanonical `json:"canonical"`
		}
		if json.Unmarshal([]byte(existing.Payload), &storedWrap) != nil {
			// payload 损坏视为异负载(保守拒绝,不冒充幂等)。
			return res, ErrDeliveryIdempotencyConflict
		}
		_, canonStr, err := deliveryEventPayload(in, "", false, "", "")
		if err != nil {
			return res, err
		}
		var computed deliveryEventCanonical
		if json.Unmarshal([]byte(canonStr), &computed) != nil {
			return res, ErrDeliveryIdempotencyConflict
		}
		if storedWrap.Canonical != computed {
			return res, ErrDeliveryIdempotencyConflict
		}
		res.Replayed = true
		res.Delivery = cur
		return res, nil
	}
	if cur.Status == delivery.StatusSuperseded {
		return res, delivery.ErrDeliveryClosed
	}

	// 回读一致性(readback 专用):先判,后落。
	readbackMatches, readbackReason := true, ""
	expectedURL := strings.TrimSpace(in.ExpectedURL)
	if in.EventType == delivery.StepReadback {
		tp, err := s.GetTouchpointAlias(cur.TouchpointID, in.TenantID)
		if err != nil {
			return res, err
		}
		v := delivery.CheckReadback(in.ReadURL, expectedURL, tp.Code)
		readbackMatches, readbackReason = v.Matches, v.Reason
	}

	// 状态机裁决(matches=false 的回读只记事实,不推进)。
	next := cur.Status
	advanced := false
	if readbackMatches {
		next, err = delivery.ApplyStep(cur.Status, in.EventType)
		if err != nil {
			return res, err
		}
		advanced = next != cur.Status
	}

	// 绑定确认的门店归属校验(4591 报告:绑定校验在交付域侧)。
	boundRevision, observedRevision := cur.BoundBindingRevision, cur.VerifiedBindingRevision
	if advanced && in.EventType == delivery.StepBound {
		tp, err := s.GetTouchpointView(cur.TouchpointID, in.TenantID)
		if err != nil {
			return res, err
		}
		if tp.Status != TagStatusActive {
			return res, ErrTouchpointDisabled
		}
		if tp.StoreID != cur.StoreID {
			return res, ErrStoreMismatch
		}
		boundRevision = tp.BindingRevision
		observedRevision = cur.VerifiedBindingRevision
	}
	// 回读一致/现场验收:实测版本 = 事实发生时触点当前绑定版本。
	if advanced && (in.EventType == delivery.StepReadback || in.EventType == delivery.StepAccepted) {
		tp, err := s.GetTouchpointAlias(cur.TouchpointID, in.TenantID)
		if err != nil {
			return res, err
		}
		observedRevision = tp.BindingRevision
	}

	payload, _, err := deliveryEventPayload(in, expectedURL, readbackMatches, readbackReason, in.Note)
	if err != nil {
		return res, err
	}

	err = s.withAudit(func(tx *sql.Tx) error {
		if _, err := tx.Exec(
			`INSERT INTO touchpoint_delivery_events(id,tenant_id,delivery_id,event_type,source,event_id,
			 operator_ref,device_ref,evidence_ref,observed_binding_revision,payload,occurred_at,created_at)
			 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			newID("tde_"), in.TenantID, in.DeliveryID, in.EventType, source, eventID,
			in.OperatorRef, in.DeviceRef, in.EvidenceRef, observedRevision, payload, occurredAt, at); err != nil {
			if isUniqueViolation(err, "event_id") {
				return ErrDeliveryIdempotencyConflict
			}
			return err
		}
		if advanced {
			writeEv, readbackEv, deviceEv := cur.WriteEvidenceRef, cur.ReadbackEvidenceRef, cur.DeviceEvidenceRef
			switch in.EventType {
			case delivery.StepWritten:
				writeEv = firstNonEmptyStr(in.EvidenceRef, writeEv)
			case delivery.StepReadback:
				readbackEv = firstNonEmptyStr(in.EvidenceRef, readbackEv)
			}
			deviceEv = firstNonEmptyStr(in.DeviceRef, deviceEv)
			if _, err := tx.Exec(
				`UPDATE touchpoint_deliveries SET status=?,bound_binding_revision=?,verified_binding_revision=?,
				 write_evidence_ref=?,readback_evidence_ref=?,device_evidence_ref=?,updated_at=? WHERE id=? AND tenant_id=?`,
				next, boundRevision, observedRevision, writeEv, readbackEv, deviceEv,
				at, cur.ID, in.TenantID); err != nil {
				return err
			}
			// 4591 接线:四步事实同事务推订单投影(shipment/write_card/readback/
			// acceptance);付款只读、绑定/交付本域。失败即整事务回滚——本域状态
			// 与订单投影永不分叉。
			//
			// ErrStepConflict 语义:订单投影是 per-order 粗粒度(一步一事实,先到
			// 先记);同一订单的第 N 张卡(N>1,补换/多件)推送同步时会撞既有事实。
			// per-card 细粒度事实以交付事件账本为权威(只追加),订单投影不回退、
			// 不覆盖——故此冲突只说明「订单级该步已记」,不回滚本域事实。
			if hw := delivery.HardwareStepFor(in.EventType); hw != "" {
				if err := ApplyHardwareStepInTx(tx, in.TenantID, cur.HardwareOrderID, billing.HardwareStepInput{
					Step: hw, Operator: in.OperatorRef, OccurredAt: occurredAt, DeviceRef: in.DeviceRef,
				}, at); err != nil && !errors.Is(err, billing.ErrStepConflict) {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	fresh, err := s.GetTouchpointDelivery(in.TenantID, in.DeliveryID)
	if err != nil {
		return res, err
	}
	res.Delivery = fresh
	res.Advanced = advanced
	res.ReadbackMatches = readbackMatches
	res.ReadbackReason = readbackReason
	return res, nil
}

func (s *Store) findDeliveryEvent(tenantID, source, eventID string) (TouchpointDeliveryEvent, bool, error) {
	var e TouchpointDeliveryEvent
	var operatorRef, deviceRef, evidenceRef, payload sql.NullString
	err := s.DB.QueryRow(
		`SELECT id,tenant_id,delivery_id,event_type,source,event_id,operator_ref,device_ref,evidence_ref,
		 observed_binding_revision,payload,occurred_at,created_at
		 FROM touchpoint_delivery_events WHERE tenant_id=? AND source=? AND event_id=?`,
		tenantID, source, eventID).Scan(
		&e.ID, &e.TenantID, &e.DeliveryID, &e.EventType, &e.Source, &e.EventID,
		&operatorRef, &deviceRef, &evidenceRef, &e.ObservedBindingRevision, &payload, &e.OccurredAt, &e.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return TouchpointDeliveryEvent{}, false, nil
	}
	if err != nil {
		return TouchpointDeliveryEvent{}, false, err
	}
	e.OperatorRef, e.DeviceRef, e.EvidenceRef, e.Payload = operatorRef.String, deviceRef.String, evidenceRef.String, payload.String
	return e, true, nil
}

// ListTouchpointDeliveryEvents returns the full ledger, oldest first.
func (s *Store) ListTouchpointDeliveryEvents(tenantID, deliveryID string) ([]TouchpointDeliveryEvent, error) {
	rows, err := s.DB.Query(
		`SELECT id,tenant_id,delivery_id,event_type,source,event_id,COALESCE(operator_ref,''),
		 COALESCE(device_ref,''),COALESCE(evidence_ref,''),observed_binding_revision,COALESCE(payload,''),
		 occurred_at,created_at
		 FROM touchpoint_delivery_events WHERE tenant_id=? AND delivery_id=? ORDER BY occurred_at,id`,
		tenantID, deliveryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]TouchpointDeliveryEvent, 0)
	for rows.Next() {
		var e TouchpointDeliveryEvent
		var operatorRef, deviceRef, evidenceRef, payload sql.NullString
		if err := rows.Scan(&e.ID, &e.TenantID, &e.DeliveryID, &e.EventType, &e.Source, &e.EventID,
			&operatorRef, &deviceRef, &evidenceRef, &e.ObservedBindingRevision, &payload, &e.OccurredAt, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.OperatorRef, e.DeviceRef, e.EvidenceRef, e.Payload = operatorRef.String, deviceRef.String, evidenceRef.String, payload.String
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetTouchpointDeliveryView assembles the management projection.
func (s *Store) GetTouchpointDeliveryView(tenantID, id string) (TouchpointDeliveryView, error) {
	d, err := s.GetTouchpointDelivery(tenantID, id)
	if err != nil {
		return TouchpointDeliveryView{}, err
	}
	return s.deliveryView(tenantID, d)
}

// TouchpointDeliveryFilter narrows ListTouchpointDeliveryViews.
type TouchpointDeliveryFilter struct {
	StoreID      string
	TouchpointID string
}

func (s *Store) ListTouchpointDeliveryViews(tenantID string, f TouchpointDeliveryFilter) ([]TouchpointDeliveryView, error) {
	conds, args := []string{"tenant_id=?"}, []any{tenantID}
	if f.StoreID != "" {
		conds, args = append(conds, "store_id=?"), append(args, f.StoreID)
	}
	if f.TouchpointID != "" {
		conds, args = append(conds, "touchpoint_id=?"), append(args, f.TouchpointID)
	}
	rows, err := s.DB.Query(
		`SELECT `+deliveryCols+` FROM touchpoint_deliveries WHERE `+strings.Join(conds, " AND ")+` ORDER BY created_at,id`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	deliveries := make([]TouchpointDelivery, 0)
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		deliveries = append(deliveries, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]TouchpointDeliveryView, 0, len(deliveries))
	for _, d := range deliveries {
		v, err := s.deliveryView(tenantID, d)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Store) deliveryView(tenantID string, d TouchpointDelivery) (TouchpointDeliveryView, error) {
	v := TouchpointDeliveryView{TouchpointDelivery: d, Steps: map[string]DeliveryStepFact{}}
	if order, err := s.GetBillingOrder(tenantID, d.HardwareOrderID); err == nil {
		v.OrderRef = order.OrderRef
	}
	if f, err := s.GetHardwareFulfillment(tenantID, d.HardwareOrderID); err == nil {
		v.Payment = f.Payment
	}
	evs, err := s.ListTouchpointDeliveryEvents(tenantID, d.ID)
	if err != nil {
		return TouchpointDeliveryView{}, err
	}
	var lastReadback *DeliveryStepFact
	for _, e := range evs {
		fact := DeliveryStepFact{
			EventType: e.EventType, OperatorRef: e.OperatorRef, DeviceRef: e.DeviceRef,
			EvidenceRef: e.EvidenceRef, ObservedBindingRevision: e.ObservedBindingRevision, OccurredAt: e.OccurredAt,
		}
		// 回读摘要 = 最新一次回读尝试(一致与否都算——操作者要看到最近核对了
		// 什么);steps 投影只收推进事实(matches=false 是纯事实记录,不冒充
		// 「已回读」步骤)。
		if e.EventType == delivery.StepReadback {
			f := fact
			lastReadback = &f
			v.LastReadbackMatches = deliveryMatches(e.Payload)
			v.LastReadbackReason = deliveryReadbackReason(e.Payload)
			if v.LastReadbackMatches {
				if _, exists := v.Steps[e.EventType]; !exists {
					v.Steps[e.EventType] = fact
				}
			}
			continue
		}
		if delivery.IsStepEvent(e.EventType) {
			if _, exists := v.Steps[e.EventType]; !exists {
				v.Steps[e.EventType] = fact
			}
		}
	}
	v.LastReadback = lastReadback
	if tp, err := s.GetTouchpointAlias(d.TouchpointID, tenantID); err == nil {
		v.CurrentBindingRevision = tp.BindingRevision
		if d.BoundBindingRevision > 0 && d.BoundBindingRevision != tp.BindingRevision {
			v.BindingStale = true
		}
		if d.VerifiedBindingRevision > 0 && d.VerifiedBindingRevision != tp.BindingRevision {
			v.BindingStale = true
		}
	}
	return v, nil
}

func deliveryMatches(payload string) bool {
	var p struct {
		Matches bool `json:"matches"`
	}
	_ = json.Unmarshal([]byte(payload), &p)
	return p.Matches
}

func deliveryReadbackReason(payload string) string {
	var p struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal([]byte(payload), &p)
	return p.Reason
}

// DeliveryReplacement mirrors one 0131 audit row.
type DeliveryReplacement struct {
	ID              string `json:"id"`
	TenantID        string `json:"tenant_id"`
	OldDeliveryID   string `json:"old_delivery_id"`
	NewDeliveryID   string `json:"new_delivery_id"`
	OldTouchpointID string `json:"old_touchpoint_id"`
	NewTouchpointID string `json:"new_touchpoint_id"`
	ReasonCategory  string `json:"reason_category"`
	ReasonDetail    string `json:"reason_detail,omitempty"`
	OldEntryRevoked int64  `json:"old_entry_revoked"`
	CreatedBy       string `json:"created_by,omitempty"`
	CreatedAt       string `json:"created_at"`
}

// ReplaceDeliveryResult is the replacement outcome.
type ReplaceDeliveryResult struct {
	NewDelivery TouchpointDelivery
	Audit       DeliveryReplacement
}

// ReplaceDeliveryInput carries the replacement decision. For
// damaged_recoverable the new touchpoint MUST equal the old one (换载体保留
// 逻辑入口); for lost_uncontrolled it MUST differ (撤销旧入口+新卡新码)。
type ReplaceDeliveryInput struct {
	TenantID        string
	OldDeliveryID   string
	NewTouchpointID string
	HardwareOrderID string // 空 = 继承原订单引用
	PhysicalUnitRef string
	TagBatch        string
	DeliveryRef     string
	ReasonCategory  string
	ReasonDetail    string
	CreatedBy       string
}

// ListTouchpointDeliveryReplacements returns audit rows referencing an old
// delivery, oldest first.
func (s *Store) ListTouchpointDeliveryReplacements(tenantID, oldDeliveryID string) ([]DeliveryReplacement, error) {
	rows, err := s.DB.Query(
		`SELECT id,tenant_id,old_delivery_id,new_delivery_id,old_touchpoint_id,new_touchpoint_id,
		 reason_category,COALESCE(reason_detail,''),old_entry_revoked,COALESCE(created_by,''),created_at
		 FROM touchpoint_delivery_replacements WHERE tenant_id=? AND old_delivery_id=? ORDER BY created_at,id`,
		tenantID, oldDeliveryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]DeliveryReplacement, 0)
	for rows.Next() {
		var r DeliveryReplacement
		var detail, createdBy sql.NullString
		if err := rows.Scan(&r.ID, &r.TenantID, &r.OldDeliveryID, &r.NewDeliveryID, &r.OldTouchpointID,
			&r.NewTouchpointID, &r.ReasonCategory, &detail, &r.OldEntryRevoked, &createdBy, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.ReasonDetail, r.CreatedBy = detail.String, createdBy.String
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReplaceTouchpointDelivery supersedes an old physical delivery with a new one
// and records the audit trail. The new lifecycle starts fresh at
// awaiting_dispatch (每次补换重新写入、回读、实测——结构保证,不是声明)。
//
//	lost_uncontrolled: 旧触点先停用(fail-closed:即使后续步骤失败,遗失卡也
//	不能再解析)——SetTouchpointStatus 自带公共缓存 epoch 失效。
//	参与/权益零触碰:1665 语义,参与挂活动不挂卡(结构保证快照不挪)。
func (s *Store) ReplaceTouchpointDelivery(in ReplaceDeliveryInput) (ReplaceDeliveryResult, error) {
	var res ReplaceDeliveryResult
	old, err := s.GetTouchpointDelivery(in.TenantID, in.OldDeliveryID)
	if err != nil {
		return res, err
	}
	if old.Status == delivery.StatusSuperseded {
		return res, delivery.ErrDeliveryClosed
	}
	switch in.ReasonCategory {
	case ReplaceReasonDamaged, ReplaceReasonLost:
	default:
		return res, ErrReplacementBadInput
	}
	newTpID := strings.TrimSpace(in.NewTouchpointID)
	if newTpID == "" {
		return res, ErrReplacementBadInput
	}
	switch in.ReasonCategory {
	case ReplaceReasonDamaged:
		if newTpID != old.TouchpointID {
			return res, ErrReplacementBadInput // 保留逻辑入口 = 同触点
		}
	case ReplaceReasonLost:
		if newTpID == old.TouchpointID {
			return res, ErrReplacementBadInput // 撤销旧入口 = 必须换新触点新码
		}
		tp, err := s.GetTouchpointView(newTpID, in.TenantID)
		if err != nil {
			return res, err // 404 masks missing/cross-tenant
		}
		if tp.Status != TagStatusActive {
			return res, ErrTouchpointDisabled
		}
		if tp.StoreID != old.StoreID {
			return res, ErrStoreMismatch
		}
	}
	orderID := strings.TrimSpace(in.HardwareOrderID)
	if orderID == "" {
		orderID = old.HardwareOrderID // 补换继承原订单(售后换卡)
	}
	if order, err := s.GetBillingOrder(in.TenantID, orderID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return res, ErrNotFound // 掩码:未知与跨租户订单同形
		}
		return res, err
	} else if order.Kind != billing.KindHardware {
		return res, ErrDeliveryBadInput
	}
	if paid, err := s.paymentFactDone(in.TenantID, orderID); err != nil {
		return res, err
	} else if !paid {
		return res, ErrPaymentRequired
	}

	// lost 分支:先停用旧入口(fail-closed 顺序)。
	revoked := int64(0)
	if in.ReasonCategory == ReplaceReasonLost {
		if _, err := s.SetTouchpointStatus(old.TouchpointID, in.TenantID, TagStatusDisabled, in.CreatedBy); err != nil {
			return res, err
		}
		revoked = 1
	}

	newD := TouchpointDelivery{
		ID: newID("dlv_"), TenantID: in.TenantID, StoreID: old.StoreID,
		TouchpointID: newTpID, HardwareOrderID: orderID,
		DeliveryRef:     strings.TrimSpace(in.DeliveryRef),
		PhysicalUnitRef: strings.TrimSpace(in.PhysicalUnitRef),
		TagBatch:        strings.TrimSpace(in.TagBatch),
		Status:          delivery.StatusAwaitingDispatch,
		ReplacementOf:   old.ID, CreatedBy: in.CreatedBy,
		CreatedAt: now(), UpdatedAt: now(),
	}
	audit := DeliveryReplacement{
		ID: newID("drp_"), TenantID: in.TenantID, OldDeliveryID: old.ID, NewDeliveryID: newD.ID,
		OldTouchpointID: old.TouchpointID, NewTouchpointID: newTpID,
		ReasonCategory: in.ReasonCategory, ReasonDetail: strings.TrimSpace(in.ReasonDetail),
		OldEntryRevoked: revoked, CreatedBy: in.CreatedBy, CreatedAt: now(),
	}
	err = s.withAudit(func(tx *sql.Tx) error {
		if _, err := tx.Exec(
			`UPDATE touchpoint_deliveries SET status='superseded',updated_at=? WHERE id=? AND tenant_id=?`,
			now(), old.ID, in.TenantID); err != nil {
			return err
		}
		// 插入顺序即外键依赖顺序:new 行先落,audit 的 new_delivery_id 才可引用
		//(连接级外键强制,事务内不可延迟)。
		if _, err := tx.Exec(
			`INSERT INTO touchpoint_deliveries(`+deliveryCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			newD.ID, newD.TenantID, newD.StoreID, newD.TouchpointID, newD.HardwareOrderID,
			newD.DeliveryRef, newD.PhysicalUnitRef, newD.TagBatch,
			newD.Status, newD.BoundBindingRevision, newD.VerifiedBindingRevision,
			newD.WriteEvidenceRef, newD.ReadbackEvidenceRef, newD.DeviceEvidenceRef,
			nullable(newD.ReplacementOf), newD.CreatedBy, newD.CreatedAt, newD.UpdatedAt); err != nil {
			if isUniqueViolation(err, "touchpoint_id") {
				return ErrLiveDeliveryExists
			}
			return err
		}
		// replaced 事实落在旧交付的账本(确定性幂等键,重复补换被 superseded 闸先拒)。
		if _, err := tx.Exec(
			`INSERT INTO touchpoint_delivery_events(id,tenant_id,delivery_id,event_type,source,event_id,
			 operator_ref,payload,occurred_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			newID("tde_"), in.TenantID, old.ID, "replaced", "merchant", "replace:"+old.ID,
			in.CreatedBy, auditReasonPayload(in), now(), now()); err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT INTO touchpoint_delivery_replacements(id,tenant_id,old_delivery_id,new_delivery_id,
			 old_touchpoint_id,new_touchpoint_id,reason_category,reason_detail,old_entry_revoked,created_by,created_at)
			 VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			audit.ID, audit.TenantID, audit.OldDeliveryID, audit.NewDeliveryID, audit.OldTouchpointID,
			audit.NewTouchpointID, audit.ReasonCategory, audit.ReasonDetail, audit.OldEntryRevoked,
			audit.CreatedBy, audit.CreatedAt); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	res.NewDelivery = newD
	res.Audit = audit
	return res, nil
}

func auditReasonPayload(in ReplaceDeliveryInput) string {
	b, err := json.Marshal(map[string]any{
		"reason_category": in.ReasonCategory, "reason_detail": strings.TrimSpace(in.ReasonDetail),
		"new_touchpoint_id": strings.TrimSpace(in.NewTouchpointID),
	})
	if err != nil {
		return "{}"
	}
	return string(b)
}
