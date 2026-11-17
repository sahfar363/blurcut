// BlurCut ffmpeg: tool detection, probing, filter building, export.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

type Tools struct {
	FFmpeg    string              `json:"ffmpeg"`
	FFprobe   string              `json:"ffprobe"`
	Tesseract string              `json:"tesseract"`
	TessData  string              `json:"tessdata,omitempty"`
	HasNVENC  bool                `json:"hasNvenc"`
	Searched  map[string][]string `json:"searched,omitempty"`
}

func (t Tools) FFmpegOK() bool    { return t.FFmpeg != "" }
func (t Tools) TesseractOK() bool { return t.Tesseract != "" }

// exeDir returns the folder blurcut.exe / blurcut lives in.
func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(exe)
}

// toolFileNames returns the filenames to look for: plain name and .exe.
func toolFileNames(name string) []string {
	if strings.HasSuffix(name, ".exe") {
		return []string{name}
	}
	return []string{name, name + ".exe"}
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// candidateDirs returns the folders searched for a tool, in priority order
// (after a config path and env var, which are handled by the caller).
func candidateDirs(exeDir, cwd string) []string {
	dirs := []string{}
	seen := map[string]bool{}
	add := func(d string) {
		if d == "" || seen[d] {
			return
		}
		seen[d] = true
		dirs = append(dirs, d)
	}
	add(exeDir)
	add(filepath.Join(exeDir, "tools"))
	add(filepath.Join(exeDir, "bin"))
	add(cwd)
	add(filepath.Join(cwd, "tools"))
	if runtime.GOOS == "windows" {
		add(`C:\ffmpeg\bin`)
		add(`C:\Program Files\ffmpeg\bin`)
		add(`C:\Program Files\Tesseract-OCR`)
		add(`C:\Program Files (x86)\Tesseract-OCR`)
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			add(filepath.Join(la, "Tesseract-OCR"))
		}
	}
	return dirs
}

// findToolFile returns the first existing file among candidates, or "".
func findToolFile(candidates []string) string {
	for _, c := range candidates {
		if fileExists(c) {
			return c
		}
	}
	return ""
}

// resolveTool resolves one tool, recording every place it looked.
// Order: config path, env var, exe folder (+ tools/, bin/), cwd (+ tools/),
// PATH, then Windows common install folders.
func resolveTool(name, cfgPath, envVar, exeDir, cwd string, lookPath func(string) (string, error)) (string, []string) {
	var searched []string
	note := func(p string) {
		if p != "" {
			searched = append(searched, p)
		}
	}
	// (a) config-saved path
	if cfgPath != "" {
		note(cfgPath)
		if fileExists(cfgPath) {
			return cfgPath, searched
		}
	}
	// (b) env var
	if envVar != "" {
		if v := os.Getenv(envVar); v != "" {
			note(v)
			if fileExists(v) {
				return v, searched
			}
		}
	}
	// (c)-(d) exe folder, tools/, bin/, cwd, tools/
	for _, dir := range candidateDirs(exeDir, cwd) {
		for _, fn := range toolFileNames(name) {
			p := filepath.Join(dir, fn)
			note(p)
			if fileExists(p) {
				return p, searched
			}
		}
	}
	// (e) PATH
	if lookPath != nil {
		if p, err := lookPath(name); err == nil && p != "" {
			note("PATH: " + p)
			return p, searched
		}
		note("PATH: " + name + " (not found)")
	}
	return "", searched
}

// findTessData locates a tessdata folder containing eng.traineddata next to
// the tesseract binary or the exe. Returns the tessdata dir itself —
// tesseract wants TESSDATA_PREFIX pointing AT tessdata, not its parent.
func findTessData(tesseractPath, exeDir string) string {
	dirs := []string{}
	if tesseractPath != "" {
		d := filepath.Dir(tesseractPath)
		dirs = append(dirs, d, filepath.Join(d, "tessdata"))
	}
	dirs = append(dirs, exeDir, filepath.Join(exeDir, "tessdata"),
		filepath.Join(exeDir, "tools"), filepath.Join(exeDir, "tools", "tessdata"))
	for _, d := range dirs {
		if d == "" {
			continue
		}
		// d may itself be the tessdata dir, or its parent.
		for _, cand := range []string{d, filepath.Join(d, "tessdata")} {
			if fileExists(filepath.Join(cand, "eng.traineddata")) {
				return cand
			}
		}
	}
	return ""
}

// detectToolsFrom is detectTools with injectable exe dir / cwd / PATH lookup,
// so tests can use temp dirs without touching the real system.
func detectToolsFrom(cfg Config, exeDir, cwd string, lookPath func(string) (string, error)) Tools {
	t := Tools{Searched: map[string][]string{}}
	var searched []string
	t.FFmpeg, searched = resolveTool("ffmpeg", cfg.FfmpegPath, "BLURCUT_FFMPEG", exeDir, cwd, lookPath)
	t.Searched["ffmpeg"] = searched
	// ffprobe: prefer the folder ffmpeg was found in (gyan bin/ ships both).
	ffprobeSearched := []string{}
	if t.FFmpeg != "" {
		p := filepath.Join(filepath.Dir(t.FFmpeg), "ffprobe.exe")
		ffprobeSearched = append(ffprobeSearched, p)
		if fileExists(p) {
			t.FFprobe = p
		} else {
			p2 := filepath.Join(filepath.Dir(t.FFmpeg), "ffprobe")
			ffprobeSearched = append(ffprobeSearched, p2)
			if fileExists(p2) {
				t.FFprobe = p2
			}
		}
	}
	if t.FFprobe == "" {
		var s []string
		t.FFprobe, s = resolveTool("ffprobe", cfg.FfprobePath, "BLURCUT_FFPROBE", exeDir, cwd, lookPath)
		ffprobeSearched = append(ffprobeSearched, s...)
	}
	t.Searched["ffprobe"] = ffprobeSearched
	t.Tesseract, searched = resolveTool("tesseract", cfg.TesseractPath, "BLURCUT_TESSERACT", exeDir, cwd, lookPath)
	t.Searched["tesseract"] = searched
	// TESSDATA_PREFIX: env wins, else discover next to the binary / exe.
	// It must point AT the tessdata dir itself.
	if v := os.Getenv("TESSDATA_PREFIX"); v != "" {
		if fileExists(filepath.Join(v, "eng.traineddata")) {
			t.TessData = v
		} else if fileExists(filepath.Join(v, "tessdata", "eng.traineddata")) {
			t.TessData = filepath.Join(v, "tessdata")
		}
	} else {
		t.TessData = findTessData(t.Tesseract, exeDir)
	}
	if t.FFmpeg != "" {
		out, err := exec.Command(t.FFmpeg, "-hide_banner", "-encoders").Output()
		if err == nil {
			t.HasNVENC = strings.Contains(string(out), "h264_nvenc")
		}
	}
	return t
}

func detectTools() Tools {
	return detectToolsFrom(Config{}, exeDir(), mustCwd(), exec.LookPath)
}

func detectToolsWithConfig(cfg Config) Tools {
	return detectToolsFrom(cfg, exeDir(), mustCwd(), exec.LookPath)
}

func mustCwd() string {
	wd, _ := os.Getwd()
	return wd
}

// searchedSummary lists, for one tool, the places that were checked.
func (t Tools) searchedSummary(name string) string {
	if t.Searched == nil {
		return "PATH"
	}
	return strings.Join(t.Searched[name], ", ")
}

// VideoInfo from probing a file.
type VideoInfo struct {
	Duration float64 `json:"duration"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	FPS      float64 `json:"fps"`
	Codec    string  `json:"codec,omitempty"`  // e.g. h264, hevc, vp9
	Format   string  `json:"format,omitempty"` // container, e.g. mp4, mkv, mov
}

type ffprobeOut struct {
	Streams []struct {
		CodecType  string `json:"codec_type"`
		CodecName  string `json:"codec_name"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
		RFrameRate string `json:"r_frame_rate"`
	} `json:"streams"`
	Format struct {
		Duration   string `json:"duration"`
		FormatName string `json:"format_name"`
	} `json:"format"`
}

// parseFFprobeJSON pulls duration/size/fps/codec/format out of
// `ffprobe -print_format json -show_format -show_streams` output.
func parseFFprobeJSON(out []byte) (VideoInfo, error) {
	var info VideoInfo
	var p ffprobeOut
	if err := json.Unmarshal(out, &p); err != nil {
		return info, err
	}
	if d, err := strconv.ParseFloat(p.Format.Duration, 64); err == nil {
		info.Duration = d
	}
	if p.Format.FormatName != "" {
		// ffprobe reports e.g. "mov,mp4,m4a,3gp,3g2,mj2" — first token.
		info.Format = strings.Split(p.Format.FormatName, ",")[0]
	}
	for _, s := range p.Streams {
		if s.CodecType == "video" {
			info.Width, info.Height = s.Width, s.Height
			info.Codec = s.CodecName
			if parts := strings.Split(s.RFrameRate, "/"); len(parts) == 2 {
				num, _ := strconv.ParseFloat(parts[0], 64)
				den, _ := strconv.ParseFloat(parts[1], 64)
				if den != 0 {
					info.FPS = num / den
				}
			}
			break
		}
	}
	return info, nil
}

// parseFFmpegStderr pulls the same fields out of `ffmpeg -i` stderr:
// "Input #0, matroska, from ..." gives the container,
// "Stream #0:0: Video: h264 ..." gives the codec and size.
func parseFFmpegStderr(s string) VideoInfo {
	var info VideoInfo
	if m := regexp.MustCompile(`Duration: (\d+):(\d+):([\d.]+)`).FindStringSubmatch(s); m != nil {
		h, _ := strconv.ParseFloat(m[1], 64)
		mi, _ := strconv.ParseFloat(m[2], 64)
		sec, _ := strconv.ParseFloat(m[3], 64)
		info.Duration = h*3600 + mi*60 + sec
	}
	if m := regexp.MustCompile(`Input #\d+, ([a-z0-9_,]+), from`).FindStringSubmatch(s); m != nil {
		info.Format = strings.Split(m[1], ",")[0]
	}
	if m := regexp.MustCompile(`Stream.*Video: ([a-z0-9_]+)`).FindStringSubmatch(s); m != nil {
		info.Codec = m[1]
	}
	if m := regexp.MustCompile(`Stream.*Video:.* (\d+)x(\d+)`).FindStringSubmatch(s); m != nil {
		info.Width, _ = strconv.Atoi(m[1])
		info.Height, _ = strconv.Atoi(m[2])
	}
	if m := regexp.MustCompile(`(\d+(?:\.\d+)?) fps`).FindStringSubmatch(s); m != nil {
		info.FPS, _ = strconv.ParseFloat(m[1], 64)
	}
	return info
}

// isBrowserPlayable reports whether Edge/WebView2 can play the video
// directly in a <video> tag. Edge plays mp4/webm with h264, vp8, vp9 or
// av1 — it does NOT play .mkv, .mov, .avi, or HEVC/h265, even though
// ffmpeg reads them fine. Those need a converted preview (see
// makePreview). Format is the file extension (ffprobe calls both .mp4
// and .mov "mov,mp4,...", so the extension is the reliable container).
func isBrowserPlayable(info VideoInfo) bool {
	switch strings.ToLower(info.Format) {
	case "mp4", "m4v", "webm":
	default:
		return false
	}
	switch strings.ToLower(info.Codec) {
	case "h264", "avc", "vp8", "vp9", "av1":
		return true
	}
	return false
}

// makePreview converts input into a browser-playable mp4 at output.
// h264 video is remuxed (-c:v copy, no quality loss, near-instant);
// anything else is re-encoded to libx264 CRF 20, downscaled to at most
// 1280 wide. Audio is copied when possible, else re-encoded to AAC.
// The preview is only for playback in the UI — scan/export keep using
// the original file.
func makePreview(tools Tools, input, output string, info VideoInfo) error {
	if tools.FFmpeg == "" {
		return fmt.Errorf("ffmpeg not found")
	}
	remux := strings.ToLower(info.Codec) == "h264" || strings.ToLower(info.Codec) == "avc"
	if remux {
		args := []string{"-hide_banner", "-y", "-i", input,
			"-map", "0:v:0", "-map", "0:a?", "-c", "copy",
			"-movflags", "+faststart", output}
		if err := exec.Command(tools.FFmpeg, args...).Run(); err == nil {
			return nil
		}
		// Copy failed (e.g. audio codec not allowed in mp4) — retry
		// with the audio re-encoded to AAC, video still copied.
		args = []string{"-hide_banner", "-y", "-i", input,
			"-map", "0:v:0", "-map", "0:a?", "-c:v", "copy", "-c:a", "aac",
			"-movflags", "+faststart", output}
		cmd := exec.Command(tools.FFmpeg, args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("preview remux failed: %w: %s", err, firstLine(string(out)))
		}
		return nil
	}
	args := []string{"-hide_banner", "-y", "-i", input,
		"-map", "0:v:0", "-map", "0:a?",
		"-vf", "scale='min(1280,iw)':-2",
		"-c:v", "libx264", "-crf", "20", "-preset", "veryfast",
		"-c:a", "aac", "-movflags", "+faststart", output}
	cmd := exec.Command(tools.FFmpeg, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("preview transcode failed: %w: %s", err, firstLine(string(out)))
	}
	return nil
}

// probeVideo returns duration/size/fps/codec/format via ffprobe,
// falling back to ffmpeg -i. Format is normalised to the file
// extension: ffprobe reports "mov,mp4,..." for both .mp4 and .mov,
// and only the extension tells them apart for playback decisions.
func probeVideo(tools Tools, path string) (VideoInfo, error) {
	var info VideoInfo
	if tools.FFmpeg == "" && tools.FFprobe == "" {
		return info, fmt.Errorf("ffmpeg not found — searched: %s", tools.searchedSummary("ffmpeg"))
	}
	if tools.FFprobe != "" {
		cmd := exec.Command(tools.FFprobe, "-v", "quiet", "-print_format", "json",
			"-show_format", "-show_streams", path)
		if out, err := cmd.Output(); err == nil {
			if p, err := parseFFprobeJSON(out); err == nil && p.Duration > 0 && p.Width > 0 {
				return normalizeProbe(path, p), nil
			}
		}
	}
	if tools.FFmpeg == "" {
		return info, fmt.Errorf("ffmpeg not found — searched: %s", tools.searchedSummary("ffmpeg"))
	}
	// Fallback: parse `ffmpeg -i` stderr.
	cmd := exec.Command(tools.FFmpeg, "-hide_banner", "-i", path)
	out, _ := cmd.CombinedOutput()
	info = parseFFmpegStderr(string(out))
	if info.Duration == 0 {
		return info, fmt.Errorf("could not probe video: %s", firstLine(string(out)))
	}
	return normalizeProbe(path, info), nil
}

// normalizeProbe sets Format from the file extension when present.
func normalizeProbe(path string, info VideoInfo) VideoInfo {
	if ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), ".")); ext != "" {
		info.Format = ext
	}
	return info
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// buildFilterComplex builds the ffmpeg filter_complex string for the given
// enabled regions. Pattern: split base into N+1, crop+boxblur each region,
// overlay them back in order with between(t,start,end).
func buildFilterComplex(regions []Region, blurStrength int) string {
	enabled := enabledRegions(regions)
	if len(enabled) == 0 {
		return ""
	}
	var b strings.Builder
	// [0:v]split=N+1[s0][s1]...[sN]
	b.WriteString("[0:v]split=")
	b.WriteString(strconv.Itoa(len(enabled) + 1))
	for i := 0; i <= len(enabled); i++ {
		fmt.Fprintf(&b, "[s%d]", i)
	}
	b.WriteString(";")
	for i, r := range enabled {
		// boxblur chroma radius must stay < 15; luma carries the blur.
		chroma := min(blurStrength, 10)
		fmt.Fprintf(&b, "[s%d]crop=%d:%d:%d:%d,boxblur=luma_radius=%d:luma_power=1:chroma_radius=%d:chroma_power=1[b%d];",
			i+1, r.W, r.H, r.X, r.Y, blurStrength, chroma, i)
	}
	cur := "s0"
	for i, r := range enabled {
		// Last overlay outputs straight to [vout]; earlier ones chain via [oN].
		next := fmt.Sprintf("o%d", i)
		if i == len(enabled)-1 {
			next = "vout"
		}
		fmt.Fprintf(&b, "[%s][b%d]overlay=%d:%d:enable='between(t,%.2f,%.2f)'[%s];",
			cur, i, r.X, r.Y, r.Start, r.End, next)
		cur = next
	}
	return strings.TrimSuffix(b.String(), ";")
}

func enabledRegions(regions []Region) []Region {
	var out []Region
	for _, r := range regions {
		if r.Enabled && r.W > 0 && r.H > 0 && r.End > r.Start {
			out = append(out, r)
		}
	}
	return out
}

// exportVideo runs ffmpeg with the blur filter, calling onProgress(0-1)
// as it parses time= from stderr. Streams the file; never loads it in RAM.
// If h264_nvenc is listed but unusable (no NVIDIA GPU), it falls back
// to libx264 automatically.
func exportVideo(tools Tools, input, output string, regions []Region, blurStrength int, onProgress func(float64)) error {
	fc := buildFilterComplex(regions, blurStrength)
	if fc == "" {
		return fmt.Errorf("no enabled blur regions")
	}
	info, err := probeVideo(tools, input)
	if err != nil {
		return err
	}
	run := func(encArgs []string) error {
		args := []string{"-hide_banner", "-y", "-i", input,
			"-filter_complex", fc, "-map", "[vout]", "-map", "0:a?"}
		args = append(args, encArgs...)
		args = append(args, "-c:a", "copy", "-movflags", "+faststart", output)
		cmd := exec.Command(tools.FFmpeg, args...)
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return err
		}
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("ffmpeg start: %w", err)
		}
		reTime := regexp.MustCompile(`time=(\d+):(\d+):([\d.]+)`)
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		var lastLines []string
		for sc.Scan() {
			line := sc.Text()
			lastLines = append(lastLines, line)
			if len(lastLines) > 5 {
				lastLines = lastLines[1:]
			}
			if m := reTime.FindStringSubmatch(line); m != nil && info.Duration > 0 {
				h, _ := strconv.ParseFloat(m[1], 64)
				mi, _ := strconv.ParseFloat(m[2], 64)
				sec, _ := strconv.ParseFloat(m[3], 64)
				p := (h*3600 + mi*60 + sec) / info.Duration
				if p > 1 {
					p = 1
				}
				if onProgress != nil {
					onProgress(p)
				}
			}
		}
		if err := cmd.Wait(); err != nil {
			return fmt.Errorf("ffmpeg export failed: %w: %s", err, strings.Join(lastLines, " | "))
		}
		return nil
	}
	if tools.HasNVENC {
		if err := run([]string{"-c:v", "h264_nvenc", "-cq", "23"}); err == nil {
			return nil
		} else {
			// NVENC listed but no usable GPU — fall through to libx264.
			if onProgress != nil {
				onProgress(0)
			}
		}
	}
	return run([]string{"-c:v", "libx264", "-crf", "18", "-preset", "veryfast"})
}

// parseProgressTime parses "HH:MM:SS.xx" from ffmpeg stderr into seconds.
func parseProgressTime(s string) (float64, bool) {
	m := regexp.MustCompile(`time=(\d+):(\d+):([\d.]+)`).FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	h, _ := strconv.ParseFloat(m[1], 64)
	mi, _ := strconv.ParseFloat(m[2], 64)
	sec, _ := strconv.ParseFloat(m[3], 64)
	return h*3600 + mi*60 + sec, true
}
