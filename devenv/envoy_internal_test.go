package devenv

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Envoy install", func() {
	Context("envoyAssetName", func() {
		It("should name the linux binaries of a release", func() {
			Expect(envoyAssetName("1.40.0", "amd64")).To(Equal("envoy-1.40.0-linux-x86_64"))
			Expect(envoyAssetName("1.40.0", "arm64")).To(Equal("envoy-1.40.0-linux-aarch_64"))
		})

		It("should refuse an architecture without binary", func() {
			_, err := envoyAssetName("1.40.0", "386")
			Expect(err).To(MatchError(ContainSubstring("386")))
		})
	})

	Context("findChecksum", func() {
		// the body of a clearsigned release checksums.txt.asc
		checksums := `-----BEGIN PGP SIGNED MESSAGE-----
Hash: SHA512

595ac27ad14fd3c45adab2e5bfbc4dc3151d768ca96ee607e18cd87525b5b286  debs.tar.gz
8aa137a7d00994ce43f613556f5a120709229f63bedb86eedde5ccbc402c2b80  envoy-1.40.0-linux-x86_64
a0e8b2850de257a11ea3a28056e28672dffd81296b140d1e9e8338890861bad5  envoy-contrib-1.40.0-linux-x86_64

-----BEGIN PGP SIGNATURE-----
wsG7BAEBCgBvBYJqzo2eCRDYzcN1AYHzH0cUAAAAAAAeACBzYWx0QG5vdGF0aW9u
-----END PGP SIGNATURE-----
`
		It("should find the checksum of the asset", func() {
			sum, err := findChecksum(strings.NewReader(checksums), "envoy-1.40.0-linux-x86_64")
			Expect(err).ToNot(HaveOccurred())
			Expect(sum).To(Equal("8aa137a7d00994ce43f613556f5a120709229f63bedb86eedde5ccbc402c2b80"))
		})

		It("should fail for an asset not in the release", func() {
			_, err := findChecksum(strings.NewReader(checksums), "envoy-1.40.0-linux-aarch_64")
			Expect(err).To(MatchError(ContainSubstring("no checksum")))
		})
	})

	Context("ensureEnvoy", func() {
		It("should keep an envoy already in the bin dir", func() {
			binDir := GinkgoT().TempDir()
			envoyPath := filepath.Join(binDir, "envoy")
			Expect(os.WriteFile(envoyPath, []byte("#!/bin/sh\n"), 0755)).To(Succeed())

			found, err := ensureEnvoy(context.Background(), binDir, defaultEnvoyVersion)
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(Equal(envoyPath))
		})
	})

	Context("resolveEnvoy", func() {
		BeforeEach(func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer GinkgoRecover()
				Fail("no download expected, got a request for " + r.URL.Path)
			}))
			DeferCleanup(server.Close)
			previousURL := releaseBaseURL
			releaseBaseURL = server.URL
			DeferCleanup(func() { releaseBaseURL = previousURL })
		})

		It("should refuse a set envoy path that doesn't exist, naming it", func() {
			missing := filepath.Join(GinkgoT().TempDir(), "no-envoy")
			_, err := resolveEnvoy(context.Background(), GinkgoT().TempDir(), defaultEnvoyVersion, missing)
			Expect(err).To(MatchError(ContainSubstring(missing)))
		})

		It("should use a set envoy path as is, downloading nothing", func() {
			envoyPath := filepath.Join(GinkgoT().TempDir(), "envoy")
			Expect(os.WriteFile(envoyPath, []byte("#!/bin/sh\n"), 0755)).To(Succeed())
			binDir := filepath.Join(GinkgoT().TempDir(), "bin")

			found, err := resolveEnvoy(context.Background(), binDir, defaultEnvoyVersion, envoyPath)
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(Equal(envoyPath))
			Expect(binDir).ToNot(BeADirectory())
		})
	})

	Context("downloadEnvoy", func() {
		var (
			binary      []byte
			listedSum   string
			envoyPath   string
			previousURL string
		)

		BeforeEach(func() {
			binary = []byte("fake envoy binary")
			hash := sha256.Sum256(binary)
			listedSum = hex.EncodeToString(hash[:])
			envoyPath = filepath.Join(GinkgoT().TempDir(), "envoy")

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1.40.0/checksums.txt.asc":
					fmt.Fprintf(w, "%s  envoy-1.40.0-linux-x86_64\n", listedSum)
				case "/v1.40.0/envoy-1.40.0-linux-x86_64":
					w.Write(binary) // nolint:errcheck
				default:
					http.NotFound(w, r)
				}
			}))
			DeferCleanup(server.Close)
			previousURL = releaseBaseURL
			releaseBaseURL = server.URL
			DeferCleanup(func() { releaseBaseURL = previousURL })
		})

		It("should install the binary once its checksum matches", func() {
			Expect(downloadEnvoy(context.Background(), envoyPath, "1.40.0", "amd64")).To(Succeed())

			installed, err := os.ReadFile(envoyPath)
			Expect(err).ToNot(HaveOccurred())
			Expect(installed).To(Equal(binary))
			info, err := os.Stat(envoyPath)
			Expect(err).ToNot(HaveOccurred())
			Expect(info.Mode().Perm() & 0100).ToNot(BeZero())
		})

		It("should install nothing when the checksum does not match", func() {
			listedSum = strings.Repeat("0", 64)

			err := downloadEnvoy(context.Background(), envoyPath, "1.40.0", "amd64")
			Expect(err).To(MatchError(ContainSubstring("checksum")))
			Expect(envoyPath).ToNot(BeAnExistingFile())
			leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(envoyPath), "envoy-download-*"))
			Expect(err).ToNot(HaveOccurred())
			Expect(leftovers).To(BeEmpty())
		})

		It("should fail within the timeout when the server stalls after the headers, leaving no file", func() {
			stalled := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1.40.0/envoy-1.40.0-linux-x86_64" {
					fmt.Fprintf(w, "%s  envoy-1.40.0-linux-x86_64\n", listedSum)
					return
				}
				w.Header().Set("Content-Length", "1000")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("start")) // nolint:errcheck
				w.(http.Flusher).Flush()
				select {
				case <-stalled:
				case <-r.Context().Done():
				}
			}))
			DeferCleanup(server.Close)
			DeferCleanup(func() { close(stalled) })
			releaseBaseURL = server.URL
			previousTimeout := envoyDownloadTimeout
			envoyDownloadTimeout = 300 * time.Millisecond
			DeferCleanup(func() { envoyDownloadTimeout = previousTimeout })

			started := time.Now()
			err := downloadEnvoy(context.Background(), envoyPath, "1.40.0", "amd64")
			Expect(err).To(MatchError(context.DeadlineExceeded))
			Expect(time.Since(started)).To(BeNumerically("<", 5*time.Second))
			Expect(envoyPath).ToNot(BeAnExistingFile())
			leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(envoyPath), "envoy-download-*"))
			Expect(err).ToNot(HaveOccurred())
			Expect(leftovers).To(BeEmpty())
		})

		It("should fail for a release that does not exist", func() {
			err := downloadEnvoy(context.Background(), envoyPath, "0.0.1", "arm64")
			Expect(err).To(MatchError(ContainSubstring("404")))
		})
	})
})
