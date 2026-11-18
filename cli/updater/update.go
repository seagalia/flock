package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	msg "github.com/ArthurHlt/messages"
)

const (
	binaryName  = "flock"
	projectName = "flock"

	defaultUpdateTimeout = 5 * time.Minute
)

// updateTimeout bounds the whole self-update, a variable so specs can shorten it. The http client has no timeout of
// its own because the archive can be large on a slow link, so a server stalling after its headers would hang forever.
var updateTimeout = defaultUpdateTimeout

// platformBinaryName is the name of the binary inside the archive of a platform.
func platformBinaryName(goos string) string {
	if goos == "windows" {
		return binaryName + ".exe"
	}
	return binaryName
}

// archiveName is the goreleaser archive of a platform, e.g. flock_1.2.3_linux_amd64.tar.gz.
func archiveName(version, goos, goarch string) string {
	extension := ".tar.gz"
	if goos == "windows" {
		extension = ".zip"
	}
	return fmt.Sprintf("%s_%s_%s_%s%s", projectName, version, goos, goarch, extension)
}

func extractFromTarGz(gzipStream io.Reader, binFolder, wanted string) (string, error) {
	uncompressedStream, err := gzip.NewReader(gzipStream)
	if err != nil {
		return "", err
	}

	tarReader := tar.NewReader(uncompressedStream)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != wanted {
			continue
		}
		return writeTarget(tarReader, wanted, binFolder)
	}
	return "", errors.New(wanted + " file not found in tgz archive")
}

func extractFromZip(archive []byte, binFolder, wanted string) (string, error) {
	zipReader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return "", err
	}
	for _, file := range zipReader.File {
		if file.FileInfo().IsDir() || filepath.Base(file.Name) != wanted {
			continue
		}
		content, err := file.Open()
		if err != nil {
			return "", err
		}
		defer content.Close()
		return writeTarget(content, wanted, binFolder)
	}
	return "", errors.New(wanted + " file not found in zip archive")
}

// writeTarget writes the new binary in the folder of the binary: the final rename stays in one
// filesystem, so it is atomic and works when the temp dir is a tmpfs.
func writeTarget(content io.Reader, name, binFolder string) (string, error) {
	outFile, err := os.CreateTemp(binFolder, name+".new-*")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(outFile, content)
	closeErr := outFile.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(outFile.Name())
		return "", err
	}
	return outFile.Name(), nil
}

func httpGet(ctx context.Context, client *http.Client, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return body, resp.StatusCode, err
}

// checksumOf finds the sha256 of an archive in a goreleaser checksums.txt (lines "<hex>  <archive name>").
func checksumOf(checksums []byte, archive string) (string, bool) {
	for line := range strings.SplitSeq(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == archive {
			return strings.ToLower(fields[0]), true
		}
	}
	return "", false
}

// downloadVerifiedArchive downloads the release archive of a platform and checks its sha256 against checksums.txt.
func downloadVerifiedArchive(ctx context.Context, client *http.Client, version, goos, goarch string) ([]byte, error) {
	archive := archiveName(version, goos, goarch)
	releaseURL := fmt.Sprintf("%s/v%s", downloadBaseURL, version)
	noAssetErr := fmt.Errorf("no release asset %s for platform %s/%s", archive, goos, goarch)

	checksums, status, err := httpGet(ctx, client, releaseURL+"/checksums.txt")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("unexpected response code %d on query %s/checksums.txt", status, releaseURL)
	}
	expected, found := checksumOf(checksums, archive)
	if !found {
		return nil, noAssetErr
	}

	content, status, err := httpGet(ctx, client, releaseURL+"/"+archive)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, noAssetErr
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("unexpected response code %d on query %s/%s", status, releaseURL, archive)
	}

	sum := sha256.Sum256(content)
	if actual := hex.EncodeToString(sum[:]); actual != expected {
		return nil, fmt.Errorf("checksum mismatch for %s: expected %s, got %s", archive, expected, actual)
	}
	return content, nil
}

// RemoveReplacedBinary deletes the flock.exe.old left by the previous update: a running exe can be
// renamed but not removed, so it is cleaned on the next run. It does nothing outside windows.
func RemoveReplacedBinary() {
	if runtime.GOOS != "windows" {
		return
	}
	selfPath, err := os.Executable()
	if err != nil {
		return
	}
	_ = os.Remove(selfPath + ".old")
}

func Update(ctx context.Context, info BinInfo, binPath string, skipSSL bool) error {
	return update(ctx, binPath, skipSSL, runtime.GOOS, runtime.GOARCH)
}

// update takes the platform as parameters so every archive format can be exercised on any host.
func update(ctx context.Context, binPath string, skipSSL bool, goos, goarch string) error {
	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()
	version, err := getLatestVersion(ctx)
	if err != nil {
		return err
	}

	content, err := downloadVerifiedArchive(ctx, newHTTPClient(0, skipSSL), version, goos, goarch)
	if err != nil {
		return err
	}

	binFolder := filepath.Dir(binPath)
	var tmpName string
	if goos == "windows" {
		tmpName, err = extractFromZip(content, binFolder, platformBinaryName(goos))
	} else {
		tmpName, err = extractFromTarGz(bytes.NewReader(content), binFolder, platformBinaryName(goos))
	}
	if err != nil {
		return err
	}

	return replaceBinary(tmpName, binPath, goos)
}

// renameFile is a var so specs can make the final move fail.
var renameFile = os.Rename

// replaceBinary moves the new binary tmpName over binPath. Whatever fails, the current binary stays
// in place and tmpName is removed.
func replaceBinary(tmpName, binPath, goos string) (err error) {
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()

	// A running exe can be renamed but not overwritten on windows: it is put aside, and put back on failure.
	oldPath := binPath + ".old"
	isPutAside := false
	if goos == "windows" {
		_ = os.Remove(oldPath)
		if err := os.Rename(binPath, oldPath); err != nil {
			return fmt.Errorf("error while renaming %s to %s: %s", binPath, oldPath, err)
		}
		isPutAside = true
	}

	err = renameFile(tmpName, binPath)
	isRemoved := false
	// a put aside binary is no longer at binPath: there is nothing to remove, the restore below handles it
	isBusy := err != nil && strings.Contains(err.Error(), "busy")
	if isBusy && !isPutAside {
		msg.Debug("device was busy, run os.Remove")
		if err := os.Remove(binPath); err != nil {
			return err
		}
		isRemoved = true
		err = renameFile(tmpName, binPath)
	}
	isCrossDevice := err != nil && (strings.Contains(err.Error(), "cross-device") || strings.Contains(err.Error(), "cannot move the file"))
	if isCrossDevice {
		msg.Debug("invalid cross-device link, run moveFile")
		err = moveFile(tmpName, binPath)
	}
	if err != nil {
		if isPutAside {
			if restoreErr := os.Rename(oldPath, binPath); restoreErr != nil {
				return fmt.Errorf("could not restore %s from %s (%s), you must reinstall manually: %w", binPath, oldPath, restoreErr, err)
			}
			return err
		}
		if isRemoved {
			return fmt.Errorf("very bad error while unlinking %s, program has been lost, you must reinstall manually: %w", binPath, err)
		}
		return err
	}
	return os.Chmod(binPath, 0755)
}

/*
GoLang: os.Rename() give error "invalid cross-device link" for Docker container with Volumes.
MoveFile(source, destination) will work moving file between folders
from gist: https://gist.github.com/var23rav/23ae5d0d4d830aff886c3c970b8f6c6b
*/
func moveFile(sourcePath, destPath string) error {
	msg.Debugf("Opening source file: %s", sourcePath)
	inputFile, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("Couldn't open source file: %s", err)
	}
	msg.Debugf("Creating dest file: %s", destPath)
	outputFile, err := os.Create(destPath)
	if err != nil {
		inputFile.Close()
		return fmt.Errorf("Couldn't open dest file: %s", err)
	}
	defer outputFile.Close()
	msg.Debug("Copying source to dest")
	_, err = io.Copy(outputFile, inputFile)
	inputFile.Close()
	if err != nil {
		return fmt.Errorf("Writing to output file failed: %s", err)
	}
	msg.Debugf("Removing original file: %s", sourcePath)
	err = os.Remove(sourcePath)
	if err != nil {
		return fmt.Errorf("Failed removing original file: %s", err)
	}
	return nil
}
