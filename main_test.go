package main

import (
	"strings"
	"testing"
)

func patternsAll() map[string]bool {
	return map[string]bool{"url": true, "email": true, "password": true, "apikey": true}
}

func TestClassifyURL(t *testing.T) {
	for _, txt := range []string{"https://example.com/secret", "www.example.com", "visit mysite.io/page"} {
		if got := classifyText(txt, nil, patternsAll()); got != "url" {
			t.Errorf("classifyText(%q) = %q, want url", txt, got)
		}
	}
}

func TestClassifyEmail(t *testing.T) {
	if got := classifyText("contact me at bob@example.com", nil, patternsAll()); got != "email" {
		t.Errorf("got %q, want email", got)
	}
}

func TestClassifyPassword(t *testing.T) {
	for _, txt := range []string{"password: hunter2", "Password = abc", "pwd: secret"} {
		if got := classifyText(txt, nil, patternsAll()); got != "password" {
			t.Errorf("classifyText(%q) = %q, want password", txt, got)
		}
	}
}

func TestClassifyAPIKey(t *testing.T) {
	for _, txt := range []string{"api_key: AKIAIOSFODNN7EXAMPLE", "ghp_abcdefghij1234567890", "api-key = xyz"} {
		if got := classifyText(txt, nil, patternsAll()); got != "apikey" {
			t.Errorf("classifyText(%q) = %q, want apikey", txt, got)
		}
	}
}

func TestClassifyKeywordAndClean(t *testing.T) {
	if got := classifyText("my secret project falcon", []string{"falcon"}, patternsAll()); got != "keyword" {
		t.Errorf("got %q, want keyword", got)
	}
	if got := classifyText("hello world tutorial", nil, patternsAll()); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	// disabled pattern must not match
	off := map[string]bool{"url": false, "email": true, "password": true, "apikey": true}
	if got := classifyText("https://example.com", nil, off); got != "" {
		t.Errorf("disabled url still matched: %q", got)
	}
}

func TestMergeBoxes(t *testing.T) {
	regions := []Region{
		{X: 10, Y: 10, W: 100, H: 20, Label: "url"},
		{X: 50, Y: 12, W: 100, H: 20, Label: "url"},
		{X: 300, Y: 200, W: 50, H: 20, Label: "email"},
	}
	merged := mergeBoxes(regions)
	if len(merged) != 2 {
		t.Fatalf("mergeBoxes = %d regions, want 2", len(merged))
	}
	if merged[0].X != 10 || merged[0].W != 140 {
		t.Errorf("merged box = %+v, want x=10 w=140", merged[0])
	}
}

func TestMergeTimeRanges(t *testing.T) {
	dets := []Detection{
		{Region: Region{Label: "url", X: 10, Y: 10, W: 100, H: 20, Start: 0, End: 1}},
		{Region: Region{Label: "url", X: 12, Y: 11, W: 100, H: 20, Start: 1, End: 2}},
		{Region: Region{Label: "url", X: 10, Y: 10, W: 100, H: 20, Start: 10, End: 11}},
	}
	merged := mergeTimeRanges(dets)
	if len(merged) != 2 {
		t.Fatalf("mergeTimeRanges = %d, want 2", len(merged))
	}
	if merged[0].End != 2 {
		t.Errorf("first range end = %v, want 2", merged[0].End)
	}
}

func TestFilterComplexOneRegion(t *testing.T) {
	regions := []Region{{X: 10, Y: 20, W: 200, H: 30, Start: 1, End: 5, Enabled: true}}
	fc := buildFilterComplex(regions, 20)
	if !strings.Contains(fc, "[0:v]split=2[s0][s1]") {
		t.Errorf("missing split: %s", fc)
	}
	if !strings.Contains(fc, "crop=200:30:10:20,boxblur=luma_radius=20") {
		t.Errorf("missing crop/boxblur: %s", fc)
	}
	if !strings.Contains(fc, "overlay=10:20:enable='between(t,1.00,5.00)'") {
		t.Errorf("missing overlay: %s", fc)
	}
	if !strings.HasSuffix(fc, "[vout]") {
		t.Errorf("missing [vout]: %s", fc)
	}
}

func TestFilterComplexTwoRegions(t *testing.T) {
	regions := []Region{
		{X: 0, Y: 0, W: 100, H: 20, Start: 0, End: 2, Enabled: true},
		{X: 50, Y: 50, W: 80, H: 20, Start: 3, End: 4, Enabled: true},
		{X: 0, Y: 0, W: 10, H: 10, Start: 0, End: 1, Enabled: false}, // disabled: skipped
	}
	fc := buildFilterComplex(regions, 15)
	if !strings.Contains(fc, "split=3") {
		t.Errorf("want split=3 (2 enabled + base), got: %s", fc)
	}
	if strings.Count(fc, "overlay=") != 2 {
		t.Errorf("want 2 overlays, got: %s", fc)
	}
	if buildFilterComplex(nil, 20) != "" {
		t.Errorf("empty regions should give empty filter")
	}
}

func TestRelativePixelConversion(t *testing.T) {
	r := Region{X: 160, Y: 90, W: 320, H: 180}
	pixelToRelative(&r, 1280, 720)
	if r.RX != 0.125 || r.RY != 0.125 || r.RW != 0.25 || r.RH != 0.25 {
		t.Errorf("relative = %v %v %v %v", r.RX, r.RY, r.RW, r.RH)
	}
	var r2 Region
	r2.RX, r2.RY, r2.RW, r2.RH = r.RX, r.RY, r.RW, r.RH
	relativeToPixel(&r2, 640, 360)
	if r2.X != 80 || r2.Y != 45 || r2.W != 160 || r2.H != 90 {
		t.Errorf("pixel = %+v, want 80,45,160,90", r2)
	}
}

func TestParseTSV(t *testing.T) {
	tsv := "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n" +
		"5\t1\t1\t1\t1\t1\t100\t50\t200\t20\t95.000000\thttps://example.com\n" +
		"5\t1\t1\t1\t1\t2\t100\t80\t50\t20\t10.000000\tnoisy\n" +
		"1\t1\t1\t1\t1\t1\t0\t0\t0\t0\t-1\t\n"
	words := parseTSV(tsv)
	if len(words) != 1 {
		t.Fatalf("parseTSV = %d words, want 1 (low-conf and header skipped)", len(words))
	}
	if words[0].Text != "https://example.com" || words[0].X != 100 {
		t.Errorf("word = %+v", words[0])
	}
}

func TestParseProgressTime(t *testing.T) {
	sec, ok := parseProgressTime("frame= 10 time=00:00:05.20 bitrate=100kbits/s")
	if !ok || sec != 5.2 {
		t.Errorf("got %v %v, want 5.2 true", sec, ok)
	}
	if _, ok := parseProgressTime("no time here"); ok {
		t.Errorf("should not match")
	}
}
