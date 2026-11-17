// BlurCut tool downloads: one-click fetch of ffmpeg / Tesseract when they
// are missing. ffmpeg comes as the pinned gyan.dev essentials zip (SHA-256
// verified, ffmpeg.exe extracted with Go's archive/zip — no 7z needed on
// Windows). Tesseract ships as the official UB-Mannheim installer, which
// is downloaded and launched for the user to complete (it needs a GUI
// install and may need admin for C:\Program Files).
package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

const (
	ffmpegDownloadURL    = "https://www.gyan.dev/ffmpeg/builds/packages/ffmpeg-8.1.2-essentials_build.zip"
	ffmpegDownloadSHA    = "db580001caa24ac104c8cb856cd113a87b0a443f7bdf47d8c12b1d740584a2ec"
	tesseractDownloadURL = "https://github.com/tesseract-ocr/tesseract/releases/download/5.5.3/tesseract-ocr-w64-setup-5.5.3.20260724.exe"
)

type downloadStatus struct {
	Tool     string  `json:"tool"`
	State    string  `json:"state"` // idle, downloading, extracting, installing, done, error
	Message  string  `json:"message"`
	Progress float64 `json:"progress"`
}

type toolDownloader struct {
	mu      sync.Mutex
	states  map[string]*downloadStatus
	dataDir string
}

func newToolDownloader(dataDir string) *toolDownloader {
	return &toolDownloader{states: map[string]*downloadStatus{}, dataDir: dataDir}
}

func (d *toolDownloader) status(tool string) downloadStatus {
	d.mu.Lock()
	defer d.mu.Unlock()
	if s, ok := d.states[tool]; ok {
		return *s
	}
	return downloadStatus{Tool: tool, State: "idle"}
}

func (d *toolDownloader) set(tool, state, msg string, progress float64) {
	d.mu.Lock()
	d.states[tool] = &downloadStatus{Tool: tool, State: state, Message: msg, Progress: progress}
	d.mu.Unlock()
}

// toolsDir is where downloaded tools go: <exe>/tools if writable, else
// <dataDir>/tools. Both are searched by detectTools().
func (d *toolDownloader) toolsDir() string {
	exeTools := filepath.Join(exeDir(), "tools")
	if exeDir() != "" {
		if err := os.MkdirAll(exeTools, 0o755); err == nil {
			probe := filepath.Join(exeTools, ".write-test")
			if err := os.WriteFile(probe, []byte("ok"), 0o644); err == nil {
				_ = os.Remove(probe)
				return exeTools
			}
		}
	}
	dir := filepath.Join(d.dataDir, "tools")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

func downloadFile(url, dest string, onProgress func(got, total int64)) error {
	client := &http.Client{Timeout: 15 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	var got int64
	buf := make([]byte, 256*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			got += int64(n)
			if onProgress != nil {
				onProgress(got, resp.ContentLength)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// downloadFFmpeg fetches the pinned gyan.dev zip, verifies its SHA-256 and
// extracts ffmpeg.exe (and ffprobe.exe if present) into toolsDir.
func (d *toolDownloader) downloadFFmpeg(redetect func() Tools) {
	d.set("ffmpeg", "downloading", "Downloading ffmpeg (about 110 MB)…", 0)
	dir := d.toolsDir()
	zipPath := filepath.Join(dir, "ffmpeg-download.zip")
	err := downloadFile(ffmpegDownloadURL, zipPath, func(got, total int64) {
		if total > 0 {
			d.set("ffmpeg", "downloading", fmt.Sprintf("Downloading ffmpeg… %d MB / %d MB", got>>20, total>>20), float64(got)/float64(total))
		}
	})
	if err != nil {
		d.set("ffmpeg", "error", "Download failed: "+err.Error(), 0)
		return
	}
	sum, err := sha256File(zipPath)
	if err != nil || sum != ffmpegDownloadSHA {
		d.set("ffmpeg", "error", fmt.Sprintf("Download failed: checksum mismatch (got %s)", sum), 0)
		_ = os.Remove(zipPath)
		return
	}
	d.set("ffmpeg", "extracting", "Extracting ffmpeg.exe…", 0.9)
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		d.set("ffmpeg", "error", "Could not open download: "+err.Error(), 0)
		return
	}
	defer zr.Close()
	extracted := 0
	for _, f := range zr.File {
		base := filepath.Base(f.Name)
		if base != "ffmpeg.exe" && base != "ffprobe.exe" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		out, err := os.Create(filepath.Join(dir, base))
		if err != nil {
			_ = rc.Close()
			continue
		}
		_, _ = io.Copy(out, rc)
		_ = out.Close()
		_ = rc.Close()
		extracted++
	}
	_ = os.Remove(zipPath)
	if extracted == 0 {
		d.set("ffmpeg", "error", "Downloaded archive did not contain ffmpeg.exe", 0)
		return
	}
	if redetect != nil {
		redetect()
	}
	d.set("ffmpeg", "done", fmt.Sprintf("ffmpeg installed to %s — restart not needed", dir), 1)
}

// downloadTesseract fetches the official UB-Mannheim installer and launches
// it. The installer needs its GUI (and possibly admin) to finish, so the
// state stays "installing" until a re-check finds tesseract.
func (d *toolDownloader) downloadTesseract() {
	d.set("tesseract", "downloading", "Downloading Tesseract installer (about 26 MB)…", 0)
	dir := d.toolsDir()
	instPath := filepath.Join(dir, "tesseract-setup.exe")
	err := downloadFile(tesseractDownloadURL, instPath, func(got, total int64) {
		if total > 0 {
			d.set("tesseract", "downloading", fmt.Sprintf("Downloading Tesseract installer… %d MB / %d MB", got>>20, total>>20), float64(got)/float64(total))
		}
	})
	if err != nil {
		d.set("tesseract", "error", "Download failed: "+err.Error(), 0)
		return
	}
	if runtime.GOOS != "windows" {
		d.set("tesseract", "done", fmt.Sprintf("Installer downloaded to %s — run it to install Tesseract, then click Re-check tools.", instPath), 1)
		return
	}
	// Launch the installer GUI; the user clicks through it (Next, Next,
	// Finish). Silent install would need admin for C:\Program Files.
	cmd := exec.Command(instPath)
	if err := cmd.Start(); err != nil {
		d.set("tesseract", "error", "Could not start installer: "+err.Error(), 0)
		return
	}
	d.set("tesseract", "installing", "Tesseract installer is running — finish the setup, then click “Re-check tools”.", 0.9)
}
