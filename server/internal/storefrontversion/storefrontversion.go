// Package storefrontversion is the HUI-1676 storefront-version leaf domain:
// the shape of a storefront home-page version (B01), the diff summary that a
// merchant must confirm before publishing, the per-action preflight verdicts,
// and the frozen reference set a publish switches atomically.
//
// 纪律(裁定书 + 票面):
//   - 纯函数叶子包:零 SQL、零 HTTP。事实集(动作行/内容绑定/准入)由调用方
//     (HTTP 层)从既有真值只读组装,本包不 import 任何仓内包;
//   - 版本只存受权引用与冻结展示载荷:不复制目标配置/库存,不写参与/奖励表
//     (旧参与快照不随换版改写,1671 域语义);
//   - 逐动作预检失败只影响相关动作:一个动作缺配置绝不拖垮其他动作的 ready;
//     页级 fatal(结构非法)才整体拒绝;
//   - 不能把受限项标可用:发布侧以 VetoableActions 为唯一放行口径——候选版本
//     引用的动作必须全部 ready;排除不可执行动作是商家在草稿里的明确动作
//     (形成新修订),本包绝不静默删项;
//   - 变更摘要是发布确认的凭据:preflight 算 diff,Fingerprint 定指纹,publish
//     回带匹配值;草稿在确认后变过 → 指纹不齐 → 拒绝(服务端单点,不信任
//     客户端勾选框)。
package storefrontversion

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Section types. Exactly two exist for this delivery: an actions group and an
// offers (套餐/团购) group. Layout rendering itself is 1896's C1-C6.
const (
	SectionActions = "actions"
	SectionOffers  = "offers"
)

// Per-action preflight states.
const (
	StateReady         = "ready"          // 可发布
	StateRestricted    = "restricted"     // 受限:平台/授权侧问题,需处理或明确排除
	StateMissingConfig = "missing_config" // 配置缺失:修复入口在渠道动作页
)

// Preflight reason codes (machine, one or more per verdict).
const (
	ReasonUnknownAction       = "action_not_found"
	ReasonDisabled            = "action_disabled"
	ReasonRevoked             = "action_revoked"
	ReasonExpired             = "action_expired"
	ReasonTargetMissing       = "target_missing"
	ReasonTargetNotOfficial   = "target_not_official"
	ReasonContentMissing      = "content_not_adopted"
	ReasonContentBlocked      = "content_blocked"
	ReasonContentGrantExpired = "content_grant_expired"
)

// Content fact values the HTTP layer maps from the 3553 binding truth.
const (
	ContentOK           = "ok"
	ContentMissing      = "missing"       // 无已采用内容(且该动作需要内容时)
	ContentBlocked      = "blocked"       // 内容版本被停发
	ContentGrantExpired = "grant_expired" // 授权过期
)

// Caps keep one version a page, not a database.
const (
	maxSections     = 20
	maxActionsTotal = 100
	maxTitleRunes   = 60
	maxRefRunes     = 200
)

// Section is one home-page group.
type Section struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"`
	Title     string   `json:"title"`
	ActionIDs []string `json:"action_ids,omitempty"`
	OfferRefs []string `json:"offer_refs,omitempty"`
}

// ActionSnapshot is the frozen whitelist payload of one action at freeze
// time. The public projection renders FROM this (version truth); live rows
// only keep the real-time veto (revocation/expiry/disable).
type ActionSnapshot struct {
	ActionID       string `json:"action_id"`
	ActionRevision int    `json:"action_revision"`
	CatalogCode    string `json:"catalog_code"`
	Platform       string `json:"platform"`
	Kind           string `json:"kind"`
	Variant        string `json:"variant"`
	Label          string `json:"label"`
	Presentation   string `json:"presentation"`
	CapabilityRef  string `json:"capability_ref"`
	SectionID      string `json:"section_id"`
}

// Document is one storefront version's merchant-authored shape.
type Document struct {
	Sections     []Section
	Actions      []ActionSnapshot // frozen payloads (aligned with ActionRefs)
	ActionRefs   map[string]int   // action_id -> frozen action_revision
	OfferRefs    []string
	CampaignRefs []string
	BrandRef     string
}

// LiveAction is the read-only live fact set the caller assembles from the
// store's action rows (HUI-4584) at save/freeze time.
type LiveAction struct {
	ID            string
	CatalogCode   string
	Platform      string
	Kind          string
	Variant       string
	Label         string
	CapabilityRef string
	Revision      int
}

// ActionFact is the read-only world fact for one action at preflight time.
type ActionFact struct {
	ActionID       string
	Found          bool
	Enabled        bool
	Revoked        bool
	Expired        bool
	TargetOfficial bool
	// ContentState: one of ContentOK/ContentMissing/ContentBlocked/ContentGrantExpired.
	ContentState string
}

// Verdict is one action's preflight outcome. Failure here never contaminates
// a neighbour: the verdicts are strictly per action id.
type Verdict struct {
	ActionID    string   `json:"action_id"`
	SectionID   string   `json:"section_id"`
	State       string   `json:"state"`
	ReasonCodes []string `json:"reason_codes"`
}

// FatalProblems returns the page-level fatal problem codes for a document.
// Empty = structurally publishable. Structural breaks refuse the WHOLE page
// (整体拒绝), which is why they are reported separately from per-action states.
func FatalProblems(doc Document) []string {
	var problems []string
	add := func(code string) { problems = append(problems, code) }
	if len(doc.Sections) == 0 {
		add("sections_empty")
	}
	if len(doc.Sections) > maxSections {
		add("sections_too_many")
	}
	seenSection := map[string]bool{}
	seenAction := map[string]bool{}
	seenOffer := map[string]bool{}
	actionCount := 0
	for _, sec := range doc.Sections {
		if strings.TrimSpace(sec.ID) == "" {
			add("section_id_empty")
			continue
		}
		if seenSection[sec.ID] {
			add("section_id_duplicate:" + sec.ID)
			continue
		}
		seenSection[sec.ID] = true
		if sec.Type != SectionActions && sec.Type != SectionOffers {
			add("section_type_unknown:" + sec.ID)
		}
		if runeCount(sec.Title) > maxTitleRunes {
			add("section_title_too_long:" + sec.ID)
		}
		switch sec.Type {
		case SectionActions:
			if len(sec.ActionIDs) == 0 {
				add("section_actions_empty:" + sec.ID)
			}
			if len(sec.OfferRefs) > 0 {
				add("section_cross_ref:" + sec.ID)
			}
			for _, id := range sec.ActionIDs {
				if strings.TrimSpace(id) == "" {
					add("action_id_empty:" + sec.ID)
					continue
				}
				if seenAction[id] {
					add("action_id_duplicate:" + id)
					continue
				}
				seenAction[id] = true
				actionCount++
			}
		case SectionOffers:
			if len(sec.OfferRefs) == 0 {
				add("section_offers_empty:" + sec.ID)
			}
			if len(sec.ActionIDs) > 0 {
				add("section_cross_ref:" + sec.ID)
			}
			for _, ref := range sec.OfferRefs {
				if strings.TrimSpace(ref) == "" {
					add("offer_ref_empty:" + sec.ID)
					continue
				}
				if seenOffer[ref] {
					add("offer_ref_duplicate:" + ref)
					continue
				}
				seenOffer[ref] = true
			}
		}
	}
	if actionCount > maxActionsTotal {
		add("actions_too_many")
	}
	if runeCount(doc.BrandRef) > maxRefRunes {
		add("brand_ref_too_long")
	}
	return problems
}

// ValidateDocument is FatalProblems as an error.
func ValidateDocument(doc Document) error {
	problems := FatalProblems(doc)
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("storefrontversion: invalid document: %s", strings.Join(problems, ", "))
}

// DraftProblems is the save-time shape check: identical to FatalProblems but
// an EMPTY document is a legal draft (a merchant starts from blank) — only
// publish treats emptiness as page-level fatal. Everything structural
// (duplicate ids, cross refs, oversize) is still refused at save.
func DraftProblems(doc Document) []string {
	var out []string
	for _, p := range FatalProblems(doc) {
		if p == "sections_empty" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// ValidateDraft is DraftProblems as an error.
func ValidateDraft(doc Document) error {
	problems := DraftProblems(doc)
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("storefrontversion: invalid draft: %s", strings.Join(problems, ", "))
}

// BuildSnapshots freezes the per-action whitelist payloads and the
// action_revision refs from the live rows, in the order the sections declare
// them. A section referencing an id that is not among the live rows is a
// caller bug (the save path validates first) and errors here.
func BuildSnapshots(sections []Section, live []LiveAction) ([]ActionSnapshot, map[string]int, error) {
	byID := make(map[string]LiveAction, len(live))
	for _, row := range live {
		byID[row.ID] = row
	}
	snaps := make([]ActionSnapshot, 0, len(live))
	refs := make(map[string]int, len(live))
	for _, sec := range sections {
		if sec.Type != SectionActions {
			continue
		}
		for _, id := range sec.ActionIDs {
			row, ok := byID[id]
			if !ok {
				return nil, nil, fmt.Errorf("storefrontversion: unknown action ref %q", id)
			}
			snaps = append(snaps, ActionSnapshot{
				ActionID: row.ID, ActionRevision: row.Revision,
				CatalogCode: row.CatalogCode, Platform: row.Platform, Kind: row.Kind,
				Variant: row.Variant, Label: row.Label, CapabilityRef: row.CapabilityRef,
				Presentation: "link", SectionID: sec.ID,
			})
			refs[row.ID] = row.Revision
		}
	}
	return snaps, refs, nil
}

// ChangeSummary compares the currently published version (prev; zero value
// when this is the first publish) with the candidate draft (next) and returns
// a deterministic, ordered machine summary. Web renders the diff view from
// it; the server fingerprint pins it. Empty = no changes.
func ChangeSummary(prev, next Document) []string {
	var out []string
	prevSections := sectionIndex(prev.Sections)
	nextSections := sectionIndex(next.Sections)

	// Sections: removed first, then added, then per-section changes (stable order).
	for _, id := range sortedSectionIDs(prevSections, nextSections) {
		_, inPrev := prevSections[id]
		_, inNext := nextSections[id]
		switch {
		case inPrev && !inNext:
			out = append(out, "section_removed:"+id)
		case !inPrev && inNext:
			out = append(out, "section_added:"+id)
			// 新段落里的动作逐个入摘要(商家 diff 关心的是动作,不只段落壳)。
			for _, a := range nextSections[id].ActionIDs {
				out = append(out, "action_added:"+a)
			}
			if len(nextSections[id].OfferRefs) > 0 {
				out = append(out, "offers_changed:"+id)
			}
		case inPrev && inNext:
			p, n := prevSections[id], nextSections[id]
			if p.Title != n.Title {
				out = append(out, "section_renamed:"+id)
			}
			if !sameIDs(p.ActionIDs, n.ActionIDs) {
				removed, added := idDiff(p.ActionIDs, n.ActionIDs)
				for _, a := range removed {
					out = append(out, "action_removed:"+a)
				}
				for _, a := range added {
					out = append(out, "action_added:"+a)
				}
				if len(removed) == 0 && len(added) == 0 && !sameOrder(p.ActionIDs, n.ActionIDs) {
					out = append(out, "section_reordered:"+id)
				}
			}
			if !sameIDs(p.OfferRefs, n.OfferRefs) {
				out = append(out, "offers_changed:"+id)
			}
		}
	}
	// Label updates come from the frozen payloads (draft snapshot vs published).
	prevLabel := map[string]string{}
	for _, a := range prev.Actions {
		prevLabel[a.ActionID] = a.Label
	}
	for _, a := range next.Actions {
		if old, ok := prevLabel[a.ActionID]; ok && old != a.Label {
			out = append(out, "action_updated:"+a.ActionID)
		}
	}
	if prev.BrandRef != next.BrandRef {
		out = append(out, "brand_changed")
	}
	if !sameIDs(normalizeRefs(prev.OfferRefs), normalizeRefs(next.OfferRefs)) {
		out = append(out, "offer_refs_changed")
	}
	if !sameIDs(prev.CampaignRefs, next.CampaignRefs) {
		out = append(out, "campaign_refs_changed")
	}
	return out
}

// Fingerprint pins a summary: publish must carry exactly this value (确认变更
// 摘要 is server-enforced, not a client checkbox).
func Fingerprint(summary []string) string {
	b, _ := json.Marshal(summary)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Preflight evaluates the document against the assembled per-action facts.
// A structurally broken document returns page-level fatal and NO verdicts
// (整体拒绝); a structurally sound document returns strictly per-action
// verdicts — one action's failure never touches its neighbours.
func Preflight(doc Document, facts []ActionFact) ([]Verdict, []string) {
	if problems := FatalProblems(doc); len(problems) > 0 {
		return nil, problems
	}
	factByID := make(map[string]ActionFact, len(facts))
	for _, f := range facts {
		factByID[f.ActionID] = f
	}
	sectionOf := map[string]string{}
	var ids []string
	for _, sec := range doc.Sections {
		if sec.Type != SectionActions {
			continue
		}
		for _, id := range sec.ActionIDs {
			sectionOf[id] = sec.ID
			ids = append(ids, id)
		}
	}
	verdicts := make([]Verdict, 0, len(ids))
	for _, id := range ids {
		v := Verdict{ActionID: id, SectionID: sectionOf[id], State: StateReady, ReasonCodes: []string{}}
		f, seen := factByID[id]
		switch {
		case !seen || !f.Found:
			v.State = StateMissingConfig
			v.ReasonCodes = append(v.ReasonCodes, ReasonUnknownAction)
		default:
			if !f.Enabled {
				v.State = StateMissingConfig
				v.ReasonCodes = append(v.ReasonCodes, ReasonDisabled)
			}
			if f.Revoked {
				v.State = StateRestricted
				v.ReasonCodes = append(v.ReasonCodes, ReasonRevoked)
			}
			if f.Expired {
				v.State = StateRestricted
				v.ReasonCodes = append(v.ReasonCodes, ReasonExpired)
			}
			if !f.TargetOfficial {
				v.State = StateMissingConfig
				v.ReasonCodes = append(v.ReasonCodes, ReasonTargetMissing)
			}
			switch f.ContentState {
			case ContentMissing:
				v.State = StateMissingConfig
				v.ReasonCodes = append(v.ReasonCodes, ReasonContentMissing)
			case ContentBlocked:
				v.State = StateRestricted
				v.ReasonCodes = append(v.ReasonCodes, ReasonContentBlocked)
			case ContentGrantExpired:
				v.State = StateRestricted
				v.ReasonCodes = append(v.ReasonCodes, ReasonContentGrantExpired)
			}
			// Strictly per action: a neighbouring action's failure above never
			// downgrades this one — the switch set v.State only from THIS fact.
		}
		if v.State == StateReady {
			v.ReasonCodes = []string{}
		}
		verdicts = append(verdicts, v)
	}
	return verdicts, nil
}

// VetoableAction is one action that blocks a publish/rollback: the candidate
// version still references it but it is not ready right now.
type VetoableAction struct {
	ActionID    string   `json:"action_id"`
	State       string   `json:"state"`
	ReasonCodes []string `json:"reason_codes"`
}

// VetoableActions extracts the publish blockers from a verdict set. This is
// the ONLY publish gate for per-action health: 排除不可执行动作 happens in the
// draft (an explicit merchant edit, forming a new revision) — never silently
// here, and a restricted action can never be labelled 可用.
func VetoableActions(verdicts []Verdict) []VetoableAction {
	var out []VetoableAction
	for _, v := range verdicts {
		if v.State == StateReady {
			continue
		}
		out = append(out, VetoableAction{ActionID: v.ActionID, State: v.State, ReasonCodes: v.ReasonCodes})
	}
	return out
}

// ---- helpers ----------------------------------------------------------------

func runeCount(s string) int { return len([]rune(s)) }

func sectionIndex(sections []Section) map[string]Section {
	out := make(map[string]Section, len(sections))
	for _, sec := range sections {
		out[sec.ID] = sec
	}
	return out
}

func sortedSectionIDs(a, b map[string]Section) []string {
	seen := map[string]bool{}
	var ids []string
	for id := range a {
		seen[id] = true
		ids = append(ids, id)
	}
	for id := range b {
		if !seen[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func sameIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameOrder(a, b []string) bool { return sameIDs(a, b) }

func idDiff(oldIDs, newIDs []string) (removed, added []string) {
	oldSet := map[string]bool{}
	for _, id := range oldIDs {
		oldSet[id] = true
	}
	newSet := map[string]bool{}
	for _, id := range newIDs {
		newSet[id] = true
	}
	for _, id := range oldIDs {
		if !newSet[id] {
			removed = append(removed, id)
		}
	}
	for _, id := range newIDs {
		if !oldSet[id] {
			added = append(added, id)
		}
	}
	return removed, added
}

func normalizeRefs(refs []string) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, strings.TrimSpace(r))
	}
	return out
}
