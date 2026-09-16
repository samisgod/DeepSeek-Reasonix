package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"reasonix/internal/config"
	fileencoding "reasonix/internal/fileutil/encoding"
	"reasonix/internal/i18n"
)

// secretsCommand manages the master password that encrypts Reasonix's
// credential store (<Reasonix home>/.env: provider API keys, bot secrets,
// remote-SSH passwords). Output is plain text, with --json on the status form,
// matching the newer machine-friendly reasonix subcommands.
func secretsCommand(args []string) int {
	return runSecretsCommand(args, os.Stdout, os.Stdin, os.Stderr)
}

const secretsUsage = `usage:
  reasonix secrets status [--json]             show master-password protection state
  reasonix secrets set [--password-stdin]      encrypt the credential store with a master password
  reasonix secrets change [--password-stdin]   re-key the credential store (reads current, then new)
  reasonix secrets unlock [--password-stdin]   verify a master password against the store
  reasonix secrets disable [--password-stdin]  decrypt the credential store back to plaintext .env

The master password also comes from ` + config.MasterPasswordEnvVar + ` or
` + config.MasterPasswordFileEnvVar + ` for non-interactive hosts. Losing the master
password makes the stored credentials unrecoverable.`

type secretsOptions struct {
	passwordStdin bool
	jsonOut       bool
}

func runSecretsCommand(args []string, stdout io.Writer, stdin io.Reader, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, secretsUsage)
		return 2
	}
	operation := args[0]
	options, rest, err := parseSecretsOptions(args[1:])
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		fmt.Fprintln(stderr, secretsUsage)
		return 2
	}
	if len(rest) > 0 {
		fmt.Fprintf(stderr, "error: unexpected argument %q\n", rest[0])
		fmt.Fprintln(stderr, secretsUsage)
		return 2
	}
	if options.jsonOut && operation != "status" {
		fmt.Fprintln(stderr, "error: --json is only supported by `reasonix secrets status`")
		return 2
	}

	// One source for the whole command so a multi-line stdin (change reads the
	// current and the new password) is consumed exactly once.
	source := &masterPasswordSource{options: options, in: stdin}

	switch operation {
	case "status":
		return secretsStatus(stdout, options)
	case "set":
		return secretsSet(stdout, stderr, source, false)
	case "change":
		return secretsChange(stdout, stderr, source)
	case "unlock":
		return secretsUnlock(stdout, stderr, source)
	case "disable":
		return secretsDisable(stdout, stderr, source)
	case "help", "--help", "-h":
		fmt.Fprintln(stdout, secretsUsage)
		return 0
	default:
		fmt.Fprintf(stderr, "error: unknown secrets operation %q\n", operation)
		fmt.Fprintln(stderr, secretsUsage)
		return 2
	}
}

func parseSecretsOptions(args []string) (secretsOptions, []string, error) {
	var options secretsOptions
	var rest []string
	for _, arg := range args {
		switch arg {
		case "--password-stdin":
			options.passwordStdin = true
		case "--json":
			options.jsonOut = true
		case "-h", "--help":
			return options, []string{"help"}, nil
		default:
			if strings.HasPrefix(arg, "-") {
				return options, nil, fmt.Errorf("unknown flag %q", arg)
			}
			rest = append(rest, arg)
		}
	}
	return options, rest, nil
}

func secretsStatus(stdout io.Writer, options secretsOptions) int {
	status := config.MasterPasswordStatusFor()
	if options.jsonOut {
		return writeMachineJSON(stdout, struct {
			SchemaVersion int    `json:"schema_version"`
			Command       string `json:"command"`
			Configured    bool   `json:"configured"`
			Unlocked      bool   `json:"unlocked"`
			Path          string `json:"path,omitempty"`
		}{
			SchemaVersion: machineSchemaVersion,
			Command:       "secrets.status",
			Configured:    status.Configured,
			Unlocked:      status.Unlocked,
			Path:          status.Path,
		})
	}
	state := "disabled"
	switch {
	case status.Configured && status.Unlocked:
		state = "enabled (unlocked in this process)"
	case status.Configured:
		state = "enabled (locked)"
	}
	fmt.Fprintf(stdout, "master password: %s\n", state)
	fmt.Fprintf(stdout, "credential store: %s\n", status.Path)
	return 0
}

func secretsSet(stdout io.Writer, stderr io.Writer, source *masterPasswordSource, allowEnv bool) int {
	if config.MasterPasswordConfigured() {
		fmt.Fprintln(stderr, "error: a master password is already enabled; use `reasonix secrets change`")
		return 1
	}
	password, err := source.read("New master password: ", "Repeat master password: ", allowEnv)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	path, err := config.SetMasterPassword(password)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	fmt.Fprintf(stdout, "master password enabled; credential store encrypted at %s\n", path)
	return 0
}

func secretsChange(stdout io.Writer, stderr io.Writer, source *masterPasswordSource) int {
	if !config.MasterPasswordConfigured() {
		fmt.Fprintln(stderr, "error: no master password is enabled; use `reasonix secrets set`")
		return 1
	}
	// The current password may come from the environment; the new one must be
	// supplied explicitly so a stray env var cannot silently re-key the store.
	current, err := source.read("Current master password: ", "", true)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	next, err := source.read("New master password: ", "Repeat master password: ", false)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	path, err := config.ChangeMasterPassword(current, next)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	fmt.Fprintf(stdout, "master password changed; credential store re-encrypted at %s\n", path)
	return 0
}

func secretsUnlock(stdout io.Writer, stderr io.Writer, source *masterPasswordSource) int {
	if !config.MasterPasswordConfigured() {
		fmt.Fprintln(stderr, "error: no master password is enabled")
		return 1
	}
	password, err := source.read("Master password: ", "", true)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if err := config.UnlockMasterPassword(password); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	fmt.Fprintln(stdout, "master password verified; credential store unlocked")
	return 0
}

func secretsDisable(stdout io.Writer, stderr io.Writer, source *masterPasswordSource) int {
	if !config.MasterPasswordConfigured() {
		fmt.Fprintln(stderr, "error: no master password is enabled")
		return 1
	}
	password, err := source.read("Master password: ", "", true)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	path, err := config.ClearMasterPassword(password)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	fmt.Fprintf(stdout, "master password disabled; credential store is plaintext again at %s\n", path)
	return 0
}

// masterPasswordSource resolves a master password from, in order: stdin
// (--password-stdin or a piped stdin), the REASONIX_MASTER_PASSWORD[_FILE]
// environment, and an interactive terminal prompt.
type masterPasswordSource struct {
	options secretsOptions
	in      io.Reader
	loaded  bool
	lines   []string
	index   int
}

// read obtains one password. A non-empty repeatLabel asks for the value twice
// and requires a match, which is only meant for freshly chosen passwords.
func (s *masterPasswordSource) read(label, repeatLabel string, allowEnv bool) (string, error) {
	repeat := strings.TrimSpace(repeatLabel) != ""
	if s.options.passwordStdin {
		return s.readPair(repeat)
	}
	if allowEnv {
		if password, ok := config.MasterPasswordFromEnv(); ok {
			return validateAcquiredPassword(password)
		}
	}
	if !isTTY(os.Stdin) {
		// Piped stdin is a natural script input even without the explicit flag;
		// an empty read is reported instead of falling back to a prompt.
		password, err := s.readLine()
		if err != nil || strings.TrimSpace(password) == "" {
			return "", fmt.Errorf("no terminal available for a password prompt; use --password-stdin or %s", config.MasterPasswordEnvVar)
		}
		if repeat {
			if err := s.requireMatch(password); err != nil {
				return "", err
			}
		}
		return validateAcquiredPassword(password)
	}
	password, err := promptMasterPassword(label)
	if err != nil {
		return "", err
	}
	if repeat {
		repeated, err := promptMasterPassword(repeatLabel)
		if err != nil {
			return "", err
		}
		if repeated != password {
			return "", errors.New("the two master passwords do not match")
		}
	}
	return validateAcquiredPassword(password)
}

// readPair reads a password, and a matching second line when repeat is set,
// from the command's stdin stream.
func (s *masterPasswordSource) readPair(repeat bool) (string, error) {
	password, err := s.readLine()
	if err != nil {
		return "", err
	}
	if repeat {
		if err := s.requireMatch(password); err != nil {
			return "", err
		}
	}
	return validateAcquiredPassword(password)
}

func (s *masterPasswordSource) requireMatch(password string) error {
	repeated, err := s.readLine()
	if err != nil {
		return err
	}
	if repeated != password {
		return errors.New("the two master passwords do not match")
	}
	return nil
}

// readLine returns the next stdin line. Stdin is slurped once (lazily, so a
// terminal stdin is never read unless a password actually comes from it) and
// decoded with Reasonix's shared encoding cascade: PowerShell pipes native
// stdin as UTF-16LE with a BOM, and Windows console redirects may use a legacy
// code page, so raw bytes must not be assumed to be UTF-8.
func (s *masterPasswordSource) readLine() (string, error) {
	if !s.loaded {
		raw, err := io.ReadAll(s.in)
		if err != nil {
			return "", err
		}
		s.lines = splitCredentialLines(string(fileencoding.DecodeToUTF8(raw)))
		s.loaded = true
	}
	if s.index >= len(s.lines) {
		return "", errors.New("no master password provided on stdin")
	}
	line := s.lines[s.index]
	s.index++
	return line, nil
}

// splitCredentialLines splits decoded stdin into password lines, tolerating
// CRLF and dropping the empty element a trailing newline leaves behind.
//
// Byte-order marks are stripped both up front and per line: Windows PowerShell
// 5.1 writes one UTF-8 BOM per piped object, so a two-line password can arrive
// as BOM+BOM+"first\nsecond", and the second BOM would otherwise become part of
// the first password and make a correct confirmation look mismatched.
func splitCredentialLines(text string) []string {
	for strings.HasPrefix(text, "\ufeff") {
		text = strings.TrimPrefix(text, "\ufeff")
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(line, "\ufeff")
	}
	return lines
}

func validateAcquiredPassword(password string) (string, error) {
	if strings.TrimSpace(password) == "" {
		return "", errors.New("empty master password")
	}
	return password, nil
}

func promptMasterPassword(label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	value, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return string(value), nil
}

// maxMasterPasswordAttempts caps interactive retries before giving up.
const maxMasterPasswordAttempts = 3

// commandNeedsCredentialStore reports whether a subcommand reads provider
// credentials and therefore must unlock a master-password protected store
// before it runs. Informational commands and `reasonix secrets` itself (which
// performs its own unlocking) are excluded so they never block on a prompt.
func commandNeedsCredentialStore(cmd string) bool {
	switch cmd {
	case "secrets", "vault", "version", "--version", "-v", "help", "--help", "-h",
		"completion", "docs-manifest", "config", "init":
		return false
	default:
		return true
	}
}

// unlockCredentialStoreForCommand unlocks a protected credential store for the
// commands that need credentials, returning a process exit code (0 = continue).
func unlockCredentialStoreForCommand(cmd string) int {
	if !commandNeedsCredentialStore(cmd) {
		return 0
	}
	if err := ensureMasterPasswordUnlocked(); err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
		return 1
	}
	return 0
}

// ensureMasterPasswordUnlocked makes a protected credential store readable from
// the environment, or by prompting on a terminal. It is a no-op when the store
// is plaintext.
func ensureMasterPasswordUnlocked() error {
	if !config.MasterPasswordConfigured() {
		return nil
	}
	unlocked, err := config.TryAutoUnlockMasterPassword()
	if err != nil {
		return err
	}
	if unlocked {
		return nil
	}
	if !isTTY(os.Stdin) {
		return fmt.Errorf("credentials store is locked; set %s or %s, or run `reasonix secrets unlock`",
			config.MasterPasswordEnvVar, config.MasterPasswordFileEnvVar)
	}
	for attempt := 0; attempt < maxMasterPasswordAttempts; attempt++ {
		password, err := promptMasterPassword("Master password: ")
		if err != nil {
			return err
		}
		err = config.UnlockMasterPassword(password)
		if err == nil {
			return nil
		}
		if !errors.Is(err, config.ErrMasterPasswordInvalid) {
			return err
		}
		fmt.Fprintln(os.Stderr, "invalid master password")
	}
	return errors.New("too many invalid master password attempts")
}

// portableCommand implements `reasonix config portable [on|off|status]`. It
// moves Reasonix's home (config, skills, commands, sessions, credentials) to a
// data folder beside the executable so the install is self-contained.
func portableCommand(args []string) int {
	return runPortableCommand(args, os.Stdout, os.Stderr)
}

func runPortableCommand(args []string, stdout io.Writer, stderr io.Writer) int {
	action := "status"
	jsonOut := false
	for _, arg := range args {
		switch arg {
		case "on", "off", "status":
			action = arg
		case "--json":
			jsonOut = true
		case "-h", "--help":
			portableUsage(stdout)
			return 0
		default:
			fmt.Fprintf(stderr, "error: unknown argument %q\n", arg)
			portableUsage(stderr)
			return 2
		}
	}
	switch action {
	case "on", "off":
		if err := config.EnablePortableMode(action == "on"); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
	}
	if jsonOut {
		return writeMachineJSON(stdout, struct {
			SchemaVersion int    `json:"schema_version"`
			Command       string `json:"command"`
			Enabled       bool   `json:"enabled"`
			Home          string `json:"home"`
			DataDir       string `json:"data_dir,omitempty"`
			MarkerPath    string `json:"marker_path,omitempty"`
			Source        string `json:"source,omitempty"`
		}{
			SchemaVersion: machineSchemaVersion,
			Command:       "config.portable",
			Enabled:       config.PortableModeEnabled(),
			Home:          config.ReasonixHomeDir(),
			DataDir:       config.PortableDataDir(),
			MarkerPath:    config.PortableMarkerPath(),
			Source:        config.PortableModeSource(),
		})
	}
	fmt.Fprintf(stdout, "portable mode: %s\n", onOff(config.PortableModeEnabled()))
	fmt.Fprintf(stdout, "reasonix home: %s\n", config.ReasonixHomeDir())
	fmt.Fprintf(stdout, "portable data dir: %s\n", config.PortableDataDir())
	if marker := config.PortableMarkerPath(); marker != "" {
		fmt.Fprintf(stdout, "marker file: %s\n", marker)
	}
	if source := config.PortableModeSource(); source != "" {
		fmt.Fprintf(stdout, "source: %s\n", source)
	}
	return 0
}

func onOff(value bool) string {
	if value {
		return "enabled"
	}
	return "disabled"
}

func portableUsage(w io.Writer) {
	fmt.Fprintln(w, `usage:
  reasonix config portable [on|off|status] [--json]

Portable mode keeps config, skills, commands, sessions, and credentials in a
data folder beside the executable. Enable it with `+config.PortableEnvVar+`
(or a `+config.PortableMarkerName+` file next to the binary) and relocate the
folder with `+config.PortableDirEnvVar+`. An explicit REASONIX_HOME still wins.`)
}
