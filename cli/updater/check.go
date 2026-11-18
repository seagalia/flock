package updater

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/blang/semver"
)

// Vars so specs can point them at an httptest server.
var (
	releasesBaseURL = "https://api.github.com/repos/seagalia/flock"
	downloadBaseURL = "https://github.com/seagalia/flock/releases/download"
)

// SetBaseURLs points the updater at other servers and returns a function restoring the previous ones.
// It exists for the specs of the packages using the updater, which must never reach GitHub.
func SetBaseURLs(releasesURL, downloadURL string) (restore func()) {
	previousReleases, previousDownload := releasesBaseURL, downloadBaseURL
	releasesBaseURL, downloadBaseURL = releasesURL, downloadURL
	return func() { releasesBaseURL, downloadBaseURL = previousReleases, previousDownload }
}

type BinInfo struct {
	Version string
	Commit  string
	Date    string
}

func newHTTPClient(timeout time.Duration, skipSSL bool) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: skipSSL}, //nolint:gosec // opt-in with --skip-ssl-validation
		},
		Timeout: timeout,
	}
}

// getLatestVersion asks the public GitHub releases API (no token) for the latest release, without its "v" prefix.
func getLatestVersion(ctx context.Context) (string, error) {
	url := releasesBaseURL + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := newHTTPClient(5*time.Second, false).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected response code %d on query %s", resp.StatusCode, url)
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("invalid response on query %s: %w", url, err)
	}
	return strings.TrimPrefix(strings.TrimSpace(release.TagName), "v"), nil
}

// IsNeedUpdate tells whether a newer release exists. GitHub is asked at most once per 24h:
// the answer is kept in the file cachePath, an empty cachePath always asks and keeps nothing.
func IsNeedUpdate(ctx context.Context, info BinInfo, cachePath string) (bool, semver.Version, error) {
	if strings.HasSuffix(info.Version, "-dev") {
		return false, semver.Version{}, nil
	}
	current, err := semver.Make(info.Version)
	if err != nil {
		return false, semver.Version{}, err
	}

	cache := checkCache{}
	isCached := false
	if cachePath != "" {
		cache, isCached = readCache(cachePath)
	}
	if isCached && cache.LatestVersion == "" {
		// The last check failed less than a day ago: do not hammer an unreachable or rate limited GitHub.
		return false, semver.Version{}, nil
	}

	latest := cache.LatestVersion
	if !isCached {
		latest, err = getLatestVersion(ctx)
		if err != nil {
			// A failure is remembered too, with the last known version, to ask once a day.
			latest = cache.LatestVersion
		}
		if cachePath != "" {
			// Best effort: failing to remember the answer only costs another request next time.
			_ = writeCache(cachePath, latest)
		}
		if err != nil {
			return false, semver.Version{}, err
		}
	}

	remote, err := semver.Make(latest)
	if err != nil {
		return false, semver.Version{}, err
	}
	return current.LT(remote), remote, nil
}
