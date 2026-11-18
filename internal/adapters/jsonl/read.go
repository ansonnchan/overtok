package jsonl

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
)

// DefaultMaxRecordBytes bounds one record to avoid unbounded local allocations.
const DefaultMaxRecordBytes = 1 << 20

// Record is one complete non-empty JSONL line and its one-based line number.
type Record struct {
	Number int
	Data   []byte
}

// Result contains complete records plus stable recovery warning codes.
type Result struct {
	Records  []Record
	Warnings []string
}

// Read returns complete bounded records. An unterminated final record is treated
// as a partial write and deferred until a later scan.
func Read(reader io.Reader, maxRecordBytes int) (Result, error) {
	if maxRecordBytes == 0 {
		maxRecordBytes = DefaultMaxRecordBytes
	}
	if maxRecordBytes < 1 {
		return Result{}, errors.New("max record bytes must be positive")
	}

	buffered := bufio.NewReaderSize(reader, 64*1024)
	result := Result{}
	lineNumber := 0
	var record []byte
	oversized := false
	for {
		fragment, err := buffered.ReadSlice('\n')
		if !oversized && len(fragment) > 0 {
			if len(record)+len(fragment) > maxRecordBytes+1 {
				oversized = true
				record = nil
			} else {
				record = append(record, fragment...)
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return result, fmt.Errorf("read JSONL record: %w", err)
		}
		if errors.Is(err, io.EOF) {
			if len(record) > 0 || oversized {
				if oversized {
					result.Warnings = append(result.Warnings, "record_too_large")
				}
				result.Warnings = append(result.Warnings, "partial_tail")
			}
			return result, nil
		}
		lineNumber++
		if oversized {
			result.Warnings = append(result.Warnings, "record_too_large")
			record = nil
			oversized = false
			continue
		}
		record = bytes.TrimSuffix(record, []byte{'\n'})
		record = bytes.TrimSuffix(record, []byte{'\r'})
		if len(record) == 0 {
			record = nil
			continue
		}
		if len(record) > maxRecordBytes {
			result.Warnings = append(result.Warnings, "record_too_large")
			record = nil
			continue
		}
		result.Records = append(result.Records, Record{Number: lineNumber, Data: append([]byte(nil), record...)})
		record = nil
	}
}
