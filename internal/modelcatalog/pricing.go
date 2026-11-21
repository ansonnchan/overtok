package modelcatalog

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/ansonnchan/tokenholic/internal/normalize"
)

const tokensPerMillion = 1_000_000

// Rate is a standard API token price in micro-US-dollars per million tokens.
// InputCacheWrite is the provider's base cache-write rate, which is the
// 5-minute lifetime for Anthropic; InputCacheWrite1h is the 1-hour rate. A nil
// rate means the provider publishes none for that category.
type Rate struct {
	InputUncached     *uint64
	InputCacheRead    *uint64
	InputCacheWrite   *uint64
	InputCacheWrite1h *uint64
	Output            *uint64
}

// Checksum returns a stable digest of the reviewed revision and sorted prices.
func (r *PricingRegistry) Checksum() ([32]byte, error) {
	encoded, err := json.Marshal(struct {
		Revision string  `json:"revision"`
		Prices   []Price `json:"prices"`
	}{Revision: r.Revision, Prices: r.Prices})
	if err != nil {
		return [32]byte{}, fmt.Errorf("encode pricing registry checksum: %w", err)
	}
	return sha256.Sum256(encoded), nil
}

// EffectiveAt returns when the complete revision became effective.
func (r *PricingRegistry) EffectiveAt() time.Time {
	var effective time.Time
	for _, price := range r.Prices {
		if price.EffectiveDate.After(effective) {
			effective = price.EffectiveDate
		}
	}
	return effective
}

// Price ties a pricing key to one provider source revision.
type Price struct {
	Key            string
	Provider       string
	Rate           Rate
	SourceURL      string
	SourceRevision string
	EffectiveDate  time.Time
}

// PricingRegistry is an immutable API-equivalent price revision.
type PricingRegistry struct {
	Revision string
	Prices   []Price
	byKey    map[string]Price
}

// EstimateLabel names every cost value tokenholic shows. It is never spending.
const EstimateLabel = "API-equivalent estimate"

// Estimate is explicitly an API-equivalent estimate, not actual spending.
//
// PricedTokens and UnpricedTokens partition the known tokens by whether a rate
// applied. LowerBound is true when the real standard-rate cost may be higher:
// some tokens had no rate, a cache write had an unknown lifetime and was
// priced at the base rate, or a billable category was unavailable.
type Estimate struct {
	MicroUSD        uint64
	PricedTokens    uint64
	UnpricedTokens  uint64
	LowerBound      bool
	PricingRevision string
	Label           string
}

// NewPricingRegistry validates a versioned price set.
func NewPricingRegistry(revision string, prices []Price) (*PricingRegistry, error) {
	if revision == "" {
		return nil, errors.New("pricing revision is required")
	}
	registry := &PricingRegistry{Revision: revision, Prices: append([]Price(nil), prices...), byKey: make(map[string]Price)}
	sort.Slice(registry.Prices, func(i, j int) bool { return registry.Prices[i].Key < registry.Prices[j].Key })
	for _, price := range registry.Prices {
		if price.Key == "" || price.Provider == "" || price.SourceURL == "" || price.SourceRevision == "" || price.EffectiveDate.IsZero() {
			return nil, fmt.Errorf("pricing entry %q is missing source metadata", price.Key)
		}
		if _, exists := registry.byKey[price.Key]; exists {
			return nil, fmt.Errorf("pricing key %q is duplicated", price.Key)
		}
		registry.byKey[price.Key] = price
	}
	return registry, nil
}

// Lookup returns one price without implying that local usage incurred a bill.
func (r *PricingRegistry) Lookup(key string) (Price, bool) {
	price, found := r.byKey[key]
	return price, found
}

// EstimateTokens prices the five billable categories: uncached input, cache
// read, base (5-minute) cache write, 1-hour cache write, and output. Reasoning
// output is already inside output and is never priced separately.
//
// A cache write whose lifetime is unknown is priced at the base rate and makes
// the estimate a lower bound. Known tokens without a rate are counted as
// unpriced instead of being dropped or guessed. available is false when the
// key has no price or no known category could be priced.
func (r *PricingRegistry) EstimateTokens(key string, tokens normalize.TokenCounts) (Estimate, bool, error) {
	price, found := r.Lookup(key)
	if !found {
		return Estimate{}, false, nil
	}
	unknownLifetime := tokens.CacheWriteUnknownTTL()
	estimate := Estimate{PricingRevision: r.Revision, Label: EstimateLabel}
	estimate.LowerBound = tokens.InputUncached == nil || tokens.InputCacheRead == nil ||
		tokens.InputCacheWrite == nil || tokens.Output == nil ||
		unknownLifetime != nil && *unknownLifetime > 0
	parts := []struct {
		count *uint64
		rate  *uint64
	}{
		{tokens.InputUncached, price.Rate.InputUncached},
		{tokens.InputCacheRead, price.Rate.InputCacheRead},
		{tokens.InputCacheWrite5m, price.Rate.InputCacheWrite},
		{tokens.InputCacheWrite1h, price.Rate.InputCacheWrite1h},
		{unknownLifetime, price.Rate.InputCacheWrite},
		{tokens.Output, price.Rate.Output},
	}
	total, priced, unpriced := new(big.Int), new(big.Int), new(big.Int)
	available := false
	for _, part := range parts {
		if part.count == nil {
			continue
		}
		count := new(big.Int).SetUint64(*part.count)
		if part.rate == nil {
			unpriced.Add(unpriced, count)
			continue
		}
		available = true
		priced.Add(priced, count)
		total.Add(total, count.Mul(count, new(big.Int).SetUint64(*part.rate)))
	}
	// One floor per estimate keeps sub-micro-dollar remainders from compounding.
	total.Div(total, big.NewInt(tokensPerMillion))
	if !total.IsUint64() || !priced.IsUint64() || !unpriced.IsUint64() {
		return Estimate{}, false, errors.New("API-equivalent estimate overflows uint64")
	}
	estimate.MicroUSD, estimate.PricedTokens, estimate.UnpricedTokens = total.Uint64(), priced.Uint64(), unpriced.Uint64()
	if estimate.UnpricedTokens > 0 {
		estimate.LowerBound = true
	}
	return estimate, available, nil
}
