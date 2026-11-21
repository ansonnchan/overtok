package modelcatalog

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

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

// Model describes one canonical model independently from observed aliases and
// pricing. A model without a PricingKey says why in UnpricedReason.
type Model struct {
	CanonicalID     string
	Provider        string
	PricingKey      string
	UnpricedReason  string
	ExactIDs        []string
	Aliases         []string
	ReviewedMapping []string
}

// AliasRules are the only pattern-based mappings the resolver applies. Each
// strips a suffix and then requires an exact catalog match for what remains;
// neither guesses a model family or version.
type AliasRules struct {
	// ContextMarkers are client-appended context-window labels such as "[1m]".
	// The match is recorded as reviewed because the marker is a source
	// convention, not a provider identifier.
	ContextMarkers []string
	// DatedSnapshots strips a trailing -YYYYMMDD or -YYYY-MM-DD snapshot date,
	// the documented form of a pinned provider model ID.
	DatedSnapshots bool
}

// Unresolvable is an identifier that must stay unknown, with the reason shown
// to users instead of a generic "not in catalog".
type Unresolvable struct {
	ID     string
	Reason string
}

// Catalog is an immutable model-resolution revision.
type Catalog struct {
	Revision         string
	Models           []Model
	AmbiguousAliases []string
	Unresolvable     []Unresolvable
	AliasRules       AliasRules
}

// Resolution preserves the observed identifier and explains the canonical
// match. UnpricedReason is set whenever PricingKey is nil.
type Resolution struct {
	ObservedID      string
	CanonicalID     *string
	Provider        *string
	PricingKey      *string
	UnpricedReason  string
	Quality         normalize.ModelResolution
	Match           MatchKind
	CatalogRevision string
}

// Resolver applies one validated catalog revision.
type Resolver struct {
	revision     string
	matches      map[string]catalogMatch
	unresolvable map[string]string
	rules        AliasRules
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
		unresolvable: make(map[string]string), rules: catalog.AliasRules,
	}
	unresolvable := append([]Unresolvable(nil), catalog.Unresolvable...)
	for _, alias := range catalog.AmbiguousAliases {
		unresolvable = append(unresolvable, Unresolvable{ID: alias, Reason: ReasonAmbiguousAlias})
	}
	for _, entry := range unresolvable {
		if entry.ID == "" || entry.Reason == "" {
			return nil, errors.New("unresolvable model identifier requires an ID and reason")
		}
		if _, exists := resolver.unresolvable[entry.ID]; exists {
			return nil, fmt.Errorf("unresolvable model identifier %q is duplicated", entry.ID)
		}
		resolver.unresolvable[entry.ID] = entry.Reason
	}
	for _, marker := range catalog.AliasRules.ContextMarkers {
		if marker == "" {
			return nil, errors.New("context marker cannot be empty")
		}
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

// Resolve maps an observed identifier through exact entries and the catalog's
// explicit alias rules. It never guesses a routing alias or a model family.
func (r *Resolver) Resolve(observed string, evidence normalize.ModelResolution) Resolution {
	unknown := Resolution{
		ObservedID: observed, Quality: normalize.ModelUnknown, UnpricedReason: ReasonUnknownModel,
		Match: MatchUnknown, CatalogRevision: r.revision,
	}
	if observed == "" || evidence == normalize.ModelUnknown {
		unknown.UnpricedReason = ReasonModelUnavailable
		return unknown
	}
	match, reason, found := r.lookup(observed)
	if !found {
		if reason != "" {
			unknown.UnpricedReason = reason
		}
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
	} else if resolution.UnpricedReason = match.model.UnpricedReason; resolution.UnpricedReason == "" {
		resolution.UnpricedReason = ReasonNoPublishedPrice
	}
	return resolution
}

// lookup finds an exact entry first, then applies the alias rules. A rule
// only ever weakens the recorded match kind. reason is set when the
// identifier, with or without its suffixes, is deliberately unresolvable.
func (r *Resolver) lookup(observed string) (match catalogMatch, reason string, found bool) {
	if reason, blocked := r.unresolvable[observed]; blocked {
		return catalogMatch{}, reason, false
	}
	if match, found := r.matches[observed]; found {
		return match, "", true
	}
	base, kind := observed, MatchExact
	for _, marker := range r.rules.ContextMarkers {
		if trimmed, cut := strings.CutSuffix(base, marker); cut && trimmed != "" {
			base, kind = trimmed, MatchReviewed
			break
		}
	}
	candidates := []struct {
		id   string
		kind MatchKind
	}{{base, kind}}
	if r.rules.DatedSnapshots {
		if undated, cut := cutSnapshotDate(base); cut {
			if kind == MatchExact {
				kind = MatchAlias
			}
			candidates = append(candidates, struct {
				id   string
				kind MatchKind
			}{undated, kind})
		}
	}
	for _, candidate := range candidates {
		if candidate.id == observed {
			continue
		}
		if reason, blocked := r.unresolvable[candidate.id]; blocked {
			return catalogMatch{}, reason, false
		}
		if match, found := r.matches[candidate.id]; found {
			if matchRank(candidate.kind) > matchRank(match.kind) {
				match.kind = candidate.kind
			}
			return match, "", true
		}
	}
	return catalogMatch{}, "", false
}

// cutSnapshotDate removes a trailing, calendar-valid snapshot date.
func cutSnapshotDate(identifier string) (string, bool) {
	for _, layout := range []string{"20060102", time.DateOnly} {
		cut := len(identifier) - len(layout) - 1
		if cut <= 0 || identifier[cut] != '-' {
			continue
		}
		if _, err := time.Parse(layout, identifier[cut+1:]); err == nil {
			return identifier[:cut], true
		}
	}
	return "", false
}

func matchRank(kind MatchKind) int {
	switch kind {
	case MatchExact:
		return 0
	case MatchAlias:
		return 1
	default:
		return 2
	}
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
	if _, blocked := r.unresolvable[identifier]; blocked {
		return fmt.Errorf("model identifier %q is also marked unresolvable", identifier)
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
