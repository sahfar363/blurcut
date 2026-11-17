package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// v2.2: odd-dimension videos (e.g. 1279x719) failed export with libx264
// error -22 "width not divisible by 2". The filter must force even
// output dims, and regions must be clamped to the frame.

func TestFilterComplexEvenDims(t *testing.T) {
	regions := []Region{{X: 10, Y: 20, W: 200, H: 30, Start: 0, End: 5, Enabled: true}}
	fc := buildFilterComplex(regions, 20, 1279, 719)
	if !strings.Contains(fc, "scale=trunc(iw/2)*2:trunc(ih/2)*2,format=yuv420p[vout]") {
		t.Errorf("missing even-dim scale at end: %s", fc)
	}
	if !strings.HasSuffix(fc, "[vout]") {
		t.Errorf("filter must end with [vout]: %s", fc)
	}
	// The last overlay must feed the scale, not [vout] directly.
	if strings.Contains(fc, ")overlay=") && strings.Contains(fc, "[vout];") {
		t.Errorf("overlay must not output straight to [vout]: %s", fc)
	}
	// Even frame: same scale is still appended (harmless no-op).
	fc2 := buildFilterComplex(regions, 20, 1280, 720)
	if !strings.Contains(fc2, "scale=trunc(iw/2)*2:trunc(ih/2)*2,format=yuv420p[vout]") {
		t.Errorf("even frame should still end with the scale: %s", fc2)
	}
}

func TestClampRegion(t *testing.T) {
	// Box overflowing the right/bottom edges gets trimmed.
	r, ok := clampRegion(Region{X: 1200, Y: 700, W: 200, H: 100}, 1280, 720)
	if !ok || r.X != 1200 || r.Y != 700 || r.W != 80 || r.H != 20 {
		t.Errorf("overflow clamp = %+v ok=%v, want 1200,700,80,20", r, ok)
	}
	// Negative origin is pulled back to 0 (size kept).
	r, ok = clampRegion(Region{X: -10, Y: -5, W: 100, H: 50}, 1280, 720)
	if !ok || r.X != 0 || r.Y != 0 {
		t.Errorf("negative clamp = %+v ok=%v, want x=0 y=0", r, ok)
	}
	// Fully outside the frame: dropped.
	if _, ok := clampRegion(Region{X: 2000, Y: 10, W: 50, H: 20}, 1280, 720); ok {
		t.Errorf("fully-outside region should be dropped")
	}
	// Too small after clamping: dropped.
	if _, ok := clampRegion(Region{X: 10, Y: 10, W: 1, H: 30}, 1280, 720); ok {
		t.Errorf("1px-wide region should be dropped")
	}
}

func TestFilterComplexClampsRegion(t *testing.T) {
	regions := []Region{
		{X: -10, Y: 20, W: 5000, H: 30, Start: 0, End: 5, Enabled: true}, // overflows
		{X: 2000, Y: 20, W: 50, H: 30, Start: 0, End: 5, Enabled: true},  // outside: dropped
	}
	fc := buildFilterComplex(regions, 20, 1280, 720)
	if !strings.Contains(fc, "crop=1280:30:0:20") {
		t.Errorf("overflowing region should be clamped to crop=1280:30:0:20, got: %s", fc)
	}
	if strings.Count(fc, "overlay=") != 1 {
		t.Errorf("outside region should be dropped, want 1 overlay, got: %s", fc)
	}
}

func TestExtractErrorLines(t *testing.T) {
	stderr := `ffmpeg version 8.1.2 Copyright (c) 2000-2026
Input #0, matroska, from 'test.mkv':
  Duration: 00:00:10.00, start: 0.000000, bitrate: 100 kb/s
  Stream #0:0: Video: h264, yuv420p, 1279x719, 30 fps
[libx264 @ 0x123] width not divisible by 2 (1279x719)
Error initializing output stream 0:0 -- Error while opening encoder for output stream #0:0 - maybe incorrect parameters such as bit_rate, rate, width or height
frame=    0 fps=0.0 q=0.0 Lsize=       0KiB time=N/A bitrate=N/A speed=N/A
Conversion failed!
`
	got := extractErrorLines(stderr)
	for _, want := range []string{"width not divisible by 2", "Error initializing output stream", "Conversion failed!"} {
		if !strings.Contains(got, want) {
			t.Errorf("extractErrorLines missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "frame=    0 fps=0.0") {
		t.Errorf("progress line should not be in the error: %q", got)
	}
	if strings.Contains(got, "Input #0") {
		t.Errorf("useless Input header should not be in the error: %q", got)
	}
	if len(got) > 1500 {
		t.Errorf("error text should be capped at 1500 chars, got %d", len(got))
	}
	if got := extractErrorLines(""); got != "" {
		t.Errorf("empty stderr should give empty error, got %q", got)
	}
}

func TestCanRemuxPreview(t *testing.T) {
	if !canRemuxPreview(VideoInfo{Codec: "h264", Width: 1280, Height: 720}) {
		t.Errorf("even h264 should remux")
	}
	if canRemuxPreview(VideoInfo{Codec: "h264", Width: 1279, Height: 719}) {
		t.Errorf("odd h264 must NOT remux (remuxed odd mp4 fails downstream)")
	}
	if canRemuxPreview(VideoInfo{Codec: "hevc", Width: 1280, Height: 720}) {
		t.Errorf("hevc must NOT remux")
	}
	if got := previewScaleFilter(); got != "scale='trunc(min(1280,iw)/2)*2':-2,format=yuv420p" {
		t.Errorf("previewScaleFilter = %q", got)
	}
}

func TestScanProgress(t *testing.T) {
	dir := t.TempDir()
	// Fake ffmpeg: creates two frame files where the output pattern
	// (last arg) points — same trick as TestScanVideoTesseractFailure.
	fakeFF := filepath.Join(dir, "ffmpeg")
	script := "#!/bin/sh\nfor last; do :; done\nd=$(dirname \"$last\")\n: > \"$d/frame_0001.jpg\"\n: > \"$d/frame_0002.jpg\"\n"
	if err := os.WriteFile(fakeFF, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// Fake tesseract: succeeds but finds no words.
	fakeTS := filepath.Join(dir, "tesseract")
	if err := os.WriteFile(fakeTS, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tools := Tools{FFmpeg: fakeFF, Tesseract: fakeTS, TessData: filepath.Join(dir, "tessdata")}
	var calls [][2]int
	_, err := scanVideo(tools, "video.mp4", 1.0, nil, map[string]bool{"url": true}, VideoInfo{Width: 1280, Height: 720}, func(done, total int) {
		calls = append(calls, [2]int{done, total})
	})
	if err != nil {
		t.Fatalf("scanVideo: %v", err)
	}
	if len(calls) != 2 || calls[0] != [2]int{1, 2} || calls[1] != [2]int{2, 2} {
		t.Errorf("progress calls = %v, want [{1 2} {2 2}]", calls)
	}
}

func TestExportAudioFallback(t *testing.T) {
	// Real codecs copy into mp4 fine on modern ffmpeg, so the AAC
	// fallback only triggers on exotic audio — prove the chain with a
	// fake ffmpeg that fails every "-c:a copy" run and succeeds on aac.
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	// Any real video file works: probing uses the real ffprobe.
	video := "/tmp/v22-audio-src.mkv"
	if _, err := os.Stat(video); err != nil {
		t.Skip("no smoke video available (copy test-obs.mkv to /tmp/v22-audio-src.mkv)")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	fakeFF := filepath.Join(dir, "ffmpeg")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> \"" + log + "\"\n" +
		"for a in \"$@\"; do if [ \"$a\" = \"copy\" ]; then echo \"muxer does not support this codec\" >&2; exit 1; fi; done\n" +
		"for last; do :; done\n: > \"$last\"\n"
	if err := os.WriteFile(fakeFF, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	tools := Tools{FFmpeg: fakeFF, FFprobe: ffprobe}
	regions := []Region{{X: 10, Y: 10, W: 100, H: 20, Start: 0, End: 2, Enabled: true}}
	out := filepath.Join(dir, "out.mp4")
	if err := exportVideo(tools, video, out, regions, 20, nil); err != nil {
		t.Fatalf("exportVideo should fall back to AAC, got: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("output file missing: %v", err)
	}
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "-c:a aac") {
		t.Errorf("expected an AAC retry in ffmpeg calls, got:\n%s", calls)
	}
}
