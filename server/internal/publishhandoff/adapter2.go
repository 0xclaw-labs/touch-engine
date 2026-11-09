// adapter2.go: touch-handoff/v2 渠道适配器接口 —— HUI-4585/4586/4587/4588
// 四平台票按本契约实现各自 adapter(落 server/internal/publishhandoff/adapters/
// <platform>.go,经 Service.SetAdapter 注册)。
//
// 契约纪律(裁定书):
//   - 四方法 Prepare/Submit/Query/VerifyCallback;不适用的方法返回
//     ErrUnsupported(诚实缺位,不伪造成功);
//   - Prepare 产出类型化 client_action,绝不返回可执行 JS;
//   - Submit 是唯一自动投稿入口,成功后状态机走 submitting(证据=system_adapter);
//   - Query 只回查不重投(先回查纪律);查询若产生平台侧已确认的受信凭据,
//     以 Receipt 形态回流,经与回调完全相同的验签→绑定→去重管线确认;
//   - VerifyCallback 验签在先,绑定在后;fixture 签名只进测试。
//
// 本文件只定义接口与注册表,零平台逻辑;所有平台知识归各 adapter 文件。
package publishhandoff

import (
	"context"
	"errors"
	"sort"
	"sync"
)

// ErrUnsupported marks an inapplicable adapter method (诚实缺位:调用方据此
// 422,状态不推进,绝不伪造成功)。
var ErrUnsupported = errors.New("publishhandoff: capability not supported by this adapter")

// PrepareRequest carries the frozen handoff context to Prepare. MediaRefs are
// short-TTL controlled URLs (3553 media-access) the platform/adapter can pull;
// the adapter never receives file bytes or internal tokens.
type PrepareRequest struct {
	Handoff     ActionHandoff
	Plan        ActionPlan
	Text        TextInput
	Media       []MediaItem
	MediaRefs   map[string]string // media id -> controlled URL
	ShareURL    string
	SessionID   string
	Environment string
}

// PrepareResult shapes the customer-facing action for one platform.
type PrepareResult struct {
	ClientAction      ClientAction
	UnsupportedFields []string
	// Payload merges extra adapter-defined params into ClientAction.Params
	// (string-valued only — the client page renders, never executes).
	Payload map[string]string
}

// QueryOutcome is the adapter's answer for one Query. State is an
// adapter-defined platform state word (recorded verbatim). Receipt, when
// non-nil, is a platform-authoritative result minted from an authenticated
// server-to-server query; it flows through the same verify→bind→dedupe→
// confirm pipeline as callbacks (query alone never confirms).
type QueryOutcome struct {
	State   string
	Receipt *Receipt
	Detail  map[string]string
}

// CallbackPayload is the raw platform callback (headers + body). Signature
// verification is entirely the adapter's job; the pipeline only sees a
// verified Receipt.
type CallbackPayload struct {
	Platform string
	Headers  map[string]string
	Body     []byte
}

// Adapter is the per-platform integration contract (HUI-4585..4588).
type Adapter interface {
	// Platform names the served platform ("douyin"/"xiaohongshu"/...).
	Platform() string
	// Prepare builds the typed client action for one planned handoff.
	Prepare(ctx context.Context, req PrepareRequest) (PrepareResult, error)
	// Submit hands the publish request to the platform surface (automated
	// paths only). Unsupported for manual/js-handoff style platforms.
	Submit(ctx context.Context, h Handoff, p PrepareResult) error
	// Query re-checks an in-flight or unknown handoff. Never resends.
	Query(ctx context.Context, h Handoff) (QueryOutcome, error)
	// VerifyCallback verifies one raw callback into a Receipt. Implementations
	// MUST verify the source signature before trusting any field.
	VerifyCallback(ctx context.Context, raw CallbackPayload) (Receipt, error)
}

// adapterRegistry maps platform -> adapter (later registration replaces).
type adapterRegistry struct {
	mu       sync.RWMutex
	byName   map[string]Adapter
	platform []string // sorted, for honest diagnostics
}

func (r *adapterRegistry) set(a Adapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byName == nil {
		r.byName = map[string]Adapter{}
	}
	if _, existed := r.byName[a.Platform()]; !existed {
		r.platform = append(r.platform, a.Platform())
		sort.Strings(r.platform)
	}
	r.byName[a.Platform()] = a
}

func (r *adapterRegistry) get(platform string) Adapter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byName[platform]
}

// SetAdapter registers (or replaces) the adapter for a.Platform().
func (s *Service) SetAdapter(a Adapter) { s.adapters.set(a) }

// AdapterFor returns the registered adapter for a platform, or nil (nil =
// every adapter method is honestly unsupported for that platform).
func (s *Service) AdapterFor(platform string) Adapter { return s.adapters.get(platform) }
