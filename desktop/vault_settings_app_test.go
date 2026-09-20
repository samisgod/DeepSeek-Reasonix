package main

import (
	"errors"
	"os"
	"testing"

	"reasonix/internal/config"
)

const (
	testVaultPassword    = "correct horse battery"
	testVaultNewPassword = "another secret passphrase"
)

// TestVaultSettingsRoundTrip drives the settings page's whole lifecycle against
// a temp home: enable, lock, unlock, re-key, and disable.
func TestVaultSettingsRoundTrip(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Cleanup(config.LockMasterPassword)

	app := &App{}
	initial := app.VaultSettings()
	if initial.Configured || initial.Unlocked {
		t.Fatalf("fresh store: configured=%v unlocked=%v, want false/false", initial.Configured, initial.Unlocked)
	}
	if initial.Path == "" {
		t.Fatal("the settings page needs the credential store path")
	}
	if initial.MinLength != config.MinMasterPasswordLength {
		t.Fatalf("min length = %d, want %d", initial.MinLength, config.MinMasterPasswordLength)
	}

	enabled, err := app.SetVaultPassword(testVaultPassword)
	if err != nil {
		t.Fatalf("SetVaultPassword: %v", err)
	}
	if !enabled.Configured || !enabled.Unlocked {
		t.Fatalf("after enable: configured=%v unlocked=%v, want true/true", enabled.Configured, enabled.Unlocked)
	}
	raw, err := os.ReadFile(enabled.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !config.IsVaultData(raw) {
		t.Fatal("the credential store must be encrypted after enabling")
	}

	if _, err := app.SetVaultPassword(testVaultNewPassword); !errors.Is(err, config.ErrMasterPasswordAlreadySet) {
		t.Fatalf("second enable = %v, want ErrMasterPasswordAlreadySet", err)
	}

	if locked := app.LockVault(); locked.Unlocked {
		t.Fatal("LockVault must drop the in-process key")
	}
	if _, err := app.UnlockVault(testVaultNewPassword); !errors.Is(err, config.ErrMasterPasswordInvalid) {
		t.Fatalf("wrong password unlock = %v, want ErrMasterPasswordInvalid", err)
	}
	unlocked, err := app.UnlockVault(testVaultPassword)
	if err != nil || !unlocked.Unlocked {
		t.Fatalf("unlock: unlocked=%v err=%v", unlocked.Unlocked, err)
	}

	changed, err := app.ChangeVaultPassword(testVaultPassword, testVaultNewPassword)
	if err != nil {
		t.Fatalf("ChangeVaultPassword: %v", err)
	}
	if !changed.Configured {
		t.Fatal("the store must stay protected after a re-key")
	}
	app.LockVault()
	if _, err := app.UnlockVault(testVaultPassword); err == nil {
		t.Fatal("the old password must stop working after a re-key")
	}

	disabled, err := app.DisableVault(testVaultNewPassword)
	if err != nil {
		t.Fatalf("DisableVault: %v", err)
	}
	if disabled.Configured || disabled.Unlocked {
		t.Fatalf("after disable: configured=%v unlocked=%v, want false/false", disabled.Configured, disabled.Unlocked)
	}
	raw, err = os.ReadFile(disabled.Path)
	if err != nil {
		t.Fatal(err)
	}
	if config.IsVaultData(raw) {
		t.Fatal("disabling must restore a plaintext .env")
	}
}

func TestVaultSettingsRejectsShortPassword(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Cleanup(config.LockMasterPassword)

	app := &App{}
	if _, err := app.SetVaultPassword("short"); err == nil {
		t.Fatal("a master password shorter than the minimum must be rejected")
	}
	if app.VaultSettings().Configured {
		t.Fatal("a rejected password must not enable protection")
	}
}
