package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"reasonix/internal/config"
)

const cliTestMasterPassword = "cli master password"

// resetCLICredentialStore restores an unprotected credential store and clears
// the session key so secrets cases cannot leak into each other.
func resetCLICredentialStore(t *testing.T) {
	t.Helper()
	config.LockMasterPassword()
	path := config.UserCredentialsPath()
	if path == "" {
		t.Fatal("credentials path must resolve in the isolated test home")
	}
	previous, readErr := os.ReadFile(path)
	existed := readErr == nil
	_ = os.Remove(path)
	t.Cleanup(func() {
		config.LockMasterPassword()
		if existed {
			_ = os.WriteFile(path, previous, 0o600)
			return
		}
		_ = os.Remove(path)
	})
}

func runSecretsForTest(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	return runSecretsForTestBytes(t, []byte(stdin), args...)
}

func runSecretsForTestBytes(t *testing.T, stdin []byte, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runSecretsCommand(args, &stdout, bytes.NewReader(stdin), &stderr)
	return code, stdout.String(), stderr.String()
}

// utf16LEWithBOM reproduces how PowerShell (and Windows console redirection)
// hands native stdin to a child process: UTF-16LE with a byte-order mark.
func utf16LEWithBOM(text string) []byte {
	units := utf16.Encode([]rune(text))
	out := make([]byte, 0, 2+len(units)*2)
	out = append(out, 0xFF, 0xFE)
	for _, unit := range units {
		out = append(out, byte(unit), byte(unit>>8))
	}
	return out
}

func TestSecretsPasswordStdinAcceptsPowerShellUTF16(t *testing.T) {
	resetCLICredentialStore(t)
	path := config.UserCredentialsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("DEEPSEEK_API_KEY=sk-utf16\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin := utf16LEWithBOM(cliTestMasterPassword + "\r\n" + cliTestMasterPassword + "\r\n")

	code, _, stderr := runSecretsForTestBytes(t, stdin, "set", "--password-stdin")
	if code != 0 {
		t.Fatalf("set from UTF-16 stdin: exit = %d, stderr = %s", code, stderr)
	}
	if !config.MasterPasswordConfigured() {
		t.Fatal("the store must be protected after a UTF-16 stdin set")
	}
	config.LockMasterPassword()
	if err := config.UnlockMasterPassword(cliTestMasterPassword); err != nil {
		t.Fatalf("the decoded password must be the ASCII one: %v", err)
	}
}

// TestSecretsPasswordStdinAcceptsPowerShellBOMs pins the exact byte stream
// Windows PowerShell 5.1 produces for a two-line pipe: one UTF-8 BOM per piped
// object, then "line\nline\r\n".
func TestSecretsPasswordStdinAcceptsPowerShellBOMs(t *testing.T) {
	resetCLICredentialStore(t)
	path := config.UserCredentialsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("DEEPSEEK_API_KEY=sk-bom\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin := "\ufeff\ufeff" + cliTestMasterPassword + "\n" + cliTestMasterPassword + "\r\n"

	code, _, stderr := runSecretsForTest(t, stdin, "set", "--password-stdin")
	if code != 0 {
		t.Fatalf("set from PowerShell stdin: exit = %d, stderr = %s", code, stderr)
	}
	config.LockMasterPassword()
	if err := config.UnlockMasterPassword(cliTestMasterPassword); err != nil {
		t.Fatalf("the decoded password must not carry the BOM: %v", err)
	}
}

func TestSecretsStatusReportsDisabledStore(t *testing.T) {
	resetCLICredentialStore(t)
	code, stdout, stderr := runSecretsForTest(t, "", "status")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, "master password: disabled") {
		t.Fatalf("stdout = %q", stdout)
	}
	if !strings.Contains(stdout, config.UserCredentialsPath()) {
		t.Fatalf("stdout must name the credential store: %q", stdout)
	}

	code, stdout, stderr = runSecretsForTest(t, "", "status", "--json")
	if code != 0 {
		t.Fatalf("json exit = %d, stderr = %s", code, stderr)
	}
	var payload struct {
		Command    string `json:"command"`
		Configured bool   `json:"configured"`
		Path       string `json:"path"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("status --json = %q: %v", stdout, err)
	}
	if payload.Command != "secrets.status" || payload.Configured {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.Path == "" {
		t.Fatal("--json status must include the credential store path")
	}
}

func TestSecretsSetUnlockDisableRoundTrip(t *testing.T) {
	resetCLICredentialStore(t)
	path := config.UserCredentialsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("DEEPSEEK_API_KEY=sk-cli\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// set reads the password twice (value, then confirmation).
	code, stdout, stderr := runSecretsForTest(t, cliTestMasterPassword+"\n"+cliTestMasterPassword+"\n", "set", "--password-stdin")
	if code != 0 {
		t.Fatalf("set exit = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, "master password enabled") {
		t.Fatalf("set stdout = %q", stdout)
	}
	if !config.MasterPasswordConfigured() {
		t.Fatal("the credential store must be protected after set")
	}
	if !config.CredentialStored("DEEPSEEK_API_KEY") {
		t.Fatal("credential lost after enabling the master password")
	}

	// A mismatched confirmation must be rejected.
	config.LockMasterPassword()
	if _, err := config.ClearMasterPassword(cliTestMasterPassword); err != nil {
		t.Fatalf("clear for mismatch case: %v", err)
	}
	code, _, stderr = runSecretsForTest(t, cliTestMasterPassword+"\nnot-the-same\n", "set", "--password-stdin")
	if code == 0 {
		t.Fatal("a mismatched confirmation must fail")
	}
	if !strings.Contains(stderr, "do not match") {
		t.Fatalf("stderr = %q", stderr)
	}
	if config.MasterPasswordConfigured() {
		t.Fatal("a rejected set must not enable protection")
	}

	if code, _, stderr = runSecretsForTest(t, cliTestMasterPassword+"\n"+cliTestMasterPassword+"\n", "set", "--password-stdin"); code != 0 {
		t.Fatalf("re-set exit = %d, stderr = %s", code, stderr)
	}

	// unlock verifies; a wrong password is rejected.
	config.LockMasterPassword()
	if code, _, _ = runSecretsForTest(t, "wrong password\n", "unlock", "--password-stdin"); code == 0 {
		t.Fatal("unlock with a wrong password must fail")
	}
	if code, stdout, stderr = runSecretsForTest(t, cliTestMasterPassword+"\n", "unlock", "--password-stdin"); code != 0 {
		t.Fatalf("unlock exit = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, "unlocked") {
		t.Fatalf("unlock stdout = %q", stdout)
	}

	// change reads the current password and the new one.
	code, _, stderr = runSecretsForTest(t, cliTestMasterPassword+"\nnew cli master password\nnew cli master password\n", "change", "--password-stdin")
	if code != 0 {
		t.Fatalf("change exit = %d, stderr = %s", code, stderr)
	}
	config.LockMasterPassword()
	if err := config.UnlockMasterPassword(cliTestMasterPassword); err == nil {
		t.Fatal("the replaced password must stop working")
	}
	if err := config.UnlockMasterPassword("new cli master password"); err != nil {
		t.Fatalf("the new password must work: %v", err)
	}

	// disable returns the store to plaintext.
	config.LockMasterPassword()
	code, stdout, stderr = runSecretsForTest(t, "new cli master password\n", "disable", "--password-stdin")
	if code != 0 {
		t.Fatalf("disable exit = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, "disabled") {
		t.Fatalf("disable stdout = %q", stdout)
	}
	if config.MasterPasswordConfigured() {
		t.Fatal("the store must be unprotected after disable")
	}
}

func TestSecretsRejectsBadInvocation(t *testing.T) {
	if code, _, _ := runSecretsForTest(t, "", "status", "--json", "--bogus"); code != 2 {
		t.Fatalf("unknown flag exit = %d, want 2", code)
	}
	if code, _, _ := runSecretsForTest(t, "", "set", "--json"); code != 2 {
		t.Fatalf("--json on set exit = %d, want 2", code)
	}
	if code, _, _ := runSecretsForTest(t, "", "frobnicate"); code != 2 {
		t.Fatalf("unknown operation exit = %d, want 2", code)
	}
	if code, _, _ := runSecretsForTest(t, ""); code != 2 {
		t.Fatalf("missing operation exit = %d, want 2", code)
	}
}

func TestPortableStatusReportsLayout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runPortableCommand([]string{"status"}, &stdout, &stderr); code != 0 {
		t.Fatalf("status exit = %d, stderr = %s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "portable mode:") || !strings.Contains(out, "reasonix home:") {
		t.Fatalf("stdout = %q", out)
	}

	stdout.Reset()
	if code := runPortableCommand([]string{"status", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("json exit = %d", code)
	}
	var payload struct {
		Command string `json:"command"`
		Home    string `json:"home"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("status --json = %q: %v", stdout.String(), err)
	}
	if payload.Command != "config.portable" || payload.Home == "" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestCommandNeedsCredentialStore(t *testing.T) {
	for _, cmd := range []string{"", "run", "chat", "serve", "web", "doctor", "acp", "bot"} {
		if !commandNeedsCredentialStore(cmd) {
			t.Fatalf("command %q must unlock the credential store", cmd)
		}
	}
	for _, cmd := range []string{"secrets", "vault", "config", "version", "help", "completion"} {
		if commandNeedsCredentialStore(cmd) {
			t.Fatalf("command %q must not block on a master-password prompt", cmd)
		}
	}
}
