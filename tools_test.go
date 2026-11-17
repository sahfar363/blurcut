package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func stubLookPathFail(name string) (string, error) {
	return "", errors.New("not in PATH")
}

func writeDummy(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestResolveToolExeDir(t *testing.T) {
	exeDir := t.TempDir()
	writeDummy(t, filepath.Join(exeDir, "ffmpeg"))
	cwd := t.TempDir()
	tools := detectToolsFrom(Config{}, exeDir, cwd, stubLookPathFail)
	if tools.FFmpeg != filepath.Join(exeDir, "ffmpeg") {
		t.Errorf("FFmpeg = %q, want exe-dir copy", tools.FFmpeg)
	}
}

func TestResolveToolToolsSubfolder(t *testing.T) {
	exeDir := t.TempDir()
	writeDummy(t, filepath.Join(exeDir, "tools", "tesseract"))
	writeDummy(t, filepath.Join(exeDir, "tools", "tessdata", "eng.traineddata"))
	cwd := t.TempDir()
	tools := detectToolsFrom(Config{}, exeDir, cwd, stubLookPathFail)
	if tools.Tesseract != filepath.Join(exeDir, "tools", "tesseract") {
		t.Errorf("Tesseract = %q, want tools/ copy", tools.Tesseract)
	}
	if tools.TessData == "" {
		t.Errorf("TessData not found in tools/tessdata")
	}
}

func TestResolveToolConfigOverride(t *testing.T) {
	exeDir := t.TempDir()
	writeDummy(t, filepath.Join(exeDir, "ffmpeg"))
	custom := filepath.Join(t.TempDir(), "my-ffmpeg")
	writeDummy(t, custom)
	tools := detectToolsFrom(Config{FfmpegPath: custom}, exeDir, t.TempDir(), stubLookPathFail)
	if tools.FFmpeg != custom {
		t.Errorf("FFmpeg = %q, want config path %q", tools.FFmpeg, custom)
	}
	// Config path that does not exist must fall through to the exe dir.
	tools = detectToolsFrom(Config{FfmpegPath: filepath.Join(t.TempDir(), "nope")}, exeDir, t.TempDir(), stubLookPathFail)
	if tools.FFmpeg != filepath.Join(exeDir, "ffmpeg") {
		t.Errorf("missing config path should fall through, got %q", tools.FFmpeg)
	}
}

func TestResolveToolEnvOverride(t *testing.T) {
	exeDir := t.TempDir()
	writeDummy(t, filepath.Join(exeDir, "ffmpeg"))
	envBin := filepath.Join(t.TempDir(), "env-ffmpeg")
	writeDummy(t, envBin)
	t.Setenv("BLURCUT_FFMPEG", envBin)
	tools := detectToolsFrom(Config{}, exeDir, t.TempDir(), stubLookPathFail)
	if tools.FFmpeg != envBin {
		t.Errorf("FFmpeg = %q, want env path %q", tools.FFmpeg, envBin)
	}
}

func TestResolveToolPathFallback(t *testing.T) {
	exeDir := t.TempDir()
	cwd := t.TempDir()
	lookPath := func(name string) (string, error) {
		if name == "ffmpeg" {
			return "/usr/bin/ffmpeg", nil
		}
		return "", errors.New("not found")
	}
	tools := detectToolsFrom(Config{}, exeDir, cwd, lookPath)
	if tools.FFmpeg != "/usr/bin/ffmpeg" {
		t.Errorf("FFmpeg = %q, want PATH fallback", tools.FFmpeg)
	}
	if tools.Tesseract != "" {
		t.Errorf("Tesseract = %q, want empty", tools.Tesseract)
	}
}

func TestResolveToolNotFoundSearched(t *testing.T) {
	exeDir := t.TempDir()
	tools := detectToolsFrom(Config{}, exeDir, t.TempDir(), stubLookPathFail)
	if tools.FFmpeg != "" || tools.Tesseract != "" {
		t.Errorf("expected no tools, got ffmpeg=%q tesseract=%q", tools.FFmpeg, tools.Tesseract)
	}
	searched := tools.Searched["ffmpeg"]
	if len(searched) == 0 {
		t.Fatalf("searched list is empty")
	}
	joined := strings.Join(searched, " ")
	if !strings.Contains(joined, exeDir) {
		t.Errorf("searched list should mention the exe dir, got %v", searched)
	}
}

func TestProbeVideoNoFFmpeg(t *testing.T) {
	_, err := probeVideo(Tools{}, "video.mp4")
	if err == nil || !strings.Contains(err.Error(), "ffmpeg not found") {
		t.Errorf("probeVideo without ffmpeg should say ffmpeg not found, got %v", err)
	}
}

func TestFindTessData(t *testing.T) { // tessdata next to the tesseract binary
	binDir := t.TempDir()
	writeDummy(t, filepath.Join(binDir, "tesseract"))
	writeDummy(t, filepath.Join(binDir, "tessdata", "eng.traineddata"))
	if got := findTessData(filepath.Join(binDir, "tesseract"), t.TempDir()); got != filepath.Join(binDir, "tessdata") {
		t.Errorf("findTessData = %q, want %q", got, filepath.Join(binDir, "tessdata"))
	}
	// tessdata under exe/tools/
	exeDir := t.TempDir()
	writeDummy(t, filepath.Join(exeDir, "tools", "tessdata", "eng.traineddata"))
	if got := findTessData("", exeDir); got != filepath.Join(exeDir, "tools", "tessdata") {
		t.Errorf("findTessData tools/ = %q, want %q", got, filepath.Join(exeDir, "tools", "tessdata"))
	}
	// nothing anywhere
	if got := findTessData("", t.TempDir()); got != "" {
		t.Errorf("findTessData = %q, want empty", got)
	}
}

func TestScanVideoTesseractFailure(t *testing.T) {
	dir := t.TempDir()
	// Fake ffmpeg: ignores its args, creates two frame files where the
	// output pattern (last arg) points.
	fakeFF := filepath.Join(dir, "ffmpeg")
	script := "#!/bin/sh\nfor last; do :; done\nd=$(dirname \"$last\")\n: > \"$d/frame_0001.jpg\"\n: > \"$d/frame_0002.jpg\"\n"
	if err := os.WriteFile(fakeFF, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// Fake tesseract: always fails (e.g. cannot load language data).
	fakeTS := filepath.Join(dir, "tesseract")
	if err := os.WriteFile(fakeTS, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tools := Tools{FFmpeg: fakeFF, Tesseract: fakeTS, TessData: filepath.Join(dir, "tessdata")}
	_, err := scanVideo(tools, "video.mp4", 1.0, nil, map[string]bool{"url": true}, VideoInfo{Width: 1280, Height: 720})
	if err == nil || !strings.Contains(err.Error(), "tesseract failed on all") {
		t.Errorf("expected tesseract-all-frames error, got %v", err)
	}
}

func TestRunTesseractMinimalTessdata(t *testing.T) {
	// Regression test: a bundled/minimal tessdata dir has eng.traineddata
	// but NO config files, so the "tsv" config argument silently falls
	// back to plain text and parseTSV finds nothing. runTesseract must
	// request TSV with -c tessedit_create_tsv=1 instead.
	tessPath, err := exec.LookPath("tesseract")
	if err != nil {
		t.Skip("tesseract not installed")
	}
	if _, err := os.Stat("testdata/frame.png"); err != nil {
		t.Skip("testdata/frame.png missing")
	}
	engs, _ := filepath.Glob("/usr/share/tesseract-ocr/*/tessdata/eng.traineddata")
	if len(engs) == 0 {
		t.Skip("no system eng.traineddata to copy")
	}
	minimal := t.TempDir()
	data, err := os.ReadFile(engs[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(minimal, "eng.traineddata"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	words, err := runTesseract(tessPath, minimal, "testdata/frame.png")
	if err != nil {
		t.Fatalf("runTesseract: %v", err)
	}
	if len(words) == 0 {
		t.Fatalf("expected OCR words from testdata/frame.png, got none")
	}
	found := false
	for _, w := range words {
		if strings.Contains(w.Text, "secret") || strings.Contains(w.Text, "password") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a sensitive word in %v", words)
	}
}
