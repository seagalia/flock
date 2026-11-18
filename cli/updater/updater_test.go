package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeReleases serves what GitHub does for seagalia/flock: the latest release, the archives and checksums.txt.
type fakeReleases struct {
	server           *httptest.Server
	tag              string
	status           int
	requests         atomic.Int32
	archives         map[string][]byte
	corruptChecksums bool
	hideArchives     bool
	// stallArchives sends the headers of an archive and nothing after, until the client gives up
	stallArchives bool
}

func newFakeReleases(tag string) *fakeReleases {
	f := &fakeReleases{tag: tag, status: http.StatusOK, archives: map[string][]byte{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	restore := SetBaseURLs(f.server.URL+"/repos/seagalia/flock", f.server.URL+"/download")
	DeferCleanup(func() {
		restore()
		f.server.Close()
	})
	return f
}

func (f *fakeReleases) serve(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	if f.status != http.StatusOK {
		w.WriteHeader(f.status)
		return
	}
	switch {
	case r.URL.Path == "/repos/seagalia/flock/releases/latest":
		fmt.Fprintf(w, `{"tag_name": %q}`, f.tag)
	case r.URL.Path == "/download/"+f.tag+"/checksums.txt":
		for name, content := range f.archives {
			sum := sha256.Sum256(content)
			hexSum := hex.EncodeToString(sum[:])
			if f.corruptChecksums {
				hexSum = strings.Repeat("0", len(hexSum))
			}
			fmt.Fprintf(w, "%s  %s\n", hexSum, name)
		}
	case strings.HasPrefix(r.URL.Path, "/download/"+f.tag+"/") && !f.hideArchives:
		content, found := f.archives[path.Base(r.URL.Path)]
		if !found {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if f.stallArchives {
			w.Header().Set("Content-Length", strconv.Itoa(len(content)))
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		w.Write(content) //nolint:errcheck
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// addArchive publishes the release archive of a platform, wrapped in a directory like goreleaser does.
func (f *fakeReleases) addArchive(goos, goarch, binaryContent string) {
	version := strings.TrimPrefix(f.tag, "v")
	name := archiveName(version, goos, goarch)
	wrapDir := fmt.Sprintf("flock_%s_%s_%s/", version, goos, goarch)
	if goos == "windows" {
		f.archives[name] = makeZip(wrapDir+platformBinaryName(goos), binaryContent)
		return
	}
	f.archives[name] = makeTarGz(
		tarEntry{name: wrapDir, typeflag: tar.TypeDir},
		tarEntry{name: wrapDir + "README.md", content: "readme", typeflag: tar.TypeReg},
		tarEntry{name: wrapDir + platformBinaryName(goos), content: binaryContent, typeflag: tar.TypeReg},
	).Bytes()
}

func makeZip(name, content string) []byte {
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	w, err := zw.Create(name)
	Expect(err).NotTo(HaveOccurred())
	_, err = w.Write([]byte(content))
	Expect(err).NotTo(HaveOccurred())
	Expect(zw.Close()).To(Succeed())
	return buf.Bytes()
}

type tarEntry struct {
	name     string
	content  string
	typeflag byte
}

func makeTarGz(entries ...tarEntry) *bytes.Buffer {
	buf := &bytes.Buffer{}
	gz := gzip.NewWriter(buf)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		header := &tar.Header{
			Name:     entry.name,
			Mode:     0755,
			Size:     int64(len(entry.content)),
			Typeflag: entry.typeflag,
		}
		Expect(tw.WriteHeader(header)).To(Succeed())
		_, err := tw.Write([]byte(entry.content))
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(tw.Close()).To(Succeed())
	Expect(gz.Close()).To(Succeed())
	return buf
}

func writeFile(filePath, content string) {
	Expect(os.WriteFile(filePath, []byte(content), 0755)).To(Succeed())
}

var _ = Describe("Updater", func() {
	Describe("IsNeedUpdate", func() {
		var (
			releases *fakeReleases
			cache    string
		)

		BeforeEach(func() {
			releases = newFakeReleases("v1.3.0")
			cache = filepath.Join(GinkgoT().TempDir(), "update-check.json")
		})

		It("finds a newer release", func() {
			need, remote, err := IsNeedUpdate(context.Background(), BinInfo{Version: "1.2.3"}, cache)

			Expect(err).NotTo(HaveOccurred())
			Expect(need).To(BeTrue())
			Expect(remote.String()).To(Equal("1.3.0"))
		})

		It("is up to date on the same version", func() {
			need, _, err := IsNeedUpdate(context.Background(), BinInfo{Version: "1.3.0"}, cache)

			Expect(err).NotTo(HaveOccurred())
			Expect(need).To(BeFalse())
		})

		It("is up to date when the local version is newer", func() {
			need, _, err := IsNeedUpdate(context.Background(), BinInfo{Version: "2.0.0"}, cache)

			Expect(err).NotTo(HaveOccurred())
			Expect(need).To(BeFalse())
		})

		It("never checks a -dev build", func() {
			need, _, err := IsNeedUpdate(context.Background(), BinInfo{Version: "1.0.0-dev"}, cache)

			Expect(err).NotTo(HaveOccurred())
			Expect(need).To(BeFalse())
			Expect(releases.requests.Load()).To(BeZero())
		})

		It("checks at most once a day", func() {
			_, _, err := IsNeedUpdate(context.Background(), BinInfo{Version: "1.2.3"}, cache)
			Expect(err).NotTo(HaveOccurred())
			need, remote, err := IsNeedUpdate(context.Background(), BinInfo{Version: "1.2.3"}, cache)

			Expect(err).NotTo(HaveOccurred())
			Expect(need).To(BeTrue())
			Expect(remote.String()).To(Equal("1.3.0"))
			Expect(releases.requests.Load()).To(BeEquivalentTo(1))
		})

		It("checks again when the cache is older than a day", func() {
			stale := checkCache{CheckedAt: time.Now().Add(-25 * time.Hour), LatestVersion: "1.0.0"}
			b, err := json.Marshal(stale)
			Expect(err).NotTo(HaveOccurred())
			writeFile(cache, string(b))

			need, remote, err := IsNeedUpdate(context.Background(), BinInfo{Version: "1.2.3"}, cache)

			Expect(err).NotTo(HaveOccurred())
			Expect(need).To(BeTrue())
			Expect(remote.String()).To(Equal("1.3.0"))
			Expect(releases.requests.Load()).To(BeEquivalentTo(1))
		})

		It("always asks without a cache path", func() {
			_, _, _ = IsNeedUpdate(context.Background(), BinInfo{Version: "1.2.3"}, "")
			_, _, _ = IsNeedUpdate(context.Background(), BinInfo{Version: "1.2.3"}, "")

			Expect(releases.requests.Load()).To(BeEquivalentTo(2))
		})

		It("fails quietly for the caller when GitHub answers 403", func() {
			releases.status = http.StatusForbidden

			need, _, err := IsNeedUpdate(context.Background(), BinInfo{Version: "1.2.3"}, cache)

			Expect(err).To(HaveOccurred())
			Expect(need).To(BeFalse())
		})

		It("remembers a failure for a day", func() {
			releases.status = http.StatusForbidden

			_, _, err := IsNeedUpdate(context.Background(), BinInfo{Version: "1.2.3"}, cache)
			Expect(err).To(HaveOccurred())
			need, _, err := IsNeedUpdate(context.Background(), BinInfo{Version: "1.2.3"}, cache)

			Expect(err).NotTo(HaveOccurred())
			Expect(need).To(BeFalse())
			Expect(releases.requests.Load()).To(BeEquivalentTo(1))
		})

		It("keeps the last known version when a later check fails", func() {
			stale := checkCache{CheckedAt: time.Now().Add(-25 * time.Hour), LatestVersion: "1.3.0"}
			b, err := json.Marshal(stale)
			Expect(err).NotTo(HaveOccurred())
			writeFile(cache, string(b))
			releases.status = http.StatusForbidden

			_, _, err = IsNeedUpdate(context.Background(), BinInfo{Version: "1.2.3"}, cache)
			Expect(err).To(HaveOccurred())
			need, remote, err := IsNeedUpdate(context.Background(), BinInfo{Version: "1.2.3"}, cache)

			Expect(err).NotTo(HaveOccurred())
			Expect(need).To(BeTrue())
			Expect(remote.String()).To(Equal("1.3.0"))
			Expect(releases.requests.Load()).To(BeEquivalentTo(1))
		})

		It("fails when GitHub is unreachable", func() {
			releases.server.Close()

			need, _, err := IsNeedUpdate(context.Background(), BinInfo{Version: "1.2.3"}, cache)

			Expect(err).To(HaveOccurred())
			Expect(need).To(BeFalse())
		})

		It("fails on an invalid local version", func() {
			_, _, err := IsNeedUpdate(context.Background(), BinInfo{Version: "abc"}, cache)

			Expect(err).To(HaveOccurred())
		})

		It("fails on an invalid release tag", func() {
			releases.tag = "not-a-version"

			_, _, err := IsNeedUpdate(context.Background(), BinInfo{Version: "1.0.0"}, cache)

			Expect(err).To(HaveOccurred())
		})
	})

	Describe("extractFromTarGz", func() {
		It("extracts only the binary into the given folder", func() {
			archive := makeTarGz(
				tarEntry{name: "dist/", typeflag: tar.TypeDir},
				tarEntry{name: "dist/README.md", content: "readme", typeflag: tar.TypeReg},
				tarEntry{name: "dist/" + platformBinaryName(runtime.GOOS), content: "binary-content", typeflag: tar.TypeReg},
			)
			dir := GinkgoT().TempDir()

			path, err := extractFromTarGz(archive, dir, platformBinaryName(runtime.GOOS))

			Expect(err).NotTo(HaveOccurred())
			Expect(filepath.Dir(path)).To(Equal(dir))
			Expect(os.ReadFile(path)).To(BeEquivalentTo("binary-content"))
		})

		It("fails when the binary is not in the archive", func() {
			archive := makeTarGz(tarEntry{name: "README.md", content: "readme", typeflag: tar.TypeReg})

			_, err := extractFromTarGz(archive, GinkgoT().TempDir(), "flock")

			Expect(err).To(MatchError(ContainSubstring("not found in tgz archive")))
		})

		It("fails on non gzip input", func() {
			_, err := extractFromTarGz(strings.NewReader("plain text"), GinkgoT().TempDir(), "flock")

			Expect(err).To(HaveOccurred())
		})
	})

	Describe("moveFile", func() {
		It("moves the content and removes the source", func() {
			dir := GinkgoT().TempDir()
			source := filepath.Join(dir, "source")
			dest := filepath.Join(dir, "dest")
			writeFile(source, "payload")

			Expect(moveFile(source, dest)).To(Succeed())
			Expect(source).NotTo(BeAnExistingFile())
			Expect(os.ReadFile(dest)).To(BeEquivalentTo("payload"))
		})

		It("fails on a missing source", func() {
			dir := GinkgoT().TempDir()

			Expect(moveFile(filepath.Join(dir, "missing"), filepath.Join(dir, "dest"))).NotTo(Succeed())
		})
	})

	Describe("Update", func() {
		var (
			binPath  string
			releases *fakeReleases
		)

		BeforeEach(func() {
			binPath = filepath.Join(GinkgoT().TempDir(), platformBinaryName(runtime.GOOS))
			writeFile(binPath, "old-binary")
			releases = newFakeReleases("v2.0.0")
		})

		It("gives up on a download stalled after its headers and leaves no temp file", func() {
			releases.addArchive(runtime.GOOS, runtime.GOARCH, "new-binary")
			releases.stallArchives = true
			previous := updateTimeout
			updateTimeout = 300 * time.Millisecond
			DeferCleanup(func() { updateTimeout = previous })
			done := make(chan error, 1)

			go func() { done <- Update(context.Background(), BinInfo{Version: "1.0.0"}, binPath, false) }()

			var err error
			Eventually(done, 5*time.Second).Should(Receive(&err))
			Expect(err).To(MatchError(context.DeadlineExceeded))
			Expect(os.ReadFile(binPath)).To(BeEquivalentTo("old-binary"))
			leftovers, globErr := filepath.Glob(filepath.Join(filepath.Dir(binPath), "*.new-*"))
			Expect(globErr).NotTo(HaveOccurred())
			Expect(leftovers).To(BeEmpty())
		})

		It("replaces the binary with the release one", func() {
			releases.addArchive(runtime.GOOS, runtime.GOARCH, "new-binary")

			Expect(Update(context.Background(), BinInfo{Version: "1.0.0"}, binPath, false)).To(Succeed())

			Expect(os.ReadFile(binPath)).To(BeEquivalentTo("new-binary"))
			info, err := os.Stat(binPath)
			Expect(err).NotTo(HaveOccurred())
			if runtime.GOOS != "windows" {
				Expect(info.Mode().Perm() & 0o111).NotTo(BeZero())
			}
		})

		It("keeps the binary on checksum mismatch", func() {
			releases.addArchive(runtime.GOOS, runtime.GOARCH, "new-binary")
			releases.corruptChecksums = true

			err := Update(context.Background(), BinInfo{Version: "1.0.0"}, binPath, false)

			Expect(err).To(MatchError(ContainSubstring("checksum mismatch")))
			Expect(os.ReadFile(binPath)).To(BeEquivalentTo("old-binary"))
		})

		It("keeps the binary when no asset matches the platform", func() {
			releases.addArchive("plan9", "mips", "other-platform")

			err := Update(context.Background(), BinInfo{Version: "1.0.0"}, binPath, false)

			Expect(err).To(MatchError(ContainSubstring("no release asset")))
			Expect(os.ReadFile(binPath)).To(BeEquivalentTo("old-binary"))
		})

		It("keeps the binary when the archive is listed but missing", func() {
			releases.addArchive(runtime.GOOS, runtime.GOARCH, "new-binary")
			releases.hideArchives = true

			err := Update(context.Background(), BinInfo{Version: "1.0.0"}, binPath, false)

			Expect(err).To(MatchError(ContainSubstring("no release asset")))
			Expect(os.ReadFile(binPath)).To(BeEquivalentTo("old-binary"))
		})

		It("writes the new binary next to the binary", func() {
			releases.addArchive("linux", "amd64", "new-binary")
			previous := renameFile
			var movedFrom string
			renameFile = func(from, to string) error {
				if to == binPath {
					movedFrom = from
				}
				return previous(from, to)
			}
			DeferCleanup(func() { renameFile = previous })

			Expect(update(context.Background(), binPath, false, "linux", "amd64")).To(Succeed())

			Expect(filepath.Dir(movedFrom)).To(Equal(filepath.Dir(binPath)))
		})

		It("restores the binary put aside on windows when the move is busy", func() {
			releases.addArchive("windows", "amd64", "new-exe")
			previous := renameFile
			renameFile = func(from, to string) error {
				if to == binPath {
					return errors.New("device or resource busy")
				}
				return previous(from, to)
			}
			DeferCleanup(func() { renameFile = previous })

			err := update(context.Background(), binPath, false, "windows", "amd64")

			Expect(err).To(MatchError(ContainSubstring("busy")))
			Expect(os.ReadFile(binPath)).To(BeEquivalentTo("old-binary"))
			Expect(binPath + ".old").NotTo(BeAnExistingFile())
		})

		Describe("when the new binary cannot be moved in", func() {
			BeforeEach(func() {
				previous := renameFile
				renameFile = func(from, to string) error {
					if to == binPath {
						return errors.New("disk on fire")
					}
					return previous(from, to)
				}
				DeferCleanup(func() { renameFile = previous })
				releases.addArchive("windows", "amd64", "new-exe")
			})

			It("restores the binary", func() {
				err := update(context.Background(), binPath, false, "windows", "amd64")

				Expect(err).To(MatchError(ContainSubstring("disk on fire")))
				Expect(os.ReadFile(binPath)).To(BeEquivalentTo("old-binary"))
				Expect(binPath + ".old").NotTo(BeAnExistingFile())
			})

			It("removes the temporary file", func() {
				releases.addArchive("linux", "arm64", "new-binary")

				Expect(update(context.Background(), binPath, false, "linux", "arm64")).NotTo(Succeed())

				files, err := os.ReadDir(filepath.Dir(binPath))
				Expect(err).NotTo(HaveOccurred())
				Expect(files).To(HaveLen(1))
				Expect(files[0].Name()).To(Equal(filepath.Base(binPath)))
			})
		})

		It("unpacks the zip archive of windows", func() {
			releases.addArchive("windows", "amd64", "new-exe")

			Expect(update(context.Background(), binPath, false, "windows", "amd64")).To(Succeed())

			Expect(os.ReadFile(binPath)).To(BeEquivalentTo("new-exe"))
		})
	})
})
