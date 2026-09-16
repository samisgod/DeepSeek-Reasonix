package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// stubPortableExecutable points portable resolution at a fake install directory.
// The package-level hook is restored on test cleanup so later cases resolve the
// real (test-binary) location again.
func stubPortableExecutable(t *testing.T, dir string, err error) {
	t.Helper()
	previous := osExecutable
	osExecutable = func() (string, error) {
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "reasonix"), nil
	}
	t.Cleanup(func() { osExecutable = previous })
}

func TestPortableDataDirSitsBesideExecutable(t *testing.T) {
	installDir := t.TempDir()
	stubPortableExecutable(t, installDir, nil)
	t.Setenv(PortableDirEnvVar, "")

	if got, want := PortableDataDir(), filepath.Join(installDir, portableDataDirName); got != want {
		t.Fatalf("PortableDataDir() = %q, want %q", got, want)
	}
	if got, want := PortableMarkerPath(), filepath.Join(installDir, PortableMarkerName); got != want {
		t.Fatalf("PortableMarkerPath() = %q, want %q", got, want)
	}
}

func TestPortableDataDirEnvOverride(t *testing.T) {
	stubPortableExecutable(t, t.TempDir(), nil)
	custom := t.TempDir()
	t.Setenv(PortableDirEnvVar, custom)

	if got := PortableDataDir(); got != filepath.Clean(custom) {
		t.Fatalf("PortableDataDir() = %q, want %q", got, custom)
	}
}

func TestPortableModeResolution(t *testing.T) {
	installDir := t.TempDir()
	stubPortableExecutable(t, installDir, nil)
	marker := filepath.Join(installDir, PortableMarkerName)

	t.Setenv(PortableEnvVar, "")
	if PortableModeEnabled() {
		t.Fatal("portable mode must be off without a marker or an explicit flag")
	}
	if source := PortableModeSource(); source != "" {
		t.Fatalf("PortableModeSource() = %q, want empty", source)
	}

	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !PortableModeEnabled() {
		t.Fatal("portable mode must be on when the marker exists")
	}
	if source := PortableModeSource(); source != marker {
		t.Fatalf("PortableModeSource() = %q, want the marker path %q", source, marker)
	}

	// An explicit off wins over the marker, and an explicit on needs no marker.
	t.Setenv(PortableEnvVar, "off")
	if PortableModeEnabled() {
		t.Fatal("REASONIX_PORTABLE=off must disable portable mode even with a marker")
	}
	t.Setenv(PortableEnvVar, "on")
	if !PortableModeEnabled() {
		t.Fatal("REASONIX_PORTABLE=on must enable portable mode")
	}
	if source := PortableModeSource(); source != PortableEnvVar+"=on" {
		t.Fatalf("PortableModeSource() = %q, want the env source", source)
	}
}

func TestPortableModeDrivesReasonixHome(t *testing.T) {
	installDir := t.TempDir()
	stubPortableExecutable(t, installDir, nil)
	t.Setenv(PortableEnvVar, "")
	t.Setenv(PortableDirEnvVar, "")
	t.Setenv("REASONIX_HOME", "")
	t.Setenv("REASONIX_CACHE_HOME", "")
	if err := os.WriteFile(filepath.Join(installDir, PortableMarkerName), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	portable := filepath.Join(installDir, portableDataDirName)
	if got := ReasonixHomeDir(); got != portable {
		t.Fatalf("ReasonixHomeDir() = %q, want the portable data dir %q", got, portable)
	}
	if got := IsolatedHomeDir(); got != portable {
		t.Fatalf("IsolatedHomeDir() = %q, want %q", got, portable)
	}
	if got := UserConfigPath(); got != filepath.Join(portable, "config.toml") {
		t.Fatalf("UserConfigPath() = %q", got)
	}
	if got := UserCredentialsPath(); got != filepath.Join(portable, ".env") {
		t.Fatalf("UserCredentialsPath() = %q", got)
	}
	if got := CacheDir(); got != filepath.Join(portable, "cache") {
		t.Fatalf("CacheDir() = %q", got)
	}

	// An explicit REASONIX_HOME stays authoritative over portable mode.
	explicit := t.TempDir()
	t.Setenv("REASONIX_HOME", explicit)
	if got := ReasonixHomeDir(); got != filepath.Clean(explicit) {
		t.Fatalf("ReasonixHomeDir() = %q, want REASONIX_HOME %q", got, explicit)
	}
}

func TestEnablePortableModeCreatesAndRemovesMarker(t *testing.T) {
	installDir := t.TempDir()
	stubPortableExecutable(t, installDir, nil)
	t.Setenv(PortableEnvVar, "")
	t.Setenv(PortableDirEnvVar, "")

	if err := EnablePortableMode(true); err != nil {
		t.Fatalf("EnablePortableMode(true): %v", err)
	}
	if !PortableModeEnabled() {
		t.Fatal("portable mode must be enabled after writing the marker")
	}
	if _, err := os.Stat(PortableDataDir()); err != nil {
		t.Fatalf("portable data dir was not created: %v", err)
	}
	if _, err := os.Stat(PortableMarkerPath()); err != nil {
		t.Fatalf("marker was not created: %v", err)
	}

	if err := EnablePortableMode(false); err != nil {
		t.Fatalf("EnablePortableMode(false): %v", err)
	}
	if _, err := os.Stat(PortableMarkerPath()); !os.IsNotExist(err) {
		t.Fatalf("marker still present after disabling: %v", err)
	}
	// Disabling is idempotent.
	if err := EnablePortableMode(false); err != nil {
		t.Fatalf("EnablePortableMode(false) must be idempotent: %v", err)
	}
}

func TestPortableResolutionFailsWithoutExecutableDir(t *testing.T) {
	stubPortableExecutable(t, "", errors.New("no executable"))
	t.Setenv(PortableDirEnvVar, "")

	if got := PortableDataDir(); got != "" {
		t.Fatalf("PortableDataDir() = %q, want empty", got)
	}
	if got := PortableMarkerPath(); got != "" {
		t.Fatalf("PortableMarkerPath() = %q, want empty", got)
	}
	if err := EnablePortableMode(true); err == nil {
		t.Fatal("EnablePortableMode must report an unresolvable executable directory")
	}
}
