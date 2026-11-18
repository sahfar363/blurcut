// BlurCut OCR: frame extraction via ffmpeg + tesseract TSV parsing.
package main

import (
	"fmt"
	"math"
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

// ocrScaleFactor is how much frames are upscaled before OCR. Small
// screen text (browser chrome, terminal, 13px at 1080p) misreads at 1x
// — tesseract reads "password:" as "sword:" with high confidence, so
// no pattern matches and the scan finds nothing — but reads cleanly
// at 2x. 4K frames are already big enough, and the upscaled long side
// is capped at ~3840 so a 2560-wide frame does not become 5120 wide.
func ocrScaleFactor(frameW int) float64 {
	if frameW <= 0 || frameW >= 2560 {
		return 1
	}
	f := 3840.0 / float64(frameW)
	if f > 2 {
		f = 2
	}
	return f
}

// scaleWordsToOriginal converts OCR word coordinates from upscaled
// frame space back to original-pixel space (divide by the upscale
// factor, rounded), so boxes land in the right place.
func scaleWordsToOriginal(words []OCRWord, f float64) []OCRWord {
	if f == 1 {
		return words
	}
	for i := range words {
		words[i].X = int(math.Round(float64(words[i].X) / f))
		words[i].Y = int(math.Round(float64(words[i].Y) / f))
		words[i].W = int(math.Round(float64(words[i].W) / f))
		words[i].H = int(math.Round(float64(words[i].H) / f))
	}
	return words
}

// ocrPreviewFrame extracts ONE frame at time t, OCRs it with the same
// upscale the scan uses, and returns the raw words (reading order,
// capped at 40) in original-pixel coordinates. Nothing is stored.
func ocrPreviewFrame(tools Tools, videoPath string, t float64, probe VideoInfo) ([]OCRWord, error) {
	tmp, err := os.MkdirTemp("", "blurcut-ocr-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	frame := filepath.Join(tmp, "frame.jpg")
	f := ocrScaleFactor(probe.Width)
	vf := ""
	if f > 1 {
		vf = fmt.Sprintf(",scale=iw*%.3f:ih*%.3f", f, f)
	}
	cmd := exec.Command(tools.FFmpeg, "-hide_banner", "-loglevel", "error",
		"-i", videoPath, "-ss", fmt.Sprintf("%.3f", t), "-frames:v", "1",
		"-vf", "format=yuv420p"+vf, "-q:v", "4", frame)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg frame extract: %w: %s", err, strings.TrimSpace(string(out)))
	}
	words, err := runTesseract(tools.Tesseract, tools.TessData, frame)
	if err != nil {
		return nil, err
	}
	words = scaleWordsToOriginal(words, f)
	// Reading order, capped at 40 so the preview response stays small.
	sort.Slice(words, func(i, j int) bool {
		if words[i].Y != words[j].Y {
			return words[i].Y < words[j].Y
		}
		return words[i].X < words[j].X
	})
	if len(words) > 40 {
		words = words[:40]
	}
	return words, nil
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

	// fps=1/interval, e.g. interval 1.0 -> 1 fps. Frames are upscaled
	// before OCR (see ocrScaleFactor): small screen text misreads at 1x.
	f := ocrScaleFactor(probe.Width)
	vf := fmt.Sprintf("fps=1/%.3f", interval)
	if f > 1 {
		vf += fmt.Sprintf(",scale=iw*%.3f:ih*%.3f", f, f)
	}
	cmd := exec.Command(tools.FFmpeg, "-hide_banner", "-loglevel", "error",
		"-i", videoPath, "-vf", vf, "-q:v", "4",
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
			// Group in upscaled space (gaps are measured there), then
			// convert the merged boxes back to original pixels.
			groups := scaleWordsToOriginal(groupWords(words, f), f)
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
