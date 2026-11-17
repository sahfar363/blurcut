// BlurCut ffmpeg: tool detection, probing, filter building, export.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

type Tools struct {
	FFmpeg    string `json:"ffmpeg"`
	FFprobe   string `json:"ffprobe"`
	Tesseract string `json:"tesseract"`
	HasNVENC  bool   `json:"hasNvenc"`
}

func detectTools() Tools {
	t := Tools{}
	t.FFmpeg, _ = exec.LookPath("ffmpeg")
	t.FFprobe, _ = exec.LookPath("ffprobe")
	t.Tesseract, _ = exec.LookPath("tesseract")
	if t.FFmpeg != "" {
		out, err := exec.Command(t.FFmpeg, "-hide_banner", "-encoders").Output()
		if err == nil {
			t.HasNVENC = strings.Contains(string(out), "h264_nvenc")
		}
	}
	return t
}

func (t Tools) FFmpegOK() bool    { return t.FFmpeg != "" }
func (t Tools) TesseractOK() bool { return t.Tesseract != "" }

// VideoInfo from probing a file.
type VideoInfo struct {
	Duration float64 `json:"duration"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	FPS      float64 `json:"fps"`
}

type ffprobeOut struct {
	Streams []struct {
		CodecType  string `json:"codec_type"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
		RFrameRate string `json:"r_frame_rate"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// probeVideo returns duration/size/fps via ffprobe, falling back to ffmpeg -i.
func probeVideo(tools Tools, path string) (VideoInfo, error) {
	var info VideoInfo
	if tools.FFprobe != "" {
		cmd := exec.Command(tools.FFprobe, "-v", "quiet", "-print_format", "json",
			"-show_format", "-show_streams", path)
		out, err := cmd.Output()
		if err == nil {
			var p ffprobeOut
			if json.Unmarshal(out, &p) == nil {
				if d, err := strconv.ParseFloat(p.Format.Duration, 64); err == nil {
					info.Duration = d
				}
				for _, s := range p.Streams {
					if s.CodecType == "video" {
						info.Width, info.Height = s.Width, s.Height
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
				if info.Duration > 0 && info.Width > 0 {
					return info, nil
				}
			}
		}
	}
	cmd := exec.Command(tools.FFmpeg, "-hide_banner", "-i", path)
	out, _ := cmd.CombinedOutput()
	s := string(out)
	if m := regexp.MustCompile(`Duration: (\d+):(\d+):([\d.]+)`).FindStringSubmatch(s); m != nil {
		h, _ := strconv.ParseFloat(m[1], 64)
		mi, _ := strconv.ParseFloat(m[2], 64)
		sec, _ := strconv.ParseFloat(m[3], 64)
		info.Duration = h*3600 + mi*60 + sec
	}
	if m := regexp.MustCompile(`Stream.*Video:.* (\d+)x(\d+)`).FindStringSubmatch(s); m != nil {
		info.Width, _ = strconv.Atoi(m[1])
		info.Height, _ = strconv.Atoi(m[2])
	}
	if m := regexp.MustCompile(`(\d+(?:\.\d+)?) fps`).FindStringSubmatch(s); m != nil {
		info.FPS, _ = strconv.ParseFloat(m[1], 64)
	}
	if info.Duration == 0 {
		return info, fmt.Errorf("could not probe video: %s", firstLine(s))
	}
	return info, nil
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// buildFilterComplex builds the ffmpeg filter_complex string for the given
// enabled regions: split base into N+1, crop+boxblur each region, overlay
// them back in order with between(t,start,end).
func buildFilterComplex(regions []Region, blurStrength int) string {
	enabled := enabledRegions(regions)
	if len(enabled) == 0 {
		return ""
	}
	var b strings.Builder
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
		}
		if onProgress != nil {
			onProgress(0)
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
