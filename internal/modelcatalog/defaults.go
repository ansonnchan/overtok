package modelcatalog

import "time"

const (
	// CatalogRevision identifies the bundled canonical model mapping.
	CatalogRevision = "2026-11-17"
	// PricingRevision identifies the bundled standard API price snapshot.
	PricingRevision = "2026-11-17"
)

// DefaultCatalog returns the reviewed model metadata bundled with this build.
func DefaultCatalog() Catalog {
	return Catalog{
		Revision:         CatalogRevision,
		AmbiguousAliases: []string{"auto", "default", "router", "smart"},
		Models: []Model{
			{CanonicalID: "gpt-6.2-astra", Provider: "openai", PricingKey: "openai/gpt-6.2-astra"},
			{CanonicalID: "gpt-6.2-sol", Provider: "openai", PricingKey: "openai/gpt-6.2-sol"},
			{CanonicalID: "claude-opus-5-5", Provider: "anthropic", PricingKey: "anthropic/claude-opus-5-5"},
			{CanonicalID: "claude-sonnet-5-5", Provider: "anthropic", PricingKey: "anthropic/claude-sonnet-5-5"},
			// Haiku pricing changes at a prompt-length threshold, so canonical
			// resolution is available while a single price key is intentionally not.
			{CanonicalID: "claude-haiku-5-5", Provider: "anthropic"},
		},
	}
}

// DefaultPricingRegistry returns standard first-party API-equivalent prices.
func DefaultPricingRegistry() (*PricingRegistry, error) {
	openAIDate := time.Date(2026, time.November, 17, 0, 0, 0, 0, time.UTC)
	anthropicDate := time.Date(2026, time.October, 11, 0, 0, 0, 0, time.UTC)
	return NewPricingRegistry(PricingRevision, []Price{
		{
			Key: "openai/gpt-6.2-astra", Provider: "openai",
			Rate:           Rate{InputUncached: rate(8_000_000), InputCacheRead: rate(400_000), InputCacheWrite: rate(10_000_000), Output: rate(40_000_000)},
			SourceURL:      "https://developers.openai.com/api/docs/models/gpt-6.2-astra",
			SourceRevision: "retrieved-2026-11-17", EffectiveDate: openAIDate,
		},
		{
			Key: "openai/gpt-6.2-sol", Provider: "openai",
			Rate:           Rate{InputUncached: rate(2_000_000), InputCacheRead: rate(100_000), InputCacheWrite: rate(2_500_000), Output: rate(10_000_000)},
			SourceURL:      "https://developers.openai.com/api/docs/models/gpt-6.2-sol",
			SourceRevision: "retrieved-2026-11-17", EffectiveDate: openAIDate,
		},
		{
			Key: "anthropic/claude-opus-5-5", Provider: "anthropic",
			Rate:           Rate{InputUncached: rate(4_000_000), InputCacheRead: rate(200_000), InputCacheWrite: rate(5_000_000), Output: rate(20_000_000)},
			SourceURL:      "https://platform.claude.com/docs/en/about-claude/pricing?v=20261011",
			SourceRevision: "2026-10-11", EffectiveDate: anthropicDate,
		},
		{
			Key: "anthropic/claude-sonnet-5-5", Provider: "anthropic",
			Rate:           Rate{InputUncached: rate(2_000_000), InputCacheRead: rate(100_000), InputCacheWrite: rate(2_500_000), Output: rate(10_000_000)},
			SourceURL:      "https://platform.claude.com/docs/en/about-claude/pricing?v=20261011",
			SourceRevision: "2026-10-11", EffectiveDate: anthropicDate,
		},
	})
}

func rate(microUSDPerMillion uint64) *uint64 {
	value := microUSDPerMillion
	return &value
}
