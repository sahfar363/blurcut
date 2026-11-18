// Checks videoContentRect() in webui/app.js — the math that fits the
// drawing overlay to the displayed video picture (see app.js). Run:
//   node scripts/check-overlay-rect.js
// Exits non-zero if any case is off, or if app.js fails `node --check`.
'use strict';
const fs = require('fs');
const path = require('path');
const { execFileSync } = require('child_process');

const root = path.join(__dirname, '..');
const appJs = path.join(root, 'webui', 'app.js');
const src = fs.readFileSync(appJs, 'utf8');

// Pull just the pure function out of the browser script (the rest of
// app.js needs document/window) and load it.
const start = src.indexOf('function videoContentRect');
if (start < 0) { console.error('FAIL: videoContentRect not found in app.js'); process.exit(1); }
const end = src.indexOf('\n}\n', start);
if (end < 0) { console.error('FAIL: could not find end of videoContentRect'); process.exit(1); }
const videoContentRect = new Function(src.slice(start, end + 3) + '\nreturn videoContentRect;')();

// [elementW, elementH, videoW, videoH, expected {left, top, width, height}]
const cases = [
  // 928x460 element (60vh cap on a short screen), 1920x1080 video:
  // picture is 817.8 wide, centred — the old full-width overlay made
  // drawn boxes ~80% wide in the export.
  [928, 460, 1920, 1080, { left: 55.1, top: 0, width: 817.8, height: 460 }],
  // Element aspect matches the video exactly: overlay == element.
  [928, 522, 1920, 1080, { left: 0, top: 0, width: 928, height: 522 }],
  // Vertical 720x1280 video in a wide short element: pillarboxed.
  [928, 460, 720, 1280, { left: 334.6, top: 0, width: 258.8, height: 460 }],
];

let failed = false;
for (const [elW, elH, iw, ih, want] of cases) {
  const got = videoContentRect(elW, elH, iw, ih);
  const close = (a, b) => Math.abs(a - b) <= 0.15;
  const ok = close(got.left, want.left) && close(got.top, want.top) &&
    close(got.width, want.width) && close(got.height, want.height);
  console.log(`${ok ? 'ok  ' : 'FAIL'} rect(${elW}x${elH}, ${iw}x${ih}) = ` +
    `left ${got.left.toFixed(1)}, top ${got.top.toFixed(1)}, ` +
    `width ${got.width.toFixed(1)}, height ${got.height.toFixed(1)}` +
    (ok ? '' : ` — expected left ${want.left}, top ${want.top}, width ${want.width}, height ${want.height}`));
  if (!ok) failed = true;
}

// The page script must still parse as valid JS.
try {
  execFileSync('node', ['--check', appJs], { stdio: 'pipe' });
  console.log('ok   node --check webui/app.js');
} catch (e) {
  console.error('FAIL: node --check webui/app.js\n' + (e.stdout || '') + (e.stderr || ''));
  failed = true;
}

if (failed) { console.error('check-overlay-rect: FAILED'); process.exit(1); }
console.log('check-overlay-rect: all passed');
