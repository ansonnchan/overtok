package normalize

type CapabilitySupport string

const (
	CapabilityReported    CapabilitySupport = "reported"
	CapabilityDerived     CapabilitySupport = "derived"
	CapabilityInferred    CapabilitySupport = "inferred"
	CapabilityUnsupported CapabilitySupport = "unsupported"
)

type Capabilities struct {
	Model           CapabilitySupport `json:"model"`
	InputUncached   CapabilitySupport `json:"input_uncached_tokens"`
	InputCacheRead  CapabilitySupport `json:"input_cache_read_tokens"`
	InputCacheWrite CapabilitySupport `json:"input_cache_write_tokens"`
	Output          CapabilitySupport `json:"output_tokens"`
	ReasoningOutput CapabilitySupport `json:"reasoning_output_tokens"`
}

type HealthStatus string

const (
	HealthDetected         HealthStatus = "detected"
	HealthNotFound         HealthStatus = "not_found"
	HealthPermissionDenied HealthStatus = "permission_denied"
	HealthUnsupported      HealthStatus = "unsupported"
	HealthDegraded         HealthStatus = "degraded"
	HealthError            HealthStatus = "error"
)

type SourceHealth struct {
	Source       Source       `json:"source"`
	Surface      Surface      `json:"surface"`
	Status       HealthStatus `json:"status"`
	RecordCount  *uint64      `json:"record_count"`
	Format       string       `json:"format,omitempty"`
	Capabilities Capabilities `json:"capabilities"`
	Warnings     []Warning    `json:"warnings,omitempty"`
}
