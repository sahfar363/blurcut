package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportDownloadName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"test-obs.mkv", "blurred-test-obs.mp4"},
		{"my video.mov", "blurred-my video.mp4"},
		{"clip.mp4", "blurred-clip.mp4"},
		{"recording.MKV", "blurred-recording.mp4"},
		{"a.b.c.mkv", "blurred-a.b.c.mp4"},
		{"noext", "blurred-noext.mp4"},
		{"", "blurred-video.mp4"},
		{".mkv", "blurred-video.mp4"},
	} {
		if got := exportDownloadName(tc.in); got != tc.want {
			t.Errorf("exportDownloadName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHumanSize(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1536, "1.5 KB"},
		{119436, "116.6 KB"},
		{88000000, "83.9 MB"},
	} {
		if got := humanSize(tc.n); got != tc.want {
			t.Errorf("humanSize(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func runCmdOK(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

func realTools(t *testing.T) Tools {
	t.Helper()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	fp, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	return Tools{FFmpeg: ff, FFprobe: fp}
}

func TestBuildFilterComplexSmallRegion(t *testing.T) {
	// A single text line can be ~20px tall: blur strength 20 must be
	// clamped to the box size, or ffmpeg rejects the whole export
	// with -22 ("Invalid luma_param radius value 20").
	fc := buildFilterComplex([]Region{
		{X: 10, Y: 10, W: 100, H: 20, Start: 0, End: 2, Enabled: true},
	}, 20, 320, 240)
	if !strings.Contains(fc, "luma_radius=9") {
		t.Errorf("100x20 box: filter = %q, want luma_radius=9", fc)
	}
	if !strings.Contains(fc, "chroma_radius=4") {
		t.Errorf("100x20 box: filter = %q, want chroma_radius=4", fc)
	}
	// A tiny box must never produce a negative radius.
	fc = buildFilterComplex([]Region{
		{X: 0, Y: 0, W: 2, H: 2, Start: 0, End: 2, Enabled: true},
	}, 20, 320, 240)
	if !strings.Contains(fc, "luma_radius=0") || !strings.Contains(fc, "chroma_radius=0") {
		t.Errorf("2x2 box: filter = %q, want radii clamped to 0", fc)
	}
	// A normal-size box keeps the full strength (capped at 10 chroma).
	fc = buildFilterComplex([]Region{
		{X: 1495, Y: 42, W: 425, H: 50, Start: 0, End: 2, Enabled: true},
	}, 20, 1920, 1080)
	if !strings.Contains(fc, "luma_radius=20") || !strings.Contains(fc, "chroma_radius=10") {
		t.Errorf("425x50 box: filter = %q, want luma_radius=20 chroma_radius=10", fc)
	}
}

func TestVerifyExport(t *testing.T) {
	tools := realTools(t)
	dir := t.TempDir()
	good := filepath.Join(dir, "good.mp4")
	runCmdOK(t, tools.FFmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=10:duration=2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", good)
	if err := verifyExport(tools, good); err != nil {
		t.Errorf("verifyExport(good mp4) = %v, want nil", err)
	}

	// Missing file.
	if err := verifyExport(tools, filepath.Join(dir, "nope.mp4")); err == nil {
		t.Errorf("verifyExport(missing) = nil, want error")
	}
	// Empty file.
	empty := filepath.Join(dir, "empty.mp4")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyExport(tools, empty); err == nil {
		t.Errorf("verifyExport(empty) = nil, want error")
	}
	// Garbage bytes, not a video at all.
	junk := filepath.Join(dir, "junk.mp4")
	if err := os.WriteFile(junk, []byte("this is not a video file at all, just text"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyExport(tools, junk); err == nil {
		t.Errorf("verifyExport(garbage) = nil, want error")
	}
	// Truncated mp4: header only, no playable video data.
	raw, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	trunc := filepath.Join(dir, "trunc.mp4")
	if err := os.WriteFile(trunc, raw[:100], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyExport(tools, trunc); err == nil {
		t.Errorf("verifyExport(truncated) = nil, want error")
	}
}

func TestExportVideoEncoderLabel(t *testing.T) {
	tools := realTools(t)
	tools.HasNVENC = false // deterministic: force the libx264 path
	dir := t.TempDir()
	video := filepath.Join(dir, "src.mp4")
	runCmdOK(t, tools.FFmpeg, "-v", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=10:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", video)
	regions := []Region{{X: 10, Y: 10, W: 100, H: 20, Start: 0, End: 2, Enabled: true}}
	out := filepath.Join(dir, "out.mp4")
	enc, err := exportVideo(tools, video, out, regions, 20, nil)
	if err != nil {
		t.Fatalf("exportVideo: %v", err)
	}
	if enc != "libx264+copy" {
		t.Errorf("encoder = %q, want libx264+copy", enc)
	}
	if err := verifyExport(tools, out); err != nil {
		t.Errorf("verifyExport(export output) = %v, want nil", err)
	}
	if !strings.HasSuffix(out, ".mp4") {
		t.Errorf("output name %q does not end in .mp4", out)
	}
}
