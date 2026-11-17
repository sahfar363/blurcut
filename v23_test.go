package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// v2.3: the drawing overlay sat on top of the video with
// pointer-events:auto and ate every click, so the play button (and the
// whole control bar) could never be reached with the mouse — the video
// just sat on its first frame. VidTest (no overlay) played the same
// files fine, which proved the bug was in BlurCut's UI, not the PC.
// Fix: overlay is pointer-events:none by default, region boxes stay
// clickable, and drawing is a toggle that turns itself off after a
// box is drawn.

func TestOverlayLetsClicksThrough(t *testing.T) {
	css, err := os.ReadFile("webui/style.css")
	if err != nil {
		t.Fatalf("read style.css: %v", err)
	}
	// The overlay must not intercept clicks by default.
	if !strings.Contains(string(css), "#overlay{position:absolute;inset:0;pointer-events:none}") {
		t.Errorf("overlay must be pointer-events:none by default:\n%s", css)
	}
	// Region boxes must stay clickable (clicking one seeks to its start).
	boxRule := regexp.MustCompile(`\.box\{[^}]*pointer-events:auto`).FindString(string(css))
	if boxRule == "" {
		t.Errorf(".box rule must set pointer-events:auto so boxes stay clickable")
	}
}

func TestDrawModeToggle(t *testing.T) {
	js, err := os.ReadFile("webui/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	src := string(js)
	for _, want := range []string{
		"let drawMode = false",
		"function setDrawMode(on)",
		"ov.style.pointerEvents = drawMode ? 'auto' : 'none'",
		"if (!drawMode) return;",                   // mousedown gated on drawing mode
		"setDrawMode(false); // box drawn",         // auto-off after drawing a box
		"setDrawMode(false);\n  $('#previewCard')", // fresh job = playback mode
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js missing draw-mode logic: %q", want)
		}
	}
	// The toggle button flips the mode.
	if !strings.Contains(src, "$('#drawToggleBtn').onclick = () => setDrawMode(!drawMode);") {
		t.Errorf("app.js missing draw toggle button handler")
	}
}

func TestDrawToggleButtonExists(t *testing.T) {
	html, err := os.ReadFile("webui/index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	src := string(html)
	if !strings.Contains(src, `id="drawToggleBtn"`) {
		t.Errorf("index.html missing #drawToggleBtn")
	}
	if !strings.Contains(src, `id="drawHint"`) {
		t.Errorf("index.html missing #drawHint")
	}
	if !strings.Contains(src, "Drawing mode on") {
		t.Errorf("index.html missing drawing-mode hint text")
	}
}
