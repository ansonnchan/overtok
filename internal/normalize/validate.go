package normalize

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

func (o Observation) Validate() error {
	if !validSource(o.Source) {
		return fmt.Errorf("source %q is unsupported", o.Source)
	}
	if !validSurface(o.Source, o.Surface) {
		return fmt.Errorf("surface %q is unsupported for source %q", o.Surface, o.Source)
	}
	if o.SourceVersion == "" || o.SourceRecordID == "" || o.AdapterVersion == "" || o.NormalizationVersion == "" {
		return errors.New("source, record, adapter, and normalization versions are required")
	}
	if o.OccurredAt.IsZero() {
		return errors.New("occurred_at is required")
	}
	if err := o.validateModel(); err != nil {
		return err
	}
	if !validCountSemantics(o.CountSemantics) {
		return fmt.Errorf("count semantics %q is invalid", o.CountSemantics)
	}
	if !validMeasurement(o.Measurement) {
		return fmt.Errorf("measurement %q is invalid", o.Measurement)
	}
	if !o.Tokens.anyKnown() {
		return errors.New("at least one token category must be known")
	}
	if o.Tokens.Output != nil && o.Tokens.ReasoningOutput != nil && *o.Tokens.ReasoningOutput > *o.Tokens.Output {
		return errors.New("reasoning output cannot exceed output")
	}
	for _, warning := range o.Warnings {
		if warning.Code == "" {
			return errors.New("warning code is required")
		}
		if strings.ContainsAny(warning.Message, "\r\n") {
			return fmt.Errorf("warning %q message must be one line", warning.Code)
		}
	}
	return nil
}

func (o Observation) validateModel() error {
	switch o.ModelResolution {
	case ModelReported:
		if o.ObservedModelID == "" {
			return errors.New("reported model requires an observed identifier")
		}
	case ModelResolved:
		if o.ObservedModelID == "" || empty(o.CanonicalModelID) {
			return errors.New("resolved model requires observed and canonical identifiers")
		}
	case ModelInferred:
		if o.ObservedModelID == "" || !hasWarning(o.Warnings, "model_inferred") {
			return errors.New("inferred model requires an observed identifier and model_inferred warning")
		}
	case ModelUnknown:
		if o.CanonicalModelID != nil || o.PricingKey != nil {
			return errors.New("unknown model cannot have canonical or pricing identifiers")
		}
	default:
		return fmt.Errorf("model resolution %q is invalid", o.ModelResolution)
	}
	return nil
}

func (t TokenCounts) KnownTotal() (*uint64, error) {
	var total uint64
	known := false
	for _, value := range []*uint64{t.InputUncached, t.InputCacheRead, t.InputCacheWrite, t.Output} {
		if value == nil {
			continue
		}
		known = true
		if math.MaxUint64-total < *value {
			return nil, errors.New("token total overflows uint64")
		}
		total += *value
	}
	if !known {
		return nil, nil
	}
	return &total, nil
}

func (t TokenCounts) anyKnown() bool {
	return t.InputUncached != nil || t.InputCacheRead != nil || t.InputCacheWrite != nil || t.Output != nil || t.ReasoningOutput != nil
}

func (c Capabilities) Validate() error {
	fields := []CapabilitySupport{c.Model, c.InputUncached, c.InputCacheRead, c.InputCacheWrite, c.Output, c.ReasoningOutput}
	for _, field := range fields {
		if field != CapabilityReported && field != CapabilityDerived && field != CapabilityInferred && field != CapabilityUnsupported {
			return fmt.Errorf("capability support %q is invalid", field)
		}
	}
	return nil
}

func validSource(source Source) bool {
	return source == SourceClaude || source == SourceCodex
}

func validSurface(source Source, surface Surface) bool {
	switch source {
	case SourceClaude:
		return surface == SurfaceCLI || surface == SurfaceDesktopCode || surface == SurfaceDesktopAgent || surface == SurfaceDesktopCowork
	case SourceCodex:
		return surface == SurfaceCLI || surface == SurfaceDesktop || surface == SurfaceSharedLocal
	default:
		return false
	}
}

func validCountSemantics(value CountSemantics) bool {
	return value == CountIncremental || value == CountCumulative
}

func validMeasurement(value Measurement) bool {
	return value == MeasurementReported || value == MeasurementDerived || value == MeasurementEstimated
}

func empty(value *string) bool {
	return value == nil || *value == ""
}

func hasWarning(warnings []Warning, code string) bool {
	for _, warning := range warnings {
		if warning.Code == code {
			return true
		}
	}
	return false
}
