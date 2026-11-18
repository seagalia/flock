//go:build !linux

package app

func (a *App) preInitOS() error {
	return nil
}
