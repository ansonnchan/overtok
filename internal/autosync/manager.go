package autosync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	usageSync "github.com/ansonnchan/tokenholic/internal/sync"
)

const (
	launchLabel = "io.tokenholic.autosync"
	interval    = 60 * 60
)

var errNotLoaded = errors.New("autosync launch agent is not loaded")

type launcher interface {
	Run(context.Context, ...string) error
}

// Config supplies the local values written into the launch agent.
type Config struct {
	Home       string
	Executable string
	APIURL     string
	CodexHome  string
	platform   string
	uid        int
	launcher   launcher
}

// Status reports whether the persistent configuration exists and whether
// launchd currently has it loaded.
type Status struct {
	Installed bool
	Loaded    bool
}

// Manager installs and removes one per-user macOS launch agent.
type Manager struct {
	home, executable, apiURL, codexHome string
	platform                            string
	uid                                 int
	launcher                            launcher
}

// NewManager validates paths and constructs the macOS autosync boundary.
func NewManager(config Config) (*Manager, error) {
	if !filepath.IsAbs(config.Home) || !filepath.IsAbs(config.Executable) {
		return nil, errors.New("autosync home and executable must be absolute")
	}
	if config.CodexHome != "" && !filepath.IsAbs(config.CodexHome) {
		return nil, errors.New("autosync Codex home must be absolute")
	}
	platform := config.platform
	if platform == "" {
		platform = runtime.GOOS
	}
	uid := config.uid
	if uid == 0 {
		uid = os.Getuid()
	}
	launcher := config.launcher
	if launcher == nil {
		launcher = commandLauncher{}
	}
	return &Manager{
		home: config.Home, executable: config.Executable, apiURL: strings.TrimSpace(config.APIURL),
		codexHome: config.CodexHome, platform: platform, uid: uid, launcher: launcher,
	}, nil
}

// Enable installs a stable executable copy and loads an hourly launch agent.
// Re-enabling atomically refreshes both files from the current release.
func (m *Manager) Enable(ctx context.Context) error {
	if err := m.supported(); err != nil {
		return err
	}
	if m.apiURL == "" {
		return errors.New("autosync requires an API origin")
	}
	if _, err := usageSync.NewClient(m.apiURL, nil); err != nil {
		return fmt.Errorf("validate autosync API origin: %w", err)
	}
	parsedAPI, err := url.Parse(m.apiURL)
	if err != nil || parsedAPI.User != nil || parsedAPI.RawQuery != "" || parsedAPI.Fragment != "" {
		return errors.New("autosync API origin must not contain credentials, a query, or a fragment")
	}
	status, err := m.Status(ctx)
	if err != nil {
		return err
	}
	if status.Loaded {
		if err := m.launcher.Run(ctx, "bootout", m.domain()+"/"+launchLabel); err != nil {
			return fmt.Errorf("unload existing autosync launch agent: %w", err)
		}
	}
	paths := m.paths()
	for _, directory := range []string{filepath.Dir(paths.binary), paths.logs} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("create autosync private directory: %w", err)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return fmt.Errorf("secure autosync private directory: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(paths.plist), 0o755); err != nil {
		return fmt.Errorf("create launch agents directory: %w", err)
	}
	if err := copyExecutable(m.executable, paths.binary); err != nil {
		return err
	}
	plist := m.plist(paths)
	if err := writeAtomic(paths.plist, plist, 0o600); err != nil {
		return fmt.Errorf("write autosync launch agent: %w", err)
	}

	if err := m.launcher.Run(ctx, "bootstrap", m.domain(), paths.plist); err != nil {
		return fmt.Errorf("load autosync launch agent: %w", err)
	}
	return nil
}

// Disable unloads the launch agent and removes only autosync-owned files. It
// never deletes the local usage database or OS-vault credential.
func (m *Manager) Disable(ctx context.Context) error {
	if err := m.supported(); err != nil {
		return err
	}
	status, err := m.Status(ctx)
	if err != nil {
		return err
	}
	if status.Loaded {
		if err := m.launcher.Run(ctx, "bootout", m.domain()+"/"+launchLabel); err != nil {
			return fmt.Errorf("unload autosync launch agent: %w", err)
		}
	}
	paths := m.paths()
	for _, path := range []string{paths.plist, paths.binary, paths.stdout, paths.stderr} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove autosync-owned file: %w", err)
		}
	}
	_ = os.Remove(filepath.Dir(paths.binary))
	_ = os.Remove(paths.logs)
	return nil
}

// Status inspects persistent configuration and the current launchd domain.
func (m *Manager) Status(ctx context.Context) (Status, error) {
	if err := m.supported(); err != nil {
		return Status{}, err
	}
	paths := m.paths()
	plistReady, err := regularFile(paths.plist, false)
	if err != nil {
		return Status{}, fmt.Errorf("inspect autosync launch agent: %w", err)
	}
	binaryReady, err := regularFile(paths.binary, true)
	if err != nil {
		return Status{}, fmt.Errorf("inspect stable autosync executable: %w", err)
	}
	launchErr := m.launcher.Run(ctx, "print", m.domain()+"/"+launchLabel)
	if launchErr != nil && !errors.Is(launchErr, errNotLoaded) {
		return Status{}, fmt.Errorf("inspect loaded autosync launch agent: %w", launchErr)
	}
	return Status{Installed: plistReady && binaryReady, Loaded: launchErr == nil}, nil
}

func regularFile(path string, executable bool) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false, nil
	}
	return !executable || info.Mode().Perm()&0o111 != 0, nil
}

func (m *Manager) supported() error {
	if m.platform != "darwin" {
		return errors.New("autosync is supported only on macOS")
	}
	return nil
}

type managerPaths struct {
	plist, binary, logs, stdout, stderr string
}

func (m *Manager) paths() managerPaths {
	logs := filepath.Join(m.home, "Library", "Logs", "Tokenholic")
	return managerPaths{
		plist:  filepath.Join(m.home, "Library", "LaunchAgents", launchLabel+".plist"),
		binary: filepath.Join(m.home, "Library", "Application Support", "Tokenholic", "bin", "tokenholic"),
		logs:   logs, stdout: filepath.Join(logs, "autosync.log"), stderr: filepath.Join(logs, "autosync-error.log"),
	}
}

func (m *Manager) domain() string {
	return "gui/" + strconv.Itoa(m.uid)
}

func (m *Manager) plist(paths managerPaths) []byte {
	environment := "\t<key>EnvironmentVariables</key>\n\t<dict>\n" +
		"\t\t<key>TOKENHOLIC_API_URL</key>\n\t\t<string>" + escapeXML(m.apiURL) + "</string>\n"
	if m.codexHome != "" {
		environment += "\t\t<key>CODEX_HOME</key>\n\t\t<string>" + escapeXML(m.codexHome) + "</string>\n"
	}
	environment += "\t\t<key>NO_COLOR</key>\n\t\t<string>1</string>\n\t</dict>\n"
	contents := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + launchLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + escapeXML(paths.binary) + `</string>
		<string>sync</string>
	</array>
` + environment + `	<key>RunAtLoad</key>
	<true/>
	<key>StartInterval</key>
	<integer>` + strconv.Itoa(interval) + `</integer>
	<key>ProcessType</key>
	<string>Background</string>
	<key>StandardOutPath</key>
	<string>` + escapeXML(paths.stdout) + `</string>
	<key>StandardErrorPath</key>
	<string>` + escapeXML(paths.stderr) + `</string>
</dict>
</plist>
`
	return []byte(contents)
}

func escapeXML(value string) string {
	var escaped bytes.Buffer
	_ = xmlEscape(&escaped, value)
	return escaped.String()
}

func xmlEscape(output io.Writer, value string) error {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	_, err := io.WriteString(output, replacer.Replace(value))
	return err
}

func copyExecutable(source, destination string) error {
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		return fmt.Errorf("resolve current executable: %w", err)
	}
	input, err := os.Open(resolved)
	if err != nil {
		return fmt.Errorf("open current executable: %w", err)
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return errors.New("current executable is not a regular executable file")
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".tokenholic-*")
	if err != nil {
		return fmt.Errorf("create stable executable: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o700); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure stable executable: %w", err)
	}
	if _, err := io.Copy(temporary, input); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("copy stable executable: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("flush stable executable: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close stable executable: %w", err)
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return fmt.Errorf("install stable executable: %w", err)
	}
	return nil
}

func writeAtomic(destination string, contents []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".autosync-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, destination)
}

type commandLauncher struct{}

func (commandLauncher) Run(ctx context.Context, arguments ...string) error {
	command := exec.CommandContext(ctx, "launchctl", arguments...)
	output, err := command.CombinedOutput()
	if err == nil {
		return nil
	}
	if len(arguments) > 0 && arguments[0] == "print" {
		var exitErr *exec.ExitError
		lower := bytes.ToLower(output)
		if (errors.As(err, &exitErr) && exitErr.ExitCode() == 113) || bytes.Contains(lower, []byte("could not find service")) {
			return errNotLoaded
		}
	}
	return errors.New("launchctl command failed")
}
