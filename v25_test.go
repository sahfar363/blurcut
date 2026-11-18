package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func testTools(t *testing.T) Tools {
	t.Helper()
	ff, _ := exec.LookPath("ffmpeg")
	fp, _ := exec.LookPath("ffprobe")
	if ff == "" && fp == "" {
		t.Skip("ffmpeg/ffprobe not installed")
	}
	return Tools{FFmpeg: ff, FFprobe: fp}
}

func TestMergeScanRegionsKeepsManual(t *testing.T) {
	manual := Region{ID: "m1", Label: "manual", X: 100, Y: 100, W: 200, H: 50, Source: "manual", Enabled: true}
	oldAuto := Region{ID: "a0", Label: "url", X: 500, Y: 500, W: 300, H: 40, Source: "auto", Enabled: true}
	// Fresh hit duplicating the manual box (IoU ~1) and one genuinely new hit.
	dup := Region{ID: "a1", Label: "url", X: 105, Y: 102, W: 190, H: 46, Source: "auto", Enabled: true}
	fresh := Region{ID: "a2", Label: "password", X: 700, Y: 100, W: 250, H: 40, Source: "auto", Enabled: true}
	out := mergeScanRegions([]Region{manual, oldAuto}, []Region{dup, fresh})
	if len(out) != 2 {
		t.Fatalf("merged = %d regions, want 2 (manual + new scan hit)", len(out))
	}
	if out[0].ID != "m1" {
		t.Errorf("manual box lost: out[0].ID = %q", out[0].ID)
	}
	if out[1].ID != "a2" {
		t.Errorf("new scan hit missing: out[1].ID = %q", out[1].ID)
	}
}

func TestMergeScanRegionsNoManual(t *testing.T) {
	oldAuto := Region{ID: "a0", Label: "url", X: 500, Y: 500, W: 300, H: 40, Source: "auto"}
	fresh := Region{ID: "a1", Label: "email", X: 100, Y: 100, W: 200, H: 40, Source: "auto"}
	out := mergeScanRegions([]Region{oldAuto}, []Region{fresh})
	if len(out) != 1 || out[0].ID != "a1" {
		t.Fatalf("old auto regions should be replaced by the fresh scan, got %+v", out)
	}
}

func TestMergeScanRegionsSameLabelNearIdentical(t *testing.T) {
	manual := Region{ID: "m1", Label: "url", X: 100, Y: 100, W: 200, H: 50, Source: "manual"}
	// Same label, IoU = 5000/15000 = 0.33: near-identical box, drop the scan hit.
	sameLabel := Region{ID: "a1", Label: "url", X: 200, Y: 100, W: 200, H: 50, Source: "auto"}
	// Different label, same IoU: a genuinely different hit, keep it.
	diffLabel := Region{ID: "a2", Label: "password", X: 200, Y: 300, W: 200, H: 50, Source: "auto"}
	out := mergeScanRegions([]Region{manual}, []Region{sameLabel, diffLabel})
	if len(out) != 2 {
		t.Fatalf("merged = %d regions, want 2 (manual + different-label hit)", len(out))
	}
	for _, r := range out {
		if r.ID == "a1" {
			t.Errorf("same-label near-identical scan hit should have been dropped")
		}
	}
}

func writeJobStoreFixture(t *testing.T, dir, id string, export []byte) {
	t.Helper()
	_ = os.MkdirAll(filepath.Join(dir, "uploads"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "exports"), 0o755)
	if export != nil {
		if err := os.WriteFile(filepath.Join(dir, "exports", id+"_blurred.mp4"), export, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	jobs := []Job{{ID: id, Name: "clip.mkv", Duration: 30}}
	b, _ := json.Marshal(jobs)
	if err := os.WriteFile(filepath.Join(dir, "jobs.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOpenJobStoreDropsCorruptExport(t *testing.T) {
	tools := testTools(t)
	dir := t.TempDir()
	writeJobStoreFixture(t, dir, "1", []byte("this is not a video at all, just 100 bytes of garbage...................."))
	store, err := openJobStore(dir, tools)
	if err != nil {
		t.Fatal(err)
	}
	j, ok := store.Get("1")
	if !ok {
		t.Fatal("job not loaded")
	}
	if j.HasExport {
		t.Errorf("corrupt export must not restore HasExport")
	}
	if _, err := os.Stat(filepath.Join(dir, "exports", "1_blurred.mp4")); !os.IsNotExist(err) {
		t.Errorf("corrupt export file should have been deleted")
	}
}

func TestOpenJobStoreKeepsValidExport(t *testing.T) {
	tools := testTools(t)
	dir := t.TempDir()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	valid := filepath.Join(dir, "valid.mp4")
	cmd := exec.Command(ff, "-hide_banner", "-y", "-f", "lavfi", "-i", "testsrc=duration=1:size=320x240:rate=10",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", valid)
	if err := cmd.Run(); err != nil {
		t.Skipf("ffmpeg could not make test video: %v", err)
	}
	data, err := os.ReadFile(valid)
	if err != nil {
		t.Fatal(err)
	}
	writeJobStoreFixture(t, dir, "2", data)
	store, err := openJobStore(dir, tools)
	if err != nil {
		t.Fatal(err)
	}
	j, ok := store.Get("2")
	if !ok {
		t.Fatal("job not loaded")
	}
	if !j.HasExport {
		t.Errorf("valid export should restore HasExport")
	}
}

func TestOpenJobStoreNoToolsFallsBackToSize(t *testing.T) {
	dir := t.TempDir()
	writeJobStoreFixture(t, dir, "3", []byte("100 bytes of unprobed garbage.........................................."))
	store, err := openJobStore(dir, Tools{})
	if err != nil {
		t.Fatal(err)
	}
	j, ok := store.Get("3")
	if !ok {
		t.Fatal("job not loaded")
	}
	if !j.HasExport {
		t.Errorf("without ffmpeg/ffprobe, a non-empty export should still restore HasExport (size fallback)")
	}
}
