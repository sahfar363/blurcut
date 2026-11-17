package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseFFprobeJSON(t *testing.T) {
	out := []byte(`{"streams":[{"codec_name":"h264","codec_type":"video","width":1280,"height":720,"r_frame_rate":"30/1"},{"codec_name":"aac","codec_type":"audio"}],"format":{"duration":"10.000000","format_name":"mov,mp4,m4a,3gp,3g2,mj2"}}`)
	info, err := parseFFprobeJSON(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Codec != "h264" || info.Format != "mov" || info.Duration != 10 || info.Width != 1280 || info.Height != 720 || info.FPS != 30 {
		t.Errorf("parseFFprobeJSON = %+v", info)
	}
}

func TestParseFFmpegStderr(t *testing.T) {
	stderr := `ffmpeg version 8.1.2
Input #0, matroska, from 'clip.mkv':
  Duration: 00:04:00.00, start: 0.000000, bitrate: 5000 kb/s
  Stream #0:0: Video: h264 (High), yuv420p, 1920x1080 [SAR 1:1 DAR 16:9], 25 fps, 25 tbr, 1k tbn
  Stream #0:1: Audio: aac (LC), 48000 Hz, stereo`
	info := parseFFmpegStderr(stderr)
	if info.Codec != "h264" || info.Format != "matroska" || info.Duration != 240 || info.Width != 1920 || info.Height != 1080 || info.FPS != 25 {
		t.Errorf("parseFFmpegStderr = %+v", info)
	}
	// ffmpeg -i fallback for a .mov/HEVC file
	stderr2 := "Input #0, mov,mp4,m4a,3gp,3g2,mj2, from 'clip.mov':\n" +
		"  Duration: 00:00:10.00, start: 0.000000\n" +
		"  Stream #0:0: Video: hevc (Main), yuv420p, 1280x720, 30 fps\n"
	info2 := parseFFmpegStderr(stderr2)
	if info2.Codec != "hevc" || info2.Format != "mov" {
		t.Errorf("parseFFmpegStderr mov/hevc = %+v", info2)
	}
}

func TestIsBrowserPlayable(t *testing.T) {
	cases := []struct {
		info VideoInfo
		want bool
	}{
		{VideoInfo{Format: "mp4", Codec: "h264"}, true},
		{VideoInfo{Format: "mp4", Codec: "vp9"}, true},
		{VideoInfo{Format: "m4v", Codec: "h264"}, true},
		{VideoInfo{Format: "webm", Codec: "vp9"}, true},
		{VideoInfo{Format: "webm", Codec: "vp8"}, true},
		{VideoInfo{Format: "mkv", Codec: "h264"}, false}, // container, not codec
		{VideoInfo{Format: "mov", Codec: "hevc"}, false}, // HEVC in QuickTime
		{VideoInfo{Format: "mp4", Codec: "hevc"}, false}, // HEVC even in mp4
		{VideoInfo{Format: "avi", Codec: "mpeg4"}, false},
		{VideoInfo{Format: "mp4", Codec: ""}, false},
		{VideoInfo{Format: "", Codec: "h264"}, false},
	}
	for _, c := range cases {
		if got := isBrowserPlayable(c.info); got != c.want {
			t.Errorf("isBrowserPlayable(%+v) = %v, want %v", c.info, got, c.want)
		}
	}
}

func TestNormalizeProbeUsesExtension(t *testing.T) {
	// ffprobe calls both .mp4 and .mov "mov" — the extension decides.
	info := normalizeProbe("/x/clip.mov", VideoInfo{Codec: "h264", Format: "mov"})
	if info.Format != "mov" {
		t.Errorf("mov format = %q", info.Format)
	}
	info = normalizeProbe("/x/clip.mp4", VideoInfo{Codec: "h264", Format: "mov"})
	if info.Format != "mp4" {
		t.Errorf("mp4 format = %q, want mp4", info.Format)
	}
}

// makeTestVideo builds a small video with ffmpeg for preview tests.
func makeTestVideo(t *testing.T, dir, name, extra string) string {
	t.Helper()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	out := filepath.Join(dir, name)
	args := []string{"-hide_banner", "-y", "-f", "lavfi", "-i", "testsrc=duration=2:size=640x360:rate=10",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:v", "libx264", "-c:a", "aac", "-shortest"}
	if extra != "" {
		args = append(args, extra)
	}
	args = append(args, out)
	if err := exec.Command(ff, args...).Run(); err != nil {
		t.Skipf("ffmpeg could not make test video: %v", err)
	}
	return out
}

func TestMakePreviewRemuxMKV(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	// h264 in an mkv container: preview must be a remuxed mp4.
	src := makeTestVideo(t, dir, "clip.mkv", "")
	tools := Tools{FFmpeg: ff}
	info, err := probeVideo(tools, src)
	if err != nil {
		t.Fatal(err)
	}
	if isBrowserPlayable(info) {
		t.Fatalf("mkv+h264 should not be playable, info=%+v", info)
	}
	out := filepath.Join(dir, "clip_preview.mp4")
	if err := makePreview(tools, src, out, info); err != nil {
		t.Fatalf("makePreview remux: %v", err)
	}
	pv, err := probeVideo(tools, out)
	if err != nil {
		t.Fatal(err)
	}
	if !isBrowserPlayable(pv) || pv.Codec != "h264" {
		t.Errorf("remuxed preview not playable: %+v", pv)
	}
}

func TestMakePreviewReencodeAVI(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	// mpeg4 in an avi container: preview must be re-encoded to h264/mp4.
	src := filepath.Join(dir, "clip.avi")
	cmd := exec.Command(ff, "-hide_banner", "-y", "-f", "lavfi", "-i", "testsrc=duration=2:size=640x360:rate=10",
		"-c:v", "mpeg4", "-q:v", "5", src)
	if err := cmd.Run(); err != nil {
		t.Skipf("ffmpeg could not make avi test video: %v", err)
	}
	tools := Tools{FFmpeg: ff}
	info, err := probeVideo(tools, src)
	if err != nil {
		t.Fatal(err)
	}
	if isBrowserPlayable(info) {
		t.Fatalf("avi+mpeg4 should not be playable, info=%+v", info)
	}
	out := filepath.Join(dir, "clip_preview.mp4")
	if err := makePreview(tools, src, out, info); err != nil {
		t.Fatalf("makePreview re-encode: %v", err)
	}
	pv, err := probeVideo(tools, out)
	if err != nil {
		t.Fatal(err)
	}
	if !isBrowserPlayable(pv) {
		t.Errorf("re-encoded preview not playable: %+v", pv)
	}
}

func TestMakePreviewSkipsPlayable(t *testing.T) {
	// Caller (server) only calls makePreview for non-playable files;
	// here just confirm an mp4/h264 file probes as playable so no
	// preview would be started for it.
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	src := makeTestVideo(t, dir, "clip.mp4", "")
	info, err := probeVideo(Tools{FFmpeg: ff}, src)
	if err != nil {
		t.Fatal(err)
	}
	if !isBrowserPlayable(info) {
		t.Errorf("mp4+h264 should be playable, info=%+v", info)
	}
}

func TestJobStoreReconstructsPreviewAndExt(t *testing.T) {
	dir := t.TempDir()
	uploads := filepath.Join(dir, "uploads")
	exports := filepath.Join(dir, "exports")
	_ = os.MkdirAll(uploads, 0o755)
	_ = os.MkdirAll(exports, 0o755)
	id := "12345"
	_ = os.WriteFile(filepath.Join(uploads, id+".mkv"), []byte("fake"), 0o644)
	_ = os.WriteFile(filepath.Join(uploads, id+"_preview.mp4"), []byte("fake"), 0o644)
	jobs := []Job{{ID: id, Name: "clip.mkv", Duration: 240}}
	b, _ := json.Marshal(jobs)
	_ = os.WriteFile(filepath.Join(dir, "jobs.json"), b, 0o644)
	store, err := openJobStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	j, ok := store.Get(id)
	if !ok {
		t.Fatal("job not loaded")
	}
	if j.VideoPath != filepath.Join(uploads, id+".mkv") {
		t.Errorf("VideoPath = %q, want the .mkv file", j.VideoPath)
	}
	if !j.PreviewConverted || j.PreviewPath != filepath.Join(uploads, id+"_preview.mp4") {
		t.Errorf("preview not reconstructed: converted=%v path=%q", j.PreviewConverted, j.PreviewPath)
	}
}

func TestJobStoreResetsInterruptedScan(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "uploads"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "exports"), 0o755)
	jobs := []Job{{ID: "9", Name: "clip.mp4", Status: "scanning", Message: "Extracting frames…"}}
	b, _ := json.Marshal(jobs)
	_ = os.WriteFile(filepath.Join(dir, "jobs.json"), b, 0o644)
	store, err := openJobStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	j, ok := store.Get("9")
	if !ok {
		t.Fatal("job not loaded")
	}
	if j.Status != "uploaded" {
		t.Errorf("interrupted scan should reset to uploaded, got %q", j.Status)
	}
}
