package main

import "reasonix/internal/config"

// VaultSettingsView is the credential-store protection state the settings page
// renders: whether a master password encrypts the store, whether this process
// currently holds the derived key, where the encrypted file lives, and the
// minimum password length so the form can validate before submitting.
type VaultSettingsView struct {
	Configured bool   `json:"configured"`
	Unlocked   bool   `json:"unlocked"`
	Path       string `json:"path"`
	MinLength  int    `json:"minLength"`
}

// VaultSettings reports master-password protection state without touching the
// store's contents, so the settings page can render even while it is locked.
func (a *App) VaultSettings() VaultSettingsView {
	return vaultSettingsView()
}

// SetVaultPassword encrypts the credential store with a new master password.
// It fails when protection is already enabled, mirroring `reasonix secrets set`.
func (a *App) SetVaultPassword(password string) (VaultSettingsView, error) {
	if _, err := config.SetMasterPassword(password); err != nil {
		return VaultSettingsView{}, err
	}
	return vaultSettingsView(), nil
}

// ChangeVaultPassword re-keys an already protected store. An empty current
// password reuses the in-process key the running host already holds.
func (a *App) ChangeVaultPassword(current, next string) (VaultSettingsView, error) {
	if _, err := config.ChangeMasterPassword(current, next); err != nil {
		return VaultSettingsView{}, err
	}
	return vaultSettingsView(), nil
}

// UnlockVault verifies a master password and keeps the derived key for this
// process so credential readers can open the store again.
func (a *App) UnlockVault(password string) (VaultSettingsView, error) {
	if err := config.UnlockMasterPassword(password); err != nil {
		return VaultSettingsView{}, err
	}
	return vaultSettingsView(), nil
}

// LockVault drops the in-process key; credential reads then fail until the next
// unlock. It never touches the encrypted file on disk.
func (a *App) LockVault() VaultSettingsView {
	config.LockMasterPassword()
	return vaultSettingsView()
}

// DisableVault decrypts the store back to a plaintext .env. An empty password
// reuses the in-process key.
func (a *App) DisableVault(password string) (VaultSettingsView, error) {
	if _, err := config.ClearMasterPassword(password); err != nil {
		return VaultSettingsView{}, err
	}
	return vaultSettingsView(), nil
}

func vaultSettingsView() VaultSettingsView {
	status := config.MasterPasswordStatusFor()
	return VaultSettingsView{
		Configured: status.Configured,
		Unlocked:   status.Unlocked,
		Path:       status.Path,
		MinLength:  config.MinMasterPasswordLength,
	}
}
