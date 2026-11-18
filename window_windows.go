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
	"syscall"

	"github.com/jchv/go-webview2"
)

var (
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	user32            = syscall.NewLazyDLL("user32.dll")
	procGetConsoleWin = kernel32.NewProc("GetConsoleWindow")
	procShowWindow    = user32.NewProc("ShowWindow")
)

// hideConsoleWindow hides this process's console window, if it has one.
// The Windows release binary is built as a console-subsystem app, so
// double-clicking blurcut.exe spawns a console window next to the app
// window. Hiding it here — once the GUI window is about to open —
// keeps the console for CLI use (scan/blur/serve --no-browser never
// call runAppWindow). If the binary is ever built with
// -ldflags="-H windowsgui" there is no console at all and
// GetConsoleWindow returns 0, so this becomes a no-op.
func hideConsoleWindow() {
	hwnd, _, _ := procGetConsoleWin.Call()
	if hwnd == 0 {
		return
	}
	const swHide = 0
	procShowWindow.Call(hwnd, swHide)
}

func runAppWindow(url, dataDir string) error {
	hideConsoleWindow()
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
