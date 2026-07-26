package store

import "testing"

func TestNormalizedHeat(t *testing.T) {
	tests := []struct {
		name            string
		value, min, max float64
		want            float64
	}{
		{name: "minimum", value: 2, min: 2, max: 6, want: 0},
		{name: "middle", value: 4, min: 2, max: 6, want: 0.5},
		{name: "maximum", value: 6, min: 2, max: 6, want: 1},
		{name: "equal values", value: 3, min: 3, max: 3, want: 0.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizedHeat(tt.value, tt.min, tt.max); got != tt.want {
				t.Fatalf("normalizedHeat() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPrimaryIndustryCodesAreUnique(t *testing.T) {
	seen := make(map[string]struct{}, len(primaryIndustryCodes))
	for _, code := range primaryIndustryCodes {
		if _, ok := seen[code]; ok {
			t.Fatalf("duplicate primary industry code %s", code)
		}
		seen[code] = struct{}{}
	}
	if got, want := len(primaryIndustryCodes), 31; got != want {
		t.Fatalf("primary industry count = %d, want %d", got, want)
	}
}

func TestConceptRankScorePrefersCoverageAndActivity(t *testing.T) {
	base := conceptRankScore(1e12, 1)
	if conceptRankScore(2e12, 1) <= base {
		t.Fatal("larger market value should increase concept rank")
	}
	if conceptRankScore(1e12, 2) <= base {
		t.Fatal("higher heat should increase concept rank")
	}
}
