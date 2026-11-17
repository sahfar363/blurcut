# BlurCut 🎬

Auto-blur sensitive info in your videos — offline, on your own laptop.
Made for YouTube tutorials: it finds links, passwords, emails and API
keys on screen and blurs them, so you don't have to do it by hand in
DaVinci.

- **Auto-scan** — OCR reads the video every second and finds URLs,
  emails, `password:` fields, API keys / secrets, plus your own custom
  keywords (your email, username, …).
- **Review first** — every found region shows on the video preview with
  its time range. Untick false alarms, delete them, or draw a manual
  box by dragging on the video.
- **Fast export** — only the blurred boxes are re-encoded by ffmpeg
  (hardware NVENC if your GPU has it, otherwise libx264). Audio is
  copied untouched, original quality kept.
- **Fully offline** — no internet, no API, no upload. Your video never
  leaves your laptop. Works on 30-minute videos by streaming, not by
  loading the whole file into RAM.
- **Light** — one static Go binary, no database server.

## Install (Windows — recommended)

1. Download the latest release zip from
   [Releases](https://github.com/sahfar363/blurcut/releases)
   — it contains `blurcut.exe`.
2. Download ffmpeg for Windows from
   [gyan.dev/ffmpeg/builds](https://www.gyan.dev/ffmpeg/builds/)
   (release essentials zip) and copy `ffmpeg.exe` into the same folder
   as `blurcut.exe`.
3. For auto-scan, also install Tesseract OCR from
   [UB-Mannheim/tesseract](https://github.com/UB-Mannheim/tesseract/wiki)
   and make sure `tesseract.exe` is on PATH. Without it, auto-scan is
   disabled but manual boxes still work.
4. Double-click `blurcut.exe`. It prints a link — open it in your browser:

```
http://localhost:8080
```

Then: upload a video → **Auto-scan** → review the regions on the
preview (draw extra boxes if needed) → **Export blurred video** →
download the result.

> If ffmpeg is missing, BlurCut tells you so in the UI and on the
> console instead of crashing. Same for tesseract.

## Install (Linux)

Download `blurcut-linux-amd64` from Releases, plus `ffmpeg` and
`tesseract-ocr` from your package manager:

```
sudo apt install ffmpeg tesseract-ocr
./blurcut-linux-amd64 serve --port 8080
```

## Build from source

```
go vet ./...
go test ./...
go build -o blurcut .
./blurcut --help
```

Windows cross-build (no Windows machine needed):

```
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o blurcut.exe .
```

## CLI (for testing / scripting)

```
# Scan only, write detections as JSON
blurcut scan video.mp4 --out detections.json

# Blur from a regions JSON file (same shape as detections.json)
blurcut blur video.mp4 --regions detections.json --out blurred.mp4

# Web UI on another port / data dir
blurcut serve --port 8080 --data-dir ~/.blurcut
```

Settings (scan interval 0.5–2s, blur strength 10–30, custom keywords,
which patterns to detect) live in the web UI and in
`~/.blurcut/config.json`. Jobs, uploads and exports live in
`~/.blurcut/` (override with `BLURCUT_DATA`).

## How it works

1. ffmpeg extracts one frame per second (`fps` filter).
2. Tesseract OCR reads each frame as TSV (word + box + confidence).
3. Words matching a sensitive pattern are grouped into boxes, padded,
   merged across frames into time ranges.
4. Export builds one ffmpeg `filter_complex` chain:
   `split → crop + boxblur → overlay` per region, with
   `enable='between(t,start,end)'`, and re-encodes with
   libx264 (or h264_nvenc when available) at CRF 18.

Auto-detection is never 100% — always review the preview before
exporting, especially for small or fast-moving text.
