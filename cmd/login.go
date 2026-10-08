package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/config"
	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/credentials"
)

// maxKeyBytes bounds what --with-key-stdin reads, so a wrong pipe cannot fill
// memory. Devcon keys are well under 100 bytes.
const maxKeyBytes = 4096

func newLoginCommand() *cobra.Command {
	var fromStdin, insecure, confirm bool

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Save an API key for aigw to use",
		Long: "login saves a devcon API key in the OS credential store: the macOS Keychain,\n" +
			"Windows Credential Manager, or the Secret Service keyring on Linux.\n\n" +
			"It asks for the key without echoing it. In scripts, pipe the key in with\n" +
			"--with-key-stdin rather than putting it on the command line.\n\n" +
			"If a key is already saved, login asks before replacing it. --confirm skips\n" +
			"the question; without a terminal, --confirm is required.\n\n" +
			"The key is not checked against the API yet.",
		Example: "  aigw login\n" +
			"  aigw login --with-key-stdin --confirm < key.txt",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := config.Dir()
			if err != nil {
				return err
			}

			cfg, err := config.Load(dir)
			if err != nil {
				return err
			}

			// Ask before reading the key, so nobody pastes a key only to be
			// told it will not be saved.
			prev, replacing := cfg[config.DefaultProfile]
			if replacing && !confirm {
				err = confirmReplace(cmd, prev.KeyHint, fromStdin)
				if err != nil {
					return err
				}
			}

			key, err := readKey(cmd, fromStdin)
			if err != nil {
				return err
			}

			if !credentials.LooksLikeKey(key) {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning: this does not look like a devcon API key (they start with aigw_sk_); saving it anyway")
			}

			where, err := credentials.Save(dir, config.DefaultProfile, key, insecure)
			if err != nil {
				return err
			}

			if replacing {
				fmt.Fprintf(cmd.ErrOrStderr(), "Replaced saved key %s with %s in %s.\n", prev.KeyHint, credentials.Hint(key), where)
			} else {
				fmt.Fprintf(cmd.ErrOrStderr(), "Saved API key %s to %s.\n", credentials.Hint(key), where)
			}

			if insecure {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning: the key is in a plain file; anyone who can read that file can use it")
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&fromStdin, "with-key-stdin", false, "Read the API key from standard input")
	cmd.Flags().BoolVar(&insecure, "insecure-storage", false, "Store the key in a plain 0600 file instead of the OS credential store")
	cmd.Flags().BoolVar(&confirm, "confirm", false, "Replace a saved key without asking")

	return cmd
}

// confirmReplace asks whether to replace the saved key. It needs a terminal on
// stdin; with --with-key-stdin, stdin is the key itself, so it cannot ask.
func confirmReplace(cmd *cobra.Command, hint string, fromStdin bool) error {
	refuse := fmt.Errorf("a key (%s) is already saved: pass --confirm to replace it, or run `aigw logout` first", hint)

	f, isFile := cmd.InOrStdin().(*os.File)
	if fromStdin || !isFile || !term.IsTerminal(int(f.Fd())) {
		return refuse
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "A key (%s) is already saved. Replace it? [y/N] ", hint)

	answer, err := readLine(f)
	if err != nil {
		return fmt.Errorf("read answer: %w", err)
	}

	if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
		return errors.New("aborted: the saved key was not changed")
	}

	return nil
}

// readLine reads up to a newline one byte at a time. A buffered reader could
// swallow bytes meant for the key prompt that follows.
func readLine(f *os.File) (string, error) {
	var (
		line []byte
		b    = make([]byte, 1)
	)

	for {
		n, err := f.Read(b)
		if n == 1 && b[0] != '\n' {
			line = append(line, b[0])
		}

		if n == 1 && b[0] == '\n' || errors.Is(err, io.EOF) {
			return string(line), nil
		}

		if err != nil {
			return "", err
		}
	}
}

func readKey(cmd *cobra.Command, fromStdin bool) (string, error) {
	in := cmd.InOrStdin()

	var raw []byte

	f, isFile := in.(*os.File)

	switch {
	case fromStdin:
		// One byte past the limit, so an oversized key is refused rather
		// than cut short and saved broken.
		b, err := io.ReadAll(io.LimitReader(in, maxKeyBytes+1))
		if err != nil {
			return "", fmt.Errorf("read API key: %w", err)
		}

		if len(b) > maxKeyBytes {
			return "", fmt.Errorf("input is longer than %d bytes; is this the right file?", maxKeyBytes)
		}

		raw = b
	case isFile && term.IsTerminal(int(f.Fd())):
		fmt.Fprint(cmd.ErrOrStderr(), "Paste your API key (input is hidden): ")

		b, err := term.ReadPassword(int(f.Fd()))

		fmt.Fprintln(cmd.ErrOrStderr())

		if err != nil {
			return "", fmt.Errorf("read API key: %w", err)
		}

		raw = b
	default:
		return "", errors.New("input is not a terminal: pass --with-key-stdin to read the key from a pipe")
	}

	key := strings.TrimSpace(string(raw))
	if key == "" {
		return "", errors.New("no API key given")
	}

	// Printable ASCII only. A control character would break the Authorization
	// header on every later request, and a multi-byte character would be cut
	// in half by the hint written to config.toml, leaving a file that neither
	// login nor logout can read.
	for _, r := range key {
		if r < 0x21 || r > 0x7e {
			return "", fmt.Errorf("the API key may only contain printable ASCII characters, with no spaces; found %q", r)
		}
	}

	return key, nil
}

func newLogoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the saved API key",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := config.Dir()
			if err != nil {
				return err
			}

			removed, err := credentials.Delete(dir, config.DefaultProfile)
			if err != nil {
				return err
			}

			if removed {
				fmt.Fprintln(cmd.ErrOrStderr(), "Removed the saved API key.")
			} else {
				fmt.Fprintln(cmd.ErrOrStderr(), "No saved API key.")
			}

			return nil
		},
	}
}
