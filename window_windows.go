//go:build windows

// BlurCut app window (Windows only): a native WebView2 window showing the
// same web UI as the browser mode. Pure Go, no CGO — jchv/go-webview2
// embeds WebView2Loader.dll and uses the Edge WebView2 runtime that ships
// with Windows 10/11. Returns an error (never crashes) when WebView2 is
// unavailable, so the caller can fall back to the browser.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jchv/go-webview2"
)

func runAppWindow(url, dataDir string) error {
	dataPath := filepath.Join(dataDir, "webview2")
	if err := os.MkdirAll(dataPath, 0o755); err != nil {
		return fmt.Errorf("webview2 data dir: %w", err)
	}
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		DataPath:  dataPath,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  "BlurCut",
			Width:  1280,
			Height: 860,
			Center: true,
		},
	})
	if w == nil {
		return fmt.Errorf("WebView2 runtime not available (install Microsoft Edge WebView2 Runtime)")
	}
	w.SetSize(1280, 860, webview2.HintNone)
	w.Navigate(url)
	w.Run()
	return nil
}
