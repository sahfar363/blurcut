// BlurCut — offline Windows app to auto-blur sensitive info in videos.
//
//	blurcut serve [--port 8080] [--data-dir DIR]   # web UI (default)
//	blurcut scan <video> --out detections.json     # OCR scan from CLI
//	blurcut blur <video> --regions regions.json --out out.mp4
//	blurcut --help | --version
//
// No internet, no API: ffmpeg + tesseract run locally via exec.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const version = "0.1.0"

func now() time.Time { return time.Now() }

func usage() {
	fmt.Printf(`BlurCut %s — auto-blur sensitive info in videos (offline)

Usage:
  blurcut serve [--port 8080] [--data-dir DIR]
  blurcut scan <video> --out detections.json
  blurcut blur <video> --regions regions.json --out out.mp4
  blurcut --help
  blurcut --version

The web UI (serve) is the main way to use it: upload a video, Scan,
review the found regions on the preview, draw manual boxes if needed,
then Export. The blurred file keeps the original audio and quality.

Needs ffmpeg (and tesseract for auto-scan) next to blurcut.exe or on
PATH. See README.md for the Windows download links.
`, version)
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--help", "-h", "help":
			usage()
			return
		case "--version", "-v", "version":
			fmt.Println("blurcut", version)
			return
		}
	}
	cmd := "serve"
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "serve", "scan", "blur":
			cmd = args[0]
			args = args[1:]
		}
	}
	tools := detectTools()

	switch cmd {
	case "scan":
		if err := runScanCLI(tools, args); err != nil {
			fmt.Fprintln(os.Stderr, "blurcut:", err)
			os.Exit(1)
		}
		return
	case "blur":
		if err := runBlurCLI(tools, args); err != nil {
			fmt.Fprintln(os.Stderr, "blurcut:", err)
			os.Exit(1)
		}
		return
	}

	// serve
	fs := flag.NewFlagSet("blurcut", flag.ExitOnError)
	fs.Usage = usage
	portFlag := fs.Int("port", 0, "web port")
	dataFlag := fs.String("data-dir", "", "data directory")
	_ = fs.Parse(args)
	dataDir := *dataFlag
	if dataDir == "" {
		dataDir = defaultDataDir()
	}
	cfg, err := loadConfig(dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "blurcut: could not load config:", err)
		os.Exit(1)
	}
	cfg.DataDir = dataDir
	if *portFlag != 0 {
		cfg.Port = *portFlag
	} else if p := os.Getenv("BLURCUT_PORT"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n >= 1 && n <= 65535 {
			cfg.Port = n
		}
	}
	store, err := openJobStore(dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "blurcut: could not open data store:", err)
		os.Exit(1)
	}
	if err := saveConfig(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "blurcut: could not save config:", err)
	}
	if err := serve(cfg, tools, store); err != nil {
		fmt.Fprintln(os.Stderr, "blurcut:", err)
		os.Exit(1)
	}
}

// parseCLIArgs moves --flag args before positionals so flags work
// whether the user writes them before or after the video path.
func parseCLIArgs(fs *flag.FlagSet, args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "--") {
			flags = append(flags, a)
			// --flag value form: also move the value.
			if !strings.Contains(a, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				i++
				flags = append(flags, args[i])
			}
		} else {
			pos = append(pos, a)
		}
	}
	_ = fs.Parse(flags)
	return pos
}

func runScanCLI(tools Tools, args []string) error {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	out := fs.String("out", "detections.json", "output JSON file")
	// Go's flag package stops at the first positional arg, so flags
	// given after the video path would be ignored — reorder first.
	rest := parseCLIArgs(fs, args)
	if len(rest) < 1 {
		return fmt.Errorf("usage: blurcut scan <video> --out detections.json")
	}
	video := rest[0]
	if !tools.FFmpegOK() {
		return fmt.Errorf("ffmpeg not found")
	}
	if !tools.TesseractOK() {
		return fmt.Errorf("tesseract not found — auto-scan needs Tesseract OCR")
	}
	cfg, _ := loadConfig(defaultDataDir())
	info, err := probeVideo(tools, video)
	if err != nil {
		return err
	}
	dets, err := scanVideo(tools.FFmpeg, tools.Tesseract, video, cfg.ScanInterval, cfg.Keywords, cfg.Patterns, info)
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(dets, "", "  ")
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		return err
	}
	fmt.Printf("Found %d region(s) in %s (%.1fs, %dx%d) -> %s\n", len(dets), video, info.Duration, info.Width, info.Height, *out)
	return nil
}

func runBlurCLI(tools Tools, args []string) error {
	fs := flag.NewFlagSet("blur", flag.ExitOnError)
	regionsFile := fs.String("regions", "", "regions JSON file")
	out := fs.String("out", "", "output mp4")
	rest := parseCLIArgs(fs, args)
	if len(rest) < 1 || *regionsFile == "" || *out == "" {
		return fmt.Errorf("usage: blurcut blur <video> --regions regions.json --out out.mp4")
	}
	if !tools.FFmpegOK() {
		return fmt.Errorf("ffmpeg not found")
	}
	video := rest[0]
	b, err := os.ReadFile(*regionsFile)
	if err != nil {
		return err
	}
	var regions []Region
	if err := json.Unmarshal(b, &regions); err != nil {
		// also accept {"regions": [...]}
		var wrap struct {
			Regions []Region `json:"regions"`
		}
		if err2 := json.Unmarshal(b, &wrap); err2 != nil {
			return err
		}
		regions = wrap.Regions
	}
	cfg, _ := loadConfig(defaultDataDir())
	// Regions from a file may only have relative coords.
	info, err := probeVideo(tools, video)
	if err != nil {
		return err
	}
	for i := range regions {
		if regions[i].W == 0 && regions[i].RW > 0 {
			relativeToPixel(&regions[i], info.Width, info.Height)
		}
	}
	if err := exportVideo(tools, video, *out, regions, cfg.BlurStrength, func(p float64) {
		fmt.Printf("\rExport %.0f%%", p*100)
	}); err != nil {
		return err
	}
	fmt.Printf("\nWrote %s\n", filepath.Clean(*out))
	return nil
}
