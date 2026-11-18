package main

import (
	"encoding/base64"
	"testing"
)

func TestDecodeStateKey(t *testing.T) {
	raw := []byte("01234567890123456789012345678901")
	for _, encoded := range []string{
		base64.StdEncoding.EncodeToString(raw),
		base64.RawStdEncoding.EncodeToString(raw),
	} {
		key, err := decodeStateKey(encoded)
		if err != nil || string(key) != string(raw) {
			t.Fatalf("decodeStateKey() = %q, %v", key, err)
		}
	}
	if _, err := decodeStateKey("short"); err == nil {
		t.Fatal("decodeStateKey() accepted an invalid key")
	}
}
