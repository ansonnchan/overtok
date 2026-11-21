package modelcatalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ReasonContextTiered marks a model whose price depends on prompt length.
// Usage buckets cannot prove which tier a request fell in, so it stays unpriced.
const ReasonContextTiered = "context_tiered"

// SnapshotPin identifies the exact upstream price file that is vendored and
// the subset of it tokenholic keeps.
type SnapshotPin struct {
	Repository string   `json:"repository"`
	Path       string   `json:"path"`
	Commit     string   `json:"commit"`
	SHA256     string   `json:"sha256"`
	Retrieved  string   `json:"retrieved"`
	Providers  []string `json:"providers"`
	Modes      []string `json:"modes"`
}

// Snapshot is the vendored, token-priced subset of the pinned upstream file.
// cmd/pricegen writes it; nothing else should edit it.
type Snapshot struct {
	Source SnapshotPin     `json:"source"`
	Models []SnapshotModel `json:"models"`
}

// SnapshotModel is one first-party model. Rates are micro-US-dollars per
// million tokens; an absent rate means the provider publishes none. A model
// with UnpricedReason resolves canonically but carries no rates.
type SnapshotModel struct {
	ID             string  `json:"id"`
	Provider       string  `json:"provider"`
	UnpricedReason string  `json:"unpriced_reason,omitempty"`
	Input          *uint64 `json:"input,omitempty"`
	CacheRead      *uint64 `json:"cache_read,omitempty"`
	CacheWrite     *uint64 `json:"cache_write,omitempty"`
	CacheWrite1h   *uint64 `json:"cache_write_1h,omitempty"`
	Output         *uint64 `json:"output,omitempty"`
}

// URL returns the immutable location of the pinned upstream file.
func (p SnapshotPin) URL() string {
	return "https://raw.githubusercontent.com/" + p.Repository + "/" + p.Commit + "/" + p.Path
}

// Validate checks that the pin names one immutable, checksummed file.
func (p SnapshotPin) Validate() error {
	if p.Repository == "" || p.Path == "" || len(p.Providers) == 0 || len(p.Modes) == 0 {
		return errors.New("price snapshot pin requires a repository, path, providers, and modes")
	}
	if len(p.Commit) != 40 || strings.Trim(p.Commit, "0123456789abcdef") != "" {
		return errors.New("price snapshot pin requires a full lowercase commit hash, not a branch or tag")
	}
	if len(p.SHA256) != 64 || strings.Trim(p.SHA256, "0123456789abcdef") != "" {
		return errors.New("price snapshot pin requires a lowercase SHA-256 digest")
	}
	if _, err := time.Parse(time.DateOnly, p.Retrieved); err != nil {
		return fmt.Errorf("price snapshot pin retrieval date: %w", err)
	}
	return nil
}

// DecodeSnapshot strictly parses a vendored snapshot and checks its ordering.
func DecodeSnapshot(data []byte) (Snapshot, error) {
	var snapshot Snapshot
	if err := decodeStrict(data, &snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("decode price snapshot: %w", err)
	}
	if err := snapshot.Source.Validate(); err != nil {
		return Snapshot{}, err
	}
	if !sort.SliceIsSorted(snapshot.Models, func(i, j int) bool { return snapshot.Models[i].ID < snapshot.Models[j].ID }) {
		return Snapshot{}, errors.New("price snapshot models are not sorted by ID")
	}
	for index, model := range snapshot.Models {
		if model.ID == "" || model.Provider == "" {
			return Snapshot{}, fmt.Errorf("price snapshot model %d is missing its ID or provider", index)
		}
		if index > 0 && snapshot.Models[index-1].ID == model.ID {
			return Snapshot{}, fmt.Errorf("price snapshot model %q is duplicated", model.ID)
		}
		priced := model.Input != nil || model.CacheRead != nil || model.CacheWrite != nil || model.CacheWrite1h != nil || model.Output != nil
		if priced == (model.UnpricedReason != "") {
			return Snapshot{}, fmt.Errorf("price snapshot model %q must have either rates or an unpriced reason", model.ID)
		}
	}
	return snapshot, nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("trailing JSON value")
	}
	return nil
}
