package ccusage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/ansonnchan/overtok/internal/normalize"
)

const (
	// PinnedVersion is the only accepted external executable version.
	PinnedVersion = "20.0.51"
	// SchemaID versions Overtok's expectations for the pinned JSON shape.
	SchemaID = "daily-agent-contract-v1"
	// AdapterVersion identifies conversion from the external schema to observations.
	AdapterVersion = "1"
	defaultLimit   = 16 << 20
)

// ErrOutputTooLarge reports that bounded stdout or stderr was exceeded.
var ErrOutputTooLarge = errors.New("ccusage output exceeds limit")

// Agent selects one pinned ccusage command and report schema.
type Agent string

const (
	// AgentClaude selects the root Claude daily command.
	AgentClaude Agent = "claude"
	// AgentCodex selects the namespaced Codex daily command.
	AgentCodex Agent = "codex"
)

// Executor runs a bounded external command and is injected by tests.
type Executor interface {
	Run(context.Context, string, []string, int) ([]byte, []byte, error)
}

// Client validates and invokes one explicitly configured ccusage executable.
type Client struct {
	Binary   string
	Executor Executor
	Limit    int
}

// Collect checks the pinned version, runs an offline daily report, validates the
// complete schema, and returns deterministic per-model observations.
func (c Client) Collect(ctx context.Context, agent Agent, surface normalize.Surface) ([]normalize.Observation, error) {
	if c.Binary == "" {
		return nil, errors.New("ccusage binary path is required")
	}
	executor := c.Executor
	if executor == nil {
		executor = OSExecutor{}
	}
	limit := c.Limit
	if limit == 0 {
		limit = defaultLimit
	}

	versionOut, _, err := executor.Run(ctx, c.Binary, []string{"--version"}, limit)
	if err != nil {
		return nil, commandError("check version", err)
	}
	if err := ValidateVersion(versionOut); err != nil {
		return nil, err
	}
	args, _, err := commandFor(agent)
	if err != nil {
		return nil, err
	}
	data, _, err := executor.Run(ctx, c.Binary, args, limit)
	if err != nil {
		return nil, commandError("collect usage", err)
	}

	switch agent {
	case AgentClaude:
		report, err := decodeClaudeReport(data)
		if err != nil {
			return nil, fmt.Errorf("validate ccusage Claude output: %w", err)
		}
		return report.observations(surface)
	case AgentCodex:
		report, err := decodeCodexReport(data)
		if err != nil {
			return nil, fmt.Errorf("validate ccusage Codex output: %w", err)
		}
		return report.observations(surface)
	default:
		return nil, fmt.Errorf("unsupported ccusage agent %q", agent)
	}
}

// ValidateVersion accepts only the pinned version with known CLI prefixes.
func ValidateVersion(data []byte) error {
	got := strings.TrimSpace(string(data))
	if got != PinnedVersion && got != "ccusage "+PinnedVersion && got != "ccusage v"+PinnedVersion && got != "v"+PinnedVersion {
		return fmt.Errorf("unsupported ccusage version %q; require %s", got, PinnedVersion)
	}
	return nil
}

func commandFor(agent Agent) ([]string, normalize.Source, error) {
	// Offline prevents metadata fetches, no-cost keeps pricing outside collection,
	// and UTC gives daily aggregates stable identities across local time zones.
	flags := []string{"daily", "--json", "--no-cost", "--offline", "--timezone", "UTC"}
	switch agent {
	case AgentClaude:
		return flags, normalize.SourceClaude, nil
	case AgentCodex:
		return append([]string{"codex"}, flags...), normalize.SourceCodex, nil
	default:
		return nil, "", fmt.Errorf("unsupported ccusage agent %q", agent)
	}
}

func commandError(action string, err error) error {
	// External stderr can contain local paths; keep it outside returned diagnostics.
	return fmt.Errorf("ccusage %s: %w", action, err)
}

// OSExecutor invokes the configured local executable without a shell.
type OSExecutor struct{}

// Run captures bounded stdout and stderr. Callers must not expose stderr because
// external diagnostics may contain identifying local paths.
func (OSExecutor) Run(ctx context.Context, binary string, args []string, limit int) ([]byte, []byte, error) {
	if limit <= 0 {
		return nil, nil, errors.New("output limit must be positive")
	}
	stdout := &cappedBuffer{limit: limit}
	stderr := &cappedBuffer{limit: limit}
	command := exec.CommandContext(ctx, binary, args...)
	command.Env = append(os.Environ(), "NO_COLOR=1")
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	if errors.Is(stdout.err, ErrOutputTooLarge) || errors.Is(stderr.err, ErrOutputTooLarge) {
		return stdout.Bytes(), stderr.Bytes(), ErrOutputTooLarge
	}
	return stdout.Bytes(), stderr.Bytes(), err
}

type cappedBuffer struct {
	buffer bytes.Buffer
	limit  int
	err    error
}

func (b *cappedBuffer) Write(data []byte) (int, error) {
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 || len(data) > remaining {
		if remaining > 0 {
			_, _ = b.buffer.Write(data[:remaining])
		}
		b.err = ErrOutputTooLarge
		return len(data), b.err
	}
	return b.buffer.Write(data)
}

func (b *cappedBuffer) Bytes() []byte { return b.buffer.Bytes() }

// Report counters use pointers so strict validation can distinguish a reported
// zero from a missing or null property in the external contract.
type claudeReport struct {
	Daily  []claudePeriod `json:"daily"`
	Totals *claudeTotals  `json:"totals"`
}

type claudePeriod struct {
	Date                string        `json:"date"`
	InputTokens         *uint64       `json:"inputTokens"`
	OutputTokens        *uint64       `json:"outputTokens"`
	CacheCreationTokens *uint64       `json:"cacheCreationTokens"`
	CacheReadTokens     *uint64       `json:"cacheReadTokens"`
	TotalTokens         *uint64       `json:"totalTokens"`
	ModelsUsed          []string      `json:"modelsUsed"`
	ModelBreakdowns     []claudeModel `json:"modelBreakdowns"`
}

type claudeModel struct {
	ModelName             string  `json:"modelName"`
	InputTokens           *uint64 `json:"inputTokens"`
	OutputTokens          *uint64 `json:"outputTokens"`
	CacheCreationTokens   *uint64 `json:"cacheCreationTokens"`
	CacheCreation1hTokens *uint64 `json:"cacheCreation1hTokens,omitempty"`
	CacheReadTokens       *uint64 `json:"cacheReadTokens"`
	MissingPricing        *bool   `json:"missingPricing,omitempty"`
}

type claudeTotals struct {
	InputTokens         *uint64  `json:"inputTokens"`
	OutputTokens        *uint64  `json:"outputTokens"`
	CacheCreationTokens *uint64  `json:"cacheCreationTokens"`
	CacheReadTokens     *uint64  `json:"cacheReadTokens"`
	TotalTokens         *uint64  `json:"totalTokens"`
	UnpricedModels      []string `json:"unpricedModels,omitempty"`
}

type codexReport struct {
	Daily  []codexPeriod `json:"daily"`
	Totals *codexTotals  `json:"totals"`
}

type codexPeriod struct {
	Date                  string                `json:"date"`
	InputTokens           *uint64               `json:"inputTokens"`
	OutputTokens          *uint64               `json:"outputTokens"`
	CacheCreationTokens   *uint64               `json:"cacheCreationTokens"`
	CacheReadTokens       *uint64               `json:"cacheReadTokens"`
	ReasoningOutputTokens *uint64               `json:"reasoningOutputTokens"`
	TotalTokens           *uint64               `json:"totalTokens"`
	Models                map[string]codexModel `json:"models"`
}

type codexModel struct {
	InputTokens           *uint64 `json:"inputTokens"`
	OutputTokens          *uint64 `json:"outputTokens"`
	CacheCreationTokens   *uint64 `json:"cacheCreationTokens"`
	CacheReadTokens       *uint64 `json:"cacheReadTokens"`
	ReasoningOutputTokens *uint64 `json:"reasoningOutputTokens"`
	TotalTokens           *uint64 `json:"totalTokens"`
	IsFallback            *bool   `json:"isFallback,omitempty"`
	MissingPricing        *bool   `json:"missingPricing,omitempty"`
}

type codexTotals struct {
	InputTokens           *uint64  `json:"inputTokens"`
	OutputTokens          *uint64  `json:"outputTokens"`
	CacheCreationTokens   *uint64  `json:"cacheCreationTokens"`
	CacheReadTokens       *uint64  `json:"cacheReadTokens"`
	ReasoningOutputTokens *uint64  `json:"reasoningOutputTokens"`
	TotalTokens           *uint64  `json:"totalTokens"`
	UnpricedModels        []string `json:"unpricedModels,omitempty"`
}

type numericTotals struct {
	input, output, cacheWrite, cacheRead, reasoning, total uint64
}

func decodeClaudeReport(data []byte) (claudeReport, error) {
	var report claudeReport
	if err := strictDecode(data, &report); err != nil {
		return report, err
	}
	return report, report.validate()
}

func decodeCodexReport(data []byte) (codexReport, error) {
	var report codexReport
	if err := strictDecode(data, &report); err != nil {
		return report, err
	}
	return report, report.validate()
}

func strictDecode(data []byte, target any) error {
	// encoding/json accepts duplicate keys by keeping the last value. Reject them
	// first so malformed external output cannot silently change meaning.
	if err := rejectDuplicateKeys(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	return expectEOF(decoder)
}

func (r claudeReport) validate() error {
	if r.Daily == nil || r.Totals == nil {
		return errors.New("daily and totals are required")
	}
	seenDates := make(map[string]struct{})
	var reportSum numericTotals
	for _, day := range r.Daily {
		if err := validateDate(day.Date, seenDates); err != nil {
			return err
		}
		if day.TotalTokens == nil {
			return fmt.Errorf("daily period %q: required totalTokens is missing or null", day.Date)
		}
		dayCounts, err := requiredCounts(day.InputTokens, day.OutputTokens, day.CacheCreationTokens, day.CacheReadTokens, nil, day.TotalTokens, false)
		if err != nil {
			return fmt.Errorf("daily period %q: %w", day.Date, err)
		}
		if len(day.ModelBreakdowns) == 0 || day.ModelsUsed == nil {
			return fmt.Errorf("daily period %q requires model breakdowns and modelsUsed", day.Date)
		}
		models := make(map[string]struct{})
		var modelSum numericTotals
		for _, model := range day.ModelBreakdowns {
			if model.ModelName == "" {
				return fmt.Errorf("daily period %q has an empty model name", day.Date)
			}
			if _, found := models[model.ModelName]; found {
				return fmt.Errorf("daily period %q repeats model %q", day.Date, model.ModelName)
			}
			models[model.ModelName] = struct{}{}
			counts, err := requiredCounts(model.InputTokens, model.OutputTokens, model.CacheCreationTokens, model.CacheReadTokens, nil, nil, false)
			if err != nil {
				return fmt.Errorf("daily period %q model %q: %w", day.Date, model.ModelName, err)
			}
			if model.CacheCreation1hTokens != nil && *model.CacheCreation1hTokens > counts.cacheWrite {
				return fmt.Errorf("daily period %q model %q has invalid 1h cache split", day.Date, model.ModelName)
			}
			if err := modelSum.add(counts); err != nil {
				return err
			}
		}
		if err := modelSum.equal(dayCounts); err != nil {
			return fmt.Errorf("daily period %q: %w", day.Date, err)
		}
		if err := validateModelsUsed(models, day.ModelsUsed, day.Date); err != nil {
			return err
		}
		if err := reportSum.add(dayCounts); err != nil {
			return err
		}
	}
	if r.Totals.TotalTokens == nil {
		return errors.New("totals: required totalTokens is missing or null")
	}
	totals, err := requiredCounts(r.Totals.InputTokens, r.Totals.OutputTokens, r.Totals.CacheCreationTokens, r.Totals.CacheReadTokens, nil, r.Totals.TotalTokens, false)
	if err != nil {
		return fmt.Errorf("totals: %w", err)
	}
	return reportSum.equal(totals)
}

func (r codexReport) validate() error {
	if r.Daily == nil || r.Totals == nil {
		return errors.New("daily and totals are required")
	}
	seenDates := make(map[string]struct{})
	var reportSum numericTotals
	for _, day := range r.Daily {
		if err := validateDate(day.Date, seenDates); err != nil {
			return err
		}
		if day.TotalTokens == nil {
			return fmt.Errorf("daily period %q: required totalTokens is missing or null", day.Date)
		}
		dayCounts, err := requiredCounts(day.InputTokens, day.OutputTokens, day.CacheCreationTokens, day.CacheReadTokens, day.ReasoningOutputTokens, day.TotalTokens, true)
		if err != nil {
			return fmt.Errorf("daily period %q: %w", day.Date, err)
		}
		if len(day.Models) == 0 {
			return fmt.Errorf("daily period %q has no models", day.Date)
		}
		var modelSum numericTotals
		for name, model := range day.Models {
			if name == "" {
				return fmt.Errorf("daily period %q has an empty model name", day.Date)
			}
			if model.TotalTokens == nil {
				return fmt.Errorf("daily period %q model %q: required totalTokens is missing or null", day.Date, name)
			}
			counts, err := requiredCounts(model.InputTokens, model.OutputTokens, model.CacheCreationTokens, model.CacheReadTokens, model.ReasoningOutputTokens, model.TotalTokens, true)
			if err != nil {
				return fmt.Errorf("daily period %q model %q: %w", day.Date, name, err)
			}
			if err := modelSum.add(counts); err != nil {
				return err
			}
		}
		if err := modelSum.equal(dayCounts); err != nil {
			return fmt.Errorf("daily period %q: %w", day.Date, err)
		}
		if err := reportSum.add(dayCounts); err != nil {
			return err
		}
	}
	if r.Totals.TotalTokens == nil {
		return errors.New("totals: required totalTokens is missing or null")
	}
	totals, err := requiredCounts(r.Totals.InputTokens, r.Totals.OutputTokens, r.Totals.CacheCreationTokens, r.Totals.CacheReadTokens, r.Totals.ReasoningOutputTokens, r.Totals.TotalTokens, true)
	if err != nil {
		return fmt.Errorf("totals: %w", err)
	}
	return reportSum.equal(totals)
}

func requiredCounts(input, output, cacheWrite, cacheRead, reasoning, total *uint64, requireReasoning bool) (numericTotals, error) {
	if input == nil || output == nil || cacheWrite == nil || cacheRead == nil || requireReasoning && reasoning == nil {
		return numericTotals{}, errors.New("required token count is missing or null")
	}
	counts := numericTotals{input: *input, output: *output, cacheWrite: *cacheWrite, cacheRead: *cacheRead}
	if reasoning != nil {
		counts.reasoning = *reasoning
		if counts.reasoning > counts.output {
			return numericTotals{}, errors.New("reasoning output exceeds output")
		}
	}
	if total != nil {
		counts.total = *total
	} else {
		calculated, err := sumValues(counts.input, counts.output, counts.cacheWrite, counts.cacheRead)
		if err != nil {
			return numericTotals{}, err
		}
		counts.total = calculated
		return counts, nil
	}
	// Reasoning is a subset of output in this contract and must not be added twice.
	calculated, err := sumValues(counts.input, counts.output, counts.cacheWrite, counts.cacheRead)
	if err != nil {
		return numericTotals{}, err
	}
	if counts.total != calculated {
		return numericTotals{}, errors.New("totalTokens does not match token categories")
	}
	return counts, nil
}

func (t *numericTotals) add(value numericTotals) error {
	fields := []struct {
		target *uint64
		value  uint64
	}{
		{&t.input, value.input}, {&t.output, value.output}, {&t.cacheWrite, value.cacheWrite},
		{&t.cacheRead, value.cacheRead}, {&t.reasoning, value.reasoning}, {&t.total, value.total},
	}
	for _, field := range fields {
		if math.MaxUint64-*field.target < field.value {
			return errors.New("token count overflows uint64")
		}
		*field.target += field.value
	}
	return nil
}

func (t numericTotals) equal(other numericTotals) error {
	if t != other {
		return errors.New("declared totals do not match model breakdowns")
	}
	return nil
}

func sumValues(values ...uint64) (uint64, error) {
	var total uint64
	for _, value := range values {
		if math.MaxUint64-total < value {
			return 0, errors.New("total token count overflows uint64")
		}
		total += value
	}
	return total, nil
}

func validateDate(date string, seen map[string]struct{}) error {
	if _, found := seen[date]; found {
		return fmt.Errorf("duplicate daily period %q", date)
	}
	seen[date] = struct{}{}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return fmt.Errorf("invalid daily period %q", date)
	}
	return nil
}

func validateModelsUsed(models map[string]struct{}, used []string, date string) error {
	if len(models) != len(used) {
		return fmt.Errorf("daily period %q modelsUsed does not match modelBreakdowns", date)
	}
	seen := make(map[string]struct{})
	for _, model := range used {
		if _, found := models[model]; !found {
			return fmt.Errorf("daily period %q modelsUsed contains unmatched model %q", date, model)
		}
		if _, found := seen[model]; found {
			return fmt.Errorf("daily period %q modelsUsed repeats model %q", date, model)
		}
		seen[model] = struct{}{}
	}
	return nil
}

func (r claudeReport) observations(surface normalize.Surface) ([]normalize.Observation, error) {
	observations := make([]normalize.Observation, 0)
	for _, day := range r.Daily {
		occurredAt, _ := time.Parse("2006-01-02", day.Date)
		for _, model := range day.ModelBreakdowns {
			tokens := normalize.TokenCounts{
				InputUncached: cloneUint(model.InputTokens), InputCacheRead: cloneUint(model.CacheReadTokens),
				InputCacheWrite: cloneUint(model.CacheCreationTokens), Output: cloneUint(model.OutputTokens),
			}
			warnings := pricingWarnings(model.MissingPricing)
			observation := baseObservation(normalize.SourceClaude, surface, day.Date, occurredAt, model.ModelName, normalize.ModelReported, tokens, warnings)
			if err := observation.Validate(); err != nil {
				return nil, fmt.Errorf("normalize %s %s: %w", day.Date, model.ModelName, err)
			}
			observations = append(observations, observation)
		}
	}
	return observations, nil
}

func (r codexReport) observations(surface normalize.Surface) ([]normalize.Observation, error) {
	observations := make([]normalize.Observation, 0)
	for _, day := range r.Daily {
		occurredAt, _ := time.Parse("2006-01-02", day.Date)
		models := make([]string, 0, len(day.Models))
		for model := range day.Models {
			models = append(models, model)
		}
		sort.Strings(models)
		for _, name := range models {
			model := day.Models[name]
			tokens := normalize.TokenCounts{
				InputUncached: cloneUint(model.InputTokens), InputCacheRead: cloneUint(model.CacheReadTokens),
				InputCacheWrite: cloneUint(model.CacheCreationTokens), Output: cloneUint(model.OutputTokens),
				ReasoningOutput: cloneUint(model.ReasoningOutputTokens),
			}
			// ccusage has already associated snapshots with this map key outside
			// Overtok's native record context, so preserve it as inferred.
			warnings := append([]normalize.Warning{{Code: "model_inferred"}}, pricingWarnings(model.MissingPricing)...)
			observation := baseObservation(normalize.SourceCodex, surface, day.Date, occurredAt, name, normalize.ModelInferred, tokens, warnings)
			if err := observation.Validate(); err != nil {
				return nil, fmt.Errorf("normalize %s %s: %w", day.Date, name, err)
			}
			observations = append(observations, observation)
		}
	}
	return observations, nil
}

func baseObservation(source normalize.Source, surface normalize.Surface, date string, occurredAt time.Time, model string, resolution normalize.ModelResolution, tokens normalize.TokenCounts, warnings []normalize.Warning) normalize.Observation {
	return normalize.Observation{
		Source: source, Surface: surface, SourceVersion: "ccusage/" + PinnedVersion,
		SourceRecordID: stableID(source, surface, date, model), OccurredAt: occurredAt,
		ObservedModelID: model, ModelResolution: resolution, Tokens: tokens,
		CountSemantics: normalize.CountCumulative, Measurement: normalize.MeasurementDerived,
		AdapterVersion: AdapterVersion, NormalizationVersion: normalize.SchemaVersion, Warnings: warnings,
	}
}

func pricingWarnings(missing *bool) []normalize.Warning {
	if missing != nil && *missing {
		return []normalize.Warning{{Code: "pricing_unavailable"}}
	}
	return nil
}

func cloneUint(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func stableID(source normalize.Source, surface normalize.Surface, period, model string) string {
	// Contract version, source, surface, day, and model uniquely identify an
	// absolute daily aggregate without retaining any provider record content.
	sum := sha256.Sum256([]byte(strings.Join([]string{SchemaID, string(source), string(surface), period, model}, "\x00")))
	return "ccusage-" + hex.EncodeToString(sum[:16])
}

func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := consumeValue(decoder); err != nil {
		return fmt.Errorf("decode JSON tokens: %w", err)
	}
	return expectEOF(decoder)
}

func consumeValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		keys := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, found := keys[key]; found {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			keys[key] = struct{}{}
			if err := consumeValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := consumeValue(decoder); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected delimiter %q", delimiter)
	}
	_, err = decoder.Token()
	return err
}

func expectEOF(decoder *json.Decoder) error {
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return fmt.Errorf("trailing JSON: %w", err)
	}
	return nil
}
