package apicmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
)

// flagSecretFile is on the commands whose response carries a one-time secret
// (Operation.SecretFields). The generator reserves the name.
const flagSecretFile = "secret-file"

func secretFileHelp(op *Operation) string {
	return "Write " + strings.Join(op.SecretFields, " and ") + " to this new file instead of printing it " +
		"(mode 0600 on macOS and Linux; on Windows, protected only by your user folder's permissions)"
}

// createSecretFile creates path for the secret, owner-only. It refuses a path
// that exists, so it never overwrites a secret saved earlier.
func createSecretFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("--%s %s: the file already exists; choose a new path", flagSecretFile, path)
	}

	if err != nil {
		return nil, fmt.Errorf("--%s: %w", flagSecretFile, err)
	}

	return f, nil
}

// discardSecretFile removes a secret file that was never written.
func discardSecretFile(f *os.File) {
	if f == nil {
		return
	}

	_ = f.Close()
	_ = os.Remove(f.Name())
}

// moveSecrets writes op's secret fields from body to f, and returns body with
// each one replaced by a note saying where it went. One secret is written as
// its bare value, so `$(cat file)` works; several are written as a JSON object.
// When body has none, f is removed and body is returned unchanged.
func moveSecrets(w io.Writer, op *Operation, body []byte, f *os.File) ([]byte, error) {
	var top map[string]any

	err := json.Unmarshal(body, &top)
	if err != nil {
		discardSecretFile(f)

		return body, nil //nolint:nilerr // not an object: nothing to move, print it as it is
	}

	found := map[string]string{}

	for _, field := range op.SecretFields {
		if v, _ := top[field].(string); v != "" {
			found[field] = v
		}
	}

	if len(found) == 0 {
		discardSecretFile(f)
		fmt.Fprintf(w, "No %s in the response; %s was not created.\n", strings.Join(op.SecretFields, " or "), f.Name())

		return body, nil
	}

	content, err := secretFileContent(found)
	if err == nil {
		_, err = f.Write(content)
	}

	if closeErr := f.Close(); err == nil {
		err = closeErr
	}

	if err != nil {
		// The caller prints body, secret included, rather than lose it.
		_ = os.Remove(f.Name())

		return body, fmt.Errorf("write %s: %w; the secret is printed above instead: store it now", f.Name(), err)
	}

	names := make([]string, 0, len(found))
	for field := range found {
		top[field] = "written to " + f.Name()
		names = append(names, field)
	}

	slices.Sort(names)
	fmt.Fprintf(w, "Wrote %s to %s. It is shown only this once.\n", strings.Join(names, " and "), f.Name())

	out, err := json.Marshal(top)
	if err != nil {
		return nil, fmt.Errorf("format result: %w", err)
	}

	return out, nil
}

func secretFileContent(found map[string]string) ([]byte, error) {
	if len(found) == 1 {
		for _, v := range found {
			return []byte(v + "\n"), nil
		}
	}

	b, err := json.Marshal(found)
	if err != nil {
		return nil, fmt.Errorf("format secrets: %w", err)
	}

	return append(b, '\n'), nil
}

// warnOneTimeSecrets tells the user, on stderr, to store any one-time secret
// in body. stdout stays pure JSON for whoever reads it.
func warnOneTimeSecrets(w io.Writer, op *Operation, body []byte) {
	if len(op.SecretFields) == 0 {
		return
	}

	var top map[string]any
	if json.Unmarshal(body, &top) != nil {
		return
	}

	for _, field := range op.SecretFields {
		if v, _ := top[field].(string); v != "" {
			fmt.Fprintf(w, "warning: %s is shown only this once and cannot be read back; store it now, "+
				"or pass --%s next time\n", field, flagSecretFile)
		}
	}
}
