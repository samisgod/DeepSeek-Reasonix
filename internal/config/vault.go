package config

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/crypto/scrypt"

	"reasonix/internal/fileutil"
	fileencoding "reasonix/internal/fileutil/encoding"
)

// Master-password protection encrypts Reasonix's credential store
// (<Reasonix home>/.env) — the single file holding provider API keys, bot
// secrets, and remote-SSH passwords — with a key derived from a user-chosen
// master password. The file is replaced in place by a self-describing JSON
// container, so every reader/writer that already funnels through
// readCredentialText / writeCredentialsBytes keeps working unchanged.
//
// Threat model: someone who copies the data folder (backup, USB stick, cloud
// sync, stolen laptop with an unlocked disk) cannot read the API keys without
// the master password. It is deliberately not a defence against a live
// process — the derived key is held in memory while the process runs.
const (
	// MasterPasswordEnvVar supplies the master password non-interactively.
	MasterPasswordEnvVar = "REASONIX_MASTER_PASSWORD"
	// MasterPasswordFileEnvVar points at a file whose content is the password.
	MasterPasswordFileEnvVar = "REASONIX_MASTER_PASSWORD_FILE"

	vaultFormatV1     = "reasonix-vault-v1"
	vaultKDFScrypt    = "scrypt"
	vaultCipherAESGCM = "aes-256-gcm"
	vaultAAD          = "reasonix-credentials-v1"

	vaultKeyLen = 32
	// scrypt cost: 128*N*r = 32 MiB and ~50-100 ms per derivation, which keeps
	// offline guessing expensive without making an interactive unlock painful.
	vaultScryptN = 1 << 15
	vaultScryptR = 8
	vaultScryptP = 1

	// Bounds on attacker-controlled KDF parameters and payload sizes read from
	// disk, so a crafted container cannot turn unlock into a memory bomb.
	vaultMaxN       = 1 << 22
	vaultMaxR       = 32
	vaultMaxP       = 16
	vaultMaxSaltLen = 64
	vaultMaxDataLen = 16 << 20

	// minMasterPasswordLen is the shortest accepted master password.
	minMasterPasswordLen = 8
)

var (
	// ErrMasterPasswordRequired is returned when the credential store is
	// encrypted and no usable master password is available in this process.
	ErrMasterPasswordRequired = errors.New("credentials store is locked: master password required")
	// ErrMasterPasswordInvalid is returned when a supplied master password
	// does not decrypt the credential store.
	ErrMasterPasswordInvalid = errors.New("invalid master password")
	// ErrMasterPasswordNotSet is returned by vault operations when the
	// credential store is not encrypted.
	ErrMasterPasswordNotSet = errors.New("master password is not enabled")
	// ErrMasterPasswordAlreadySet is returned when enabling twice.
	ErrMasterPasswordAlreadySet = errors.New("master password is already enabled")
)

// vaultContainer is the on-disk form of the encrypted credential store. The
// reasonix_vault field doubles as the detection marker: a plaintext dotenv file
// can never look like this.
type vaultContainer struct {
	Format string `json:"reasonix_vault"`
	KDF    string `json:"kdf"`
	Salt   string `json:"salt"`
	N      int    `json:"n"`
	R      int    `json:"r"`
	P      int    `json:"p"`
	Cipher string `json:"cipher"`
	Nonce  string `json:"nonce"`
	Data   string `json:"data"`
}

type vaultKDFParams struct {
	Salt []byte
	N    int
	R    int
	P    int
}

// masterKeySession holds the derived key (and the KDF parameters that produced
// it, needed to re-seal rewrites with the same salt) for the current process
// only. It is never written to disk; locking zeroes the key.
var masterKeySession = struct {
	sync.RWMutex
	key    []byte
	params vaultKDFParams
}{}

// MasterPasswordStatus summarizes protection state for status output.
type MasterPasswordStatus struct {
	Configured bool   `json:"configured"`
	Unlocked   bool   `json:"unlocked"`
	Path       string `json:"path,omitempty"`
}

// MasterPasswordStatusFor reports whether the credential store is encrypted and
// whether this process currently holds the key.
func MasterPasswordStatusFor() MasterPasswordStatus {
	return MasterPasswordStatus{
		Configured: MasterPasswordConfigured(),
		Unlocked:   MasterPasswordUnlocked(),
		Path:       UserCredentialsPath(),
	}
}

// MasterPasswordConfigured reports whether the credential store is encrypted.
func MasterPasswordConfigured() bool {
	return vaultAtPath(UserCredentialsPath())
}

// MasterPasswordUnlocked reports whether this process holds a derived key.
func MasterPasswordUnlocked() bool {
	return masterKeyCopy() != nil
}

// IsVaultData reports whether raw looks like an encrypted credential store.
func IsVaultData(raw []byte) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	var head struct {
		Format string `json:"reasonix_vault"`
	}
	if err := json.Unmarshal(trimmed, &head); err != nil {
		return false
	}
	return strings.TrimSpace(head.Format) != ""
}

// UnlockMasterPassword derives the key from password and verifies it against the
// stored container. On success the key stays in memory until LockMasterPassword.
func UnlockMasterPassword(password string) error {
	raw, err := fileencoding.ReadFileUTF8(UserCredentialsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return ErrMasterPasswordNotSet
		}
		return err
	}
	if !IsVaultData(raw) {
		return ErrMasterPasswordNotSet
	}
	cont, err := parseVaultContainer(raw)
	if err != nil {
		return err
	}
	params, err := kdfParamsFromContainer(cont)
	if err != nil {
		return err
	}
	key, err := deriveVaultKey(password, params)
	if err != nil {
		return err
	}
	if _, err := openVaultWithKey(raw, key); err != nil {
		return err
	}
	setMasterKey(key, params)
	return nil
}

// LockMasterPassword drops the in-memory key. Subsequent credential reads fail
// with ErrMasterPasswordRequired until the store is unlocked again.
func LockMasterPassword() {
	masterKeySession.Lock()
	for i := range masterKeySession.key {
		masterKeySession.key[i] = 0
	}
	masterKeySession.key = nil
	masterKeySession.params = vaultKDFParams{}
	masterKeySession.Unlock()
}

// TryAutoUnlockMasterPassword unlocks from REASONIX_MASTER_PASSWORD or
// REASONIX_MASTER_PASSWORD_FILE when those are set. It reports whether the store
// is unlocked and never prompts, so it is safe in headless hosts.
func TryAutoUnlockMasterPassword() (bool, error) {
	if !MasterPasswordConfigured() {
		return false, nil
	}
	if MasterPasswordUnlocked() {
		return true, nil
	}
	password, ok := masterPasswordFromEnv()
	if !ok {
		return false, nil
	}
	if err := UnlockMasterPassword(password); err != nil {
		return false, err
	}
	return true, nil
}

// MasterPasswordFromEnv resolves the master password from
// REASONIX_MASTER_PASSWORD or REASONIX_MASTER_PASSWORD_FILE. It is exported so
// every frontend (CLI, desktop, tests) reads the environment identically.
func MasterPasswordFromEnv() (string, bool) {
	return masterPasswordFromEnv()
}

// masterPasswordFromEnv reads the master password from the environment. An
// unreadable password file is reported as absent so the caller can fall back to
// an interactive prompt.
func masterPasswordFromEnv() (string, bool) {
	if password := os.Getenv(MasterPasswordEnvVar); password != "" {
		return password, true
	}
	path := strings.TrimSpace(os.Getenv(MasterPasswordFileEnvVar))
	if path == "" {
		return "", false
	}
	expanded := ExpandVars(path)
	data, err := fileencoding.ReadFileUTF8(expanded)
	if err != nil {
		return "", false
	}
	password := strings.TrimRight(string(data), "\r\n")
	if password == "" {
		return "", false
	}
	return password, true
}

// SetMasterPassword encrypts the current credential store with password. It
// fails when protection is already enabled, so an existing vault can never be
// silently re-keyed by a typo.
func SetMasterPassword(password string) (string, error) {
	if err := validateMasterPassword(password); err != nil {
		return "", err
	}
	path := UserCredentialsPath()
	if path == "" {
		return "", errors.New("credentials store unavailable")
	}
	if MasterPasswordConfigured() {
		return "", ErrMasterPasswordAlreadySet
	}
	unlock, err := LockUserCredentialEdits()
	if err != nil {
		return "", err
	}
	defer unlock()
	plain, err := readCredentialText(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	params := newVaultKDFParams()
	key, err := deriveVaultKey(password, params)
	if err != nil {
		return "", err
	}
	sealed, err := sealVaultWithKey(plain, key, params)
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic0600(path, sealed); err != nil {
		return "", err
	}
	setMasterKey(key, params)
	return path, nil
}

// ClearMasterPassword decrypts the credential store back to plaintext .env. An
// empty password reuses the in-process key, otherwise password must verify.
func ClearMasterPassword(password string) (string, error) {
	path := UserCredentialsPath()
	if path == "" {
		return "", errors.New("credentials store unavailable")
	}
	raw, err := fileencoding.ReadFileUTF8(path)
	if err != nil {
		if os.IsNotExist(err) {
			return path, ErrMasterPasswordNotSet
		}
		return "", err
	}
	if !IsVaultData(raw) {
		return path, ErrMasterPasswordNotSet
	}
	unlock, err := LockUserCredentialEdits()
	if err != nil {
		return "", err
	}
	defer unlock()
	key, err := keyForWrite(raw, password)
	if err != nil {
		return "", err
	}
	plain, err := openVaultWithKey(raw, key)
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic0600(path, plain); err != nil {
		return "", err
	}
	LockMasterPassword()
	return path, nil
}

// ChangeMasterPassword re-encrypts the store under a new master password. An
// empty current password reuses the in-process key.
func ChangeMasterPassword(current, next string) (string, error) {
	if err := validateMasterPassword(next); err != nil {
		return "", err
	}
	path := UserCredentialsPath()
	if path == "" {
		return "", errors.New("credentials store unavailable")
	}
	raw, err := fileencoding.ReadFileUTF8(path)
	if err != nil {
		if os.IsNotExist(err) {
			return path, ErrMasterPasswordNotSet
		}
		return "", err
	}
	if !IsVaultData(raw) {
		return path, ErrMasterPasswordNotSet
	}
	unlock, err := LockUserCredentialEdits()
	if err != nil {
		return "", err
	}
	defer unlock()
	currentKey, err := keyForWrite(raw, current)
	if err != nil {
		return "", err
	}
	plain, err := openVaultWithKey(raw, currentKey)
	if err != nil {
		return "", err
	}
	params := newVaultKDFParams()
	nextKey, err := deriveVaultKey(next, params)
	if err != nil {
		return "", err
	}
	sealed, err := sealVaultWithKey(plain, nextKey, params)
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic0600(path, sealed); err != nil {
		return "", err
	}
	setMasterKey(nextKey, params)
	return path, nil
}

// validateMasterPassword enforces the minimum secret strength Reasonix accepts.
func validateMasterPassword(password string) error {
	if len([]rune(password)) < minMasterPasswordLen {
		return fmt.Errorf("master password must be at least %d characters", minMasterPasswordLen)
	}
	return nil
}

// keyForWrite returns the key to decrypt an existing container: the supplied
// password when given (verified against the container), else the session key.
func keyForWrite(raw []byte, password string) ([]byte, error) {
	if strings.TrimSpace(password) != "" {
		return deriveKeyForContainer(raw, password)
	}
	key := masterKeyCopy()
	if key == nil {
		return nil, ErrMasterPasswordRequired
	}
	return key, nil
}

// readCredentialText returns the plaintext credential content of path, decoding
// an encrypted store transparently. It is the single read funnel for both the
// dotenv and the line-oriented credential writers.
func readCredentialText(path string) ([]byte, error) {
	raw, err := fileencoding.ReadFileUTF8(path)
	if err != nil {
		return nil, err
	}
	if !IsVaultData(raw) {
		return raw, nil
	}
	if !samePath(path, UserCredentialsPath()) {
		return nil, fmt.Errorf("encrypted credential store at unexpected path %s", path)
	}
	key := masterKeyCopy()
	if key == nil {
		return nil, ErrMasterPasswordRequired
	}
	plain, err := openVaultWithKey(raw, key)
	if err != nil {
		return nil, err
	}
	return plain, nil
}

// writeCredentialsBytes writes credential content to path, encrypting it when
// encrypt is set. Callers that manage the protection flag themselves (the vault
// conversion commands) pass encrypt=false with already-sealed bytes.
func writeCredentialsBytes(path string, data []byte, encrypt bool) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("credentials store unavailable")
	}
	out := data
	if encrypt {
		key, params, ok := masterKeySessionSnapshot()
		if !ok {
			return ErrMasterPasswordRequired
		}
		sealed, err := sealVaultWithKey(data, key, params)
		if err != nil {
			return err
		}
		out = sealed
	}
	return writeFileAtomic0600(path, out)
}

// vaultAtPath reports whether the file at path is an encrypted credential store.
func vaultAtPath(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	raw, err := fileencoding.ReadFileUTF8(path)
	if err != nil {
		return false
	}
	return IsVaultData(raw)
}

func masterKeyCopy() []byte {
	masterKeySession.RLock()
	defer masterKeySession.RUnlock()
	if len(masterKeySession.key) == 0 {
		return nil
	}
	return append([]byte(nil), masterKeySession.key...)
}

func masterKeySessionSnapshot() ([]byte, vaultKDFParams, bool) {
	masterKeySession.RLock()
	defer masterKeySession.RUnlock()
	if len(masterKeySession.key) == 0 || len(masterKeySession.params.Salt) < 8 {
		return nil, vaultKDFParams{}, false
	}
	return append([]byte(nil), masterKeySession.key...), masterKeySession.params, true
}

func setMasterKey(key []byte, params vaultKDFParams) {
	masterKeySession.Lock()
	defer masterKeySession.Unlock()
	masterKeySession.key = append([]byte(nil), key...)
	masterKeySession.params = params
}

func newVaultKDFParams() vaultKDFParams {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		// crypto/rand failure is unrecoverable for a secret; the caller
		// surfaces the empty salt as an error instead of weakening the KDF.
		return vaultKDFParams{}
	}
	return vaultKDFParams{Salt: salt, N: vaultScryptN, R: vaultScryptR, P: vaultScryptP}
}

func deriveVaultKey(password string, params vaultKDFParams) ([]byte, error) {
	if len(params.Salt) < 8 {
		return nil, errors.New("cannot derive master key: salt unavailable")
	}
	if params.N <= 1 || params.N > vaultMaxN || params.R <= 0 || params.R > vaultMaxR || params.P <= 0 || params.P > vaultMaxP {
		return nil, errors.New("invalid master key derivation parameters")
	}
	key, err := scrypt.Key([]byte(password), params.Salt, params.N, params.R, params.P, vaultKeyLen)
	if err != nil {
		return nil, fmt.Errorf("derive master key: %w", err)
	}
	return key, nil
}

// deriveKeyForContainer derives the key from an on-disk container and verifies
// it by decrypting, so a wrong password is detected immediately.
func deriveKeyForContainer(raw []byte, password string) ([]byte, error) {
	cont, err := parseVaultContainer(raw)
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
	if _, err := openVaultWithKey(raw, key); err != nil {
		return nil, err
	}
	return key, nil
}

// kdfParamsFromContainer decodes the persisted KDF parameters of a container.
func kdfParamsFromContainer(cont vaultContainer) (vaultKDFParams, error) {
	salt, err := base64.StdEncoding.DecodeString(cont.Salt)
	if err != nil {
		return vaultKDFParams{}, errors.New("invalid master key salt")
	}
	return vaultKDFParams{Salt: salt, N: cont.N, R: cont.R, P: cont.P}, nil
}

func parseVaultContainer(raw []byte) (vaultContainer, error) {
	var cont vaultContainer
	if err := json.Unmarshal(bytes.TrimSpace(raw), &cont); err != nil {
		return vaultContainer{}, fmt.Errorf("parse credential vault: %w", err)
	}
	if cont.Format != vaultFormatV1 {
		return vaultContainer{}, fmt.Errorf("unsupported credential vault format %q", cont.Format)
	}
	if cont.KDF != vaultKDFScrypt {
		return vaultContainer{}, fmt.Errorf("unsupported credential vault KDF %q", cont.KDF)
	}
	if cont.Cipher != vaultCipherAESGCM {
		return vaultContainer{}, fmt.Errorf("unsupported credential vault cipher %q", cont.Cipher)
	}
	if cont.N <= 1 || cont.N > vaultMaxN || cont.R <= 0 || cont.R > vaultMaxR || cont.P <= 0 || cont.P > vaultMaxP {
		return vaultContainer{}, errors.New("invalid credential vault KDF parameters")
	}
	if len(cont.Salt) > vaultMaxSaltLen || len(cont.Data) > vaultMaxDataLen {
		return vaultContainer{}, errors.New("credential vault payload too large")
	}
	return cont, nil
}

func sealVaultWithKey(plain, key []byte, params vaultKDFParams) ([]byte, error) {
	if len(params.Salt) < 8 {
		return nil, errors.New("cannot encrypt credential store: salt unavailable")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate credential vault nonce: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, plain, []byte(vaultAAD))
	cont := vaultContainer{
		Format: vaultFormatV1,
		KDF:    vaultKDFScrypt,
		Salt:   base64.StdEncoding.EncodeToString(params.Salt),
		N:      params.N,
		R:      params.R,
		P:      params.P,
		Cipher: vaultCipherAESGCM,
		Nonce:  base64.StdEncoding.EncodeToString(nonce),
		Data:   base64.StdEncoding.EncodeToString(sealed),
	}
	out, err := json.MarshalIndent(cont, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func openVaultWithKey(raw, key []byte) ([]byte, error) {
	cont, err := parseVaultContainer(raw)
	if err != nil {
		return nil, err
	}
	nonce, err := base64.StdEncoding.DecodeString(cont.Nonce)
	if err != nil {
		return nil, errors.New("invalid credential vault nonce")
	}
	data, err := base64.StdEncoding.DecodeString(cont.Data)
	if err != nil {
		return nil, errors.New("invalid credential vault payload")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, errors.New("invalid credential vault nonce")
	}
	plain, err := gcm.Open(nil, nonce, data, []byte(vaultAAD))
	if err != nil {
		return nil, ErrMasterPasswordInvalid
	}
	return plain, nil
}

// writeFileAtomic0600 replaces path with data at 0600 via a same-directory
// temporary file, so a crash never leaves a truncated credential store.
func writeFileAtomic0600(path string, data []byte) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(dir, "credentials.*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := fileutil.ReplaceFile(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}
