package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteArchiveIsDeterministic(t *testing.T) {
	entries := []archiveEntry{
		{name: binaryName, mode: 0o755, data: []byte("synthetic-binary")},
		{name: lockName, mode: 0o644, data: []byte("synthetic-lock")},
	}
	first := filepath.Join(t.TempDir(), "first.tar.gz")
	second := filepath.Join(t.TempDir(), "second.tar.gz")
	if err := writeArchive(first, entries); err != nil {
		t.Fatal(err)
	}
	if err := writeArchive(second, entries); err != nil {
		t.Fatal(err)
	}
	firstBytes, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("archives differ for identical inputs")
	}

	gzipReader, err := gzip.NewReader(bytes.NewReader(firstBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	for index, want := range entries {
		header, err := tarReader.Next()
		if err != nil {
			t.Fatalf("entry %d: %v", index, err)
		}
		contents, err := io.ReadAll(tarReader)
		if err != nil {
			t.Fatal(err)
		}
		if header.Name != want.name || header.Mode != want.mode || !header.ModTime.Equal(time.Unix(0, 0)) || !bytes.Equal(contents, want.data) {
			t.Fatalf("entry %d = %#v/%q", index, header, contents)
		}
	}
	if _, err := tarReader.Next(); err != io.EOF {
		t.Fatalf("trailing archive entry: %v", err)
	}
}

func TestReleaseInputValidation(t *testing.T) {
	for _, version := range []string{"", "v1.2.3", "1", "1.2.3/unsafe", "1.2.3-", "1.2.3-foo..bar"} {
		var output strings.Builder
		if err := run([]string{"-version", version, "-output", t.TempDir()}, &output); err == nil {
			t.Fatalf("run(%q) succeeded", version)
		}
	}
}

func TestReplaceEnvironment(t *testing.T) {
	got := strings.Join(replaceEnvironment([]string{"PATH=/bin", "GOOS=linux", "GOARCH=386"}, map[string]string{
		"GOOS": "darwin", "GOARCH": "arm64", "CGO_ENABLED": "0",
	}), "\n")
	for _, want := range []string{"PATH=/bin", "CGO_ENABLED=0", "GOARCH=arm64", "GOOS=darwin"} {
		if !strings.Contains(got, want) {
			t.Fatalf("environment %q does not contain %q", got, want)
		}
	}
}
