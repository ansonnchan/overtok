package jsonl

import (
	"strings"
	"testing"
)

func TestReadDefersPartialTail(t *testing.T) {
	t.Parallel()

	result, err := Read(strings.NewReader("{\"complete\":true}\n{\"partial\":"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Records) != 1 || len(result.Warnings) != 1 || result.Warnings[0] != "partial_tail" {
		t.Fatalf("Read() = %#v", result)
	}
}

func TestReadSkipsOversizedRecord(t *testing.T) {
	t.Parallel()

	result, err := Read(strings.NewReader("12345\n{}\n"), 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Records) != 1 || result.Records[0].Number != 2 || len(result.Warnings) != 1 || result.Warnings[0] != "record_too_large" {
		t.Fatalf("Read() = %#v", result)
	}
}
