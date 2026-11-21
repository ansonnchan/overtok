package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/ansonnchan/tokenholic/internal/modelcatalog"
)

// upstream is a synthetic file in the upstream shape. It exercises every
// keep and drop rule without copying real price data into the test.
const upstream = `{
  "sample_spec": {"litellm_provider": "one of the providers", "mode": "chat", "input_cost_per_token": 0.0},
  "model-flat": {"litellm_provider": "anthropic", "mode": "chat", "input_cost_per_token": 0.000004,
    "output_cost_per_token": 0.00002, "cache_read_input_token_cost": 2E-7,
    "cache_creation_input_token_cost": 0.000005, "cache_creation_input_token_cost_above_1hr": 0.000008,
    "input_cost_per_token_batches": 0.000002, "max_input_tokens": 1000000, "supports_vision": true},
  "model-fraction": {"litellm_provider": "openai", "mode": "responses", "input_cost_per_token": 3.125E-7, "output_cost_per_token": 1.5625e-06},
  "model-tiered": {"litellm_provider": "openai", "mode": "chat", "input_cost_per_token": 0.000004,
    "output_cost_per_token": 0.00002, "input_cost_per_token_above_272k_tokens": 0.000008},
  "model-latest": {"alias_of": "model-flat"},
  "other/model-flat": {"litellm_provider": "anthropic", "mode": "chat", "input_cost_per_token": 0.000004, "output_cost_per_token": 0.00002},
  "model-embedding": {"litellm_provider": "openai", "mode": "embedding", "input_cost_per_token": 1E-7},
  "model-image": {"litellm_provider": "openai", "mode": "chat", "output_cost_per_image": 0.04},
  "model-elsewhere": {"litellm_provider": "bedrock", "mode": "chat", "input_cost_per_token": 0.000004, "output_cost_per_token": 0.00002}
}`

func TestBuildKeepsOnlyFirstPartyTokenPrices(t *testing.T) {
	t.Parallel()
	snapshot, err := build(pinFor(upstream), []byte(upstream))
	if err != nil {
		t.Fatal(err)
	}
	value := func(v uint64) *uint64 { return &v }
	want := []modelcatalog.SnapshotModel{
		{ID: "model-flat", Provider: "anthropic", Input: value(4_000_000), CacheRead: value(200_000), CacheWrite: value(5_000_000), CacheWrite1h: value(8_000_000), Output: value(20_000_000)},
		{ID: "model-fraction", Provider: "openai", Input: value(312_500), Output: value(1_562_500)},
		{ID: "model-tiered", Provider: "openai", UnpricedReason: modelcatalog.ReasonContextTiered},
	}
	if len(snapshot.Models) != len(want) {
		t.Fatalf("models = %#v, want %d entries", snapshot.Models, len(want))
	}
	for index, model := range snapshot.Models {
		if model.ID != want[index].ID || model.Provider != want[index].Provider || model.UnpricedReason != want[index].UnpricedReason ||
			!same(model.Input, want[index].Input) || !same(model.CacheRead, want[index].CacheRead) || !same(model.CacheWrite, want[index].CacheWrite) ||
			!same(model.CacheWrite1h, want[index].CacheWrite1h) || !same(model.Output, want[index].Output) {
			t.Errorf("model %d = %#v, want %#v", index, model, want[index])
		}
	}

	first, err := encode(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	again, err := build(pinFor(upstream), []byte(upstream))
	if err != nil {
		t.Fatal(err)
	}
	second, err := encode(again)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || !bytes.HasSuffix(first, []byte("}\n")) {
		t.Fatal("generated snapshot is not deterministic")
	}
	if strings.Contains(string(first), "supports_vision") || strings.Contains(string(first), "batches") {
		t.Fatalf("generated snapshot kept a non-price field:\n%s", first)
	}
}

func TestBuildRefusesUnverifiedOrUnrepresentableInput(t *testing.T) {
	t.Parallel()
	const subMicro = `{"model-a": {"litellm_provider": "anthropic", "mode": "chat", "input_cost_per_token": 1E-13, "output_cost_per_token": 0.00002}}`
	const negative = `{"model-a": {"litellm_provider": "anthropic", "mode": "chat", "input_cost_per_token": -0.000004, "output_cost_per_token": 0.00002}}`
	const textual = `{"model-a": {"litellm_provider": "anthropic", "mode": "chat", "input_cost_per_token": "free", "output_cost_per_token": 0.00002}}`
	const unrelated = `{"model-a": {"litellm_provider": "bedrock", "mode": "chat", "input_cost_per_token": 0.000004}}`
	tampered := pinFor(upstream)
	tests := []struct {
		name    string
		pin     modelcatalog.SnapshotPin
		data    string
		wantErr string
	}{
		{name: "checksum mismatch", pin: tampered, data: upstream + " ", wantErr: "SHA-256"},
		{name: "price finer than the unit", pin: pinFor(subMicro), data: subMicro, wantErr: "whole number"},
		{name: "negative price", pin: pinFor(negative), data: negative, wantErr: "non-negative"},
		{name: "non-numeric price", pin: pinFor(textual), data: textual, wantErr: "not a number"},
		{name: "nothing matched the pin", pin: pinFor(unrelated), data: unrelated, wantErr: "no upstream model"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := build(tt.pin, []byte(tt.data)); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("build() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func pinFor(data string) modelcatalog.SnapshotPin {
	digest := sha256.Sum256([]byte(data))
	return modelcatalog.SnapshotPin{
		Repository: "example/prices", Path: "prices.json", Commit: strings.Repeat("a", 40),
		SHA256: hex.EncodeToString(digest[:]), Retrieved: "2026-11-21",
		Providers: []string{"anthropic", "openai"}, Modes: []string{"chat", "responses"},
	}
}

func same(left, right *uint64) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}
