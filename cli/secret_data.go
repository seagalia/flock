package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
)

const stdinValue = "-"

// readSecretData builds the data of a secret from `key=value` pairs and `key=path` files. A value of `-` reads stdin.
// Errors name the key and never the value, which may be a password typed on the command line.
func readSecretData(data []string, fromFiles []string, stdin io.Reader) (map[string]string, error) {
	result := make(map[string]string, len(data)+len(fromFiles))
	var stdinKey string

	for _, pair := range data {
		key, value, found := strings.Cut(pair, "=")
		if !found || key == "" {
			return nil, fmt.Errorf("--data expects key=value")
		}
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("key %s is given twice", key)
		}
		if value != stdinValue {
			result[key] = value
			continue
		}
		if stdinKey != "" {
			return nil, fmt.Errorf("only one key can read stdin, got %s and %s", stdinKey, key)
		}
		stdinKey = key
		content, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("error when reading stdin for key %s: %w", key, err)
		}
		// `echo secret | flock ...` appends a newline the user never meant to store.
		result[key] = strings.TrimSuffix(strings.TrimSuffix(string(content), "\n"), "\r")
	}

	for _, pair := range fromFiles {
		key, path, found := strings.Cut(pair, "=")
		if !found || key == "" || path == "" {
			return nil, fmt.Errorf("--from-file expects key=path")
		}
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("key %s is given twice", key)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("error when reading file of key %s: %w", key, err)
		}
		result[key] = string(content)
	}

	// The controller reads an empty value as "keep the stored one", so an empty value would be dropped without a word.
	for key, value := range result {
		if value == "" {
			return nil, fmt.Errorf("key %s has an empty value", key)
		}
	}
	return result, nil
}
