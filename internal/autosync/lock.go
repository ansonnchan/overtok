package autosync

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// ErrAlreadyRunning means another manual or scheduled synchronization owns the
// process lock.
var ErrAlreadyRunning = errors.New("synchronization is already running")

// Lock is an advisory process lock. Keep the lock file in place across runs so
// competing processes always address the same inode.
type Lock struct {
	files []*os.File
}

// AcquireDefault acquires the per-user synchronization lock without waiting.
func AcquireDefault() (*Lock, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("resolve autosync lock directory: %w", err)
	}
	return acquireDefault(root)
}

func acquireDefault(root string) (*Lock, error) {
	preferredPath := filepath.Join(root, applicationDirectory, "sync.lock")
	preferred, err := Acquire(preferredPath)
	if err != nil {
		return nil, err
	}
	legacyPath := filepath.Join(root, legacyApplicationDirectory(), "sync.lock")
	preferredInfo, preferredErr := os.Lstat(preferredPath)
	legacyInfo, legacyErr := os.Lstat(legacyPath)
	if preferredErr == nil && legacyErr == nil && os.SameFile(preferredInfo, legacyInfo) {
		return preferred, nil
	}
	// Lock both directory spellings while older scheduled binaries may still run.
	legacy, err := Acquire(legacyPath)
	if err != nil {
		_ = preferred.Close()
		return nil, err
	}
	preferred.files = append(preferred.files, legacy.files...)
	legacy.files = nil
	return preferred, nil
}

// Acquire takes a non-blocking exclusive lock at an explicit path.
func Acquire(path string) (*Lock, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("autosync lock path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create autosync lock directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("secure autosync lock directory: %w", err)
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("autosync lock must not be a symbolic link")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect autosync lock: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open autosync lock: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure autosync lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("acquire autosync lock: %w", err)
	}
	return &Lock{files: []*os.File{file}}, nil
}

// Close releases the synchronization lock.
func (l *Lock) Close() error {
	if l == nil || len(l.files) == 0 {
		return nil
	}
	var result error
	for _, file := range l.files {
		if err := unix.Flock(int(file.Fd()), unix.LOCK_UN); err != nil {
			result = errors.Join(result, fmt.Errorf("release autosync lock: %w", err))
		}
		if err := file.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close autosync lock: %w", err))
		}
	}
	l.files = nil
	return result
}

func legacyApplicationDirectory() string {
	return strings.ToUpper(applicationDirectory[:1]) + applicationDirectory[1:]
}
