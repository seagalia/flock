package cli

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/seagalia/flock/cli/resfile"
)

// maxResourceFileSize caps the body of a file write, resource files are a few kilobytes.
const maxResourceFileSize = 1 << 20

// resourceFiles lets the builder of the dashboard save the resource files it generates, paths are relative to cwd or
// absolute. It reads no file, the builder never appends to an existing one.
type resourceFiles struct {
	cwd string
}

type fileProblem struct {
	File    string `json:"file"`
	Index   int    `json:"index"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// info tells the page it runs under the cli, and where relative paths go.
func (f resourceFiles) info(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"cwd": f.cwd})
}

// write saves the body after the checks of `flock apply`, so the builder never writes a file apply would refuse.
func (f resourceFiles) write(w http.ResponseWriter, req *http.Request) {
	path, ok := f.path(w, req)
	if !ok {
		return
	}
	content, err := io.ReadAll(http.MaxBytesReader(w, req.Body, maxResourceFileSize))
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "file larger than 1 MiB")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	file := req.URL.Query().Get("path")
	docs, problems := resfile.Parse(file, content)
	problems = append(problems, resfile.Check(docs)...)
	if len(problems) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string][]fileProblem{"problems": toFileProblems(problems)})
		return
	}

	err = writeFile(path, content, req.URL.Query().Get("overwrite") == "true")
	if errors.Is(err, fs.ErrExist) {
		writeJSONError(w, http.StatusConflict, "file exists")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// onlyPut answers every method of /cli/v1/file but PUT, the builder only writes files.
func onlyPut(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Allow", http.MethodPut)
	writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
}

// path gives the file of the path query argument, or answers 400 when there is none.
func (f resourceFiles) path(w http.ResponseWriter, req *http.Request) (string, bool) {
	path := req.URL.Query().Get("path")
	if path == "" {
		writeJSONError(w, http.StatusBadRequest, "missing path")
		return "", false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(f.cwd, path)
	}
	return path, true
}

// writeFile never leaves a partial file. A new file is created with O_EXCL, which refuses an existing path in the same
// step, and removed when the write fails. An overwrite goes through a temp file next to path moved in place.
func writeFile(path string, content []byte, overwrite bool) error {
	if overwrite {
		return replaceFile(path, content)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	err = writeAndClose(file, content)
	if err != nil {
		os.Remove(path) // nolint:errcheck
	}
	return err
}

// replaceFile moves the temp file onto the target of a symlink, so the link stays, and gives it the mode of the file
// it replaces. A file that doesn't exist yet gets the mode of one the user writes.
func replaceFile(path string, content []byte) error {
	mode := os.FileMode(0644)
	resolved, err := filepath.EvalSymlinks(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		resolved = path
	case err != nil:
		return err
	default:
		info, err := os.Stat(resolved)
		if err != nil {
			return err
		}
		mode = info.Mode().Perm()
	}

	temp, err := os.CreateTemp(filepath.Dir(resolved), "."+filepath.Base(resolved)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name()) // nolint:errcheck
	err = writeAndClose(temp, content)
	if err != nil {
		return err
	}
	err = os.Chmod(temp.Name(), mode)
	if err != nil {
		return err
	}
	return os.Rename(temp.Name(), resolved)
}

func writeAndClose(file *os.File, content []byte) error {
	_, err := file.Write(content)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func toFileProblems(problems []resfile.Problem) []fileProblem {
	converted := make([]fileProblem, 0, len(problems))
	for _, problem := range problems {
		converted = append(converted, fileProblem{
			File:    problem.File,
			Index:   problem.Index,
			Field:   problem.Field,
			Message: problem.Message,
		})
	}
	return converted
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body) // nolint:errcheck
}
