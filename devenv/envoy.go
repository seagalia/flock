package devenv

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	msg "github.com/ArthurHlt/messages"
)

const (
	defaultEnvoyVersion         = "1.40.0"
	defaultEnvoyDownloadTimeout = 5 * time.Minute
)

// envoyDownloadTimeout bounds the whole download of the envoy release, specs shorten it
var envoyDownloadTimeout = defaultEnvoyDownloadTimeout

// releaseBaseURL is where envoy publishes its linux binaries, specs point it at a local server
var releaseBaseURL = "https://github.com/envoyproxy/envoy/releases/download"

// resolveEnvoy gives the envoy to run: envoyPath when set, which must exist since nothing is installed for it (an image
// ships its own envoy), otherwise the one of binDir, installed when missing.
func resolveEnvoy(ctx context.Context, binDir, version, envoyPath string) (string, error) {
	if envoyPath == "" {
		return ensureEnvoy(ctx, binDir, version)
	}
	_, err := os.Stat(envoyPath)
	if err != nil {
		return "", fmt.Errorf("envoy path %s: %w", envoyPath, err)
	}
	return envoyPath, nil
}

// ensureEnvoy gives the envoy binary of binDir, installed first when missing: downloaded from the
// envoy github releases on linux, installed with brew on darwin (envoy publishes no darwin binary).
func ensureEnvoy(ctx context.Context, binDir, version string) (string, error) {
	envoyPath := filepath.Join(binDir, "envoy")
	// Stat follows symlinks: a symlink to a removed brew envoy is installed again
	_, err := os.Stat(envoyPath)
	if err == nil {
		return envoyPath, nil
	}
	err = os.MkdirAll(binDir, 0755)
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "linux":
		err = downloadEnvoy(ctx, envoyPath, version, runtime.GOARCH)
	case "darwin":
		err = brewInstallEnvoy(envoyPath)
	default:
		err = fmt.Errorf("envoy can't be installed on %s", runtime.GOOS)
	}
	if err != nil {
		return "", fmt.Errorf("install envoy: %w", err)
	}
	return envoyPath, nil
}

func envoyAssetName(version, goarch string) (string, error) {
	archNames := map[string]string{"amd64": "x86_64", "arm64": "aarch_64"}
	archName, ok := archNames[goarch]
	if !ok {
		return "", fmt.Errorf("envoy publishes no linux binary for %s", goarch)
	}
	return fmt.Sprintf("envoy-%s-linux-%s", version, archName), nil
}

// downloadEnvoy writes the release binary in envoyPath once its sha256 matches the release checksums
func downloadEnvoy(ctx context.Context, envoyPath, version, goarch string) error {
	ctx, cancel := context.WithTimeout(ctx, envoyDownloadTimeout)
	defer cancel()
	assetName, err := envoyAssetName(version, goarch)
	if err != nil {
		return err
	}
	releaseURL := fmt.Sprintf("%s/v%s", releaseBaseURL, version)

	checksums, err := httpGet(ctx, releaseURL+"/checksums.txt.asc")
	if err != nil {
		return err
	}
	defer checksums.Close()
	expectedSum, err := findChecksum(checksums, assetName)
	if err != nil {
		return err
	}

	binaryURL := releaseURL + "/" + assetName
	msg.Infof("Downloading envoy %s from %s ...\n", version, binaryURL)
	binary, err := httpGet(ctx, binaryURL)
	if err != nil {
		return err
	}
	defer binary.Close()

	// written next to envoyPath then renamed: an interrupted download never leaves a broken envoy
	tempFile, err := os.CreateTemp(filepath.Dir(envoyPath), "envoy-download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tempFile.Name()) // nolint:errcheck
	hash := sha256.New()
	_, err = io.Copy(io.MultiWriter(tempFile, hash), binary)
	closeErr := tempFile.Close()
	if err != nil {
		return fmt.Errorf("download %s: %w", binaryURL, err)
	}
	if closeErr != nil {
		return closeErr
	}
	gotSum := hex.EncodeToString(hash.Sum(nil))
	if gotSum != expectedSum {
		return fmt.Errorf("checksum of %s is %s, release checksums give %s", binaryURL, gotSum, expectedSum)
	}
	err = os.Chmod(tempFile.Name(), 0755)
	if err != nil {
		return err
	}
	err = os.Rename(tempFile.Name(), envoyPath)
	if err != nil {
		return err
	}
	msg.Successf("envoy %s installed in %s\n", version, envoyPath)
	return nil
}

func httpGet(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", url, err)
	}
	resp, err := http.DefaultClient.Do(req) // nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close() // nolint:errcheck
		return nil, fmt.Errorf("get %s: status %s", url, resp.Status)
	}
	return resp.Body, nil
}

// findChecksum reads the sha256 of assetName in a release checksums.txt.asc. The file is clearsigned:
// its body is the plain `<sha256>  <asset>` lines, the signature is not checked.
func findChecksum(checksums io.Reader, assetName string) (string, error) {
	scanner := bufio.NewScanner(checksums)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		isAssetLine := len(fields) == 2 && fields[1] == assetName
		if isAssetLine {
			return fields[0], nil
		}
	}
	err := scanner.Err()
	if err != nil {
		return "", err
	}
	return "", fmt.Errorf("no checksum for %s in the release checksums", assetName)
}

// brewInstallEnvoy links the brew envoy in envoyPath, installed first with brew when missing as
// https://www.envoyproxy.io/docs/envoy/latest/start/install says for macOS. An installed envoy is
// linked as is: `brew install` would upgrade it.
func brewInstallEnvoy(envoyPath string) error {
	_, err := exec.LookPath("brew")
	if err != nil {
		return fmt.Errorf("envoy is installed with homebrew on macOS, install brew (https://brew.sh) or run: brew update && brew install envoy")
	}
	prefix, err := exec.Command("brew", "--prefix").Output()
	if err != nil {
		return fmt.Errorf("brew --prefix: %w", err)
	}
	brewEnvoy := filepath.Join(string(bytes.TrimSpace(prefix)), "bin", "envoy")

	_, err = os.Stat(brewEnvoy)
	if err != nil {
		msg.Info("Installing envoy with brew ...")
		install := exec.Command("brew", "install", "envoy")
		install.Stdout = os.Stdout
		install.Stderr = os.Stderr
		err = install.Run()
		if err != nil {
			return fmt.Errorf("brew install envoy: %w", err)
		}
		_, err = os.Stat(brewEnvoy)
		if err != nil {
			return fmt.Errorf("envoy not found where brew installs it: %w", err)
		}
	}

	// a link left to a removed brew envoy is replaced
	err = os.Remove(envoyPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	err = os.Symlink(brewEnvoy, envoyPath)
	if err != nil {
		return err
	}
	msg.Successf("brew envoy linked in %s\n", envoyPath)
	return nil
}
