package modelcatalog

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/ansonnchan/overtok/internal/normalize"
)

const tokensPerMillion = 1_000_000

// Rate is a standard API token price in micro-US-dollars per million tokens.
type Rate struct {
	InputUncached   *uint64
	InputCacheRead  *uint64
	InputCacheWrite *uint64
	Output          *uint64
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

// Estimate is explicitly an API-equivalent estimate, not actual spending.
type Estimate struct {
	MicroUSD        uint64
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

// EstimateTokens calculates a complete standard API-equivalent estimate. Any
// unavailable token category or price makes the estimate unavailable.
func (r *PricingRegistry) EstimateTokens(key string, tokens normalize.TokenCounts) (Estimate, bool, error) {
	price, found := r.Lookup(key)
	if !found {
		return Estimate{}, false, nil
	}
	pairs := []struct {
		count *uint64
		rate  *uint64
	}{
		{tokens.InputUncached, price.Rate.InputUncached},
		{tokens.InputCacheRead, price.Rate.InputCacheRead},
		{tokens.InputCacheWrite, price.Rate.InputCacheWrite},
		{tokens.Output, price.Rate.Output},
	}
	total := new(big.Int)
	for _, pair := range pairs {
		if pair.count == nil || pair.rate == nil {
			return Estimate{}, false, nil
		}
		component := new(big.Int).SetUint64(*pair.count)
		component.Mul(component, new(big.Int).SetUint64(*pair.rate))
		total.Add(total, component)
	}
	total.Div(total, big.NewInt(tokensPerMillion))
	if !total.IsUint64() {
		return Estimate{}, false, errors.New("API-equivalent estimate overflows uint64 micro-USD")
	}
	return Estimate{
		MicroUSD: total.Uint64(), PricingRevision: r.Revision,
		Label: "API-equivalent estimate",
	}, true, nil
}
