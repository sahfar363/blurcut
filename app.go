// BlurCut app launcher: start the local server on a free port, then open
// the UI — in the native app window on Windows, or in the browser.
package main

import (
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"time"
)

// listenOn binds cfg port, falling back to any free port if it is taken.
func listenOn(port int) (net.Listener, int, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err == nil {
		return ln, port, nil
	}
	ln, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, 0, err
	}
	return ln, ln.Addr().(*net.TCPAddr).Port, nil
}

// openBrowser opens url in the default browser (plain tab).
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

// openEdgeApp opens url in a chromeless Edge app window
// (msedge --app=...), falling back to the default browser.
func openEdgeApp(url string) {
	if runtime.GOOS == "windows" {
		for _, cand := range []string{
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		} {
			if fileExists(cand) {
				_ = exec.Command(cand, "--app="+url).Start()
				return
			}
		}
		if p, err := exec.LookPath("msedge"); err == nil {
			_ = exec.Command(p, "--app="+url).Start()
			return
		}
	}
	openBrowser(url)
}

// runAppMode starts the server and shows it in the native app window.
// On failure it falls back to Edge app mode, then the plain browser.
func runAppMode(cfg Config, tools Tools, store *JobStore) error {
	ln, port, err := listenOn(cfg.Port)
	if err != nil {
		return err
	}
	cfg.Port = port
	s := newServer(cfg, tools, store)
	go func() { _ = http.Serve(ln, s.routes()) }()
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	// Give the server a moment to start listening.
	for i := 0; i < 50; i++ {
		if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond); err == nil {
			_ = c.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := runAppWindow(url, cfg.DataDir); err != nil {
		fmt.Printf("BlurCut: app window unavailable (%v) — opening browser instead\n", err)
		openEdgeApp(url)
		// Block forever serving until killed; the window path returns
		// only when the window closes.
		select {}
	}
	return nil
}
func serveAndOpen(cfg Config, tools Tools, store *JobStore, noBrowser bool) error {
	ln, port, err := listenOn(cfg.Port)
	if err != nil {
		return err
	}
	cfg.Port = port
	fmt.Printf("BlurCut %s — open http://localhost:%d\n", version, port)
	printToolWarnings(tools)
	if !noBrowser {
		go func() {
			time.Sleep(300 * time.Millisecond)
			openBrowser(fmt.Sprintf("http://localhost:%d/", port))
		}()
	}
	s := newServer(cfg, tools, store)
	return http.Serve(ln, s.routes())
}

func printToolWarnings(tools Tools) {
	if !tools.FFmpegOK() {
		fmt.Printf("WARNING: ffmpeg not found — searched: %s\n", tools.searchedSummary("ffmpeg"))
		fmt.Println("  Put ffmpeg.exe next to blurcut.exe (or in tools/), or use the Tools section in the UI to download it.")
	} else {
		fmt.Printf("ffmpeg: %s\n", tools.FFmpeg)
	}
	if !tools.TesseractOK() {
		fmt.Printf("NOTE: tesseract not found — searched: %s\n", tools.searchedSummary("tesseract"))
		fmt.Println("  Auto-scan is off until Tesseract is found; manual boxes still work. Use the Tools section in the UI to download it.")
	} else {
		fmt.Printf("tesseract: %s\n", tools.Tesseract)
	}
}
