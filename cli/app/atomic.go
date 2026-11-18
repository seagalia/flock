package app

import (
	"os"
	"path/filepath"
)

// renameFile is a var so specs can make the final move fail.
var renameFile = os.Rename

// writeFileAtomic replaces path with data, 0600. The temp file sits in the same directory so the rename stays in one
// filesystem, and a crash or a failure leaves the previous file whole instead of a truncated session.
func writeFileAtomic(path, tmpPattern string, data []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), tmpPattern)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	err = tmp.Chmod(0600)
	if err != nil {
		_ = tmp.Close()
		return err
	}
	_, err = tmp.Write(data)
	if err != nil {
		_ = tmp.Close()
		return err
	}
	err = tmp.Close()
	if err != nil {
		return err
	}
	return renameFile(tmp.Name(), path)
}
