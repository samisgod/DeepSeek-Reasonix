package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testMasterPassword  = "correct horse battery"
	otherMasterPassword = "another secret passphrase"
)

// resetCredentialStore restores a clean, unprotected credential store and
// clears the process-wide session key so vault cases cannot leak into each
// other. The previous file content is restored on cleanup.
func resetCredentialStore(t *testing.T) {
	t.Helper()
	LockMasterPassword()
	path := UserCredentialsPath()
	if path == "" {
		t.Fatal("credentials path must resolve in the isolated test home")
	}
	previous, readErr := os.ReadFile(path)
	existed := readErr == nil
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		LockMasterPassword()
		if existed {
			_ = os.WriteFile(path, previous, 0o600)
			return
		}
		_ = os.Remove(path)
	})
}

func writePlainCredentialStore(t *testing.T, content string) {
	t.Helper()
	path := UserCredentialsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestVaultSealOpenRoundTrip(t *testing.T) {
	plain := []byte("DEEPSEEK_API_KEY=sk-plaintext\n")
	sealed, err := sealVaultForPassword(plain, testMasterPassword)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if !IsVaultData(sealed) {
		t.Fatal("sealed output must be detectable as a vault")
	}
	if bytes.Contains(sealed, []byte("sk-plaintext")) {
		t.Fatal("sealed output must not contain the plaintext secret")
	}
	got, err := openVaultWithPassword(sealed, testMasterPassword)
	if err != nil {
		t.Fatalf("open with the right password: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round trip = %q, want %q", got, plain)
	}
	if _, err := openVaultWithPassword(sealed, otherMasterPassword); !errors.Is(err, ErrMasterPasswordInvalid) {
		t.Fatalf("wrong password error = %v, want ErrMasterPasswordInvalid", err)
	}
}

func TestSetMasterPasswordEncryptsCredentialStore(t *testing.T) {
	resetCredentialStore(t)
	writePlainCredentialStore(t, "DEEPSEEK_API_KEY=sk-secret\n")

	path, err := SetMasterPassword(testMasterPassword)
	if err != nil {
		t.Fatalf("SetMasterPassword: %v", err)
	}
	if !MasterPasswordConfigured() {
		t.Fatal("the store must report as protected")
	}
	if !MasterPasswordUnlocked() {
		t.Fatal("setting a master password must leave the store unlocked")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !IsVaultData(raw) {
		t.Fatal("the credential file must become an encrypted vault")
	}
	if bytes.Contains(raw, []byte("sk-secret")) {
		t.Fatal("the API key must not survive in plaintext on disk")
	}
	// Readers keep seeing the decrypted dotenv content.
	value, ok := envFileValue(path, "DEEPSEEK_API_KEY")
	if !ok || value != "sk-secret" {
		t.Fatalf("envFileValue = %q/%v, want sk-secret/true", value, ok)
	}
	if !CredentialStored("DEEPSEEK_API_KEY") {
		t.Fatal("CredentialStored must report an encrypted-but-present key")
	}
}

func TestSetMasterPasswordRejectsAlreadyProtectedStore(t *testing.T) {
	resetCredentialStore(t)
	writePlainCredentialStore(t, "DEEPSEEK_API_KEY=sk-secret\n")
	if _, err := SetMasterPassword(testMasterPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := SetMasterPassword(otherMasterPassword); !errors.Is(err, ErrMasterPasswordAlreadySet) {
		t.Fatalf("second SetMasterPassword error = %v, want ErrMasterPasswordAlreadySet", err)
	}
}

func TestSetMasterPasswordRejectsShortPassword(t *testing.T) {
	resetCredentialStore(t)
	writePlainCredentialStore(t, "DEEPSEEK_API_KEY=sk-secret\n")
	if _, err := SetMasterPassword("short"); err == nil {
		t.Fatal("a master password shorter than the minimum must be rejected")
	}
	if MasterPasswordConfigured() {
		t.Fatal("a rejected password must not create a vault")
	}
}

func TestLockedStoreRefusesReadsAndWrites(t *testing.T) {
	resetCredentialStore(t)
	writePlainCredentialStore(t, "DEEPSEEK_API_KEY=sk-secret\n")
	if _, err := SetMasterPassword(testMasterPassword); err != nil {
		t.Fatal(err)
	}
	LockMasterPassword()
	if MasterPasswordUnlocked() {
		t.Fatal("LockMasterPassword must drop the session key")
	}
	if _, ok := readDotEnvFile(UserCredentialsPath()); ok {
		t.Fatal("a locked store must not be readable")
	}
	if _, ok := envFileValue(UserCredentialsPath(), "DEEPSEEK_API_KEY"); ok {
		t.Fatal("a locked store must not leak decrypted values")
	}
	err := storeCredentialsInFile(UserCredentialsPath(), map[string]string{"OPENAI_API_KEY": "sk-2"})
	if !errors.Is(err, ErrMasterPasswordRequired) {
		t.Fatalf("write on a locked store = %v, want ErrMasterPasswordRequired", err)
	}
	raw, readErr := os.ReadFile(UserCredentialsPath())
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !IsVaultData(raw) {
		t.Fatal("a refused write must not downgrade the store to plaintext")
	}
}

func TestUnlockMasterPasswordVerifiesPassword(t *testing.T) {
	resetCredentialStore(t)
	writePlainCredentialStore(t, "DEEPSEEK_API_KEY=sk-secret\n")
	if _, err := SetMasterPassword(testMasterPassword); err != nil {
		t.Fatal(err)
	}
	LockMasterPassword()

	if err := UnlockMasterPassword(otherMasterPassword); !errors.Is(err, ErrMasterPasswordInvalid) {
		t.Fatalf("wrong password = %v, want ErrMasterPasswordInvalid", err)
	}
	if MasterPasswordUnlocked() {
		t.Fatal("a failed unlock must not leave the store unlocked")
	}
	if err := UnlockMasterPassword(testMasterPassword); err != nil {
		t.Fatalf("correct password: %v", err)
	}
	if value, ok := envFileValue(UserCredentialsPath(), "DEEPSEEK_API_KEY"); !ok || value != "sk-secret" {
		t.Fatalf("after unlock envFileValue = %q/%v", value, ok)
	}
}

func TestCredentialWritesStayEncrypted(t *testing.T) {
	resetCredentialStore(t)
	writePlainCredentialStore(t, "DEEPSEEK_API_KEY=sk-secret\n")
	if _, err := SetMasterPassword(testMasterPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := SetCredential("OPENAI_API_KEY", "sk-second"); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}
	raw, err := os.ReadFile(UserCredentialsPath())
	if err != nil {
		t.Fatal(err)
	}
	if !IsVaultData(raw) {
		t.Fatal("a credential write must keep the store encrypted")
	}
	if bytes.Contains(raw, []byte("sk-second")) {
		t.Fatal("a newly stored key must not land on disk in plaintext")
	}
	for key, want := range map[string]string{"DEEPSEEK_API_KEY": "sk-secret", "OPENAI_API_KEY": "sk-second"} {
		if value, ok := envFileValue(UserCredentialsPath(), key); !ok || value != want {
			t.Fatalf("envFileValue(%s) = %q/%v, want %q/true", key, value, ok, want)
		}
	}

	// RemoveCredential writes through the same path and must stay encrypted too.
	if err := RemoveCredential("OPENAI_API_KEY"); err != nil {
		t.Fatalf("RemoveCredential: %v", err)
	}
	raw, err = os.ReadFile(UserCredentialsPath())
	if err != nil {
		t.Fatal(err)
	}
	if !IsVaultData(raw) {
		t.Fatal("removing a credential must keep the store encrypted")
	}
	if _, ok := envFileValue(UserCredentialsPath(), "OPENAI_API_KEY"); ok {
		t.Fatal("the removed credential must be gone")
	}
}

func TestClearMasterPasswordRestoresPlaintext(t *testing.T) {
	resetCredentialStore(t)
	writePlainCredentialStore(t, "DEEPSEEK_API_KEY=sk-secret\n")
	if _, err := SetMasterPassword(testMasterPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := ClearMasterPassword(testMasterPassword); err != nil {
		t.Fatalf("ClearMasterPassword: %v", err)
	}
	if MasterPasswordConfigured() {
		t.Fatal("the store must report as unprotected after clearing")
	}
	if MasterPasswordUnlocked() {
		t.Fatal("clearing must drop the session key")
	}
	raw, err := os.ReadFile(UserCredentialsPath())
	if err != nil {
		t.Fatal(err)
	}
	if IsVaultData(raw) {
		t.Fatal("the store must be a plaintext .env again")
	}
	if !strings.Contains(string(raw), "DEEPSEEK_API_KEY=sk-secret") {
		t.Fatalf("plaintext content lost on clear: %q", raw)
	}
	if !CredentialStored("DEEPSEEK_API_KEY") {
		t.Fatal("the credential must remain stored after clearing")
	}
}

func TestClearMasterPasswordRejectsWrongPassword(t *testing.T) {
	resetCredentialStore(t)
	writePlainCredentialStore(t, "DEEPSEEK_API_KEY=sk-secret\n")
	if _, err := SetMasterPassword(testMasterPassword); err != nil {
		t.Fatal(err)
	}
	LockMasterPassword()
	if _, err := ClearMasterPassword(otherMasterPassword); !errors.Is(err, ErrMasterPasswordInvalid) {
		t.Fatalf("clear with the wrong password = %v, want ErrMasterPasswordInvalid", err)
	}
	if !MasterPasswordConfigured() {
		t.Fatal("a failed clear must not disable protection")
	}
}

func TestChangeMasterPasswordReKeys(t *testing.T) {
	resetCredentialStore(t)
	writePlainCredentialStore(t, "DEEPSEEK_API_KEY=sk-secret\n")
	if _, err := SetMasterPassword(testMasterPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := ChangeMasterPassword(testMasterPassword, otherMasterPassword); err != nil {
		t.Fatalf("ChangeMasterPassword: %v", err)
	}
	LockMasterPassword()
	if err := UnlockMasterPassword(testMasterPassword); !errors.Is(err, ErrMasterPasswordInvalid) {
		t.Fatalf("the old password must stop working, got %v", err)
	}
	if err := UnlockMasterPassword(otherMasterPassword); err != nil {
		t.Fatalf("the new password must work: %v", err)
	}
	if value, ok := envFileValue(UserCredentialsPath(), "DEEPSEEK_API_KEY"); !ok || value != "sk-secret" {
		t.Fatalf("credentials lost across re-key: %q/%v", value, ok)
	}
}

func TestTryAutoUnlockMasterPasswordFromEnvironment(t *testing.T) {
	resetCredentialStore(t)
	writePlainCredentialStore(t, "DEEPSEEK_API_KEY=sk-secret\n")
	if _, err := SetMasterPassword(testMasterPassword); err != nil {
		t.Fatal(err)
	}
	LockMasterPassword()

	t.Setenv(MasterPasswordEnvVar, otherMasterPassword)
	if unlocked, err := TryAutoUnlockMasterPassword(); !errors.Is(err, ErrMasterPasswordInvalid) || unlocked {
		t.Fatalf("wrong env password: unlocked=%v err=%v, want ErrMasterPasswordInvalid", unlocked, err)
	}
	if MasterPasswordUnlocked() {
		t.Fatal("a wrong environment password must not leave the store unlocked")
	}

	t.Setenv(MasterPasswordEnvVar, testMasterPassword)
	unlocked, err := TryAutoUnlockMasterPassword()
	if err != nil || !unlocked {
		t.Fatalf("env password unlock: unlocked=%v err=%v", unlocked, err)
	}

	file := filepath.Join(t.TempDir(), "master.txt")
	if err := os.WriteFile(file, []byte(testMasterPassword+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	LockMasterPassword()
	t.Setenv(MasterPasswordEnvVar, "")
	t.Setenv(MasterPasswordFileEnvVar, file)
	if unlocked, err := TryAutoUnlockMasterPassword(); err != nil || !unlocked {
		t.Fatalf("password-file unlock: unlocked=%v err=%v", unlocked, err)
	}
}

func TestAutoUnlockIsNoOpForPlaintextStore(t *testing.T) {
	resetCredentialStore(t)
	writePlainCredentialStore(t, "DEEPSEEK_API_KEY=sk-plain\n")
	if unlocked, err := TryAutoUnlockMasterPassword(); err != nil || unlocked {
		t.Fatalf("plaintext store: unlocked=%v err=%v", unlocked, err)
	}
	if value, ok := envFileValue(UserCredentialsPath(), "DEEPSEEK_API_KEY"); !ok || value != "sk-plain" {
		t.Fatalf("plaintext store must stay readable: %q/%v", value, ok)
	}
}

func TestVaultRejectsTamperedPayload(t *testing.T) {
	sealed, err := sealVaultForPassword([]byte("DEEPSEEK_API_KEY=sk\n"), testMasterPassword)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(sealed, []byte(`"data": "`), []byte(`"data": "AA`), 1)
	if _, err := openVaultWithPassword(tampered, testMasterPassword); err == nil {
		t.Fatal("a tampered vault must not decrypt")
	}
	if IsVaultData([]byte("DEEPSEEK_API_KEY=sk-plain\n")) {
		t.Fatal("a plaintext dotenv file must never look like a vault")
	}
}

// sealVaultForPassword / openVaultWithPassword expose the container primitives
// for tests without depending on the session-key state machine.
func sealVaultForPassword(plain []byte, password string) ([]byte, error) {
	params := newVaultKDFParams()
	key, err := deriveVaultKey(password, params)
	if err != nil {
		return nil, err
	}
	return sealVaultWithKey(plain, key, params)
}

func openVaultWithPassword(sealed []byte, password string) ([]byte, error) {
	cont, err := parseVaultContainer(sealed)
	if err != nil {
		return nil, err
	}
	params, err := kdfParamsFromContainer(cont)
	if err != nil {
		return nil, err
	}
	key, err := deriveVaultKey(password, params)
	if err != nil {
		return nil, err
	}
	return openVaultWithKey(sealed, key)
}
