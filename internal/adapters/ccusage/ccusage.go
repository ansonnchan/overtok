package ccusage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

const (
	// PinnedVersion is the only accepted external executable version.
	PinnedVersion = "20.0.51"
	// SchemaID versions the pinned JSON compatibility contract.
	SchemaID = "daily-agent-contract-v1"
)

// ValidateVersion accepts only the pinned version with known CLI prefixes.
func ValidateVersion(data []byte) error {
	got := strings.TrimSpace(string(data))
	if got != PinnedVersion && got != "ccusage "+PinnedVersion && got != "ccusage v"+PinnedVersion && got != "v"+PinnedVersion {
		return fmt.Errorf("unsupported ccusage version %q; require %s", got, PinnedVersion)
	}
	return nil
}

// ValidateLock checks the parser manifest shipped with a release.
func ValidateLock(data []byte) error {
	var lock struct {
		Tool     string              `json:"tool"`
		Version  string              `json:"version"`
		Schema   string              `json:"schema"`
		Commands map[string][]string `json:"commands"`
	}
	if err := decodeLock(data, &lock); err != nil {
		return fmt.Errorf("decode lock manifest: %w", err)
	}
	flags := []string{"daily", "--json", "--no-cost", "--offline", "--timezone", "UTC"}
	if lock.Tool != "ccusage" || lock.Version != PinnedVersion || lock.Schema != SchemaID || len(lock.Commands) != 2 ||
		!slices.Equal(lock.Commands["claude"], flags) ||
		!slices.Equal(lock.Commands["codex"], append([]string{"codex"}, flags...)) {
		return errors.New("lock manifest does not match the compiled ccusage contract")
	}
	return nil
}

func decodeLock(data []byte, target any) error {
	if err := rejectDuplicateKeys(json.NewDecoder(bytes.NewReader(data))); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("lock manifest has trailing JSON")
	}
	return nil
}

func rejectDuplicateKeys(decoder *json.Decoder) error {
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
			if _, duplicate := keys[key]; duplicate {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			keys[key] = struct{}{}
			if err := rejectDuplicateKeys(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := rejectDuplicateKeys(decoder); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	_, err = decoder.Token()
	return err
}
