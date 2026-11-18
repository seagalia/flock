package devenv

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	adminPasswordFile    = "admin-password"
	generatedPasswordLen = 18
)

// resolveAdminPassword returns the admin password of the dev controller. A password from the environment always wins
// and is never written. On loopback the well known "admin" is kept, an exposed controller gets a random one stored in
// the data dir so that it survives restarts.
func resolveAdminPassword(dataDir string, exposed bool, fromEnv string) (string, error) {
	if fromEnv != "" {
		return fromEnv, nil
	}
	if !exposed {
		return loopbackAdminPassword, nil
	}

	path := filepath.Join(dataDir, adminPasswordFile)
	stored, err := os.ReadFile(path)
	if err == nil {
		if password := strings.TrimSpace(string(stored)); password != "" {
			return password, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read admin password: %w", err)
	}

	password, err := generatePassword()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(password+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write admin password: %w", err)
	}
	return password, nil
}

// generatePassword draws again a password starting with a dash: `flock login -p <password>`, printed by the banner,
// would read it as a flag.
func generatePassword() (string, error) {
	raw := make([]byte, generatedPasswordLen)
	for {
		if _, err := rand.Read(raw); err != nil {
			return "", fmt.Errorf("generate admin password: %w", err)
		}
		password := base64.RawURLEncoding.EncodeToString(raw)
		if !strings.HasPrefix(password, "-") {
			return password, nil
		}
	}
}
