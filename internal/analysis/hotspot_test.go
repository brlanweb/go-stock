package analysis

import (
	"testing"

	"github.com/hoax/go-stock/internal/store"
)

func TestValidateHotspotAIResultRejectsUnknownConcept(t *testing.T) {
	allowed := map[string]store.HotspotSectorStat{
		"BK001": {SectorCode: "BK001", SectorName: "光模块"},
	}
	result := hotspotAIResult{Mainlines: []HotspotAIMainline{{
		Name: "AI 算力",
		Chokepoints: []HotspotAIChokepoint{{
			SectorCode: "BK999", Status: "latent", Confidence: 80,
			Reason: "产业链卡点", Invalidation: "需求下降",
		}},
	}}}
	if err := validateHotspotAIResult(&result, allowed); err == nil {
		t.Fatal("expected unknown AI concept to be rejected")
	}
}

func TestValidateHotspotAIResultAcceptsGroundedResult(t *testing.T) {
	allowed := map[string]store.HotspotSectorStat{
		"BK001": {SectorCode: "BK001", SectorName: "光模块"},
		"BK002": {SectorCode: "BK002", SectorName: "磷化铟"},
	}
	result := hotspotAIResult{Mainlines: []HotspotAIMainline{{
		Name:         "AI 算力",
		Thesis:       "算力建设向高速互连传导",
		ConceptCodes: []string{"BK001", "BK002"},
		Relations:    []HotspotAIRelation{{FromCode: "BK002", ToCode: "BK001", Type: "上游材料", Reason: "光芯片衬底"}},
		Chokepoints: []HotspotAIChokepoint{{
			SectorCode: "BK002", Status: "accelerating", Confidence: 86,
			Reason: "量价与产业逻辑共振", Invalidation: "量能回落且需求预期下修",
		}},
	}}}
	if err := validateHotspotAIResult(&result, allowed); err != nil {
		t.Fatalf("expected grounded result to pass: %v", err)
	}
}
