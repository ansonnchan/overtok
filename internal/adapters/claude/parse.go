package claude

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

// AdapterVersion identifies the Claude-to-normalized mapping contract.
const AdapterVersion = "1"

// Result contains valid observations and safe warning codes from one stream.
type Result struct {
	Observations []normalize.Observation
	Warnings     []normalize.Warning
}

type record struct {
	Type      string    `json:"type"`
	SessionID string    `json:"sessionId"`
	RequestID string    `json:"requestId"`
	Timestamp time.Time `json:"timestamp"`
	Message   struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage struct {
			InputTokens         *uint64 `json:"input_tokens"`
			CacheReadTokens     *uint64 `json:"cache_read_input_tokens"`
			CacheCreationTokens *uint64 `json:"cache_creation_input_tokens"`
			OutputTokens        *uint64 `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// Parse reads Claude assistant usage records and skips all non-usage content.
func Parse(reader io.Reader, surface normalize.Surface, sourceVersion string) (Result, error) {
	lines, err := jsonl.Read(reader, 0)
	if err != nil {
		return Result{}, err
	}
	result := Result{}
	for _, code := range lines.Warnings {
		result.Warnings = append(result.Warnings, normalize.Warning{Code: code})
	}
	for _, line := range lines.Records {
		var parsed record
		if err := json.Unmarshal(line.Data, &parsed); err != nil {
			result.Warnings = append(result.Warnings, normalize.Warning{Code: "malformed_record"})
			continue
		}
		if parsed.Type != "assistant" {
			continue
		}
		tokens := normalize.TokenCounts{
			InputUncached: parsed.Message.Usage.InputTokens, InputCacheRead: parsed.Message.Usage.CacheReadTokens,
			InputCacheWrite: parsed.Message.Usage.CacheCreationTokens, Output: parsed.Message.Usage.OutputTokens,
		}
		if !anyKnown(tokens) || parsed.Timestamp.IsZero() {
			result.Warnings = append(result.Warnings, normalize.Warning{Code: "unsupported_record"})
			continue
		}
		resolution := normalize.ModelReported
		warnings := []normalize.Warning(nil)
		if parsed.Message.Model == "" {
			resolution = normalize.ModelUnknown
			warnings = append(warnings, normalize.Warning{Code: "model_unavailable"})
		}
		session := optionalString(parsed.SessionID)
		observation := normalize.Observation{
			Source: normalize.SourceClaude, Surface: surface, SourceVersion: sourceVersion,
			SourceRecordID: recordID(parsed, line.Number), SessionIDLocal: session, OccurredAt: parsed.Timestamp,
			ObservedModelID: parsed.Message.Model, ModelResolution: resolution, Tokens: tokens,
			CountSemantics: normalize.CountIncremental, Measurement: normalize.MeasurementReported,
			AdapterVersion: AdapterVersion, NormalizationVersion: normalize.SchemaVersion, Warnings: warnings,
		}
		if err := observation.Validate(); err != nil {
			result.Warnings = append(result.Warnings, normalize.Warning{Code: "invalid_normalized_record"})
			continue
		}
		result.Observations = append(result.Observations, observation)
	}
	return result, nil
}

func recordID(parsed record, ordinal int) string {
	// Provider identifiers are stable enough for deduplication but are hashed so
	// normalized state never retains a raw source identifier.
	switch {
	case parsed.RequestID != "":
		return "claude-request-" + safeIdentifier(parsed.RequestID)
	case parsed.Message.ID != "":
		return "claude-message-" + safeIdentifier(parsed.Message.ID)
	default:
		parts := []string{
			parsed.SessionID, parsed.Timestamp.UTC().Format(time.RFC3339Nano), parsed.Message.Model,
			pointerValue(parsed.Message.Usage.InputTokens), pointerValue(parsed.Message.Usage.CacheReadTokens),
			pointerValue(parsed.Message.Usage.CacheCreationTokens), pointerValue(parsed.Message.Usage.OutputTokens), fmt.Sprint(ordinal),
		}
		return "claude-fallback-" + digest(strings.Join(parts, "\x00"))
	}
}

func safeIdentifier(value string) string {
	return digest(value)
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:16])
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

func anyKnown(tokens normalize.TokenCounts) bool {
	return tokens.InputUncached != nil || tokens.InputCacheRead != nil || tokens.InputCacheWrite != nil || tokens.Output != nil
}
