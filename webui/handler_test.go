package webui_test

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"testing/fstest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/seagalia/flock/webui"
)

var _ = Describe("Mount", func() {
	// names written by the angular build of webui, hashed ones and the ones copied from public/ as they are
	hashedNames := []string{
		"main-Q6URIFR3.js",
		"styles-2LKPVUVL.css",
		"chunk-07kOFmJn.js",
		"chunk-lXpY8A-1.js",
		"chunk-D6_b6YfX.js",
		"chunk-B-xKlJPK.js",
		"media/material-icons-LEZCGFVT.woff2",
		"media/titillium-web-latin-400-normal-MFFBB4CQ.woff",
	}
	// not hashed even when some look like hashed names, so their content can change with a release
	plainNames := []string{
		"brand/icon.svg",
		"brand/lockup-dark.svg",
		"brand/icon-32.png",
		"app-settings.js",
		"chunk-short.js",
		"chunk-toolongname.js",
		"media/material-icons.woff2",
		"media/titillium-web-latin-400-normal.woff2",
		"index-Q6URIFR3.html",
	}
	builtFiles := fstest.MapFS{"index.html": {Data: []byte("<html>index</html>")}}
	for _, name := range append(append([]string{}, hashedNames...), plainNames...) {
		builtFiles[name] = &fstest.MapFile{Data: []byte(name)}
	}

	get := func(files fstest.MapFS, path string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		mux := http.NewServeMux()
		Mount(mux, "/dashboard", files)
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		return recorder
	}

	It("should redirect the prefix to its root", func() {
		resp := get(builtFiles, "/dashboard")
		Expect(resp.Code).To(Equal(http.StatusMovedPermanently))
		Expect(resp.Header().Get("Location")).To(Equal("/dashboard/"))
	})

	It("should serve index.html at the root without cache", func() {
		resp := get(builtFiles, "/dashboard/")
		Expect(resp.Code).To(Equal(http.StatusOK))
		Expect(resp.Body.String()).To(Equal("<html>index</html>"))
		Expect(resp.Header().Get("Cache-Control")).To(Equal("no-cache"))
		Expect(resp.Header().Get("Content-Type")).To(HavePrefix("text/html"))
	})

	It("should serve index.html for the pages of the angular router", func() {
		resp := get(builtFiles, "/dashboard/backends")
		Expect(resp.Code).To(Equal(http.StatusOK))
		Expect(resp.Body.String()).To(Equal("<html>index</html>"))
	})

	It("should serve the hashed files of the build as immutable", func() {
		for _, name := range hashedNames {
			resp := get(builtFiles, "/dashboard/"+name)
			Expect(resp.Code).To(Equal(http.StatusOK), name)
			Expect(resp.Body.String()).To(Equal(name))
			Expect(resp.Header().Get("Cache-Control")).To(Equal("public, max-age=31536000, immutable"), name)
		}
	})

	It("should not force the cache of files whose name has no hash", func() {
		for _, name := range plainNames {
			resp := get(builtFiles, "/dashboard/"+name)
			Expect(resp.Code).To(Equal(http.StatusOK), name)
			Expect(resp.Body.String()).To(Equal(name))
			Expect(resp.Header().Get("Cache-Control")).To(BeEmpty(), name)
		}
	})

	Context("with a file the build keeps gzipped", func() {
		var gzippedFiles fstest.MapFS

		BeforeEach(func() {
			var compressed bytes.Buffer
			writer := gzip.NewWriter(&compressed)
			_, err := writer.Write([]byte("console.log('main')"))
			Expect(err).NotTo(HaveOccurred())
			Expect(writer.Close()).To(Succeed())
			gzippedFiles = fstest.MapFS{
				"index.html":          {Data: []byte("<html>index</html>")},
				"main-Q6URIFR3.js.gz": {Data: compressed.Bytes()},
			}
		})

		getWith := func(acceptEncoding string) *httptest.ResponseRecorder {
			recorder := httptest.NewRecorder()
			mux := http.NewServeMux()
			Mount(mux, "/dashboard", gzippedFiles)
			req := httptest.NewRequest(http.MethodGet, "/dashboard/main-Q6URIFR3.js", nil)
			req.Header.Set("Accept-Encoding", acceptEncoding)
			mux.ServeHTTP(recorder, req)
			return recorder
		}

		It("should send it as it is to a client accepting gzip", func() {
			resp := getWith("gzip, deflate, br")
			Expect(resp.Code).To(Equal(http.StatusOK))
			Expect(resp.Header().Get("Content-Encoding")).To(Equal("gzip"))
			Expect(resp.Header().Get("Content-Type")).To(HavePrefix("text/javascript"))
			Expect(resp.Header().Get("Cache-Control")).To(Equal("public, max-age=31536000, immutable"))
			Expect(resp.Body.Bytes()).To(Equal(gzippedFiles["main-Q6URIFR3.js.gz"].Data))
		})

		It("should decompress it for a client not accepting gzip", func() {
			resp := getWith("")
			Expect(resp.Code).To(Equal(http.StatusOK))
			Expect(resp.Header().Get("Content-Encoding")).To(BeEmpty())
			Expect(resp.Body.String()).To(Equal("console.log('main')"))
		})
	})

	It("should tell how to build the dashboard when it is not built", func() {
		resp := get(fstest.MapFS{}, "/dashboard/")
		Expect(resp.Code).To(Equal(http.StatusServiceUnavailable))
		Expect(resp.Body.String()).To(ContainSubstring("make webui"))
	})
})
