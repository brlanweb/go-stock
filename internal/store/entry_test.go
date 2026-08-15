package store

import "testing"

func TestIsBrokerCandidateFiltersBrokerStocks(t *testing.T) {
	cases := []struct {
		industry string
		name     string
		want     bool
	}{
		{"证券", "中信证券", true},
		{"证券Ⅱ", "华泰证券", true},
		{"券商概念", "某某股份", true},
		{"互联网服务", "东方财富证券化", true}, // 名称含证券兜底
		{"白酒", "贵州茅台", false},
		{"半导体", "中芯国际", false},
	}
	for _, c := range cases {
		if got := isBrokerCandidate(c.industry, c.name); got != c.want {
			t.Fatalf("isBrokerCandidate(%q,%q)=%v want %v", c.industry, c.name, got, c.want)
		}
	}
}

func TestTrailingAverage(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	avg, ok := trailingAverage(values, 10)
	if !ok || avg != 5.5 {
		t.Fatalf("expected 5.5, got %f ok=%v", avg, ok)
	}
	avg, ok = trailingAverage(values, 5)
	if !ok || avg != 8 {
		t.Fatalf("expected 8 (末尾5个均值), got %f ok=%v", avg, ok)
	}
	if _, ok := trailingAverage(values[:3], 10); ok {
		t.Fatal("样本不足必须返回 false")
	}
}
