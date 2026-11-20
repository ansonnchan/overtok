package modelcatalog

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ansonnchan/tokenholic/internal/normalize"
)

// MatchKind records how an observed identifier matched catalog metadata.
type MatchKind string

const (
	// MatchExact means the observed identifier is a canonical or pinned model ID.
	MatchExact MatchKind = "exact"
	// MatchAlias means the provider documents the exact alias.
	MatchAlias MatchKind = "alias"
	// MatchReviewed means tokenholic explicitly reviewed a source-specific mapping.
	MatchReviewed MatchKind = "reviewed"
	// MatchUnknown means no safe mapping exists.
	MatchUnknown MatchKind = "unknown"
)

// Model describes one canonical model independently from observed aliases and pricing.
type Model struct {
	CanonicalID     string
	Provider        string
	PricingKey      string
	ExactIDs        []string
	Aliases         []string
	ReviewedMapping []string
}

// Catalog is an immutable model-resolution revision.
type Catalog struct {
	Revision         string
	Models           []Model
	AmbiguousAliases []string
}

// Resolution preserves the observed identifier and explains the canonical match.
type Resolution struct {
	ObservedID      string
	CanonicalID     *string
	Provider        *string
	PricingKey      *string
	Quality         normalize.ModelResolution
	Match           MatchKind
	CatalogRevision string
}

// Resolver applies one validated catalog revision.
type Resolver struct {
	revision  string
	matches   map[string]catalogMatch
	ambiguous map[string]struct{}
}

type catalogMatch struct {
	model Model
	kind  MatchKind
}

// NewResolver validates a catalog and builds exact, alias, and reviewed indexes.
func NewResolver(catalog Catalog) (*Resolver, error) {
	if strings.TrimSpace(catalog.Revision) == "" {
		return nil, errors.New("model catalog revision is required")
	}
	resolver := &Resolver{
		revision: catalog.Revision, matches: make(map[string]catalogMatch),
		ambiguous: make(map[string]struct{}),
	}
	for _, alias := range catalog.AmbiguousAliases {
		if alias == "" {
			return nil, errors.New("ambiguous model alias cannot be empty")
		}
		if _, exists := resolver.ambiguous[alias]; exists {
			return nil, fmt.Errorf("ambiguous model alias %q is duplicated", alias)
		}
		resolver.ambiguous[alias] = struct{}{}
	}
	models := append([]Model(nil), catalog.Models...)
	sort.Slice(models, func(i, j int) bool { return models[i].CanonicalID < models[j].CanonicalID })
	for _, model := range models {
		if model.CanonicalID == "" || model.Provider == "" {
			return nil, errors.New("canonical model ID and provider are required")
		}
		if err := resolver.add(model.CanonicalID, model, MatchExact); err != nil {
			return nil, err
		}
		for _, identifier := range model.ExactIDs {
			if err := resolver.add(identifier, model, MatchExact); err != nil {
				return nil, err
			}
		}
		for _, identifier := range model.Aliases {
			if err := resolver.add(identifier, model, MatchAlias); err != nil {
				return nil, err
			}
		}
		for _, identifier := range model.ReviewedMapping {
			if err := resolver.add(identifier, model, MatchReviewed); err != nil {
				return nil, err
			}
		}
	}
	return resolver, nil
}

// Resolve maps an exact observed identifier without pattern or routing guesses.
func (r *Resolver) Resolve(observed string, evidence normalize.ModelResolution) Resolution {
	unknown := Resolution{
		ObservedID: observed, Quality: normalize.ModelUnknown,
		Match: MatchUnknown, CatalogRevision: r.revision,
	}
	if observed == "" || evidence == normalize.ModelUnknown {
		return unknown
	}
	if _, ambiguous := r.ambiguous[observed]; ambiguous {
		return unknown
	}
	match, found := r.matches[observed]
	if !found {
		return unknown
	}
	quality := normalize.ModelResolved
	if evidence == normalize.ModelInferred {
		quality = normalize.ModelInferred
	} else if match.kind == MatchExact && evidence == normalize.ModelReported {
		quality = normalize.ModelReported
	}
	resolution := Resolution{
		ObservedID: observed, CanonicalID: stringPointer(match.model.CanonicalID),
		Provider: stringPointer(match.model.Provider), Quality: quality,
		Match: match.kind, CatalogRevision: r.revision,
	}
	if match.model.PricingKey != "" {
		resolution.PricingKey = stringPointer(match.model.PricingKey)
	}
	return resolution
}

// ResolveObservation returns a copy with catalog metadata while preserving all
// observed usage and source evidence.
func (r *Resolver) ResolveObservation(observation normalize.Observation) normalize.Observation {
	resolution := r.Resolve(observation.ObservedModelID, observation.ModelResolution)
	observation.CanonicalModelID = resolution.CanonicalID
	observation.ModelProvider = resolution.Provider
	observation.PricingKey = resolution.PricingKey
	observation.ModelResolution = resolution.Quality
	if resolution.Match == MatchUnknown {
		observation.Warnings = appendWarning(observation.Warnings, normalize.Warning{Code: "model_unresolved"})
	}
	return observation
}

func (r *Resolver) add(identifier string, model Model, kind MatchKind) error {
	if identifier == "" {
		return fmt.Errorf("model %q has an empty %s identifier", model.CanonicalID, kind)
	}
	if _, ambiguous := r.ambiguous[identifier]; ambiguous {
		return fmt.Errorf("model identifier %q is also marked ambiguous", identifier)
	}
	if existing, found := r.matches[identifier]; found {
		return fmt.Errorf("model identifier %q maps to both %q and %q", identifier, existing.model.CanonicalID, model.CanonicalID)
	}
	r.matches[identifier] = catalogMatch{model: model, kind: kind}
	return nil
}

func appendWarning(warnings []normalize.Warning, warning normalize.Warning) []normalize.Warning {
	for _, existing := range warnings {
		if existing.Code == warning.Code {
			return warnings
		}
	}
	return append(warnings, warning)
}

func stringPointer(value string) *string {
	copy := value
	return &copy
}
