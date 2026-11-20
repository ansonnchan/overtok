package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestListenAddress(t *testing.T) {
	tests := []struct {
		name    string
		values  map[string]string
		want    string
		wantErr string
	}{
		{name: "local default", want: "127.0.0.1:8080"},
		{name: "platform port", values: map[string]string{"PORT": "10000"}, want: "0.0.0.0:10000"},
		{name: "explicit address wins", values: map[string]string{"TOKENHOLIC_LISTEN_ADDR": "127.0.0.1:9090", "PORT": "10000"}, want: "127.0.0.1:9090"},
		{name: "trims values", values: map[string]string{"PORT": " 8081 "}, want: "0.0.0.0:8081"},
		{name: "zero port", values: map[string]string{"PORT": "0"}, wantErr: "between 1 and 65535"},
		{name: "large port", values: map[string]string{"PORT": "65536"}, wantErr: "between 1 and 65535"},
		{name: "invalid port", values: map[string]string{"PORT": "http"}, wantErr: "between 1 and 65535"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			getenv := func(key string) string { return test.values[key] }
			got, err := listenAddress(getenv)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("listenAddress() error = %v, want containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("listenAddress() = %q, %v, want %q", got, err, test.want)
			}
		})
	}
}

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
