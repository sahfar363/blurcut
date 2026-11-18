package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// makeSmallScreenTextVideo builds the regression video for the v2.6
// scan fix: 1920x1080, 3s, 13px text (real browser-bar size), CRF 23.
// At 1x tesseract misreads it ("password:" -> "sword:") and the scan
// finds nothing; at 2x it reads cleanly. The key is fake (sk-test-…),
// never a real secret. Skips if ffmpeg or the DejaVu font is missing.
func makeSmallScreenTextVideo(t *testing.T, ffmpeg string) string {
	t.Helper()
	font := "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"
	if _, err := os.Stat(font); err != nil {
		t.Skip("DejaVuSans font not installed")
	}
	dir := t.TempDir()
	texts := map[string]string{
		"url.txt":    "https://github.com/sahfar363/blurcut",
		"pw.txt":     "password: hunter2",
		"key.txt":    "export API_KEY=sk-test-Ab3dEf7gHk9mNp2rSt5",
		"wallet.txt": "wallet 0x7aB3c4D5e6F708192a3B4c5D6e7F8091a2B3c4d5",
	}
	for name, text := range texts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(dir, "small.mp4")
	vf := "drawtext=fontfile=" + font + ":textfile=" + filepath.Join(dir, "url.txt") + ":fontsize=13:fontcolor=black:x=121:y=60" +
		",drawtext=fontfile=" + font + ":textfile=" + filepath.Join(dir, "pw.txt") + ":fontsize=13:fontcolor=black:x=121:y=340" +
		",drawtext=fontfile=" + font + ":textfile=" + filepath.Join(dir, "key.txt") + ":fontsize=13:fontcolor=black:x=121:y=380" +
		",drawtext=fontfile=" + font + ":textfile=" + filepath.Join(dir, "wallet.txt") + ":fontsize=13:fontcolor=black:x=121:y=420"
	args := []string{"-hide_banner", "-y", "-f", "lavfi", "-i", "color=c=white:s=1920x1080:d=3:r=25",
		"-vf", vf, "-c:v", "libx264", "-crf", "23", "-pix_fmt", "yuv420p", out}
	if err := exec.Command(ffmpeg, args...).Run(); err != nil {
		t.Skipf("ffmpeg could not make small-screen test video: %v", err)
	}
	return out
}

// v2.6: Auto-scan found nothing on real tutorial videos — 13px screen
// text misreads at 1x ("password:" -> "sword:"), so frames are now
// upscaled 2x before OCR and coordinates halved back. Also: bare
// "password" labels match, and a new opt-in "suspicious" pattern.

func TestOCRScaleFactor(t *testing.T) {
	cases := []struct {
		w    int
		want float64
	}{
		{0, 1}, {1280, 2}, {1920, 2}, {2560, 1}, {3840, 1},
	}
	for _, c := range cases {
		if got := ocrScaleFactor(c.w); got != c.want {
			t.Errorf("ocrScaleFactor(%d) = %v, want %v", c.w, got, c.want)
		}
	}
	// Between 1920 and 2560 the factor shrinks so the upscaled long
	// side stays ~3840.
	if got := ocrScaleFactor(2000); got < 1.9 || got > 1.93 {
		t.Errorf("ocrScaleFactor(2000) = %v, want ~1.92", got)
	}
	if got := ocrScaleFactor(2400); got < 1.59 || got > 1.61 {
		t.Errorf("ocrScaleFactor(2400) = %v, want ~1.6", got)
	}
}

func TestScaleWordsToOriginal(t *testing.T) {
	// A 2x-space word (the URL on small-screen-text.mp4) maps back to
	// the original-pixel box.
	words := []OCRWord{{Text: "https://github.com/sahfar363/blurcut", X: 242, Y: 120, W: 478, H: 28, Conf: 91}}
	got := scaleWordsToOriginal(words, 2)
	if got[0].X != 121 || got[0].Y != 60 || got[0].W != 239 || got[0].H != 14 {
		t.Errorf("halved word = %+v, want 121,60,239,14", got[0])
	}
	// Odd coordinates round to nearest int.
	got = scaleWordsToOriginal([]OCRWord{{Text: "x", X: 101, Y: 51, W: 33, H: 11}}, 2)
	if got[0].X != 51 || got[0].Y != 26 || got[0].W != 17 || got[0].H != 6 {
		t.Errorf("rounded halving = %+v, want 51,26,17,6", got[0])
	}
	// Factor 1 is a no-op.
	got = scaleWordsToOriginal([]OCRWord{{Text: "x", X: 10, Y: 20, W: 30, H: 40}}, 1)
	if got[0].X != 10 || got[0].Y != 20 || got[0].W != 30 || got[0].H != 40 {
		t.Errorf("factor 1 must not change coords: %+v", got[0])
	}
}

func TestGroupWordsScalesWithUpscale(t *testing.T) {
	// Two words 60px apart in 2x space are on the same line (gap
	// threshold 40*2=80); at 1x they would be separate groups.
	words := []OCRWord{
		{Text: "password:", X: 290, Y: 680, W: 90, H: 22, Conf: 90},
		{Text: "hunter2", X: 440, Y: 680, W: 105, H: 22, Conf: 90},
	}
	groups := groupWords(words, 2)
	if len(groups) != 1 {
		t.Fatalf("2x grouping = %d groups, want 1", len(groups))
	}
	if groups[0].Text != "password: hunter2" {
		t.Errorf("grouped text = %q, want %q", groups[0].Text, "password: hunter2")
	}
	// Same words at 1x with a 60px gap: separate.
	groups = groupWords([]OCRWord{
		{Text: "a", X: 0, Y: 0, W: 10, H: 10, Conf: 90},
		{Text: "b", X: 70, Y: 0, W: 10, H: 10, Conf: 90},
	}, 1)
	if len(groups) != 2 {
		t.Errorf("1x grouping = %d groups, want 2", len(groups))
	}
}

func TestPasswordBareLabel(t *testing.T) {
	pats := map[string]bool{"password": true}
	for _, text := range []string{"password", "Password", "passwd", "pwd", "password: hunter2", "Password = hunter2", "enter your password here"} {
		if got := classifyText(text, nil, pats); got != "password" {
			t.Errorf("classifyText(%q) = %q, want password", text, got)
		}
	}
	// A value, not a label, must not match.
	if got := classifyText("password123", nil, pats); got != "" {
		t.Errorf("classifyText(password123) = %q, want empty", got)
	}
	// Pattern off: nothing matches.
	if got := classifyText("password", nil, map[string]bool{"password": false}); got != "" {
		t.Errorf("password pattern off, got %q", got)
	}
}

func TestSuspiciousPattern(t *testing.T) {
	token := "aB3dEf7gHk9mNp2rSt5vX" // 22 chars, letters + digits
	off := map[string]bool{"suspicious": false}
	on := map[string]bool{"suspicious": true}
	if got := classifyText(token, nil, off); got != "" {
		t.Errorf("suspicious off, got %q", got)
	}
	if got := classifyText(token, nil, on); got != "possible-key" {
		t.Errorf("classifyText(%q) = %q, want possible-key", token, got)
	}
	// Needs a digit AND a letter.
	if got := classifyText("abcdefghijklmnopqrstuv", nil, on); got != "" {
		t.Errorf("letters-only token = %q, want empty", got)
	}
	if got := classifyText("12345678901234567890", nil, on); got != "" {
		t.Errorf("digits-only token = %q, want empty", got)
	}
	// Too short.
	if got := classifyText("aB3dEf7gHk9", nil, on); got != "" {
		t.Errorf("short token = %q, want empty", got)
	}
	// A real URL is never relabelled, even with suspicious on.
	all := map[string]bool{"url": true, "email": true, "password": true, "apikey": true, "suspicious": true}
	if got := classifyText("https://github.com/sahfar363/blurcut", nil, all); got != "url" {
		t.Errorf("URL with suspicious on = %q, want url", got)
	}
	if got := classifyText("user@example.com", nil, all); got != "email" {
		t.Errorf("email with suspicious on = %q, want email", got)
	}
}

func TestScanSmallScreenText(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	ts, err := exec.LookPath("tesseract")
	if err != nil {
		t.Skip("tesseract not installed")
	}
	video := makeSmallScreenTextVideo(t, ff)
	tools := Tools{FFmpeg: ff, Tesseract: ts}
	patterns := map[string]bool{"url": true, "email": true, "password": true, "apikey": true, "crypto": true}
	probe := VideoInfo{Duration: 3, Width: 1920, Height: 1080, FPS: 25}
	dets, err := scanVideo(tools, video, 1.0, nil, patterns, probe, nil)
	if err != nil {
		t.Fatalf("scanVideo: %v", err)
	}
	labels := map[string]Region{}
	for _, d := range dets {
		labels[d.Label] = d.Region
		t.Logf("found %s %q at %d,%d %dx%d (%.2f-%.2fs)", d.Label, d.Text, d.X, d.Y, d.W, d.H, d.Start, d.End)
	}
	// The video draws a URL (top), "password: hunter2",
	// "export API_KEY=<redacted>" and an Ethereum address — all four must be
	// found now.
	for _, want := range []string{"url", "password", "apikey", "crypto"} {
		if _, ok := labels[want]; !ok {
			t.Errorf("scan missed %q; found labels: %v", want, labels)
		}
	}
	// Boxes must land on the drawn text (original 1920x1080 coords,
	// within ~10% of the frame): URL near y=60, password near y=340,
	// API key near y=382.
	if r, ok := labels["url"]; ok && (r.Y < 40 || r.Y > 90 || r.X < 80 || r.X > 180) {
		t.Errorf("url box at %d,%d, want near 121,60", r.X, r.Y)
	}
	if r, ok := labels["password"]; ok && (r.Y < 310 || r.Y > 370 || r.X < 100 || r.X > 220) {
		t.Errorf("password box at %d,%d, want near 145,340", r.X, r.Y)
	}
	if r, ok := labels["apikey"]; ok && (r.Y < 350 || r.Y > 410 || r.X < 80 || r.X > 220) {
		t.Errorf("apikey box at %d,%d, want near 121,382", r.X, r.Y)
	}
	if r, ok := labels["crypto"]; ok && (r.Y < 390 || r.Y > 450 || r.X < 80 || r.X > 220) {
		t.Errorf("crypto box at %d,%d, want near 121,420", r.X, r.Y)
	}
}

func TestCryptoPattern(t *testing.T) {
	pats := map[string]bool{"crypto": true}
	// Bitcoin bech32, Bitcoin legacy, Ethereum.
	for _, addr := range []string{
		"bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq",
		"1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
		"3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy",
		"0x7aB3c4D5e6F708192a3B4c5D6e7F8091a2B3c4d5",
		"send it to 0x7aB3c4D5e6F708192a3B4c5D6e7F8091a2B3c4d5 please",
	} {
		if got := classifyText(addr, nil, pats); got != "crypto" {
			t.Errorf("classifyText(%q) = %q, want crypto", addr, got)
		}
	}
	// Ordinary words and short hex are not addresses.
	for _, text := range []string{"hello world", "0x1234", "bc1short", "wallet"} {
		if got := classifyText(text, nil, pats); got != "" {
			t.Errorf("classifyText(%q) = %q, want empty", text, got)
		}
	}
	// Pattern off: nothing matches.
	if got := classifyText("0x7aB3c4D5e6F708192a3B4c5D6e7F8091a2B3c4d5", nil, map[string]bool{"crypto": false}); got != "" {
		t.Errorf("crypto pattern off, got %q", got)
	}
	// A URL still wins over an address-shaped token in its path.
	all := map[string]bool{"url": true, "crypto": true}
	if got := classifyText("https://example.com", nil, all); got != "url" {
		t.Errorf("plain URL = %q, want url", got)
	}
}

func TestUpdateRegionTimes(t *testing.T) {
	store, err := openJobStore(t.TempDir(), Tools{})
	if err != nil {
		t.Fatal(err)
	}
	store.Add(&Job{ID: "j1", Duration: 60, Width: 1920, Height: 1080,
		Regions: []Region{{ID: "m1", Label: "manual", Start: 10, End: 15, Enabled: true, Source: "manual"}}})
	srv := newServer(Config{DataDir: store.dir}, Tools{}, store)
	j, _ := store.Get("j1")

	post := func(body string) (int, Region) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/jobs/j1/regions/m1", strings.NewReader(body))
		rec := httptest.NewRecorder()
		srv.updateRegion(rec, req, j, "m1")
		var r Region
		if rec.Code == 200 {
			_ = json.NewDecoder(rec.Body).Decode(&r)
		}
		return rec.Code, r
	}

	// Plain time change persists.
	if code, r := post(`{"start":20,"end":25}`); code != 200 || r.Start != 20 || r.End != 25 {
		t.Fatalf("time update = code %d region %+v, want 200 with 20-25", code, r)
	}
	// Start of 0 is a real value, not "not sent".
	if code, r := post(`{"start":0,"end":5}`); code != 200 || r.Start != 0 || r.End != 5 {
		t.Fatalf("start=0 update = code %d region %+v, want 200 with 0-5", code, r)
	}
	// End past the video duration is clamped.
	if code, r := post(`{"start":55,"end":999}`); code != 200 || r.Start != 55 || r.End != 60 {
		t.Fatalf("clamp update = code %d region %+v, want 200 with 55-60", code, r)
	}
	// End before start is rejected and the region is unchanged.
	if code, _ := post(`{"start":30,"end":25}`); code != 400 {
		t.Fatalf("end<=start update = code %d, want 400", code)
	}
	if got := j.Regions[0]; got.Start != 55 || got.End != 60 {
		t.Errorf("rejected update changed the region to %v-%v, want 55-60", got.Start, got.End)
	}
	// An enabled-only update must not clobber the times (or vice versa).
	if code, r := post(`{"enabled":false}`); code != 200 || r.Enabled || r.Start != 55 || r.End != 60 {
		t.Fatalf("enabled-only update = code %d region %+v, want 200 with times kept", code, r)
	}
}

func TestOCRPreviewFrame(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	ts, err := exec.LookPath("tesseract")
	if err != nil {
		t.Skip("tesseract not installed")
	}
	video := makeSmallScreenTextVideo(t, ff)
	tools := Tools{FFmpeg: ff, Tesseract: ts}
	probe := VideoInfo{Duration: 3, Width: 1920, Height: 1080, FPS: 25}
	words, err := ocrPreviewFrame(tools, video, 1.0, probe)
	if err != nil {
		t.Fatalf("ocrPreviewFrame: %v", err)
	}
	if len(words) == 0 {
		t.Fatalf("preview returned no words")
	}
	if len(words) > 40 {
		t.Errorf("preview returned %d words, want at most 40", len(words))
	}
	joined := ""
	for _, w := range words {
		joined += w.Text + " "
	}
	for _, want := range []string{"https://github.com/sahfar363/blurcut", "password:", "API_KEY="} {
		if !strings.Contains(joined, want) {
			t.Errorf("preview words missing %q: %q", want, joined)
		}
	}
	// Words are in original-pixel coordinates: URL x must be ~121,
	// not the 2x value ~242.
	for _, w := range words {
		if strings.HasPrefix(w.Text, "https://") && (w.X < 80 || w.X > 180) {
			t.Errorf("preview URL word x=%d, want near 121 (original coords)", w.X)
		}
	}
}
