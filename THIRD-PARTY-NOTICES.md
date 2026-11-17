# Third-party components bundled in the Windows zip

BlurCut itself is a small Go program. The Windows zip also bundles the
two tools it shells out to, so you do not have to install them
separately. They keep their own licences.

## FFmpeg — GPLv3

- What: `tools/ffmpeg.exe` (Windows essentials build 8.1.2, by Gyan Doshi)
- Download: https://www.gyan.dev/ffmpeg/builds/packages/ffmpeg-8.1.2-essentials_build.zip
- SHA-256: `db580001caa24ac104c8cb856cd113a87b0a443f7bdf47d8c12b1d740584a2ec`
- Source: https://github.com/FFmpeg/FFmpeg/tree/n8.1.2
- Licence: GNU General Public License v3 — https://www.gnu.org/licenses/gpl-3.0.html
  (the build's own licence text ships in its README; FFmpeg source code
  is available from the link above as the GPL requires).

## Tesseract OCR — Apache 2.0

- What: `tools/tesseract.exe` + its DLLs + `tools/tessdata/eng.traineddata`
- Build: UB-Mannheim Tesseract 5.5.3 installer
  (`tesseract-ocr-w64-setup-5.5.3.20260724.exe`),
  https://github.com/UB-Mannheim/tesseract/wiki
- Language data: `eng.traineddata` from tessdata_fast,
  https://github.com/tesseract-ocr/tessdata_fast
- Licence: Apache License 2.0 — https://www.apache.org/licenses/LICENSE-2.0

## Microsoft Edge WebView2 runtime — not bundled

BlurCut's app window uses the WebView2 runtime that already ships with
Windows 10/11. It is not included in the zip. If it is missing,
BlurCut falls back to opening Edge in app mode, then your default
browser. The WebView2 loader DLL embedded in blurcut.exe comes from
the WebView2 SDK, which Microsoft licences permissively.
