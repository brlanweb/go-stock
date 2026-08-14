package store

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestIsGenericConceptFiltersBlacklistedNames(t *testing.T) {
	for _, name := range []string{"融资融券", "深股通", "沪股通", "MSCI中国", "QFII重仓股", "社保重仓"} {
		if !isGenericConcept(name) {
			t.Fatalf("%q must be recognized as generic concept", name)
		}
	}
	for _, name := range []string{"医疗器械", "减肥药", "CPO", "低空经济"} {
		if isGenericConcept(name) {
			t.Fatalf("%q must not be treated as generic concept", name)
		}
	}
}

// 黑名单文件读取失败时只剩内置兜底列表，因此文件里的每一项都必须
// 同步进内置列表，避免部署路径变化导致过滤静默缺项。
func TestGenericConceptBuiltinCoversBlacklistFile(t *testing.T) {
	raw, err := os.ReadFile("../../config/hotspot_blacklist.txt")
	if err != nil {
		t.Fatalf("read config/hotspot_blacklist.txt: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !slices.Contains(genericConceptNames, line) {
			t.Fatalf("blacklist entry %q missing from builtin genericConceptNames; keep them in sync", line)
		}
	}
}
