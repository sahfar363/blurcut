// BlurCut OCR: frame extraction via ffmpeg + tesseract TSV parsing.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// parseTSV parses `tesseract stdout tsv` output into words.
// Columns: level page block par line word left top width height conf text
func parseTSV(tsv string) []OCRWord {
	var words []OCRWord
	for _, line := range strings.Split(tsv, "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) < 12 || parts[0] != "5" {
			continue
		}
		text := strings.TrimSpace(parts[11])
		if text == "" {
			continue
		}
		left, _ := strconv.Atoi(parts[6])
		top, _ := strconv.Atoi(parts[7])
		w, _ := strconv.Atoi(parts[8])
		h, _ := strconv.Atoi(parts[9])
		// Tesseract 5 prints confidence as a float, e.g. "95.000000".
		confF, _ := strconv.ParseFloat(parts[10], 64)
		conf := int(confF)
		if conf < 20 {
			continue
		}
		words = append(words, OCRWord{Text: text, X: left, Y: top, W: w, H: h, Conf: conf})
	}
	return words
}

// runTesseract runs tesseract on one image file and returns OCR words.
// tessData is the tessdata dir itself, used as TESSDATA_PREFIX
// ("" = tesseract's default). TSV output is requested with
// -c tessedit_create_tsv=1 instead of the "tsv" config file, because a
// bundled/minimal tessdata folder has no config files — with the plain
// "tsv" argument tesseract silently falls back to plain text and every
// word is lost.
func runTesseract(tesseractPath, tessData, imagePath string) ([]OCRWord, error) {
	cmd := exec.Command(tesseractPath, imagePath, "stdout", "-c", "tessedit_create_tsv=1")
	if tessData != "" {
		cmd.Env = append(os.Environ(), "TESSDATA_PREFIX="+tessData)
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("tesseract: %w", err)
	}
	return parseTSV(string(out)), nil
}

// scanVideo extracts frames every interval seconds, OCRs each, and returns
// merged detections. Frames are streamed to a temp dir, never held in RAM.
// onProgress (may be nil) is called after each frame is OCRed so a long
// scan shows real progress instead of sitting at 0% for minutes.
func scanVideo(tools Tools, videoPath string, interval float64, keywords []string, patterns map[string]bool, probe VideoInfo, onProgress func(done, total int)) ([]Detection, error) {
	tmp, err := os.MkdirTemp("", "blurcut-scan-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	// fps=1/interval, e.g. interval 1.0 -> 1 fps.
	fps := fmt.Sprintf("fps=1/%.3f", interval)
	cmd := exec.Command(tools.FFmpeg, "-hide_banner", "-loglevel", "error",
		"-i", videoPath, "-vf", fps, "-q:v", "4",
		filepath.Join(tmp, "frame_%04d.jpg"))
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg frame extract: %w: %s", err, strings.TrimSpace(string(out)))
	}
	frames, err := filepath.Glob(filepath.Join(tmp, "frame_*.jpg"))
	if err != nil {
		return nil, err
	}
	sort.Strings(frames)

	var raw []Detection
	ocrErrs, lastErr := 0, error(nil)
	for i, frame := range frames {
		// Frame 1 is at t=0, frame n at t=(n-1)*interval.
		t := float64(i) * interval
		words, err := runTesseract(tools.Tesseract, tools.TessData, frame)
		if err == nil && len(words) > 0 {
			groups := groupWords(words)
			var frameRegions []Region
			for _, g := range groups {
				label, text := FindSensitive([]OCRWord{g}, keywords, patterns)
				if label == "" {
					continue
				}
				r := Region{
					Label: label, Text: text,
					X: g.X, Y: g.Y, W: g.W, H: g.H,
					Start: t, End: t + interval, Enabled: true, Source: "auto",
				}
				padRegion(&r, 8, probe.Width, probe.Height)
				pixelToRelative(&r, probe.Width, probe.Height)
				frameRegions = append(frameRegions, r)
			}
			for _, r := range mergeBoxes(frameRegions) {
				raw = append(raw, Detection{Region: r, Frames: 1})
			}
		} else if err != nil {
			ocrErrs++
			lastErr = err
			// one bad frame must not kill the whole scan
		}
		if onProgress != nil {
			onProgress(i+1, len(frames))
		}
	}
	merged := mergeTimeRanges(raw)
	for i := range merged {
		merged[i].ID = fmt.Sprintf("det-%d", i+1)
	}
	// If OCR failed on every frame, say so — "0 regions found" would be a
	// lie (e.g. tesseract cannot load its language data).
	if len(frames) > 0 && ocrErrs == len(frames) {
		return nil, fmt.Errorf("tesseract failed on all %d frames (tessdata=%q): %v", len(frames), tools.TessData, lastErr)
	}
	return merged, nil
}
