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

## Install (Windows — one zip, everything included)

1. Download the latest release zip from
   [Releases](https://github.com/sahfar363/blurcut/releases)
   — it contains `blurcut.exe` plus everything it needs in `tools/`:
   `ffmpeg.exe`, `tesseract.exe` (+ its DLLs) and
   `tessdata/eng.traineddata`.
2. Unzip it to a folder, e.g. `C:\blurcut`. Keep the `tools/` folder
   next to `blurcut.exe`.
3. Double-click `blurcut.exe`. It opens its own BlurCut window — no
   browser, no URL to type. (If the WebView2 runtime is missing it
   opens Edge in app mode instead, then your default browser.)

Then: upload a video → **Auto-scan** → review the regions on the
preview (draw extra boxes if needed) → **Export blurred video** →
download the result.

> The Tools card at the top of the window shows green ✓ ticks for
> ffmpeg and tesseract with the exact path found. If one is ever red,
> use the Download buttons there, or paste a path manually. BlurCut
> looks for tools next to `blurcut.exe`, in `tools/`, and on PATH —
> in that order — so a `ffmpeg.exe` you drop in yourself just works.

Prefer the browser instead? Run `blurcut.exe serve` — it opens
`http://localhost:8080` for you. `blurcut.exe serve --no-browser`
starts the server without opening anything.

## Install (Linux)

Download `blurcut-linux-amd64` from Releases, plus `ffmpeg` and
`tesseract-ocr` from your package manager:

```
sudo apt install ffmpeg tesseract-ocr
./blurcut-linux-amd64 serve --port 8080
```

## Tools (ffmpeg / tesseract)

BlurCut finds its tools in this order: paths saved in settings,
`BLURCUT_FFMPEG` / `BLURCUT_FFPROBE` / `BLURCUT_TESSERACT` env vars,
the folder next to `blurcut.exe`, `tools/` (and `bin/`) there, the
current folder, PATH, and — on Windows — the usual install locations
(`C:\Program Files\Tesseract-OCR`, `C:\ffmpeg\bin`, ...). If tesseract
is found, its `tessdata` folder is picked up automatically. The Tools
card in the UI shows what was found and where it looked; missing tools
can be downloaded from there (ffmpeg) or via the official installer
(tesseract).

The Windows zip bundles ffmpeg (GPLv3 — see
[THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md)) and Tesseract
(Apache 2.0). `ffprobe.exe` is optional: BlurCut uses `ffmpeg -i`
when ffprobe is absent, so it is not bundled — the zip stays smaller.

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
