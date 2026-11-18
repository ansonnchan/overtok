package normalize

import "time"

const SchemaVersion = "1"

type Source string

const (
	SourceClaude Source = "claude"
	SourceCodex  Source = "codex"
)

type Surface string

const (
	SurfaceCLI           Surface = "cli"
	SurfaceDesktopCode   Surface = "desktop_code"
	SurfaceDesktopAgent  Surface = "desktop_agent"
	SurfaceDesktopCowork Surface = "desktop_cowork"
	SurfaceDesktop       Surface = "desktop"
	SurfaceSharedLocal   Surface = "shared_local"
)

type ModelResolution string

const (
	ModelReported ModelResolution = "reported"
	ModelResolved ModelResolution = "resolved"
	ModelInferred ModelResolution = "inferred"
	ModelUnknown  ModelResolution = "unknown"
)

type CountSemantics string

const (
	CountIncremental CountSemantics = "incremental"
	CountCumulative  CountSemantics = "cumulative_snapshot"
)

type Measurement string

const (
	MeasurementReported  Measurement = "reported"
	MeasurementDerived   Measurement = "derived"
	MeasurementEstimated Measurement = "estimated"
)

type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

type TokenCounts struct {
	InputUncached   *uint64 `json:"input_uncached_tokens"`
	InputCacheRead  *uint64 `json:"input_cache_read_tokens"`
	InputCacheWrite *uint64 `json:"input_cache_write_tokens"`
	Output          *uint64 `json:"output_tokens"`
	ReasoningOutput *uint64 `json:"reasoning_output_tokens"`
}

type Observation struct {
	Source               Source          `json:"source"`
	Surface              Surface         `json:"surface"`
	SourceVersion        string          `json:"source_version"`
	SourceRecordID       string          `json:"source_record_id"`
	SessionIDLocal       *string         `json:"session_id_local"`
	OccurredAt           time.Time       `json:"occurred_at"`
	ObservedModelID      string          `json:"observed_model_id,omitempty"`
	CanonicalModelID     *string         `json:"canonical_model_id"`
	ModelProvider        *string         `json:"model_provider"`
	PricingKey           *string         `json:"pricing_key"`
	ModelResolution      ModelResolution `json:"model_resolution"`
	Tokens               TokenCounts     `json:"tokens"`
	CountSemantics       CountSemantics  `json:"count_semantics"`
	Measurement          Measurement     `json:"measurement"`
	AdapterVersion       string          `json:"adapter_version"`
	NormalizationVersion string          `json:"normalization_version"`
	Warnings             []Warning       `json:"warnings,omitempty"`
}
