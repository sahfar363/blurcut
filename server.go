// BlurCut web server: embedded offline UI + JSON API.
package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

//go:embed webui
var webFS embed.FS

type server struct {
	cfg     Config
	tools   Tools
	toolsMu sync.RWMutex
	store   *JobStore
	dl      *toolDownloader
}

func newServer(cfg Config, tools Tools, store *JobStore) *server {
	return &server{cfg: cfg, tools: tools, store: store, dl: newToolDownloader(cfg.DataDir)}
}

func (s *server) getTools() Tools {
	s.toolsMu.RLock()
	defer s.toolsMu.RUnlock()
	return s.tools
}

func (s *server) setTools(t Tools) {
	s.toolsMu.Lock()
	s.tools = t
	s.toolsMu.Unlock()
}

// redetect re-runs tool detection with the current config and stores it.
func (s *server) redetect() Tools {
	t := detectToolsWithConfig(s.cfg)
	s.setTools(t)
	return t
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/webui/", s.handleWebUI)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/tools", s.handleTools)
	mux.HandleFunc("/api/tools/recheck", s.handleToolsRecheck)
	mux.HandleFunc("/api/tools/download-ffmpeg", s.handleDownloadFFmpeg)
	mux.HandleFunc("/api/tools/download-tesseract", s.handleDownloadTesseract)
	mux.HandleFunc("/api/settings", s.handleSettings)
	mux.HandleFunc("/api/jobs", s.handleJobs)
	mux.HandleFunc("/api/jobs/", s.handleJobSub)
	mux.HandleFunc("/video/", s.handleVideo)
	mux.HandleFunc("/download/", s.handleDownload)
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := webFS.ReadFile("webui/index.html")
	if err != nil {
		http.Error(w, "ui missing", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

func (s *server) handleWebUI(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/webui/")
	if name == "" || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	b, err := webFS.ReadFile("webui/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasSuffix(name, ".css"):
		w.Header().Set("Content-Type", "text/css")
	case strings.HasSuffix(name, ".js"):
		w.Header().Set("Content-Type", "application/javascript")
	}
	_, _ = w.Write(b)
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	t := s.getTools()
	writeJSON(w, 200, map[string]any{
		"version":       version,
		"ffmpeg":        t.FFmpeg,
		"ffprobe":       t.FFprobe,
		"tesseract":     t.Tesseract,
		"tessdata":      t.TessData,
		"ffmpegOk":      t.FFmpegOK(),
		"tesseractOk":   t.TesseractOK(),
		"hasNvenc":      t.HasNVENC,
		"searched":      t.Searched,
		"ffmpegHelp":    "Bundled in the Windows zip (tools/ffmpeg.exe). Or download from https://www.gyan.dev/ffmpeg/builds/",
		"tesseractHelp": "Bundled in the Windows zip (tools/tesseract.exe). Or use Tools → Download Tesseract below.",
	})
}

// handleTools: GET returns full tool status; POST saves manual tool paths.
func (s *server) handleTools(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		t := s.getTools()
		writeJSON(w, 200, map[string]any{
			"ffmpeg":            map[string]any{"found": t.FFmpegOK(), "path": t.FFmpeg, "searched": t.Searched["ffmpeg"]},
			"ffprobe":           map[string]any{"found": t.FFprobe != "", "path": t.FFprobe, "searched": t.Searched["ffprobe"]},
			"tesseract":         map[string]any{"found": t.TesseractOK(), "path": t.Tesseract, "searched": t.Searched["tesseract"]},
			"tessdata":          t.TessData,
			"ffmpegDownload":    s.dl.status("ffmpeg"),
			"tesseractDownload": s.dl.status("tesseract"),
		})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		FfmpegPath    string `json:"ffmpegPath"`
		FfprobePath   string `json:"ffprobePath"`
		TesseractPath string `json:"tesseractPath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), 400)
		return
	}
	for _, p := range []string{req.FfmpegPath, req.FfprobePath, req.TesseractPath} {
		if p != "" && !fileExists(p) {
			http.Error(w, "file not found: "+p, 400)
			return
		}
	}
	s.cfg.FfmpegPath = req.FfmpegPath
	s.cfg.FfprobePath = req.FfprobePath
	s.cfg.TesseractPath = req.TesseractPath
	if err := saveConfig(s.cfg); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, s.redetect())
}

func (s *server) handleToolsRecheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	writeJSON(w, 200, s.redetect())
}

func (s *server) handleDownloadFFmpeg(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	if s.getTools().FFmpegOK() {
		writeJSON(w, 200, map[string]any{"ok": true, "message": "ffmpeg already found"})
		return
	}
	go s.dl.downloadFFmpeg(func() Tools { return s.redetect() })
	writeJSON(w, 202, map[string]bool{"ok": true})
}

func (s *server) handleDownloadTesseract(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	if s.getTools().TesseractOK() {
		writeJSON(w, 200, map[string]any{"ok": true, "message": "tesseract already found"})
		return
	}
	go s.dl.downloadTesseract()
	writeJSON(w, 202, map[string]bool{"ok": true})
}

func (s *server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, 200, s.cfg)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var cfg Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "bad json: "+err.Error(), 400)
		return
	}
	cfg.DataDir = s.cfg.DataDir
	if cfg.FfmpegPath == "" {
		cfg.FfmpegPath = s.cfg.FfmpegPath
	}
	if cfg.FfprobePath == "" {
		cfg.FfprobePath = s.cfg.FfprobePath
	}
	if cfg.TesseractPath == "" {
		cfg.TesseractPath = s.cfg.TesseractPath
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		cfg.Port = s.cfg.Port
	}
	if cfg.ScanInterval < 0.5 || cfg.ScanInterval > 2 {
		cfg.ScanInterval = 1.0
	}
	if cfg.BlurStrength < 10 || cfg.BlurStrength > 30 {
		cfg.BlurStrength = 20
	}
	if cfg.Patterns == nil {
		cfg.Patterns = s.cfg.Patterns
	}
	s.cfg = cfg
	if err := saveConfig(cfg); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.redetect()
	writeJSON(w, 200, cfg)
}

func (s *server) handleJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, 200, s.store.List())
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	if !s.getTools().FFmpegOK() {
		http.Error(w, "ffmpeg not found — searched: "+s.getTools().searchedSummary("ffmpeg")+". Put ffmpeg.exe next to blurcut.exe or in tools/, or use Tools → Download ffmpeg in the UI, then restart.", 503)
		return
	}
	if err := r.ParseMultipartForm(512 << 20); err != nil {
		http.Error(w, "upload too large or bad form: "+err.Error(), 400)
		return
	}
	f, hdr, err := r.FormFile("video")
	if err != nil {
		http.Error(w, "no video file in upload", 400)
		return
	}
	defer f.Close()
	id := newJobID()
	ext := strings.ToLower(filepath.Ext(hdr.Filename))
	if ext == "" {
		ext = ".mp4"
	}
	videoPath := filepath.Join(s.cfg.DataDir, "uploads", id+ext)
	dst, err := os.Create(videoPath)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if _, err := io.Copy(dst, f); err != nil {
		_ = dst.Close()
		http.Error(w, err.Error(), 500)
		return
	}
	_ = dst.Close()
	info, err := probeVideo(s.getTools(), videoPath)
	if err != nil {
		_ = os.Remove(videoPath)
		http.Error(w, "could not read video (ffmpeg problem?): "+err.Error(), 400)
		return
	}
	j := &Job{
		ID: id, Name: hdr.Filename, VideoPath: videoPath,
		ExportPath: filepath.Join(s.cfg.DataDir, "exports", id+"_blurred.mp4"),
		Duration:   info.Duration, Width: info.Width, Height: info.Height, FPS: info.FPS,
		Status: "uploaded", Detections: []Detection{}, Regions: []Region{},
		CreatedAt: now(),
	}
	s.store.Add(j)
	writeJSON(w, 201, j)
}

// handleJobSub routes /api/jobs/{id}[/scan|/regions|/export|...]
func (s *server) handleJobSub(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	j, ok := s.store.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch {
	case action == "" && r.Method == http.MethodGet:
		writeJSON(w, 200, j)
	case action == "" && r.Method == http.MethodDelete:
		s.store.Delete(id)
		writeJSON(w, 200, map[string]bool{"ok": true})
	case action == "scan" && r.Method == http.MethodPost:
		s.handleScan(w, r, j)
	case action == "regions" && r.Method == http.MethodGet:
		writeJSON(w, 200, j.Regions)
	case action == "regions" && r.Method == http.MethodPost:
		s.handleAddRegion(w, r, j)
	case strings.HasPrefix(action, "regions/") && r.Method == http.MethodDelete:
		rid := strings.TrimPrefix(action, "regions/")
		s.deleteRegion(j, rid)
		writeJSON(w, 200, map[string]bool{"ok": true})
	case strings.HasPrefix(action, "regions/") && r.Method == http.MethodPost:
		rid := strings.TrimPrefix(action, "regions/")
		s.updateRegion(w, r, j, rid)
	case action == "export" && r.Method == http.MethodPost:
		s.handleExport(w, r, j)
	default:
		http.Error(w, "unknown action", 404)
	}
}

func (s *server) handleScan(w http.ResponseWriter, r *http.Request, j *Job) {
	tools := s.getTools()
	if !tools.TesseractOK() {
		http.Error(w, "tesseract not found — searched: "+tools.searchedSummary("tesseract")+". Auto-scan needs Tesseract OCR; it is bundled in the Windows zip (tools/tesseract.exe), or use Tools → Download Tesseract in the UI. Manual boxes still work.", 503)
		return
	}
	if !tools.FFmpegOK() {
		http.Error(w, "ffmpeg not found — searched: "+tools.searchedSummary("ffmpeg"), 503)
		return
	}
	if j.Status == "scanning" || j.Status == "exporting" {
		http.Error(w, "job busy", 409)
		return
	}
	s.store.SetStatus(j, "scanning", "Extracting frames and running OCR…", 0)
	go func() {
		info := VideoInfo{Duration: j.Duration, Width: j.Width, Height: j.Height, FPS: j.FPS}
		dets, err := scanVideo(tools, j.VideoPath, s.cfg.ScanInterval, s.cfg.Keywords, s.cfg.Patterns, info)
		if err != nil {
			s.store.SetStatus(j, "error", "Scan failed: "+err.Error(), 0)
			return
		}
		j.Detections = dets
		regions := make([]Region, 0, len(dets))
		for _, d := range dets {
			regions = append(regions, d.Region)
		}
		j.Regions = regions
		s.store.SetStatus(j, "scanned", fmt.Sprintf("Found %d sensitive region(s)", len(dets)), 1)
	}()
	writeJSON(w, 202, map[string]bool{"ok": true})
}

func (s *server) handleAddRegion(w http.ResponseWriter, r *http.Request, j *Job) {
	var reg Region
	if err := json.NewDecoder(r.Body).Decode(&reg); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if reg.ID == "" {
		reg.ID = "man-" + newJobID()
	}
	reg.Source = "manual"
	if reg.Label == "" {
		reg.Label = "manual"
	}
	reg.Enabled = true
	if reg.W == 0 || reg.H == 0 {
		if reg.RW > 0 && reg.RH > 0 {
			relativeToPixel(&reg, j.Width, j.Height)
		} else {
			http.Error(w, "region needs a size", 400)
			return
		}
	}
	if reg.RX == 0 && reg.RW == 0 {
		pixelToRelative(&reg, j.Width, j.Height)
	}
	if reg.End <= reg.Start {
		reg.End = reg.Start + 5
	}
	if reg.End > j.Duration && j.Duration > 0 {
		reg.End = j.Duration
	}
	j.Regions = append(j.Regions, reg)
	s.store.save()
	writeJSON(w, 201, reg)
}

func (s *server) deleteRegion(j *Job, rid string) {
	for i, r := range j.Regions {
		if r.ID == rid {
			j.Regions = append(j.Regions[:i], j.Regions[i+1:]...)
			break
		}
	}
	s.store.save()
}

func (s *server) updateRegion(w http.ResponseWriter, r *http.Request, j *Job, rid string) {
	var upd Region
	if err := json.NewDecoder(r.Body).Decode(&upd); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	for i := range j.Regions {
		if j.Regions[i].ID == rid {
			if upd.Label != "" {
				j.Regions[i].Label = upd.Label
			}
			j.Regions[i].Enabled = upd.Enabled
			if upd.Start != 0 || upd.End != 0 {
				j.Regions[i].Start, j.Regions[i].End = upd.Start, upd.End
			}
			if upd.W > 0 {
				j.Regions[i].X, j.Regions[i].Y, j.Regions[i].W, j.Regions[i].H = upd.X, upd.Y, upd.W, upd.H
				pixelToRelative(&j.Regions[i], j.Width, j.Height)
			}
			s.store.save()
			writeJSON(w, 200, j.Regions[i])
			return
		}
	}
	http.NotFound(w, r)
}

func (s *server) handleExport(w http.ResponseWriter, r *http.Request, j *Job) {
	tools := s.getTools()
	if !tools.FFmpegOK() {
		http.Error(w, "ffmpeg not found — searched: "+tools.searchedSummary("ffmpeg"), 503)
		return
	}
	if len(enabledRegions(j.Regions)) == 0 {
		http.Error(w, "no enabled blur regions — run a scan or draw a manual box first", 400)
		return
	}
	if j.Status == "exporting" || j.Status == "scanning" {
		http.Error(w, "job busy", 409)
		return
	}
	s.store.SetStatus(j, "exporting", "Blurring and re-encoding…", 0)
	go func() {
		err := exportVideo(tools, j.VideoPath, j.ExportPath, j.Regions, s.cfg.BlurStrength, func(p float64) {
			s.store.SetStatus(j, "exporting", "Blurring and re-encoding…", p)
		})
		if err != nil {
			s.store.SetStatus(j, "error", "Export failed: "+err.Error(), 0)
			return
		}
		j.HasExport = true
		s.store.SetStatus(j, "done", "Export ready", 1)
	}()
	writeJSON(w, 202, map[string]bool{"ok": true})
}

func (s *server) handleVideo(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/video/")
	id = strings.SplitN(id, "/", 2)[0]
	j, ok := s.store.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(j.VideoPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	// ServeContent handles HTTP Range, so the <video> tag can seek.
	http.ServeContent(w, r, j.Name, st.ModTime(), f)
}

func (s *server) handleDownload(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/download/")
	id = strings.SplitN(id, "/", 2)[0]
	j, ok := s.store.Get(id)
	if !ok || !j.HasExport {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(j.ExportPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	w.Header().Set("Content-Disposition", `attachment; filename="blurred-`+j.Name+`"`)
	http.ServeContent(w, r, "blurred-"+j.Name, st.ModTime(), f)
}
