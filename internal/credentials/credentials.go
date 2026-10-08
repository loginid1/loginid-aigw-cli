// Package credentials keeps a profile's API key in the OS credential store:
// the macOS Keychain, Windows Credential Manager, or the Secret Service on
// Linux and the BSDs that have it.
//
// Where there is no store, as on most servers, containers and CI runners, Save
// fails unless the user asked for insecure storage. Then the key goes to a
// 0600 file next to the config. That is never a silent fallback: a user who
// thinks a key is in the Keychain must not find it in a plain file.
package credentials

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/zalando/go-keyring"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/config"
)

// keyringService names aigw's entries in the OS store; the profile is the
// account under it.
const keyringService = "aigw"

// keyPrefix is the marker devcon puts on every API key.
const keyPrefix = "aigw_sk_"

// ErrNotLoggedIn means the profile has no stored key.
var ErrNotLoggedIn = errors.New("not logged in")

// Save stores key for profile and records the profile in the config. It
// returns a short description of where the key went.
func Save(dir, profile, key string, insecure bool) (string, error) {
	cfg, err := config.Load(dir)
	if err != nil {
		return "", err
	}

	// Replacing a login must not leave the old key behind in the other store.
	prev, hadPrev := cfg[profile]

	p := config.Profile{Storage: config.StorageKeyring, KeyHint: Hint(key)}
	where := storeName()

	if insecure {
		p.Storage = config.StorageFile
		where = fileStorePath(dir)
		err = writeFileKey(dir, profile, key)
	} else {
		err = keyringSet(profile, key)
		if err != nil {
			err = fmt.Errorf("save to %s: %w\n"+
				"No usable credential store here. Set AIGW_API_KEY instead, or run "+
				"`aigw login --insecure-storage` to keep the key in a plain file", storeName(), err)
		}
	}

	if err != nil {
		return "", err
	}

	if hadPrev && prev.Storage != p.Storage {
		_ = deleteKey(dir, profile, prev.Storage)
	}

	cfg[profile] = p

	err = config.Save(dir, cfg)
	if err != nil {
		return "", err
	}

	return where, nil
}

// Load returns profile's key, or ErrNotLoggedIn.
func Load(dir, profile string) (string, error) {
	cfg, err := config.Load(dir)
	if err != nil {
		return "", err
	}

	p, ok := cfg[profile]
	if !ok {
		return "", ErrNotLoggedIn
	}

	var key string

	switch p.Storage {
	case config.StorageFile:
		key, err = readFileKey(dir, profile)
	default:
		key, err = keyringGet(profile)
		if errors.Is(err, keyring.ErrNotFound) {
			err = ErrNotLoggedIn
		}
	}

	if err != nil {
		if errors.Is(err, ErrNotLoggedIn) {
			return "", ErrNotLoggedIn
		}

		return "", fmt.Errorf("read API key for profile %q: %w", profile, err)
	}

	return key, nil
}

// Delete removes profile's key and its config entry. It reports false when
// there was nothing to remove.
func Delete(dir, profile string) (bool, error) {
	cfg, err := config.Load(dir)
	if err != nil {
		return false, err
	}

	p, ok := cfg[profile]
	if !ok {
		return false, nil
	}

	err = deleteKey(dir, profile, p.Storage)
	if err != nil {
		return false, err
	}

	delete(cfg, profile)

	return true, config.Save(dir, cfg)
}

// Hint masks key for display: "aigw_sk_…Xk7Q". Every key shares the prefix,
// so the last four characters are what tells two keys apart.
func Hint(key string) string {
	const tail = 4

	// Runes, not bytes: cutting a multi-byte character in half would write
	// invalid UTF-8 into config.toml. Login only accepts ASCII keys, but a key
	// from elsewhere must not be able to break the file either.
	rest := []rune(strings.TrimPrefix(key, keyPrefix))
	if len(rest) <= tail*2 {
		return "…"
	}

	head := ""
	if strings.HasPrefix(key, keyPrefix) {
		head = keyPrefix
	}

	return head + "…" + string(rest[len(rest)-tail:])
}

// LooksLikeKey reports whether key carries devcon's marker.
func LooksLikeKey(key string) bool { return strings.HasPrefix(key, keyPrefix) }

func deleteKey(dir, profile, storage string) error {
	if storage == config.StorageFile {
		return deleteFileKey(dir, profile)
	}

	err := keyringDelete(profile)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("delete from %s: %w", storeName(), err)
	}

	return nil
}

func storeName() string {
	switch runtime.GOOS {
	case "darwin":
		return "macOS Keychain"
	case "windows":
		return "Windows Credential Manager"
	default:
		return "the Secret Service keyring"
	}
}

// ---- insecure storage: a 0600 TOML file of profile = key ----

func fileStorePath(dir string) string { return filepath.Join(dir, "credentials.toml") }

func readFileKeys(dir string) (map[string]string, error) {
	keys := map[string]string{}

	_, err := toml.DecodeFile(fileStorePath(dir), &keys)
	if errors.Is(err, fs.ErrNotExist) {
		return keys, nil
	}

	if err != nil {
		return nil, fmt.Errorf("read %s: %w", fileStorePath(dir), err)
	}

	return keys, nil
}

func writeFileKeys(dir string, keys map[string]string) error {
	if len(keys) == 0 {
		err := os.Remove(fileStorePath(dir))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", fileStorePath(dir), err)
		}

		return nil
	}

	f, err := config.CreateTemp(dir)
	if err != nil {
		return err
	}

	err = toml.NewEncoder(f).Encode(keys)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())

		return fmt.Errorf("write credentials: %w", err)
	}

	return config.Commit(f, fileStorePath(dir))
}

func readFileKey(dir, profile string) (string, error) {
	keys, err := readFileKeys(dir)
	if err != nil {
		return "", err
	}

	key, ok := keys[profile]
	if !ok {
		return "", ErrNotLoggedIn
	}

	return key, nil
}

func writeFileKey(dir, profile, key string) error {
	keys, err := readFileKeys(dir)
	if err != nil {
		return err
	}

	keys[profile] = key

	return writeFileKeys(dir, keys)
}

func deleteFileKey(dir, profile string) error {
	keys, err := readFileKeys(dir)
	if err != nil {
		return err
	}

	delete(keys, profile)

	return writeFileKeys(dir, keys)
}
