// Command release builds deterministic Overtok distribution archives.
package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"debug/macho"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/ansonnchan/overtok/internal/adapters/ccusage"
)

const (
	binaryName = "overtok"
	lockName   = "ccusage.lock.json"
	versionKey = "github.com/ansonnchan/overtok/internal/cli.version"
)

var semanticVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

type target struct {
	archiveArch string
	goArch      string
	cpu         macho.Cpu
}

var targets = []target{
	{archiveArch: "arm64", goArch: "arm64", cpu: macho.CpuArm64},
	{archiveArch: "x64", goArch: "amd64", cpu: macho.CpuAmd64},
}

type archiveEntry struct {
	name string
	mode int64
	data []byte
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("overtok-release", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	version := flags.String("version", "", "release version without a v prefix")
	outputDir := flags.String("output", "dist", "artifact output directory")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse release arguments: %w", err)
	}
	if flags.NArg() != 0 || !semanticVersion.MatchString(*version) {
		return errors.New("release version must be semantic version text without a v prefix")
	}
	if strings.TrimSpace(*outputDir) == "" {
		return errors.New("release output directory is required")
	}
	lock, err := os.ReadFile(lockName)
	if err != nil {
		return fmt.Errorf("read pinned parser contract: %w", err)
	}
	if err := ccusage.ValidateLock(lock); err != nil {
		return fmt.Errorf("validate pinned parser contract: %w", err)
	}
	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		return fmt.Errorf("create release output: %w", err)
	}
	temporary, err := os.MkdirTemp("", "overtok-release-")
	if err != nil {
		return fmt.Errorf("create release workspace: %w", err)
	}
	defer os.RemoveAll(temporary)

	checksums := make(map[string]string, len(targets))
	for _, target := range targets {
		binaryPath := filepath.Join(temporary, "overtok-"+target.goArch)
		if err := build(binaryPath, *version, target.goArch); err != nil {
			return err
		}
		if err := verifyMachO(binaryPath, target.cpu); err != nil {
			return err
		}
		if runtime.GOOS == "darwin" && runtime.GOARCH == target.goArch {
			if err := verifyVersion(binaryPath, *version); err != nil {
				return err
			}
		}
		binary, err := os.ReadFile(binaryPath)
		if err != nil {
			return fmt.Errorf("read %s binary: %w", target.archiveArch, err)
		}
		archiveName := fmt.Sprintf("overtok_%s_darwin_%s.tar.gz", *version, target.archiveArch)
		archivePath := filepath.Join(*outputDir, archiveName)
		entries := []archiveEntry{
			{name: binaryName, mode: 0o755, data: binary},
			{name: lockName, mode: 0o644, data: lock},
		}
		if err := writeArchive(archivePath, entries); err != nil {
			return fmt.Errorf("package %s: %w", target.archiveArch, err)
		}
		checksum, err := fileChecksum(archivePath)
		if err != nil {
			return err
		}
		checksums[archiveName] = checksum
		fmt.Fprintf(output, "built %s\n", archivePath)
	}
	if err := writeChecksums(filepath.Join(*outputDir, "SHA256SUMS"), checksums); err != nil {
		return err
	}
	fmt.Fprintf(output, "built %s\n", filepath.Join(*outputDir, "SHA256SUMS"))
	return nil
}

func build(destination, version, goarch string) error {
	command := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags", "-s -w -X "+versionKey+"="+version, "-o", destination, "./cmd/overtok")
	command.Env = replaceEnvironment(os.Environ(), map[string]string{
		"CGO_ENABLED": "0",
		"GOARCH":      goarch,
		"GOOS":        "darwin",
	})
	if combined, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("build darwin/%s: %w: %s", goarch, err, strings.TrimSpace(string(combined)))
	}
	return nil
}

func replaceEnvironment(environment []string, replacements map[string]string) []string {
	result := make([]string, 0, len(environment)+len(replacements))
	for _, entry := range environment {
		key, _, found := strings.Cut(entry, "=")
		if _, replace := replacements[key]; found && replace {
			continue
		}
		result = append(result, entry)
	}
	keys := make([]string, 0, len(replacements))
	for key := range replacements {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		result = append(result, key+"="+replacements[key])
	}
	return result
}

func verifyMachO(path string, want macho.Cpu) error {
	file, err := macho.Open(path)
	if err != nil {
		return fmt.Errorf("inspect release binary: %w", err)
	}
	defer file.Close()
	if file.Cpu != want {
		return fmt.Errorf("release binary architecture is %s, want %s", file.Cpu, want)
	}
	return nil
}

func verifyVersion(path, version string) error {
	output, err := exec.Command(path, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("run native release binary: %w", err)
	}
	if got := strings.TrimSpace(string(output)); got != version {
		return fmt.Errorf("native release version is %q, want %q", got, version)
	}
	return nil
}

func writeArchive(path string, entries []archiveEntry) (err error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer closeWithError(file, &err)
	gzipWriter, err := gzip.NewWriterLevel(file, gzip.BestCompression)
	if err != nil {
		return err
	}
	gzipWriter.Header.ModTime = time.Unix(0, 0).UTC()
	gzipWriter.Header.OS = 255
	defer closeWithError(gzipWriter, &err)
	tarWriter := tar.NewWriter(gzipWriter)
	defer closeWithError(tarWriter, &err)
	for _, entry := range entries {
		header := &tar.Header{
			Name: entry.name, Mode: entry.mode, Size: int64(len(entry.data)),
			ModTime: time.Unix(0, 0).UTC(), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if _, err := tarWriter.Write(entry.data); err != nil {
			return err
		}
	}
	return nil
}

func closeWithError(closer io.Closer, err *error) {
	if closeErr := closer.Close(); *err == nil && closeErr != nil {
		*err = closeErr
	}
}

func fileChecksum(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open release archive for checksum: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("checksum release archive: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writeChecksums(path string, checksums map[string]string) error {
	names := make([]string, 0, len(checksums))
	for name := range checksums {
		names = append(names, name)
	}
	sort.Strings(names)
	var contents strings.Builder
	for _, name := range names {
		fmt.Fprintf(&contents, "%s  %s\n", checksums[name], name)
	}
	if err := os.WriteFile(path, []byte(contents.String()), 0o644); err != nil {
		return fmt.Errorf("write release checksums: %w", err)
	}
	return nil
}
