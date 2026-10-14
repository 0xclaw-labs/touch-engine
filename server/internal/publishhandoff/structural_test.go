package publishhandoff

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T5.7(结构性保证): 本包零平台外呼、零付费生成调用、零奖励写账——
// 交接失败不触发任何付费生成不是运行时承诺,而是包依赖结构。测试直接
// 扫描本包非测试源文件的 import 与可疑调用面(测试工作目录=包源码目录)。
func TestPackageHasNoOutboundOrGenerationSurface(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	forbiddenImports := []string{
		"net/http",      // 零平台外呼
		"net/smtp",      // 零通知外呼
		"os/exec",       // 零子进程(无模拟器/群控)
		"copyjob",       // 零付费生成
		"publishreward", // 零奖励写账(事实只落事件,消费归 1671)
		"rewardrule",
		"rewardops",
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(".", e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		src := string(raw)
		for _, bad := range forbiddenImports {
			if strings.Contains(src, `"`+bad+`"`) {
				t.Fatalf("%s imports %q: 交接包必须零外呼/零生成/零奖励写账", e.Name(), bad)
			}
		}
		// 客户端路径不允许出现任何 http 客户端构造。
		for _, call := range []string{"http.Client", "http.Post", "http.Get", "http.NewRequest"} {
			if strings.Contains(src, call) {
				t.Fatalf("%s references %s: no outbound surface is allowed", e.Name(), call)
			}
		}
	}
}
