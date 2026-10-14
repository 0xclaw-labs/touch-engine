package tagcsv

import (
	"strings"
	"testing"
)

// 注入防护矩阵:以 = + - @ 开头的字段必须被加 ' 前缀;其余字段原样。
func TestSanitizeFieldInjectionMatrix(t *testing.T) {
	cases := []struct{ in, want string }{
		{"=cmd|' /C calc'!A0", "'=cmd|' /C calc'!A0"},
		{"=1+1", "'=1+1"},
		{"+1337", "'+1337"},
		{"-2+3", "'-2+3"},
		{"@SUM(A1)", "'@SUM(A1)"},
		{"普通标签", "普通标签"},
		{"ABC123", "ABC123"},
		{"", ""},
		{"a=b", "a=b"},     // 只有开头字符才触发
		{" =cmd", " =cmd"}, // 前导空格不触发(规范只看首字符)
		{"'已带引号", "'已带引号"}, // 已有前导 ' 原样保留
	}
	for _, c := range cases {
		if got := SanitizeField(c.in); got != c.want {
			t.Fatalf("SanitizeField(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildHeaderIsFixed(t *testing.T) {
	out, err := Build(nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// BOM + 表头恒定:消费者(NFC 写入工具)按此接口解析
	want := "\xEF\xBB\xBFlabel,short_code,url,store,group,uid_hint\n"
	if out != want {
		t.Fatalf("header output = %q, want %q", out, want)
	}
}

func TestBuildRowsRoundTrip(t *testing.T) {
	rows := []Row{
		{Label: "周年庆-001", ShortCode: "ABC123XYZ012", URL: "https://h5.test/c/ABC123XYZ012", Store: "旗舰店", Group: "一组", UIDHint: ""},
		{Label: "=evil", ShortCode: "ZZZ", URL: "https://h5.test/c/ZZZ", Store: "a,b", Group: "", UIDHint: "04:A2:2F"},
	}
	out, err := Build(rows)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.HasPrefix(out, "\xEF\xBB\xBF") {
		t.Fatal("missing UTF-8 BOM")
	}
	if !strings.Contains(out, `"a,b"`) {
		t.Fatalf("comma field not quoted:\n%s", out)
	}
	if !strings.Contains(out, "'=evil") {
		t.Fatalf("injection field not escaped:\n%s", out)
	}
	// 回读:合法行解析回原始值(注入行解析为转义后的展示值,见契约)
	lines := strings.Split(strings.TrimPrefix(out, "\xEF\xBB\xBF"), "\n")
	if len(lines) != 4 { // header + 2 rows + trailing empty
		t.Fatalf("line count = %d, want 4:\n%s", len(lines), out)
	}
	if lines[0] != "label,short_code,url,store,group,uid_hint" {
		t.Fatalf("header line = %q", lines[0])
	}
	if !strings.Contains(lines[1], "周年庆-001") || !strings.Contains(lines[1], "https://h5.test/c/ABC123XYZ012") {
		t.Fatalf("row1 = %q", lines[1])
	}
}
