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
  box: click "✏️ Draw a box" first, drag on the video, and drawing
  turns itself off again so you can press play right away.
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
   `ffmpeg.exe`, `ffprobe.exe`, `tesseract.exe` (+ its DLLs) and
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
(Apache 2.0), including `ffprobe.exe`, so all three Tools rows show
green ✓ ticks.

**Any video format plays in the preview.** Edge cannot play .mkv,
.mov, .avi or HEVC files directly, so when you upload one of those
BlurCut builds a playable mp4 preview in the background (h264 video
is just remuxed, no quality loss; anything else is re-encoded at
most 1280 wide). The preview is only for watching — scanning and
export always use your original file.

**Odd-size videos work too.** Cropped or window recordings often have
odd dimensions (e.g. 1279x719), which libx264 rejects ("width not
divisible by 2"). Export now forces the output to even dimensions
(shrinking by at most 1px) and clamps every blur box to the frame, so
those videos export instead of failing. If an export still fails, the
error message now shows ffmpeg's real reason instead of a generic
"Conversion failed!".

**The play button works now.** In 0.2.2 and earlier, the drawing layer
sat on top of the video and swallowed every click — including clicks on
the play button — so the video could never be started with the mouse
and just sat on its first frame. Drawing is now a toggle (off by
default, and it switches itself off after you draw a box), so clicks
reach the video controls normally.

**Downloads are real .mp4 files.** The blurred export is always an
mp4, and the download is now always named `blurred-<name>.mp4` — in
0.2.3 and earlier it kept the original file's extension (e.g.
`blurred-clip.mkv`), so players like VLC picked the wrong format from
the name and refused to open a perfectly valid file. Every export is
also verified before it is offered (file exists, non-empty, probes as
a video with a duration); a bad export is reported as an error with
the reason instead of a silent "done". The version (e.g. v0.2.5) is
shown in the header so you can check which build you are running.

**Auto-scan keeps your manual boxes.** In 0.2.4 and earlier, pressing
Auto-scan replaced the whole region list, so boxes you had drawn by
hand were wiped. Auto-scan now only replaces the auto-detected
regions; manual boxes are kept (a fresh scan hit that duplicates a
manual box is dropped in favour of yours). Old jobs also remember
their regions and exports between restarts — if an export file left
over from an older version turns out to be broken when the app
starts, it is deleted instead of being offered as a download. Jobs
can be removed from the list with the "Delete job" button, which
deletes the job and its files.

**Auto-scan can now read small screen text.** In 0.2.5 and earlier,
frames were OCRed at their original size, and tesseract misread
small text (browser bars, terminal, 13px at 1080p) — "password:"
came back as "sword:", so no pattern matched and the scan found
nothing on real tutorial videos. Frames are now upscaled 2x before
OCR (4K video is already big enough), and the boxes are mapped back
to the original coordinates. The region list shows each region's
source (manual/auto) and time range, so you can see at a glance what
the scan found versus what you drew. A "What does the scan see
here?" button OCRs the frame at the video's current time and shows
the raw words — if it reads your screen, the scan will find things.
Bare "password" labels (login forms, no `:` after them) are now
found too, as are cryptocurrency addresses (Bitcoin bech32 and
legacy, Ethereum `0x…`) — they have recognizable shapes, so the scan
can find them without any AI. There is also a new opt-in pattern, off
by default, that flags long random-looking tokens (20+ letters/digits,
"possible-key") — off because it can blur innocent text. Region
start/end times are editable right in the region list (seconds, one
decimal), for manual and auto-detected boxes alike — the old 5-second
window on a hand-drawn box was invisible before, which is also why a
manual blur could look like a second, random blur a few seconds later.

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

1. ffmpeg extracts one frame per second (`fps` filter), upscaled 2x
   before OCR so small screen text reads correctly (4K is left alone).
2. Tesseract OCR reads each frame as TSV (word + box + confidence).
3. Words matching a sensitive pattern are grouped into boxes, padded,
   merged across frames into time ranges.
4. Export builds one ffmpeg `filter_complex` chain:
   `split → crop + boxblur → overlay` per region, with
   `enable='between(t,start,end)'`, then a final
   `scale=trunc(iw/2)*2:trunc(ih/2)*2,format=yuv420p` so odd-sized
   videos (e.g. 1279x719) still encode, and re-encodes with
   libx264 (or h264_nvenc when available) at CRF 18. Audio is copied
   when the container allows it, otherwise re-encoded to AAC.

Auto-detection is never 100% — always review the preview before
exporting, especially for small or fast-moving text.
