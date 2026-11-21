package modelcatalog

import (
	"math"
	"testing"
	"time"

	"github.com/ansonnchan/tokenholic/internal/normalize"
)

// The synthetic price is $3 input, $0.30 cache read, $3.75 base cache write,
// $6 one-hour cache write, and $15 output per million tokens. Expected values
// below are worked by hand in micro-dollars.
func TestEstimateTokensGoldenCosts(t *testing.T) {
	t.Parallel()
	registry := syntheticPricing(t)
	count := func(v uint64) *uint64 { return &v }
	zero := count(0)
	tests := []struct {
		name           string
		key            string
		tokens         normalize.TokenCounts
		wantAvailable  bool
		wantMicroUSD   uint64
		wantPriced     uint64
		wantUnpriced   uint64
		wantLowerBound bool
	}{
		{
			// 1000*3 + 20000*0.30 + 1000*3.75 + 2000*6 + 500*15 = 32250 micro-USD.
			name: "all five priced categories", key: "provider/model", wantAvailable: true,
			tokens: normalize.TokenCounts{
				InputUncached: count(1_000), InputCacheRead: count(20_000), InputCacheWrite: count(3_000),
				InputCacheWrite5m: count(1_000), InputCacheWrite1h: count(2_000), Output: count(500),
			},
			wantMicroUSD: 32_250, wantPriced: 24_500,
		},
		{
			// 4000 tokens with no lifetime are priced at the base rate: 4000*3.75.
			name: "combined cache write has an unknown lifetime", key: "provider/model", wantAvailable: true,
			tokens:       normalize.TokenCounts{InputUncached: zero, InputCacheRead: zero, InputCacheWrite: count(4_000), Output: zero},
			wantMicroUSD: 15_000, wantPriced: 4_000, wantLowerBound: true,
		},
		{
			// 3000*6 for the known hour, 2000*3.75 for the unexplained remainder.
			name: "partly split cache write", key: "provider/model", wantAvailable: true,
			tokens: normalize.TokenCounts{
				InputUncached: zero, InputCacheRead: zero, InputCacheWrite: count(5_000), InputCacheWrite1h: count(3_000), Output: zero,
			},
			wantMicroUSD: 25_500, wantPriced: 5_000, wantLowerBound: true,
		},
		{
			name: "one-hour writes cost more than five-minute writes", key: "provider/model", wantAvailable: true,
			tokens: normalize.TokenCounts{
				InputUncached: zero, InputCacheRead: zero, InputCacheWrite: count(1_000_000),
				InputCacheWrite5m: zero, InputCacheWrite1h: count(1_000_000), Output: zero,
			},
			wantMicroUSD: 6_000_000, wantPriced: 1_000_000,
		},
		{
			name: "unavailable category makes a lower bound", key: "provider/model", wantAvailable: true,
			tokens:       normalize.TokenCounts{InputUncached: count(1_000)},
			wantMicroUSD: 3_000, wantPriced: 1_000, wantLowerBound: true,
		},
		{
			// 1*3 + 1*0.30 = 3.3 micro-USD, floored once to 3.
			name: "sub-micro-dollar remainder floors once", key: "provider/model", wantAvailable: true,
			tokens:       normalize.TokenCounts{InputUncached: count(1), InputCacheRead: count(1), InputCacheWrite: zero, InputCacheWrite5m: zero, InputCacheWrite1h: zero, Output: zero},
			wantMicroUSD: 3, wantPriced: 2,
		},
		{
			name: "reasoning is already inside output", key: "provider/model", wantAvailable: true,
			tokens: normalize.TokenCounts{
				InputUncached: zero, InputCacheRead: zero, InputCacheWrite: zero, InputCacheWrite5m: zero, InputCacheWrite1h: zero,
				Output: count(500), ReasoningOutput: count(400),
			},
			wantMicroUSD: 7_500, wantPriced: 500,
		},
		{
			name: "reported zero usage is a complete zero", key: "provider/model", wantAvailable: true,
			tokens: normalize.TokenCounts{InputUncached: zero, InputCacheRead: zero, InputCacheWrite: zero, InputCacheWrite5m: zero, InputCacheWrite1h: zero, Output: zero},
		},
		{
			// The provider publishes no one-hour rate, so those tokens stay unpriced.
			name: "category without a published rate", key: "provider/base-only", wantAvailable: true,
			tokens: normalize.TokenCounts{
				InputUncached: count(1_000), InputCacheRead: zero, InputCacheWrite: count(2_000),
				InputCacheWrite5m: zero, InputCacheWrite1h: count(2_000), Output: zero,
			},
			wantMicroUSD: 3_000, wantPriced: 1_000, wantUnpriced: 2_000, wantLowerBound: true,
		},
		{
			name: "nothing priceable", key: "provider/base-only",
			tokens:       normalize.TokenCounts{InputCacheWrite: count(10), InputCacheWrite5m: zero, InputCacheWrite1h: count(10)},
			wantUnpriced: 10, wantLowerBound: true,
		},
		{name: "unknown pricing key", key: "provider/unknown", tokens: normalize.TokenCounts{Output: count(500)}},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			estimate, available, err := registry.EstimateTokens(tt.key, tt.tokens)
			if err != nil {
				t.Fatal(err)
			}
			if available != tt.wantAvailable || estimate.MicroUSD != tt.wantMicroUSD || estimate.PricedTokens != tt.wantPriced ||
				estimate.UnpricedTokens != tt.wantUnpriced || estimate.LowerBound != tt.wantLowerBound {
				t.Fatalf("EstimateTokens() = %#v available=%t, want %d micro-USD, %d priced, %d unpriced, lowerBound=%t, available=%t",
					estimate, available, tt.wantMicroUSD, tt.wantPriced, tt.wantUnpriced, tt.wantLowerBound, tt.wantAvailable)
			}
			if available && (estimate.Label != EstimateLabel || estimate.PricingRevision != "pricing-r1") {
				t.Fatalf("estimate metadata = %#v", estimate)
			}
		})
	}
}

func TestEstimateTokensReportsOverflow(t *testing.T) {
	t.Parallel()
	registry := syntheticPricing(t)
	maximum := uint64(math.MaxUint64)
	if _, _, err := registry.EstimateTokens("provider/model", normalize.TokenCounts{InputUncached: &maximum, Output: &maximum}); err == nil {
		t.Fatal("EstimateTokens() accepted an estimate beyond uint64")
	}
}

func syntheticPricing(t *testing.T) *PricingRegistry {
	t.Helper()
	effective := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	registry, err := NewPricingRegistry("pricing-r1", []Price{
		{
			Key: "provider/model", Provider: "provider",
			Rate: Rate{
				InputUncached: rate(3_000_000), InputCacheRead: rate(300_000),
				InputCacheWrite: rate(3_750_000), InputCacheWrite1h: rate(6_000_000), Output: rate(15_000_000),
			},
			SourceURL: "https://provider.invalid/pricing", SourceRevision: "synthetic-r1", EffectiveDate: effective,
		},
		{
			Key: "provider/base-only", Provider: "provider",
			Rate:      Rate{InputUncached: rate(3_000_000), InputCacheRead: rate(300_000), Output: rate(15_000_000)},
			SourceURL: "https://provider.invalid/pricing", SourceRevision: "synthetic-r1", EffectiveDate: effective,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}
