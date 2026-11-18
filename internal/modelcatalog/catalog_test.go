package modelcatalog

import (
	"reflect"
	"testing"
	"time"

	"github.com/ansonnchan/overtok/internal/normalize"
)

func TestResolverMatchesExactAliasReviewedInferredAndUnknown(t *testing.T) {
	t.Parallel()
	resolver := syntheticResolver(t, "catalog-r1")
	tests := []struct {
		name      string
		observed  string
		evidence  normalize.ModelResolution
		match     MatchKind
		quality   normalize.ModelResolution
		canonical string
	}{
		{name: "exact", observed: "model-canonical", evidence: normalize.ModelReported, match: MatchExact, quality: normalize.ModelReported, canonical: "model-canonical"},
		{name: "pinned exact", observed: "model-canonical-20261117", evidence: normalize.ModelReported, match: MatchExact, quality: normalize.ModelReported, canonical: "model-canonical"},
		{name: "alias", observed: "model-latest", evidence: normalize.ModelReported, match: MatchAlias, quality: normalize.ModelResolved, canonical: "model-canonical"},
		{name: "reviewed", observed: "source-model-label", evidence: normalize.ModelReported, match: MatchReviewed, quality: normalize.ModelResolved, canonical: "model-canonical"},
		{name: "inferred", observed: "model-canonical", evidence: normalize.ModelInferred, match: MatchExact, quality: normalize.ModelInferred, canonical: "model-canonical"},
		{name: "unknown", observed: "new-model", evidence: normalize.ModelReported, match: MatchUnknown, quality: normalize.ModelUnknown},
		{name: "ambiguous routing alias", observed: "auto", evidence: normalize.ModelReported, match: MatchUnknown, quality: normalize.ModelUnknown},
		{name: "uncertain source stays unknown", observed: "model-canonical", evidence: normalize.ModelUnknown, match: MatchUnknown, quality: normalize.ModelUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolution := resolver.Resolve(test.observed, test.evidence)
			if resolution.Match != test.match || resolution.Quality != test.quality || resolution.CatalogRevision != "catalog-r1" {
				t.Fatalf("Resolve() = %#v", resolution)
			}
			if test.canonical == "" {
				if resolution.CanonicalID != nil || resolution.PricingKey != nil {
					t.Fatalf("unknown resolution has canonical metadata: %#v", resolution)
				}
			} else if resolution.CanonicalID == nil || *resolution.CanonicalID != test.canonical {
				t.Fatalf("canonical ID = %v, want %q", resolution.CanonicalID, test.canonical)
			}
		})
	}
}

func TestResolveObservationKeepsUnknownTokensAndClearsPrice(t *testing.T) {
	t.Parallel()
	resolver := syntheticResolver(t, "catalog-r1")
	input, output := uint64(7), uint64(11)
	observation := normalize.Observation{
		ObservedModelID: "new-model", ModelResolution: normalize.ModelReported,
		Tokens: normalize.TokenCounts{InputUncached: &input, Output: &output},
	}
	resolved := resolver.ResolveObservation(observation)
	if !reflect.DeepEqual(resolved.Tokens, observation.Tokens) {
		t.Fatalf("tokens changed: got %#v want %#v", resolved.Tokens, observation.Tokens)
	}
	if resolved.ModelResolution != normalize.ModelUnknown || resolved.CanonicalModelID != nil || resolved.PricingKey != nil {
		t.Fatalf("unknown resolution = %#v", resolved)
	}
	if len(resolved.Warnings) != 1 || resolved.Warnings[0].Code != "model_unresolved" {
		t.Fatalf("warnings = %#v", resolved.Warnings)
	}
}

func TestCatalogRevisionIsExplicit(t *testing.T) {
	t.Parallel()
	first := syntheticResolver(t, "catalog-r1")
	second := syntheticResolver(t, "catalog-r2")
	if first.Resolve("model-canonical", normalize.ModelReported).CatalogRevision ==
		second.Resolve("model-canonical", normalize.ModelReported).CatalogRevision {
		t.Fatal("catalog revisions are indistinguishable")
	}
}

func TestNewResolverRejectsAmbiguousOrDuplicateMappings(t *testing.T) {
	t.Parallel()
	tests := []Catalog{
		{Revision: "r1", AmbiguousAliases: []string{"auto"}, Models: []Model{{CanonicalID: "one", Provider: "provider", Aliases: []string{"auto"}}}},
		{Revision: "r1", Models: []Model{{CanonicalID: "one", Provider: "provider", Aliases: []string{"shared"}}, {CanonicalID: "two", Provider: "provider", Aliases: []string{"shared"}}}},
	}
	for _, catalog := range tests {
		if _, err := NewResolver(catalog); err == nil {
			t.Fatalf("NewResolver(%#v) succeeded", catalog)
		}
	}
}

func TestPricingRegistryEstimatesAndReportsUnavailablePrices(t *testing.T) {
	t.Parallel()
	registry := syntheticPricing(t)
	million := uint64(1_000_000)
	tokens := normalize.TokenCounts{
		InputUncached: &million, InputCacheRead: &million,
		InputCacheWrite: &million, Output: &million,
	}
	estimate, available, err := registry.EstimateTokens("provider/model", tokens)
	if err != nil || !available {
		t.Fatalf("EstimateTokens() = %#v, %t, %v", estimate, available, err)
	}
	if estimate.MicroUSD != 14_600_000 || estimate.Label != "API-equivalent estimate" || estimate.PricingRevision != "pricing-r1" {
		t.Fatalf("estimate = %#v", estimate)
	}
	if _, available, err := registry.EstimateTokens("provider/unknown", tokens); err != nil || available {
		t.Fatalf("unknown price availability = %t, %v", available, err)
	}
	tokens.InputCacheWrite = nil
	if _, available, err := registry.EstimateTokens("provider/model", tokens); err != nil || available {
		t.Fatalf("partial tokens availability = %t, %v", available, err)
	}
}

func TestPricingRegistryChecksumAndEffectiveDateAreStable(t *testing.T) {
	t.Parallel()
	registry := syntheticPricing(t)
	first, err := registry.Checksum()
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.Checksum()
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first == [32]byte{} || registry.EffectiveAt().IsZero() {
		t.Fatalf("pricing checksum/effective date = %x/%x/%v", first, second, registry.EffectiveAt())
	}
}

func syntheticResolver(t *testing.T, revision string) *Resolver {
	t.Helper()
	resolver, err := NewResolver(Catalog{
		Revision: revision, AmbiguousAliases: []string{"auto", "router"},
		Models: []Model{{
			CanonicalID: "model-canonical", Provider: "provider", PricingKey: "provider/model",
			ExactIDs: []string{"model-canonical-20261117"}, Aliases: []string{"model-latest"},
			ReviewedMapping: []string{"source-model-label"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return resolver
}

func syntheticPricing(t *testing.T) *PricingRegistry {
	t.Helper()
	registry, err := NewPricingRegistry("pricing-r1", []Price{{
		Key: "provider/model", Provider: "provider",
		Rate: Rate{
			InputUncached: rate(2_000_000), InputCacheRead: rate(100_000),
			InputCacheWrite: rate(2_500_000), Output: rate(10_000_000),
		},
		SourceURL: "https://provider.invalid/pricing", SourceRevision: "synthetic-r1",
		EffectiveDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}
