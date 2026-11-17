// BlurCut detection: regex matching of OCR words, box grouping/merging,
// pixel <-> relative coordinate conversion.
package main

import (
	"regexp"
	"sort"
	"strings"
)

// Region is one blur target: a box (pixels + relative 0-1) over a time range.
type Region struct {
	ID      string  `json:"id"`
	Label   string  `json:"label"`
	Text    string  `json:"text"`
	X       int     `json:"x"`
	Y       int     `json:"y"`
	W       int     `json:"w"`
	H       int     `json:"h"`
	RX      float64 `json:"rx"`
	RY      float64 `json:"ry"`
	RW      float64 `json:"rw"`
	RH      float64 `json:"rh"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Enabled bool    `json:"enabled"`
	Source  string  `json:"source"`
}

// Detection is one merged scan hit; converted to a Region on scan completion.
type Detection struct {
	Region
	Frames int `json:"frames"`
}

var (
	reURL      = regexp.MustCompile(`(?i)(https?://\S+|www\.\S+|[a-z0-9-]+\.(com|net|io|dev|org|app|gg|me|co|tv)\S*)`)
	reEmail    = regexp.MustCompile(`(?i)[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}`)
	rePassword = regexp.MustCompile(`(?i)(password|passwd|pwd)\s*[:=]`)
	reAPIKey   = regexp.MustCompile(`(?i)(api[_-]?key|apikey|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-[A-Za-z0-9]{10,}|-----BEGIN.*PRIVATE KEY-----)`)
	reKeyValue = regexp.MustCompile(`(?i)(api[_-]?key|secret|token|bearer)\s*[:=]`)
)

// classifyText returns the label for a word/phrase, or "" if not sensitive.
func classifyText(text string, keywords []string, patterns map[string]bool) string {
	t := strings.TrimSpace(text)
	if t == "" {
		return ""
	}
	if patterns["email"] && reEmail.MatchString(t) {
		return "email"
	}
	if patterns["apikey"] && (reAPIKey.MatchString(t) || reKeyValue.MatchString(t)) {
		return "apikey"
	}
	if patterns["password"] && rePassword.MatchString(t) {
		return "password"
	}
	if patterns["url"] && reURL.MatchString(t) {
		return "url"
	}
	lower := strings.ToLower(t)
	for _, kw := range keywords {
		if kw != "" && strings.Contains(lower, strings.ToLower(kw)) {
			return "keyword"
		}
	}
	return ""
}

// FindSensitive scans a list of OCR words and returns (label, matchedText)
// for the first sensitive hit in the joined text, or "".
func FindSensitive(words []OCRWord, keywords []string, patterns map[string]bool) (string, string) {
	joined := ""
	for i, w := range words {
		if i > 0 {
			joined += " "
		}
		joined += w.Text
	}
	if label := classifyText(joined, keywords, patterns); label != "" {
		return label, joined
	}
	for _, w := range words {
		if label := classifyText(w.Text, keywords, patterns); label != "" {
			return label, w.Text
		}
	}
	return "", ""
}

// OCRWord is one word from tesseract TSV output.
type OCRWord struct {
	Text string
	X    int
	Y    int
	W    int
	H    int
	Conf int
}

// groupWords merges adjacent words on the same line into one box.
func groupWords(words []OCRWord) []OCRWord {
	if len(words) == 0 {
		return nil
	}
	sorted := append([]OCRWord(nil), words...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Y != sorted[j].Y {
			return sorted[i].Y < sorted[j].Y
		}
		return sorted[i].X < sorted[j].X
	})
	var groups []OCRWord
	cur := sorted[0]
	for _, w := range sorted[1:] {
		sameLine := abs(w.Y-cur.Y) <= 12
		near := w.X <= cur.X+cur.W+40
		if sameLine && near {
			right := max(cur.X+cur.W, w.X+w.W)
			bottom := max(cur.Y+cur.H, w.Y+w.H)
			cur.X = min(cur.X, w.X)
			cur.Y = min(cur.Y, w.Y)
			cur.W = right - cur.X
			cur.H = bottom - cur.Y
			cur.Text += " " + w.Text
			if w.Conf < cur.Conf {
				cur.Conf = w.Conf
			}
		} else {
			groups = append(groups, cur)
			cur = w
		}
	}
	return append(groups, cur)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func boxesOverlap(a, b Region) bool {
	return a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
}

func iou(a, b Region) float64 {
	ix := max(0, min(a.X+a.W, b.X+b.W)-max(a.X, b.X))
	iy := max(0, min(a.Y+a.H, b.Y+b.H)-max(a.Y, b.Y))
	inter := float64(ix * iy)
	union := float64(a.W*a.H+b.W*b.H) - inter
	if union <= 0 {
		return 0
	}
	return inter / union
}

// mergeBoxes merges overlapping regions (same frame), unioning the box.
func mergeBoxes(regions []Region) []Region {
	if len(regions) == 0 {
		return nil
	}
	out := append([]Region(nil), regions...)
	changed := true
	for changed {
		changed = false
		for i := 0; i < len(out); i++ {
			for j := i + 1; j < len(out); j++ {
				if boxesOverlap(out[i], out[j]) {
					out[i] = unionRegion(out[i], out[j])
					out = append(out[:j], out[j+1:]...)
					changed = true
					break
				}
			}
			if changed {
				break
			}
		}
	}
	return out
}

func unionRegion(a, b Region) Region {
	x := min(a.X, b.X)
	y := min(a.Y, b.Y)
	right := max(a.X+a.W, b.X+b.W)
	bottom := max(a.Y+a.H, b.Y+b.H)
	a.X, a.Y = x, y
	a.W, a.H = right-x, bottom-y
	if b.Label != "" && a.Label == "" {
		a.Label = b.Label
	}
	if a.Text == "" {
		a.Text = b.Text
	}
	a.End = maxFloat(a.End, b.End)
	return a
}

// mergeTimeRanges merges detections across consecutive frames into time
// ranges: same label, box IoU > 0.3, gap < 2s.
func mergeTimeRanges(dets []Detection) []Detection {
	if len(dets) == 0 {
		return nil
	}
	sort.Slice(dets, func(i, j int) bool { return dets[i].Start < dets[j].Start })
	var out []Detection
	for _, d := range dets {
		merged := false
		for i := range out {
			o := &out[i]
			if o.Label == d.Label && iou(o.Region, d.Region) > 0.3 && d.Start-o.End < 2 {
				o.End = maxFloat(o.End, d.End)
				o.Frames += d.Frames
				if d.W*d.H > o.W*o.H {
					o.X, o.Y, o.W, o.H = d.X, d.Y, d.W, d.H
				}
				merged = true
				break
			}
		}
		if !merged {
			out = append(out, d)
		}
	}
	return out
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// pixelToRelative fills RX/RY/RW/RH from X/Y/W/H given the frame size.
func pixelToRelative(r *Region, frameW, frameH int) {
	if frameW <= 0 || frameH <= 0 {
		return
	}
	r.RX = float64(r.X) / float64(frameW)
	r.RY = float64(r.Y) / float64(frameH)
	r.RW = float64(r.W) / float64(frameW)
	r.RH = float64(r.H) / float64(frameH)
}

// relativeToPixel fills X/Y/W/H from RX/RY/RW/RH given the frame size.
func relativeToPixel(r *Region, frameW, frameH int) {
	r.X = int(r.RX * float64(frameW))
	r.Y = int(r.RY * float64(frameH))
	r.W = int(r.RW * float64(frameW))
	r.H = int(r.RH * float64(frameH))
}

// padRegion expands a box by pad px, clamped to the frame.
func padRegion(r *Region, pad, frameW, frameH int) {
	r.X = max(0, r.X-pad)
	r.Y = max(0, r.Y-pad)
	r.W = min(frameW-r.X, r.W+2*pad)
	r.H = min(frameH-r.Y, r.H+2*pad)
}
