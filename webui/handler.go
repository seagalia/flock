package webui

import (
	"compress/gzip"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strings"
)

// The angular build names the files it hashes in two ways, their content never changes for a name:
// entry bundles and media with an uppercase base32 hash (main-Q6URIFR3.js, styles-2LKPVUVL.css,
// media/titillium-web-latin-400-normal-46Z6MBEQ.woff2), lazy chunks with a base64url one (chunk-lXpY8A-1.js).
var (
	hashedBundleName = regexp.MustCompile(`-[A-Z0-9]{8}\.(js|css|woff2?|ttf)$`)
	hashedChunkName  = regexp.MustCompile(`(^|/)chunk-[A-Za-z0-9_-]{8}\.js$`)
)

// Mount serves the dashboard files under prefix (e.g. /dashboard) on mux, files is the angular build output
// (Browser() in production).
func Mount(mux *http.ServeMux, prefix string, files fs.FS) {
	prefix = strings.TrimSuffix(prefix, "/")
	handler := &dashboardHandler{prefix: prefix, files: files}
	// the prefix alone is registered too, so the handler redirects it to its root
	mux.Handle(prefix, handler)
	mux.Handle(prefix+"/", handler)
}

type dashboardHandler struct {
	prefix string
	files  fs.FS
}

func (d *dashboardHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path == d.prefix {
		http.Redirect(w, req, d.prefix+"/", http.StatusMovedPermanently)
		return
	}
	name := strings.TrimPrefix(req.URL.Path, d.prefix+"/")
	isGzipped := d.isFile(name + ".gz")
	if !isGzipped && !d.isFile(name) {
		// paths of the angular router, e.g. /dashboard/backends, are pages of index.html
		d.serveIndex(w)
		return
	}
	isHashed := hashedBundleName.MatchString(name) || hashedChunkName.MatchString(name)
	if isHashed {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	if !isGzipped {
		http.ServeFileFS(w, req, d.files, name)
		return
	}
	d.serveGzipped(w, req, name)
}

// serveGzipped serves a file `make webui` keeps only gzipped (scripts and styles, to shrink the binary), decompressed
// for a client that doesn't accept gzip
func (d *dashboardHandler) serveGzipped(w http.ResponseWriter, req *http.Request, name string) {
	file, err := d.files.Open(name + ".gz")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer file.Close() // nolint:errcheck
	w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
	w.Header().Set("Vary", "Accept-Encoding")
	if strings.Contains(req.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = io.Copy(w, file) // the client went away
		return
	}
	reader, err := gzip.NewReader(file)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = io.Copy(w, reader) // the client went away
}

func (d *dashboardHandler) isFile(name string) bool {
	if name == "" || name == "index.html" {
		return false
	}
	info, err := fs.Stat(d.files, name)
	return err == nil && !info.IsDir()
}

func (d *dashboardHandler) serveIndex(w http.ResponseWriter) {
	index, err := fs.ReadFile(d.files, "index.html")
	if err != nil {
		http.Error(w, "dashboard not built: run `make webui` then rebuild flock", http.StatusServiceUnavailable)
		return
	}
	// a new release changes the bundle names in index.html, browsers must not keep an old one
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(index) // nolint:errcheck
}
