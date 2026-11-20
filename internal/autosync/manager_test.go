package autosync

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestManagerInstallsInspectsAndRemovesLaunchAgent(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(t.TempDir(), "tokenholic")
	if err := os.WriteFile(source, []byte("synthetic executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	launcher := &fakeLauncher{}
	manager, err := NewManager(Config{
		Home: home, Executable: source, APIURL: "https://api.example.test", CodexHome: filepath.Join(home, "codex"),
		platform: "darwin", uid: 501, launcher: launcher,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Enable(t.Context()); err != nil {
		t.Fatal(err)
	}
	paths := manager.paths()
	binary, err := os.ReadFile(paths.binary)
	if err != nil || string(binary) != "synthetic executable" {
		t.Fatalf("installed binary = %q, %v", binary, err)
	}
	plist, err := os.ReadFile(paths.plist)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{launchLabel, paths.binary, "<integer>3600</integer>", "TOKENHOLIC_API_URL", "CODEX_HOME", "NO_COLOR"} {
		if !strings.Contains(string(plist), expected) {
			t.Fatalf("plist does not contain %q: %s", expected, plist)
		}
	}
	if info, err := os.Stat(paths.plist); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("plist mode = %v, %v", info, err)
	}
	if runtime.GOOS == "darwin" {
		if output, err := exec.Command("plutil", "-lint", paths.plist).CombinedOutput(); err != nil {
			t.Fatalf("plutil = %v: %s", err, output)
		}
	}
	decoder := xml.NewDecoder(strings.NewReader(string(plist)))
	for {
		if _, err := decoder.Token(); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("plist XML = %v", err)
		}
	}
	launcher.loaded = true
	status, err := manager.Status(t.Context())
	if err != nil || !status.Installed || !status.Loaded {
		t.Fatalf("Status() = %#v, %v", status, err)
	}
	if err := os.Remove(paths.binary); err != nil {
		t.Fatal(err)
	}
	status, err = manager.Status(t.Context())
	if err != nil || status.Installed || !status.Loaded {
		t.Fatalf("incomplete Status() = %#v, %v", status, err)
	}
	if err := manager.Disable(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, owned := range []string{paths.plist, paths.binary, paths.stdout, paths.stderr} {
		if _, err := os.Stat(owned); !os.IsNotExist(err) {
			t.Fatalf("autosync-owned file remains: %v", err)
		}
	}
	if len(launcher.calls) < 5 || launcher.calls[0][0] != "print" || launcher.calls[1][0] != "bootstrap" {
		t.Fatalf("launchctl calls = %#v", launcher.calls)
	}
}

func TestManagerRequiresMacOSAndValidAPI(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "tokenholic")
	if err := os.WriteFile(executable, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, config := range []Config{
		{Home: t.TempDir(), Executable: executable, APIURL: "https://api.example.test", platform: "linux", uid: 501, launcher: &fakeLauncher{}},
		{Home: t.TempDir(), Executable: executable, APIURL: "http://remote.example.test", platform: "darwin", uid: 501, launcher: &fakeLauncher{}},
		{Home: t.TempDir(), Executable: executable, APIURL: "https://user:secret@api.example.test", platform: "darwin", uid: 501, launcher: &fakeLauncher{}},
	} {
		manager, err := NewManager(config)
		if err != nil {
			t.Fatal(err)
		}
		if err := manager.Enable(t.Context()); err == nil {
			t.Fatalf("Enable(%#v) succeeded", config)
		}
	}
}

func TestManagerRecognizesAndRemovesLegacyInstallation(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(t.TempDir(), "tokenholic")
	if err := os.WriteFile(source, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	launcher := &fakeLauncher{loaded: true}
	manager, err := NewManager(Config{
		Home: home, Executable: source, APIURL: "https://api.example.test",
		platform: "darwin", uid: 501, launcher: launcher,
	})
	if err != nil {
		t.Fatal(err)
	}
	legacy := manager.legacyPaths()
	if err := os.MkdirAll(filepath.Dir(legacy.binary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(legacy.plist), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy.binary, []byte("legacy"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy.plist, []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status(t.Context())
	if err != nil || !status.Installed || !status.Loaded {
		t.Fatalf("Status() = %#v, %v", status, err)
	}
	if err := manager.Disable(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy.binary); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy binary still exists: %v", err)
	}
}

type fakeLauncher struct {
	loaded bool
	calls  [][]string
}

func (f *fakeLauncher) Run(_ context.Context, arguments ...string) error {
	f.calls = append(f.calls, append([]string(nil), arguments...))
	if len(arguments) > 0 && arguments[0] == "print" && !f.loaded {
		return errNotLoaded
	}
	if len(arguments) > 0 && arguments[0] == "bootout" {
		f.loaded = false
	}
	if len(arguments) > 0 && arguments[0] == "bootstrap" {
		f.loaded = true
	}
	return nil
}
