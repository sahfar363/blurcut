//go:build !windows

// BlurCut app window stub for non-Windows builds: the native window is
// Windows-only, so the caller falls back to opening the browser.
package main

import "errors"

func runAppWindow(url, dataDir string) error {
	return errors.New("app window is Windows-only")
}
