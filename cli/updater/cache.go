package updater

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const checkInterval = 24 * time.Hour

type checkCache struct {
	CheckedAt     time.Time `json:"checked_at"`
	LatestVersion string    `json:"latest_version"`
}

// readCache returns the cache and whether it was written less than 24h ago.
func readCache(cachePath string) (cache checkCache, isFresh bool) {
	b, err := os.ReadFile(cachePath)
	if err != nil {
		return checkCache{}, false
	}
	if err := json.Unmarshal(b, &cache); err != nil {
		return checkCache{}, false
	}
	return cache, time.Since(cache.CheckedAt) < checkInterval
}

func writeCache(cachePath, latestVersion string) error {
	b, err := json.Marshal(checkCache{CheckedAt: time.Now(), LatestVersion: latestVersion})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(cachePath, b, 0o600)
}
