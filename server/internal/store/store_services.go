package store

// HUI-4589 B07 门店连接域：门店服务配置（Wi-Fi / 加微信 / 公众号·社群二维码引用）。
//
// 纪律（4584 store_actions 同风格）：
//   - service_id 是门店范围内随机的不透明实例 ID（sv_<24hex>），不用自增暴露。
//   - 同店同 kind+variant 可并存多条（个人微信与企微、公众号与社群互不覆盖）。
//   - PUT 是批量原子替换 + config_revision 乐观锁：旧版本零写入；负载内重复
//     service_id 明确拒绝；内容完全一致的重放是幂等（不推高修订、不写快照）。
//   - 每次真实变更写一条不可变 JSON 快照。
//   - config 按 kind 在模型层校验：Wi-Fi 密码是商家主动公示的连接凭据（顾客侧
//     仅展示+主动复制）；企微必须是 work.weixin.qq.com 官方「联系我」链接；
//     外链/二维码一律公网 https。审批中状态原样透传展示，绝不升级为可承诺。
//   - 事件只存 open/jump_attempt/copy 行为证据；成功语义枚举（已关注/已连接/
//     已添加/已点评）在 CHECK 与模型层双重不存在，结构上写不进去。

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/extrajump"
)

// ServiceKind is the service category (B07 连接域三类).
const (
	ServiceKindWifi      = "wifi"
	ServiceKindContact   = "contact"
	ServiceKindCommunity = "community"
)

// ServiceEvent kinds: 顾客行为证据枚举（CHECK 与此处双重锁定）。
const (
	ServiceEventOpen        = "open"
	ServiceEventJumpAttempt = "jump_attempt"
	ServiceEventCopy        = "copy"
)

// WeCom callback honest states（wecom_callback_log.decrypt_state）.
const (
	WecomCallbackPendingQualification = "pending_qualification"
	WecomCallbackSignatureMismatch    = "signature_mismatch"
	WecomCallbackDecryptFailed        = "decrypt_failed"
	WecomCallbackVerified             = "verified"
)

// Replace-level and validation failures. The HTTP layer maps them onto codes.
// ErrStaleRevision is shared with store_actions (same optimistic-lock refusal).
var (
	ErrDuplicateServiceID = errors.New("store: duplicate service id in payload")
	ErrUnknownServiceID   = errors.New("store: service id does not exist in this store")
	ErrBadServiceConfig   = errors.New("store: service config rejected")
	ErrUnknownService     = errors.New("store: unknown service for event")
)

var errBadConfig = func(field, reason string) error {
	return fmt.Errorf("%w: %s %s", ErrBadServiceConfig, field, reason)
}

// wifi encryption enum（不含已被淘汰的 wep；open 无密码）。
var wifiEncryptions = map[string]bool{
	"open": true, "wpa": true, "wpa2": true, "wpa2_wpa3": true, "wpa3": true,
}

// wechatIDPattern: 微信号形状（字母开头 6-20 位，字母数字下划线短横线）。
var wechatIDPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{5,19}$`)

// StoreService is one store service config instance.
type StoreService struct {
	ID          string         `json:"service_id"`
	TenantID    string         `json:"-"`
	StoreID     string         `json:"-"`
	CatalogCode string         `json:"catalog_code"`
	Kind        string         `json:"kind"`
	Variant     string         `json:"variant"`
	Label       string         `json:"label"`
	ConfigJSON  map[string]any `json:"config"`
	SortOrder   int            `json:"order"`
	Enabled     bool           `json:"enabled"`
	Revision    int            `json:"revision"`
	CreatedAt   string         `json:"-"`
	UpdatedAt   string         `json:"-"`
}

// StoreServiceInput is one desired item of a ReplaceStoreServices call.
type StoreServiceInput struct {
	ID          string
	CatalogCode string
	Kind        string
	Variant     string
	Label       string
	Config      map[string]any
	Order       int
	Enabled     bool
}

// ValidateStoreServiceConfig checks one config payload by kind. The Wi-Fi
// password is a merchant-published connection credential (display+copy by
// design); wecom URLs must be official work.weixin.qq.com 联系我 links; every
// external URL must be public https.
func ValidateStoreServiceConfig(kind, variant string, enabled bool, cfg map[string]any) error {
	if cfg == nil {
		cfg = map[string]any{}
	}
	str := func(key string) string {
		v, _ := cfg[key].(string)
		return strings.TrimSpace(v)
	}
	switch kind {
	case ServiceKindWifi:
		if len(cfg) == 0 && !enabled {
			return nil // 停用行允许留空待补
		}
		ssid := str("ssid")
		if ssid == "" || len(ssid) > 64 {
			return errBadConfig("ssid", "必填且不超过 64 字符")
		}
		encryption := str("encryption")
		if !wifiEncryptions[encryption] {
			return errBadConfig("encryption", "必须是 open|wpa|wpa2|wpa2_wpa3|wpa3")
		}
		password := str("password")
		if encryption == "open" {
			if password != "" {
				return errBadConfig("password", "开放网络不留密码")
			}
			return nil
		}
		if len(password) < 8 || len(password) > 64 {
			return errBadConfig("password", "非开放网络需要 8-64 位密码（仅展示+主动复制）")
		}
		return nil
	case ServiceKindContact:
		if len(cfg) == 0 && !enabled {
			return nil
		}
		switch variant {
		case "wecom":
			url := str("url")
			if url == "" {
				return errBadConfig("url", "企微需要官方「联系我」链接")
			}
			if !isOfficialWecomContactURL(url) {
				return errBadConfig("url", "必须是 work.weixin.qq.com 官方「联系我」链接（带可关联来源）")
			}
			return nil
		case "personal", "":
			id := str("wechat_id")
			qr := str("qr_ref")
			if id == "" && qr == "" {
				return errBadConfig("contact", "个人微信需要微信号或二维码引用其一")
			}
			if id != "" && !wechatIDPattern.MatchString(id) {
				return errBadConfig("wechat_id", "微信号形状不正确（字母开头 6-20 位）")
			}
			if qr != "" && !officialHTTPS(qr) {
				return errBadConfig("qr_ref", "二维码引用必须是公网 https 地址")
			}
			return nil
		default:
			return errBadConfig("variant", "必须是 personal|wecom")
		}
	case ServiceKindCommunity:
		if len(cfg) == 0 && !enabled {
			return nil
		}
		group := str("group_no")
		invite := str("invite_url")
		qr := str("qr_ref")
		if group == "" && invite == "" && qr == "" {
			return errBadConfig("community", "群号/邀请链接/二维码引用至少其一")
		}
		if invite != "" && !officialHTTPS(invite) {
			return errBadConfig("invite_url", "邀请链接必须是公网 https 地址")
		}
		if qr != "" && !officialHTTPS(qr) {
			return errBadConfig("qr_ref", "二维码引用必须是公网 https 地址")
		}
		return nil
	default:
		return errBadConfig("kind", "必须是 wifi|contact|community")
	}
}

// isOfficialWecomContactURL: 企微官方「联系我」链接域校验（带可关联来源的
// work.weixin.qq.com/ca/… 形态；来源参数由商家链接自带，不伪造）。
func isOfficialWecomContactURL(raw string) bool {
	if !officialHTTPS(raw) {
		return false
	}
	return strings.Contains(strings.ToLower(raw), "work.weixin.qq.com")
}

// officialHTTPS accepts only public https URLs (extrajump.OfficialAddress
// 单点复用——语义与本仓其他外链门完全一致).
func officialHTTPS(raw string) bool {
	return extrajump.OfficialAddress(raw)
}

// ListStoreServices returns the store's service instances with the current
// config_revision.
func (s *Store) ListStoreServices(tenantID, storeID string) ([]StoreService, int, error) {
	rev, _, err := s.serviceConfigRevision(storeID)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.listServiceRows(tenantID, storeID)
	if err != nil {
		return nil, 0, err
	}
	return rows, rev, nil
}

func (s *Store) listServiceRows(tenantID, storeID string) ([]StoreService, error) {
	rs, err := s.DB.Query(`SELECT id,catalog_code,kind,variant,label,config,sort_order,enabled,revision,created_at,updated_at
		FROM store_services WHERE tenant_id=? AND store_id=? ORDER BY sort_order, created_at, id`, tenantID, storeID)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []StoreService
	for rs.Next() {
		item, scanErr := scanStoreService(rs)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rs.Err()
}

func scanStoreService(rs rowScanner) (StoreService, error) {
	var item StoreService
	var raw string
	var enabled int
	if err := rs.Scan(&item.ID, &item.CatalogCode, &item.Kind, &item.Variant, &item.Label, &raw,
		&item.SortOrder, &enabled, &item.Revision, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return StoreService{}, err
	}
	item.Enabled = enabled == 1
	item.ConfigJSON = map[string]any{}
	if raw != "" && raw != "{}" {
		if err := json.Unmarshal([]byte(raw), &item.ConfigJSON); err != nil {
			return StoreService{}, fmt.Errorf("store: service %s config corrupt: %w", item.ID, err)
		}
	}
	return item, nil
}

func (s *Store) serviceConfigRevision(storeID string) (int, bool, error) {
	var rev int
	err := s.DB.QueryRow(`SELECT config_revision FROM store_service_configs WHERE store_id=?`, storeID).Scan(&rev)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return rev, true, nil
}

// ReplaceStoreServices applies the full desired item set atomically. Semantics
// mirror ReplaceStoreActions: expectedRevision optimistic lock, duplicate id
// refusal, unknown id refusal, idempotent replay, snapshot on real change.
func (s *Store) ReplaceStoreServices(tenantID, storeID string, expectedRevision int, inputs []StoreServiceInput) (int, []StoreService, error) {
	if _, err := s.GetStore(storeID, tenantID); err != nil {
		return 0, nil, err
	}
	seen := map[string]bool{}
	for _, in := range inputs {
		if in.ID == "" {
			continue
		}
		if seen[in.ID] {
			return 0, nil, ErrDuplicateServiceID
		}
		seen[in.ID] = true
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback()

	var current int
	if err := tx.QueryRow(`SELECT config_revision FROM store_service_configs WHERE store_id=?`, storeID).Scan(&current); errors.Is(err, sql.ErrNoRows) {
		current = 0
	} else if err != nil {
		return 0, nil, err
	}
	if expectedRevision != current {
		return 0, nil, ErrStaleRevision
	}
	existing, err := loadServiceRowsInTx(tx, tenantID, storeID)
	if err != nil {
		return 0, nil, err
	}

	desired := make([]StoreService, 0, len(inputs))
	for _, in := range inputs {
		kind := strings.TrimSpace(in.Kind)
		variant := strings.TrimSpace(in.Variant)
		code := strings.TrimSpace(in.CatalogCode)
		if code != "" && !validServiceCatalogCode(code) {
			return 0, nil, errBadConfig("catalog_code", "必须是 A01-A30 或空")
		}
		if err := ValidateStoreServiceConfig(kind, variant, in.Enabled, in.Config); err != nil {
			return 0, nil, err
		}
		if in.Config == nil {
			in.Config = map[string]any{}
		}
		// open 网络的密码规范化为显式空串（存进 config 的是展示语义字段，
		// 不留含糊的缺键）。
		if kind == ServiceKindWifi {
			if enc, _ := in.Config["encryption"].(string); enc == "open" {
				in.Config["password"] = ""
			}
		}
		item := StoreService{
			TenantID: tenantID, StoreID: storeID, CatalogCode: code,
			Kind: kind, Variant: variant,
			Label:      strings.TrimSpace(in.Label),
			ConfigJSON: in.Config,
			SortOrder:  in.Order,
			Enabled:    in.Enabled,
			Revision:   1,
		}
		if in.ID != "" {
			prev, ok := serviceByID(existing, in.ID)
			if !ok {
				return 0, nil, fmt.Errorf("%w: %s", ErrUnknownServiceID, in.ID)
			}
			item.ID = prev.ID
			item.CreatedAt = prev.CreatedAt
			item.Revision = prev.Revision
			if !sameServiceContent(prev, item) {
				item.Revision = prev.Revision + 1
			}
		} else {
			item.ID = "sv_" + randHex(12)
			item.CreatedAt = now()
		}
		item.UpdatedAt = now()
		desired = append(desired, item)
	}

	if sameServiceSet(existing, desired) {
		return current, existing, nil
	}

	keep := map[string]bool{}
	for _, item := range desired {
		keep[item.ID] = true
	}
	for _, prev := range existing {
		if keep[prev.ID] {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM store_services WHERE id=? AND tenant_id=? AND store_id=?`,
			prev.ID, tenantID, storeID); err != nil {
			return 0, nil, err
		}
	}
	for _, item := range desired {
		cfg, err := json.Marshal(item.ConfigJSON)
		if err != nil {
			return 0, nil, err
		}
		if _, err := tx.Exec(`INSERT INTO store_services(
				id,tenant_id,store_id,catalog_code,kind,variant,label,config,sort_order,enabled,revision,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET
				catalog_code=excluded.catalog_code, kind=excluded.kind, variant=excluded.variant,
				label=excluded.label, config=excluded.config, sort_order=excluded.sort_order,
				enabled=excluded.enabled, revision=excluded.revision, updated_at=excluded.updated_at`,
			item.ID, item.TenantID, item.StoreID, item.CatalogCode, item.Kind, item.Variant,
			item.Label, string(cfg), item.SortOrder, boolInt(item.Enabled), item.Revision,
			item.CreatedAt, item.UpdatedAt); err != nil {
			return 0, nil, err
		}
	}

	nextRev := current + 1
	if _, err := tx.Exec(`INSERT INTO store_service_configs(store_id,config_revision,updated_at) VALUES(?,?,?)
		ON CONFLICT(store_id) DO UPDATE SET config_revision=excluded.config_revision, updated_at=excluded.updated_at`,
		storeID, nextRev, now()); err != nil {
		return 0, nil, err
	}
	payload := make([]map[string]any, 0, len(desired))
	for _, item := range desired {
		payload = append(payload, map[string]any{
			"service_id": item.ID, "catalog_code": item.CatalogCode, "kind": item.Kind,
			"variant": item.Variant, "label": item.Label, "config": item.ConfigJSON,
			"order": item.SortOrder, "enabled": item.Enabled, "revision": item.Revision,
		})
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}
	if _, err := tx.Exec(`INSERT INTO store_service_snapshots(id,store_id,config_revision,payload,created_at)
		VALUES(?,?,?,?,?)`, "svs_"+randHex(12), storeID, nextRev, string(raw), now()); err != nil {
		return 0, nil, err
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, err
	}
	s.bumpEpoch()
	return nextRev, desired, nil
}

// StoreServiceScope resolves the trusted tenant+store that own a service id.
// The id alone is NOT an authorization: callers must still authorize scope.
func (s *Store) StoreServiceScope(serviceID string) (string, string, error) {
	var tenantID, storeID string
	err := s.DB.QueryRow(`SELECT tenant_id,store_id FROM store_services WHERE id=?`, serviceID).
		Scan(&tenantID, &storeID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	return tenantID, storeID, nil
}

// GetStoreServiceByID resolves one service strictly within tenant+store.
func (s *Store) GetStoreServiceByID(tenantID, storeID, serviceID string) (StoreService, error) {
	rows, err := s.listServiceRows(tenantID, storeID)
	if err != nil {
		return StoreService{}, err
	}
	for _, item := range rows {
		if item.ID == serviceID {
			return item, nil
		}
	}
	return StoreService{}, ErrNotFound
}

// ListPublicStoreServices is the public projection source: only enabled rows
// whose stored config still passes validation. Anything else stays invisible
// (fail-closed — a merchant may have hand-edited history).
func (s *Store) ListPublicStoreServices(storeID string) ([]StoreService, error) {
	rs, err := s.DB.Query(`SELECT id,catalog_code,kind,variant,label,config,sort_order,enabled,revision,created_at,updated_at
		FROM store_services WHERE store_id=? ORDER BY sort_order, created_at, id`, storeID)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []StoreService
	for rs.Next() {
		item, scanErr := scanStoreService(rs)
		if scanErr != nil {
			return nil, scanErr
		}
		if !item.Enabled || item.Kind == "" {
			continue
		}
		if err := ValidateStoreServiceConfig(item.Kind, item.Variant, item.Enabled, item.ConfigJSON); err != nil {
			continue
		}
		out = append(out, item)
	}
	return out, rs.Err()
}

// RecordServiceEvent stores one customer behavior evidence row. Only the three
// behavior kinds exist; there is no success semantic anywhere in this table.
func (s *Store) RecordServiceEvent(tenantID, storeID, serviceID, eventKind, environment string) error {
	switch eventKind {
	case ServiceEventOpen:
	case ServiceEventJumpAttempt:
	case ServiceEventCopy:
	default:
		return fmt.Errorf("%w: event kind %q has no evidence tier", ErrBadServiceConfig, eventKind)
	}
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(1) FROM store_services WHERE id=? AND tenant_id=? AND store_id=? AND enabled=1`,
		serviceID, tenantID, storeID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrUnknownService
	}
	tier := "click"
	if eventKind == ServiceEventJumpAttempt {
		tier = "jump_attempt"
	}
	_, err := s.DB.Exec(`INSERT INTO store_service_events(id,tenant_id,store_id,service_id,event_kind,environment,evidence_tier,occurred_at)
		VALUES(?,?,?,?,?,?,?,?)`, "sse_"+randHex(12), tenantID, storeID, serviceID, eventKind,
		strings.TrimSpace(environment), tier, now())
	if err == nil {
		s.bumpEpoch()
	}
	return err
}

// RecordActionEvent stores one customer behavior evidence row for a store
// action instance (sa_*，点评/关注/加群面板). Same enum discipline as
// RecordServiceEvent: only behavior evidence, no success semantics anywhere.
func (s *Store) RecordActionEvent(tenantID, storeID, actionID, eventKind, environment string) error {
	switch eventKind {
	case ServiceEventOpen:
	case ServiceEventJumpAttempt:
	case ServiceEventCopy:
	default:
		return fmt.Errorf("%w: event kind %q has no evidence tier", ErrBadServiceConfig, eventKind)
	}
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(1) FROM store_actions WHERE id=? AND tenant_id=? AND store_id=? AND enabled=1`,
		actionID, tenantID, storeID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrUnknownService
	}
	tier := "click"
	if eventKind == ServiceEventJumpAttempt {
		tier = "jump_attempt"
	}
	_, err := s.DB.Exec(`INSERT INTO store_service_events(id,tenant_id,store_id,service_id,event_kind,environment,evidence_tier,occurred_at)
		VALUES(?,?,?,?,?,?,?,?)`, "sse_"+randHex(12), tenantID, storeID, actionID, eventKind,
		strings.TrimSpace(environment), tier, now())
	if err == nil {
		s.bumpEpoch()
	}
	return err
}

// CountServiceEvents is a small readback used by tests and merchant health.
func (s *Store) CountServiceEvents(tenantID, storeID, serviceID string) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(1) FROM store_service_events WHERE tenant_id=? AND store_id=? AND service_id=?`,
		tenantID, storeID, serviceID).Scan(&n)
	return n, err
}

// RecordWecomCallback stores one callback evidence row (digest-only, honest
// state). It NEVER carries or implies a customer-added success.
func (s *Store) RecordWecomCallback(decryptState, msgSignature string, signatureValid bool, payloadDigest, note string) error {
	switch decryptState {
	case WecomCallbackPendingQualification, WecomCallbackSignatureMismatch, WecomCallbackDecryptFailed, WecomCallbackVerified:
	default:
		return fmt.Errorf("%w: callback state %q has no honest meaning", ErrBadServiceConfig, decryptState)
	}
	_, err := s.DB.Exec(`INSERT INTO wecom_callback_log(id,occurred_at,msg_signature,signature_valid,decrypt_state,payload_digest,note)
		VALUES(?,?,?,?,?,?,?)`, "wcl_"+randHex(12), now(), msgSignature, boolInt(signatureValid),
		decryptState, payloadDigest, strings.TrimSpace(note))
	return err
}

// CountWecomCallbacks reads back the log size (tests/health).
func (s *Store) CountWecomCallbacks() (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(1) FROM wecom_callback_log`).Scan(&n)
	return n, err
}

func loadServiceRowsInTx(tx *sql.Tx, tenantID, storeID string) ([]StoreService, error) {
	rs, err := tx.Query(`SELECT id,catalog_code,kind,variant,label,config,sort_order,enabled,revision,created_at,updated_at
		FROM store_services WHERE tenant_id=? AND store_id=? ORDER BY sort_order, created_at, id`, tenantID, storeID)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []StoreService
	for rs.Next() {
		item, scanErr := scanStoreService(rs)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rs.Err()
}

func serviceByID(items []StoreService, id string) (StoreService, bool) {
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return StoreService{}, false
}

// sameServiceContent compares the merchant-editable surface.
func sameServiceContent(a, b StoreService) bool {
	return a.CatalogCode == b.CatalogCode && a.Kind == b.Kind && a.Variant == b.Variant &&
		a.Label == b.Label && configEqual(a.ConfigJSON, b.ConfigJSON) &&
		a.SortOrder == b.SortOrder && a.Enabled == b.Enabled
}

func configEqual(a, b map[string]any) bool {
	ab, aerr := json.Marshal(a)
	bb, berr := json.Marshal(b)
	if aerr != nil || berr != nil {
		return false
	}
	return string(ab) == string(bb)
}

func sameServiceSet(existing, desired []StoreService) bool {
	if len(existing) != len(desired) {
		return false
	}
	byID := map[string]StoreService{}
	for _, item := range existing {
		byID[item.ID] = item
	}
	for _, item := range desired {
		prev, ok := byID[item.ID]
		if !ok || !sameServiceContent(prev, item) || prev.Revision != item.Revision {
			return false
		}
	}
	return true
}

func validServiceCatalogCode(code string) bool {
	if len(code) != 3 || code[0] != 'A' {
		return false
	}
	return code[1] >= '0' && code[1] <= '9' && code[2] >= '0' && code[2] <= '9'
}
