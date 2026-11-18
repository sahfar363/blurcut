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
	Label   string  `json:"label"` // url, email, password, apikey, crypto, keyword, possible-key, manual
	Text    string  `json:"text"`  // matched text (redacted in UI if long)
	X       int     `json:"x"`
	Y       int     `json:"y"`
	W       int     `json:"w"`
	H       int     `json:"h"`
	RX      float64 `json:"rx"` // relative 0-1
	RY      float64 `json:"ry"`
	RW      float64 `json:"rw"`
	RH      float64 `json:"rh"`
	Start   float64 `json:"start"` // seconds
	End     float64 `json:"end"`
	Enabled bool    `json:"enabled"`
	Source  string  `json:"source"` // auto | manual
}

// Detection is one merged scan hit; converted to a Region on scan completion.
type Detection struct {
	Region
	Frames int `json:"frames"`
}

var (
	reURL   = regexp.MustCompile(`(?i)(https?://\S+|www\.\S+|[a-z0-9-]+\.(com|net|io|dev|org|app|gg|me|co|tv)\S*)`)
	reEmail = regexp.MustCompile(`(?i)[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}`)
	// Bare label words too: login forms show "password" with no : or =
	// after it. \b keeps "password123" (a value, not a label) unmatched.
	rePassword = regexp.MustCompile(`(?i)\b(password|passwd|pwd)\b`)
	reAPIKey   = regexp.MustCompile(`(?i)(api[_-]?key|apikey|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-[A-Za-z0-9]{10,}|-----BEGIN.*PRIVATE KEY-----)`)
	reKeyValue = regexp.MustCompile(`(?i)(api[_-]?key|secret|token|bearer)\s*[:=]`)
	// Crypto addresses: Bitcoin bech32 (bc1…), legacy (1…/3…, base58),
	// Ethereum (0x + 40 hex). No dot+TLD, so they never look like URLs.
	reCrypto = regexp.MustCompile(`(?i)(bc1[a-z0-9]{25,}|[13][a-km-zA-HJ-NP-Z1-9]{25,34}|0x[a-f0-9]{40})`)
	// Suspicious token: long run of letters/digits/_/- only (checked
	// per token in classifyText, with a letter+digit requirement).
	reSuspicious = regexp.MustCompile(`^[A-Za-z0-9_-]{20,}$`)
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
	if patterns["crypto"] && reCrypto.MatchString(t) {
		return "crypto"
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
	// Last, so real URLs/emails/keys are never relabelled: a long
	// token (letters/digits/_/-, at least one letter AND one digit).
	if patterns["suspicious"] {
		for _, tok := range strings.Fields(t) {
			if !reSuspicious.MatchString(tok) {
				continue
			}
			hasLetter, hasDigit := false, false
			for _, c := range tok {
				if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
					hasLetter = true
				}
				if c >= '0' && c <= '9' {
					hasDigit = true
				}
			}
			if hasLetter && hasDigit {
				return "possible-key"
			}
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
// Words within 40px horizontally and 12px vertically of the current box join it.
// scale is the OCR upscale factor: gaps are measured in upscaled
// pixels, so the thresholds scale with it (1 = original size).
func groupWords(words []OCRWord, scale float64) []OCRWord {
	if len(words) == 0 {
		return nil
	}
	if scale < 1 {
		scale = 1
	}
	gapX, gapY := 40.0*scale, 12.0*scale
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
		sameLine := float64(abs(w.Y-cur.Y)) <= gapY
		near := float64(w.X) <= float64(cur.X+cur.W)+gapX
		if sameLine && near {
			// extend cur box to include w
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

// boxesOverlap reports whether two regions overlap at all.
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
				// keep the larger box
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

// mergeScanRegions merges fresh auto-scan results into the existing region
// list: manual boxes are always kept, old auto regions are replaced by the
// fresh scan, and a fresh region that duplicates a kept manual box is
// dropped in favour of the manual one — heavy overlap (IoU > 0.5), or the
// same label on a near-identical box (IoU > 0.3).
func mergeScanRegions(existing, fresh []Region) []Region {
	var manuals []Region
	for _, r := range existing {
		if r.Source == "manual" {
			manuals = append(manuals, r)
		}
	}
	out := append([]Region(nil), manuals...)
	for _, f := range fresh {
		dup := false
		for _, m := range manuals {
			if iou(m, f) > 0.5 || (m.Label != "" && m.Label == f.Label && iou(m, f) > 0.3) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, f)
		}
	}
	return out
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
