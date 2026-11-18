package codex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ansonnchan/overtok/internal/adapters/jsonl"
	"github.com/ansonnchan/overtok/internal/normalize"
)

// AdapterVersion identifies the Codex-to-normalized mapping contract.
const AdapterVersion = "1"

// Result contains valid observations and safe warning codes from one rollout.
type Result struct {
	Observations []normalize.Observation
	Warnings     []normalize.Warning
}

type record struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Payload   struct {
		Type      string `json:"type"`
		Model     string `json:"model"`
		SessionID string `json:"session_id"`
		Info      struct {
			Total tokenUsage `json:"total_token_usage"`
		} `json:"info"`
	} `json:"payload"`
}

type tokenUsage struct {
	InputTokens           *uint64 `json:"input_tokens"`
	CachedInputTokens     *uint64 `json:"cached_input_tokens"`
	OutputTokens          *uint64 `json:"output_tokens"`
	ReasoningOutputTokens *uint64 `json:"reasoning_output_tokens"`
}

// Parse follows turn context and token events in source order. logicalFileID
// isolates cumulative state when the rollout does not expose a session ID.
func Parse(reader io.Reader, surface normalize.Surface, sourceVersion, logicalFileID string) (Result, error) {
	lines, err := jsonl.Read(reader, 0)
	if err != nil {
		return Result{}, err
	}
	result := Result{}
	for _, code := range lines.Warnings {
		result.Warnings = append(result.Warnings, normalize.Warning{Code: code})
	}
	var activeModel string
	sessionID := logicalFileID
	for _, line := range lines.Records {
		var parsed record
		if err := json.Unmarshal(line.Data, &parsed); err != nil {
			result.Warnings = append(result.Warnings, normalize.Warning{Code: "malformed_record"})
			continue
		}
		switch parsed.Type {
		case "turn_context":
			// Token events omit the model, so the active turn context makes later
			// attribution inferred rather than directly reported.
			activeModel = parsed.Payload.Model
			if parsed.Payload.SessionID != "" {
				sessionID = parsed.Payload.SessionID
			}
		case "event_msg":
			if parsed.Payload.Type != "token_count" {
				continue
			}
			observation, warning, ok := usageObservation(parsed, activeModel, sessionID, surface, sourceVersion)
			if warning.Code != "" {
				result.Warnings = append(result.Warnings, warning)
			}
			if ok {
				result.Observations = append(result.Observations, observation)
			}
		}
	}
	return result, nil
}

func usageObservation(parsed record, model, session string, surface normalize.Surface, sourceVersion string) (normalize.Observation, normalize.Warning, bool) {
	usage := parsed.Payload.Info.Total
	if usage.InputTokens == nil && usage.CachedInputTokens == nil && usage.OutputTokens == nil && usage.ReasoningOutputTokens == nil {
		return normalize.Observation{}, normalize.Warning{Code: "unsupported_record"}, false
	}
	if parsed.Timestamp.IsZero() {
		return normalize.Observation{}, normalize.Warning{Code: "unsupported_record"}, false
	}
	uncached, valid := uncachedInput(usage.InputTokens, usage.CachedInputTokens)
	if !valid {
		return normalize.Observation{}, normalize.Warning{Code: "invalid_token_relationship"}, false
	}
	tokens := normalize.TokenCounts{
		InputUncached: uncached, InputCacheRead: usage.CachedInputTokens,
		Output: usage.OutputTokens, ReasoningOutput: usage.ReasoningOutputTokens,
	}
	resolution := normalize.ModelInferred
	warnings := []normalize.Warning{{Code: "model_inferred"}}
	if model == "" {
		resolution = normalize.ModelUnknown
		warnings = []normalize.Warning{{Code: "model_unavailable"}}
	}
	sessionLocal := optionalString(session)
	observation := normalize.Observation{
		Source: normalize.SourceCodex, Surface: surface, SourceVersion: sourceVersion,
		SourceRecordID: recordID(session, parsed.Timestamp, model, tokens), SessionIDLocal: sessionLocal, OccurredAt: parsed.Timestamp,
		ObservedModelID: model, ModelResolution: resolution, Tokens: tokens,
		CountSemantics: normalize.CountCumulative, Measurement: normalize.MeasurementReported,
		AdapterVersion: AdapterVersion, NormalizationVersion: normalize.SchemaVersion, Warnings: warnings,
	}
	if err := observation.Validate(); err != nil {
		return normalize.Observation{}, normalize.Warning{Code: "invalid_normalized_record"}, false
	}
	return observation, normalize.Warning{}, true
}

func uncachedInput(total, cached *uint64) (*uint64, bool) {
	// Codex input includes cached input. Normalize to non-overlapping categories
	// before any total is calculated.
	if total == nil {
		return nil, true
	}
	if cached == nil {
		value := *total
		return &value, true
	}
	if *cached > *total {
		return nil, false
	}
	value := *total - *cached
	return &value, true
}

func recordID(session string, timestamp time.Time, model string, tokens normalize.TokenCounts) string {
	// Overlapping CLI/Desktop roots produce the same identity because only source
	// semantics participate; storage paths and raw event content do not.
	parts := []string{
		"codex-rollout-v1", session, timestamp.UTC().Format(time.RFC3339Nano), model,
		pointerValue(tokens.InputUncached), pointerValue(tokens.InputCacheRead),
		pointerValue(tokens.Output), pointerValue(tokens.ReasoningOutput),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "codex-snapshot-" + hex.EncodeToString(sum[:16])
}

func pointerValue(value *uint64) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprint(*value)
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
