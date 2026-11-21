package pricing

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/ansonnchan/tokenholic/internal/modelcatalog"
	protocol "github.com/ansonnchan/tokenholic/internal/sync"
)

// The synthetic price is $3 input, $0.30 cache read, $3.75 base cache write,
// $6 one-hour cache write, and $15 output per million tokens.
func TestDeriveOwnsModelAndEstimateColumns(t *testing.T) {
	t.Parallel()
	engine := syntheticEngine(t, "synthetic-r1")
	complete := func(v uint64) protocol.Value { return protocol.Value{Value: &v, State: "complete"} }
	unavailable := protocol.Value{State: "unavailable"}
	usage := func(model, evidence string, mutate func(*protocol.Tokens)) Usage {
		tokens := protocol.Tokens{
			InputUncached: complete(1_000), InputCacheRead: complete(20_000), InputCacheWrite: complete(3_000),
			InputCacheWrite5m: complete(1_000), InputCacheWrite1h: complete(2_000), Output: complete(500),
			ReasoningOutput: unavailable,
		}
		if mutate != nil {
			mutate(&tokens)
		}
		return Usage{ObservedModelID: model, ModelEvidence: evidence, Tokens: tokens}
	}
	text := func(v string) *string { return &v }
	value := func(v uint64) *uint64 { return &v }
	tests := []struct {
		name  string
		usage Usage
		want  Derived
	}{
		{
			// 1000*3 + 20000*0.30 + 1000*3.75 + 2000*6 + 500*15 = 32250.
			name: "priced model", usage: usage("model-a", "reported", nil),
			want: Derived{
				CanonicalModelID: text("model-a"), ModelProvider: text("provider"), PricingKey: text("provider/model-a"),
				ModelResolution: "reported", ModelMatch: "exact", EstimateMicroUSD: value(32_250), EstimateState: "complete",
				PricedTokens: 24_500, PricingRevision: "synthetic-r1",
			},
		},
		{
			name: "reviewed alias keeps inferred evidence", usage: usage("model-a[1m]", "inferred", nil),
			want: Derived{
				CanonicalModelID: text("model-a"), ModelProvider: text("provider"), PricingKey: text("provider/model-a"),
				ModelResolution: "inferred", ModelMatch: "reviewed", EstimateMicroUSD: value(32_250), EstimateState: "complete",
				PricedTokens: 24_500, PricingRevision: "synthetic-r1",
			},
		},
		{
			// The 3000 write tokens have no lifetime: 3000*3.75 instead of 15750.
			name: "unknown cache-write lifetime is a lower bound",
			usage: usage("model-a", "reported", func(tokens *protocol.Tokens) {
				tokens.InputCacheWrite5m, tokens.InputCacheWrite1h = unavailable, unavailable
			}),
			want: Derived{
				CanonicalModelID: text("model-a"), ModelProvider: text("provider"), PricingKey: text("provider/model-a"),
				ModelResolution: "reported", ModelMatch: "exact", EstimateMicroUSD: value(27_750), EstimateState: "partial",
				PricedTokens: 24_500, PricingRevision: "synthetic-r1",
			},
		},
		{
			name:  "partial category coverage is a lower bound",
			usage: usage("model-a", "reported", func(tokens *protocol.Tokens) { tokens.Output.State = "partial" }),
			want: Derived{
				CanonicalModelID: text("model-a"), ModelProvider: text("provider"), PricingKey: text("provider/model-a"),
				ModelResolution: "reported", ModelMatch: "exact", EstimateMicroUSD: value(32_250), EstimateState: "partial",
				PricedTokens: 24_500, PricingRevision: "synthetic-r1",
			},
		},
		{
			name: "model the catalog does not know", usage: usage("model-released-tomorrow", "reported", nil),
			want: Derived{
				ModelResolution: "unknown", ModelMatch: "unknown", UnpricedReason: text(modelcatalog.ReasonUnknownModel),
				EstimateState: "unavailable", UnpricedTokens: 24_500, PricingRevision: "synthetic-r1",
			},
		},
		{
			name: "prompt-length tiers stay unpriced", usage: usage("model-tiered", "reported", nil),
			want: Derived{
				CanonicalModelID: text("model-tiered"), ModelProvider: text("provider"),
				ModelResolution: "reported", ModelMatch: "exact", UnpricedReason: text(modelcatalog.ReasonContextTiered),
				EstimateState: "unavailable", UnpricedTokens: 24_500, PricingRevision: "synthetic-r1",
			},
		},
		{
			name: "no model evidence", usage: usage("", "unknown", nil),
			want: Derived{
				ModelResolution: "unknown", ModelMatch: "unknown", UnpricedReason: text(modelcatalog.ReasonModelUnavailable),
				EstimateState: "unavailable", UnpricedTokens: 24_500, PricingRevision: "synthetic-r1",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := engine.Derive(tt.usage)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Derive() =\n%s\nwant\n%s", describe(got), describe(tt.want))
			}
			again, err := engine.Derive(tt.usage)
			if err != nil || !reflect.DeepEqual(got, again) {
				t.Fatalf("second Derive() differs: %s, %v", describe(again), err)
			}
		})
	}
}

func TestDeriveReportsOverflowInsteadOfWrapping(t *testing.T) {
	t.Parallel()
	maximum := ^uint64(0)
	huge := protocol.Value{Value: &maximum, State: "complete"}
	_, err := syntheticEngine(t, "synthetic-r1").Derive(Usage{ObservedModelID: "model-a", ModelEvidence: "reported", Tokens: protocol.Tokens{
		InputUncached: huge, InputCacheRead: huge, InputCacheWrite: protocol.Value{State: "unavailable"},
		InputCacheWrite5m: protocol.Value{State: "unavailable"}, InputCacheWrite1h: protocol.Value{State: "unavailable"},
		Output: huge, ReasoningOutput: protocol.Value{State: "unavailable"},
	}})
	if err == nil {
		t.Fatal("Derive() accepted a total beyond uint64")
	}
}

func syntheticEngine(t *testing.T, revision string) *Engine {
	t.Helper()
	rate := func(v uint64) *uint64 { return &v }
	registry, err := modelcatalog.NewPricingRegistry(revision, []modelcatalog.Price{{
		Key: "provider/model-a", Provider: "provider",
		Rate: modelcatalog.Rate{
			InputUncached: rate(3_000_000), InputCacheRead: rate(300_000), InputCacheWrite: rate(3_750_000),
			InputCacheWrite1h: rate(6_000_000), Output: rate(15_000_000),
		},
		SourceURL: "https://provider.invalid/pricing", SourceRevision: "synthetic", EffectiveDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}})
	if err != nil {
		t.Fatal(err)
	}
	deriver, err := modelcatalog.NewDeriver(modelcatalog.Catalog{
		Revision:   revision,
		AliasRules: modelcatalog.AliasRules{ContextMarkers: []string{"[1m]"}},
		Models: []modelcatalog.Model{
			{CanonicalID: "model-a", Provider: "provider", PricingKey: "provider/model-a"},
			{CanonicalID: "model-tiered", Provider: "provider", UnpricedReason: modelcatalog.ReasonContextTiered},
		},
	}, registry)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(deriver)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func describe(derived Derived) string {
	text := func(v *string) string {
		if v == nil {
			return "<nil>"
		}
		return *v
	}
	estimate := "<nil>"
	if derived.EstimateMicroUSD != nil {
		estimate = fmt.Sprint(*derived.EstimateMicroUSD)
	}
	return fmt.Sprintf("canonical=%s provider=%s key=%s resolution=%s match=%s reason=%s estimate=%s state=%s priced=%d unpriced=%d revision=%s",
		text(derived.CanonicalModelID), text(derived.ModelProvider), text(derived.PricingKey), derived.ModelResolution, derived.ModelMatch,
		text(derived.UnpricedReason), estimate, derived.EstimateState, derived.PricedTokens, derived.UnpricedTokens, derived.PricingRevision)
}
