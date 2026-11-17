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
func runTesseract(tesseractPath, imagePath string) ([]OCRWord, error) {
	cmd := exec.Command(tesseractPath, imagePath, "stdout", "tsv")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("tesseract: %w", err)
	}
	return parseTSV(string(out)), nil
}

// scanVideo extracts frames every interval seconds, OCRs each, and returns
// merged detections. Frames are streamed to a temp dir, never held in RAM.
func scanVideo(ffmpegPath, tesseractPath, videoPath string, interval float64, keywords []string, patterns map[string]bool, probe VideoInfo) ([]Detection, error) {
	tmp, err := os.MkdirTemp("", "blurcut-scan-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	fps := fmt.Sprintf("fps=1/%.3f", interval)
	cmd := exec.Command(ffmpegPath, "-hide_banner", "-loglevel", "error",
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
	for i, frame := range frames {
		t := float64(i) * interval
		words, err := runTesseract(tesseractPath, frame)
		if err != nil {
			continue
		}
		if len(words) == 0 {
			continue
		}
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
	}
	merged := mergeTimeRanges(raw)
	for i := range merged {
		merged[i].ID = fmt.Sprintf("det-%d", i+1)
	}
	return merged, nil
}
