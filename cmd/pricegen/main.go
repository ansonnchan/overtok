// Command pricegen vendors the pinned LiteLLM price file into the model
// catalog. It downloads the exact commit named by the lock file, refuses any
// content whose SHA-256 differs, and writes only first-party token prices.
//
// Run it through `make prices` after changing the lock file. The output is
// deterministic, so an unchanged pin regenerates a byte-identical file.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/ansonnchan/tokenholic/internal/modelcatalog"
)

const (
	maxUpstreamBytes = 64 << 20
	downloadTimeout  = 2 * time.Minute
)

// contextTier matches upstream fields that reprice a request above a prompt
// length, such as input_cost_per_token_above_272k_tokens.
var contextTier = regexp.MustCompile(`_above_[0-9]+k_tokens`)

// microUSDPerMillion converts USD per token to micro-USD per million tokens.
var microUSDPerMillion = new(big.Rat).SetInt(big.NewInt(1_000_000_000_000))

func main() {
	lockPath := flag.String("lock", "internal/modelcatalog/data/litellm.lock.json", "pinned upstream description")
	outputPath := flag.String("output", "internal/modelcatalog/data/litellm_prices.json", "vendored snapshot to write")
	inputPath := flag.String("input", "", "read the upstream file from disk instead of downloading it")
	flag.Parse()
	if err := run(context.Background(), *lockPath, *outputPath, *inputPath); err != nil {
		fmt.Fprintln(os.Stderr, "pricegen:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, lockPath, outputPath, inputPath string) error {
	lockData, err := os.ReadFile(lockPath)
	if err != nil {
		return fmt.Errorf("read price snapshot pin: %w", err)
	}
	var pin modelcatalog.SnapshotPin
	decoder := json.NewDecoder(bytes.NewReader(lockData))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&pin); err != nil {
		return fmt.Errorf("decode price snapshot pin: %w", err)
	}
	if err := pin.Validate(); err != nil {
		return err
	}
	var upstream []byte
	if inputPath != "" {
		upstream, err = os.ReadFile(inputPath)
	} else {
		upstream, err = download(ctx, pin.URL())
	}
	if err != nil {
		return err
	}
	snapshot, err := build(pin, upstream)
	if err != nil {
		return err
	}
	encoded, err := encode(snapshot)
	if err != nil {
		return err
	}
	if err := os.WriteFile(outputPath, encoded, 0o644); err != nil {
		return fmt.Errorf("write vendored price snapshot: %w", err)
	}
	fmt.Printf("vendored %d models from %s@%s\n", len(snapshot.Models), pin.Repository, pin.Commit[:12])
	return nil
}

func download(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create upstream price request: %w", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download upstream prices: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download upstream prices: status %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxUpstreamBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read upstream prices: %w", err)
	}
	if len(data) > maxUpstreamBytes {
		return nil, errors.New("upstream price file exceeds the size limit")
	}
	return data, nil
}

// build verifies the upstream bytes against the pin and reduces them to the
// vendored subset. Nothing is parsed before the checksum matches.
func build(pin modelcatalog.SnapshotPin, upstream []byte) (modelcatalog.Snapshot, error) {
	digest := sha256.Sum256(upstream)
	if actual := hex.EncodeToString(digest[:]); actual != pin.SHA256 {
		return modelcatalog.Snapshot{}, fmt.Errorf("upstream price file SHA-256 is %s, pin expects %s", actual, pin.SHA256)
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(upstream, &entries); err != nil {
		return modelcatalog.Snapshot{}, fmt.Errorf("decode upstream prices: %w", err)
	}
	identifiers := make([]string, 0, len(entries))
	for identifier := range entries {
		identifiers = append(identifiers, identifier)
	}
	sort.Strings(identifiers)
	snapshot := modelcatalog.Snapshot{Source: pin}
	for _, identifier := range identifiers {
		model, keep, err := convert(pin, identifier, entries[identifier])
		if err != nil {
			return modelcatalog.Snapshot{}, fmt.Errorf("model %q: %w", identifier, err)
		}
		if keep {
			snapshot.Models = append(snapshot.Models, model)
		}
	}
	if len(snapshot.Models) == 0 {
		return modelcatalog.Snapshot{}, errors.New("no upstream model matched the pinned providers and modes")
	}
	return snapshot, nil
}

// convert keeps one upstream entry when it is a first-party, token-priced
// model. Prefixed routes and moving aliases are dropped: a route is another
// vendor's price, and an alias would reprice history when it moves.
func convert(pin modelcatalog.SnapshotPin, identifier string, raw json.RawMessage) (modelcatalog.SnapshotModel, bool, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || strings.ContainsAny(identifier, "/ ") {
		return modelcatalog.SnapshotModel{}, false, nil
	}
	if _, alias := fields["alias_of"]; alias {
		return modelcatalog.SnapshotModel{}, false, nil
	}
	provider, mode := text(fields["litellm_provider"]), text(fields["mode"])
	if !slices.Contains(pin.Providers, provider) || !slices.Contains(pin.Modes, mode) {
		return modelcatalog.SnapshotModel{}, false, nil
	}
	_, hasInput := fields["input_cost_per_token"]
	_, hasOutput := fields["output_cost_per_token"]
	if !hasInput && !hasOutput {
		return modelcatalog.SnapshotModel{}, false, nil
	}
	model := modelcatalog.SnapshotModel{ID: identifier, Provider: provider}
	for field := range fields {
		if contextTier.MatchString(field) {
			model.UnpricedReason = modelcatalog.ReasonContextTiered
			return model, true, nil
		}
	}
	targets := []struct {
		field  string
		target **uint64
	}{
		{"input_cost_per_token", &model.Input},
		{"cache_read_input_token_cost", &model.CacheRead},
		{"cache_creation_input_token_cost", &model.CacheWrite},
		{"cache_creation_input_token_cost_above_1hr", &model.CacheWrite1h},
		{"output_cost_per_token", &model.Output},
	}
	for _, pair := range targets {
		value, found := fields[pair.field]
		if !found {
			continue
		}
		rate, err := rateFromCostPerToken(value)
		if err != nil {
			return modelcatalog.SnapshotModel{}, false, fmt.Errorf("%s: %w", pair.field, err)
		}
		*pair.target = &rate
	}
	return model, true, nil
}

// rateFromCostPerToken converts the upstream decimal exactly. A price that is
// not a whole number of micro-dollars per million tokens is an error rather
// than a rounded value.
func rateFromCostPerToken(raw json.RawMessage) (uint64, error) {
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return 0, fmt.Errorf("price is not a number: %w", err)
	}
	value, ok := new(big.Rat).SetString(number.String())
	if !ok || value.Sign() < 0 {
		return 0, fmt.Errorf("price %q is not a non-negative decimal", number)
	}
	value.Mul(value, microUSDPerMillion)
	if !value.IsInt() || !value.Num().IsUint64() {
		return 0, fmt.Errorf("price %q is not a whole number of micro-USD per million tokens", number)
	}
	return value.Num().Uint64(), nil
}

func text(raw json.RawMessage) string {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}

func encode(snapshot modelcatalog.Snapshot) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(snapshot); err != nil {
		return nil, fmt.Errorf("encode vendored price snapshot: %w", err)
	}
	if _, err := modelcatalog.DecodeSnapshot(buffer.Bytes()); err != nil {
		return nil, fmt.Errorf("generated snapshot is invalid: %w", err)
	}
	return buffer.Bytes(), nil
}
