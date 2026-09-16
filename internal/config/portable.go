package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Portable mode keeps every Reasonix-owned file (config.toml, skills,
// commands, sessions, credentials, caches) in one data folder beside the
// executable, so a copied install is self-contained across machines.
//
// It is enabled when a reasonix.portable marker sits next to the executable,
// or by REASONIX_PORTABLE=on|1|true; REASONIX_PORTABLE=off disables it even
// with the marker, and unset (or =auto) falls back to the marker. The folder
// defaults to <executable dir>/reasonix-data and moves with
// REASONIX_PORTABLE_DIR. An explicit REASONIX_HOME still wins.
const (
	// PortableEnvVar turns portable mode on/off without a marker file.
	PortableEnvVar = "REASONIX_PORTABLE"
	// PortableDirEnvVar overrides where portable data is stored.
	PortableDirEnvVar = "REASONIX_PORTABLE_DIR"
	// PortableMarkerName is the marker file that enables portable mode.
	PortableMarkerName = "reasonix.portable"
	// portableDataDirName is the default data folder beside the executable.
	portableDataDirName = "reasonix-data"
)

// osExecutable is stubbed by tests that need a fake install location.
var osExecutable = os.Executable

// PortableDataDir is the resolved portable data folder, or "" when the
// executable directory cannot be determined.
func PortableDataDir() string {
	if dir := cleanEnvDir(PortableDirEnvVar); dir != "" {
		return dir
	}
	dir := executableDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, portableDataDirName)
}

// PortableMarkerPath is the marker file path, or "" when the executable
// directory cannot be determined.
func PortableMarkerPath() string {
	dir := executableDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, PortableMarkerName)
}

// PortableModeEnabled reports whether the effective home directory is the
// portable data folder.
func PortableModeEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(PortableEnvVar))) {
	case "1", "true", "on", "yes", "always":
		return true
	case "0", "false", "off", "no", "never":
		return false
	}
	marker := PortableMarkerPath()
	if marker == "" {
		return false
	}
	_, err := os.Stat(marker)
	return err == nil
}

// PortableModeSource describes why portable mode is (or is not) active, for
// `reasonix config portable status`.
func PortableModeSource() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(PortableEnvVar))) {
	case "1", "true", "on", "yes", "always":
		return PortableEnvVar + "=" + strings.TrimSpace(os.Getenv(PortableEnvVar))
	case "0", "false", "off", "no", "never":
		return PortableEnvVar + "=" + strings.TrimSpace(os.Getenv(PortableEnvVar))
	}
	if PortableModeEnabled() {
		return PortableMarkerPath()
	}
	return ""
}

// EnablePortableMode creates (enable) or removes (disable) the portable marker
// next to the executable. Enabling also creates the data folder up front so a
// missing one is an immediate error instead of a later surprise.
func EnablePortableMode(enable bool) error {
	marker := PortableMarkerPath()
	if marker == "" {
		return fmt.Errorf("cannot resolve the executable directory; set %s instead", PortableDirEnvVar)
	}
	if !enable {
		if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	dir := PortableDataDir()
	if dir == "" {
		return fmt.Errorf("cannot resolve the portable data directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create portable data directory %s: %w", dir, err)
	}
	if err := os.WriteFile(marker, []byte("Reasonix portable mode.\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", marker, err)
	}
	return nil
}

// portableHomeDir is the portable home contribution to home resolution: "" when
// portable mode is off or the directory cannot be resolved.
func portableHomeDir() string {
	if !PortableModeEnabled() {
		return ""
	}
	return PortableDataDir()
}

// isolatedHomeDir is the self-contained home directory currently in effect: an
// explicit REASONIX_HOME, or the portable data folder. A non-empty value means
// the runtime must not fall back to legacy OS-default data paths or import data
// from a system-wide production install.
func isolatedHomeDir() string {
	if dir := cleanEnvDir("REASONIX_HOME"); dir != "" {
		return dir
	}
	return portableHomeDir()
}

func executableDir() string {
	exe, err := osExecutable()
	if err != nil {
		return ""
	}
	exe = strings.TrimSpace(exe)
	if exe == "" {
		return ""
	}
	dir := filepath.Dir(exe)
	// A symlinked launcher (Linux /usr/local/bin, macOS Homebrew shim) must
	// resolve to the real install directory so the marker and data folder land
	// beside the actual binary.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	return filepath.Clean(dir)
}
