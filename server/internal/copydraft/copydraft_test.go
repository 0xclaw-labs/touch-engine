package copydraft

import (
	"strings"
	"testing"
)

func withheldSample() Input {
	return Input{
		StoreName:       "江边小馆",
		CampaignTitle:   "周末到店",
		PublicContent:   "到店有礼",
		Price:           Fact{Text: "19.9元", Status: StatusExpired},
		Address:         Fact{Text: "上海市南京西路1号", Status: StatusUncertain},
		Hours:           Fact{Text: "10:00-22:00", Status: StatusUncertain},
		Claims:          []Claim{{Text: "全市最低", Evidence: ""}},
		POINames:        []string{"江边小馆(南京西路店)", " 江边小馆(人民广场店) "},
		AssetIDs:        []string{"ast_1"},
		ModelAuthorized: false,
	}
}

func TestComposeKeepsStructureAndDoesNotInventFacts(t *testing.T) {
	d := Compose(withheldSample())
	blob := d.Title + "\n" + d.Intro + "\n" + strings.Join(d.Topics, "\n")
	for _, forbidden := range []string{"19.9", "南京西路", "10:00", "全市最低"} {
		if strings.Contains(blob, forbidden) {
			t.Fatalf("copy field contains %q: %+v", forbidden, d)
		}
		if strings.Contains(strings.Join(d.ConfirmedFacts, "\n"), forbidden) {
			t.Fatalf("confirmed facts contain %q: %+v", forbidden, d.ConfirmedFacts)
		}
	}
	if d.Usable || d.ModelStatus != ModelNotAuthorized || d.Billed {
		t.Fatalf("unauthorized draft must stay non-usable and unbilled: %+v", d)
	}
	if d.Title != "" || d.Intro != "" || len(d.Topics) != 0 {
		t.Fatalf("rule draft must leave copy fields empty: %+v", d)
	}
	if !strings.Contains(d.Notice, "不是真实可用文案") {
		t.Fatalf("notice = %q", d.Notice)
	}
	codes := map[string]bool{}
	for _, g := range d.Gaps {
		codes[g.Code] = true
		if strings.Contains(g.Message, "19.9") || strings.Contains(g.Message, "全市最低") {
			t.Fatalf("gap repeats withheld text: %+v", g)
		}
	}
	for _, code := range []string{"price_expired", "address_uncertain", "hours_uncertain", "claim_without_evidence"} {
		if !codes[code] {
			t.Fatalf("missing gap %s in %+v", code, d.Gaps)
		}
	}
	if d.POI.Mounted || len(d.POI.Suggestions) != 2 {
		t.Fatalf("poi = %+v", d.POI)
	}
	if !strings.Contains(d.POI.Message, "没有绑定") {
		t.Fatalf("poi message = %q", d.POI.Message)
	}
	if d.Handoff.StoreName != "江边小馆" || d.Handoff.CampaignTitle != "周末到店" || d.Handoff.PublicContent != "到店有礼" {
		t.Fatalf("handoff dropped confirmed campaign facts: %+v", d.Handoff)
	}
	if d.Handoff.Price != "" || d.Handoff.Address != "" || d.Handoff.Hours != "" || len(d.Handoff.EvidencedClaims) != 0 {
		t.Fatalf("handoff leaked withheld facts: %+v", d.Handoff)
	}
	if len(d.Handoff.AssetIDs) != 1 || d.Handoff.AssetIDs[0] != "ast_1" {
		t.Fatalf("assets = %+v", d.Handoff.AssetIDs)
	}
	raw := d.Handoff.StoreName + d.Handoff.CampaignTitle + d.Handoff.PublicContent + d.Handoff.Price + d.Handoff.Address
	if strings.Contains(raw, "http") || strings.Contains(raw, ".invalid") || strings.Contains(raw, "localhost") {
		t.Fatalf("handoff must not carry a jump target: %s", raw)
	}
}

func TestComposeKeepsConfirmedPriceOutOfUnauthorizedCopy(t *testing.T) {
	in := withheldSample()
	in.Price = Fact{Text: "39元", Status: StatusConfirmed}
	in.Address = Fact{Text: "杭州市湖滨路8号", Status: StatusConfirmed}
	in.Hours = Fact{Text: "11:00-21:00", Status: StatusConfirmed}
	in.Claims = []Claim{{Text: "到店赠饮", Evidence: "菜单第2页"}}
	d := Compose(in)
	if d.Usable || d.Title != "" {
		t.Fatalf("confirmed facts still are not a model copy: %+v", d)
	}
	joined := strings.Join(d.ConfirmedFacts, "\n")
	for _, want := range []string{"39元", "杭州市湖滨路8号", "11:00-21:00", "到店赠饮"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("confirmed facts missing %s: %s", want, joined)
		}
	}
	if d.Handoff.Price != "39元" || d.Handoff.Address != "杭州市湖滨路8号" || len(d.Handoff.EvidencedClaims) != 1 {
		t.Fatalf("handoff = %+v", d.Handoff)
	}
}

func TestPOINamesDoNotMountUnlessChannelAvailable(t *testing.T) {
	off := Compose(withheldSample())
	if off.POI.Mounted {
		t.Fatal("names alone mounted")
	}
	in := withheldSample()
	in.ChannelMountAvailable = true
	on := Compose(in)
	if !on.POI.Mounted || !strings.Contains(on.POI.Message, "可挂载") {
		t.Fatalf("channel mount = %+v", on.POI)
	}
}

func TestUnauthorizedModelTextIsIgnored(t *testing.T) {
	d := ApplyModel(withheldSample(), ModelOutput{Title: "假的真实可用标题19.9元", Intro: "全市最低", Topics: []string{"#南京西路"}})
	if d.Title != "" || d.Intro != "" || len(d.Topics) != 0 || d.Usable || d.ModelStatus != ModelNotAuthorized {
		t.Fatalf("unauthorized model text leaked: %+v", d)
	}
}

func TestAuthorizedModelDropsWithheldPhrasesAndDoesNotBill(t *testing.T) {
	in := withheldSample()
	in.ModelAuthorized = true
	d := ApplyModel(in, ModelOutput{
		Title:  "江边小馆周末到店仅19.9元",
		Intro:  "全市最低，快来",
		Topics: []string{"#周末到店", "#南京西路美食"},
	})
	if strings.Contains(d.Title, "19.9") || d.Intro != "" || len(d.Topics) != 1 || d.Topics[0] != "#周末到店" {
		t.Fatalf("withheld phrases survived: %+v", d)
	}
	if d.Billed {
		t.Fatal("model filter must not bill")
	}
	if d.Usable {
		t.Fatal("title that was withheld entirely must not become usable")
	}
	found := false
	for _, g := range d.Gaps {
		if g.Code == "model_output_withheld" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing model_output_withheld: %+v", d.Gaps)
	}
}

func TestAuthorizedModelMayKeepCleanRestatement(t *testing.T) {
	in := withheldSample()
	in.ModelAuthorized = true
	in.Price = Fact{Text: "39元", Status: StatusConfirmed}
	d := ApplyModel(in, ModelOutput{Title: "江边小馆周末到店", Intro: "到店有礼", Topics: []string{"#周末到店"}})
	if !d.Usable || d.ModelStatus != ModelAuthorized || d.Title != "江边小馆周末到店" || d.Billed {
		t.Fatalf("clean authorized draft = %+v", d)
	}
	if strings.Contains(d.Title, "19.9") {
		t.Fatal(d.Title)
	}
}
