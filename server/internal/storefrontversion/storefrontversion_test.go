package storefrontversion

import (
	"strings"
	"testing"
)

// doc helpers ---------------------------------------------------------------

func sampleDoc() Document {
	return Document{
		Sections: []Section{
			{ID: "s1", Type: SectionActions, Title: "常用服务", ActionIDs: []string{"sa_a", "sa_b"}},
			{ID: "s2", Type: SectionOffers, Title: "到店套餐", OfferRefs: []string{"off_1"}},
		},
		OfferRefs:    []string{"off_1"},
		CampaignRefs: []string{"camp_1"},
		BrandRef:     "brand_9",
	}
}

// ValidateDocument / FatalProblems ------------------------------------------

func TestValidateDocumentAcceptsWellFormed(t *testing.T) {
	if err := ValidateDocument(sampleDoc()); err != nil {
		t.Fatalf("well-formed doc rejected: %v", err)
	}
	if got := FatalProblems(sampleDoc()); len(got) != 0 {
		t.Fatalf("fatal problems on well-formed doc: %v", got)
	}
}

func TestValidateDocumentRejectsStructuralBreaks(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Document)
	}{
		{"empty section id", func(d *Document) { d.Sections[0].ID = "" }},
		{"duplicate section id", func(d *Document) { d.Sections[1].ID = "s1" }},
		{"unknown section type", func(d *Document) { d.Sections[0].Type = "banner" }},
		{"actions section without ids", func(d *Document) { d.Sections[0].ActionIDs = nil }},
		{"empty action id", func(d *Document) { d.Sections[0].ActionIDs = []string{"sa_a", " "} }},
		{"action id in offers section", func(d *Document) { d.Sections[1].ActionIDs = []string{"sa_a"} }},
		{"offer ref in actions section", func(d *Document) { d.Sections[0].OfferRefs = []string{"off_1"} }},
		{"offers section without refs", func(d *Document) { d.Sections[1].OfferRefs = nil }},
		{"duplicate action id across sections", func(d *Document) {
			d.Sections[1].Type = SectionActions
			d.Sections[1].ActionIDs = []string{"sa_a"}
			d.Sections[1].OfferRefs = nil
		}},
		{"duplicate offer ref across sections", func(d *Document) {
			d.Sections[0].Type = SectionOffers
			d.Sections[0].ActionIDs = nil
			d.Sections[0].OfferRefs = []string{"off_1"}
		}},
		{"no sections at all", func(d *Document) { d.Sections = nil }},
		{"oversize section title", func(d *Document) { d.Sections[0].Title = strings.Repeat("长", 61) }},
		{"oversize brand ref", func(d *Document) { d.BrandRef = strings.Repeat("b", 201) }},
		{"too many actions", func(d *Document) {
			ids := make([]string, 101)
			for i := range ids {
				ids[i] = "sa_x" + string(rune('a'+i%26)) + string(rune('0'+i/26))
			}
			d.Sections[0].ActionIDs = ids
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := sampleDoc()
			tc.mut(&doc)
			if err := ValidateDocument(doc); err == nil {
				t.Fatalf("expected rejection, got nil")
			}
			if len(FatalProblems(doc)) == 0 {
				t.Fatalf("expected fatal problems, got none")
			}
		})
	}
}

// BuildSnapshots / refs freeze ----------------------------------------------

func liveRows() []LiveAction {
	return []LiveAction{
		{ID: "sa_a", CatalogCode: "A01", Platform: "douyin", Kind: "publish", Variant: "",
			Label: "抖音发布", Revision: 3},
		{ID: "sa_b", CatalogCode: "A19", Platform: "meituan", Kind: "group_buy", Variant: "",
			Label: "美团团购", Revision: 1},
	}
}

func TestBuildSnapshotsFreezesRefsFromLiveRows(t *testing.T) {
	doc := sampleDoc()
	snaps, refs, err := BuildSnapshots(doc.Sections, liveRows())
	if err != nil {
		t.Fatalf("build snapshots: %v", err)
	}
	if refs["sa_a"] != 3 || refs["sa_b"] != 1 {
		t.Fatalf("refs = %v, want sa_a:3 sa_b:1", refs)
	}
	if len(snaps) != 2 {
		t.Fatalf("snapshots = %d, want 2", len(snaps))
	}
	byID := map[string]ActionSnapshot{}
	for _, s := range snaps {
		byID[s.ActionID] = s
	}
	if byID["sa_a"].CatalogCode != "A01" || byID["sa_a"].SectionID != "s1" {
		t.Fatalf("snapshot sa_a wrong: %+v", byID["sa_a"])
	}
	if byID["sa_b"].Kind != "group_buy" || byID["sa_b"].SectionID != "s1" {
		t.Fatalf("snapshot sa_b wrong: %+v", byID["sa_b"])
	}
}

func TestBuildSnapshotsRejectsUnknownActionRef(t *testing.T) {
	doc := sampleDoc()
	doc.Sections[0].ActionIDs = append(doc.Sections[0].ActionIDs, "sa_missing")
	if _, _, err := BuildSnapshots(doc.Sections, liveRows()); err == nil {
		t.Fatalf("unknown action ref must be rejected at freeze time")
	}
}

// ChangeSummary / fingerprint -----------------------------------------------

func TestChangeSummaryEmptyForIdenticalDocs(t *testing.T) {
	doc := sampleDoc()
	if got := ChangeSummary(doc, doc); len(got) != 0 {
		t.Fatalf("identical docs must produce empty summary, got %v", got)
	}
}

func TestChangeSummaryListsDiffKinds(t *testing.T) {
	prev := sampleDoc()
	next := sampleDoc()
	next.Sections[0].ActionIDs = []string{"sa_a"} // sa_b removed
	next.Sections = append(next.Sections, Section{ID: "s3", Type: SectionActions, Title: "关注我们", ActionIDs: []string{"sa_c"}})
	next.BrandRef = "brand_new"
	got := ChangeSummary(prev, next)
	joined := strings.Join(got, "|")
	for _, want := range []string{"action_removed:sa_b", "section_added:s3", "action_added:sa_c", "brand_changed"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("summary missing %q: %v", want, got)
		}
	}
}

func TestChangeSummaryDetectsLabelUpdateAndReorder(t *testing.T) {
	prev := sampleDoc()
	next := sampleDoc()
	next.Sections[0].ActionIDs = []string{"sa_b", "sa_a"} // reordered
	next.Actions = []ActionSnapshot{
		{ActionID: "sa_a", Label: "抖音发布"},
		{ActionID: "sa_b", Label: "团购改价"},
	}
	prev.Actions = []ActionSnapshot{
		{ActionID: "sa_a", Label: "抖音发布"},
		{ActionID: "sa_b", Label: "美团团购"},
	}
	got := ChangeSummary(prev, next)
	joined := strings.Join(got, "|")
	if !strings.Contains(joined, "action_updated:sa_b") {
		t.Fatalf("label change missing: %v", got)
	}
	if !strings.Contains(joined, "section_reordered:s1") {
		t.Fatalf("reorder missing: %v", got)
	}
}

func TestFingerprintStableAndDistinct(t *testing.T) {
	a := Fingerprint([]string{"section_added:s3"})
	b := Fingerprint([]string{"section_added:s3"})
	c := Fingerprint([]string{"section_added:s4"})
	if a == "" || a != b {
		t.Fatalf("fingerprint unstable: %q vs %q", a, b)
	}
	if a == c {
		t.Fatalf("different summaries share fingerprint")
	}
}

// Preflight ------------------------------------------------------------------

func TestPreflightReadyAndMissingConfigSplit(t *testing.T) {
	doc := sampleDoc()
	facts := []ActionFact{
		{ActionID: "sa_a", Found: true, Enabled: true, TargetOfficial: true, ContentState: ContentOK},
		{ActionID: "sa_b", Found: true, Enabled: true, TargetOfficial: true, ContentState: ContentOK},
		{ActionID: "sa_c", Found: false},
	}
	doc.Sections = append(doc.Sections, Section{ID: "s3", Type: SectionActions, Title: "备用", ActionIDs: []string{"sa_c"}})
	verdicts, fatal := Preflight(doc, facts)
	if len(fatal) != 0 {
		t.Fatalf("unexpected fatal: %v", fatal)
	}
	byID := map[string]Verdict{}
	for _, v := range verdicts {
		byID[v.ActionID] = v
	}
	if byID["sa_a"].State != StateReady || byID["sa_b"].State != StateReady {
		t.Fatalf("ready actions misjudged: %+v", verdicts)
	}
	if byID["sa_c"].State != StateMissingConfig || !contains(byID["sa_c"].ReasonCodes, ReasonUnknownAction) {
		t.Fatalf("missing action misjudged: %+v", byID["sa_c"])
	}
}

func TestPreflightMissingConfigOnlyAffectsItsAction(t *testing.T) {
	doc := sampleDoc()
	// sa_a loses its content adoption; sa_b keeps everything.
	facts := []ActionFact{
		{ActionID: "sa_a", Found: true, Enabled: true, TargetOfficial: true, ContentState: ContentMissing},
		{ActionID: "sa_b", Found: true, Enabled: true, TargetOfficial: true, ContentState: ContentOK},
	}
	verdicts, _ := Preflight(doc, facts)
	byID := map[string]Verdict{}
	for _, v := range verdicts {
		byID[v.ActionID] = v
	}
	if byID["sa_a"].State != StateMissingConfig || !contains(byID["sa_a"].ReasonCodes, ReasonContentMissing) {
		t.Fatalf("content-missing action misjudged: %+v", byID["sa_a"])
	}
	if byID["sa_b"].State != StateReady {
		t.Fatalf("neighbouring action contaminated: %+v", byID["sa_b"])
	}
}

func TestPreflightRestrictedReasons(t *testing.T) {
	doc := sampleDoc()
	facts := []ActionFact{
		{ActionID: "sa_a", Found: true, Enabled: true, Revoked: true, TargetOfficial: true, ContentState: ContentOK},
		{ActionID: "sa_b", Found: true, Enabled: true, Expired: true, TargetOfficial: true, ContentState: ContentBlocked},
	}
	verdicts, _ := Preflight(doc, facts)
	byID := map[string]Verdict{}
	for _, v := range verdicts {
		byID[v.ActionID] = v
	}
	if byID["sa_a"].State != StateRestricted || !contains(byID["sa_a"].ReasonCodes, ReasonRevoked) {
		t.Fatalf("revoked action misjudged: %+v", byID["sa_a"])
	}
	if byID["sa_b"].State != StateRestricted {
		t.Fatalf("expired+blocked action must be restricted: %+v", byID["sa_b"])
	}
	if !contains(byID["sa_b"].ReasonCodes, ReasonExpired) || !contains(byID["sa_b"].ReasonCodes, ReasonContentBlocked) {
		t.Fatalf("reason codes incomplete: %+v", byID["sa_b"].ReasonCodes)
	}
}

func TestPreflightFatalForStructuralBreak(t *testing.T) {
	doc := sampleDoc()
	doc.Sections[1].ID = "s1" // duplicate section id
	verdicts, fatal := Preflight(doc, nil)
	if len(fatal) == 0 {
		t.Fatalf("structural break must produce page-level fatal")
	}
	if len(verdicts) != 0 {
		t.Fatalf("fatal doc must not produce per-action verdicts, got %v", verdicts)
	}
}

func TestPreflightVetoableDetectsPublishBlockers(t *testing.T) {
	verdicts := []Verdict{
		{ActionID: "sa_a", State: StateReady},
		{ActionID: "sa_b", State: StateRestricted, ReasonCodes: []string{ReasonRevoked}},
	}
	blocked := VetoableActions(verdicts)
	if len(blocked) != 1 || blocked[0].ActionID != "sa_b" {
		t.Fatalf("veto list = %+v, want only sa_b", blocked)
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
