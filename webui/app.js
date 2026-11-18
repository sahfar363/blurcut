// BlurCut web UI — no CDN, no external requests. All fetch() is local.
const $ = s => document.querySelector(s);
let currentJob = null, pollTimer = null;
let drawMode = false;

// Drawing is OFF by default so clicks reach the video and its controls.
// Turning it on lets the overlay catch drags to draw a manual box.
function setDrawMode(on) {
  drawMode = on;
  const ov = $('#overlay'), btn = $('#drawToggleBtn'), hint = $('#drawHint');
  if (ov) ov.style.pointerEvents = drawMode ? 'auto' : 'none';
  if (btn) { btn.textContent = drawMode ? '✏️ Drawing: ON' : '✏️ Draw a box'; btn.classList.toggle('active', drawMode); }
  if (hint) hint.hidden = !drawMode;
}

// The video element can be wider than the picture it shows: max-height
// caps the element, and object-fit: contain letterboxes the picture
// inside it (black bars left/right). Draw coordinates are fractions of
// the OVERLAY, and the export treats them as fractions of the true
// video — so the overlay must match the displayed picture exactly, not
// the element box. Before this, on a short screen a hand-drawn box came
// out ~80% wide in the export while the preview looked right, because
// the preview rendered the same fractions on the same too-wide overlay.
// (Auto-scan boxes had the same error in reverse in the preview: their
// fractions are true video fractions, shown stretched on the overlay.)
function videoContentRect(elW, elH, iw, ih) {
  if (!elW || !elH || !iw || !ih) return { left: 0, top: 0, width: elW, height: elH };
  const s = Math.min(elW / iw, elH / ih);
  const w = iw * s, h = ih * s;
  return { left: (elW - w) / 2, top: (elH - h) / 2, width: w, height: h };
}

function fitOverlay() {
  const v = $('#video'), ov = $('#overlay');
  if (!v || !ov) return;
  if (!v.videoWidth || !v.videoHeight) {
    // No video loaded yet (or metadata not in): full-size overlay.
    ov.style.left = '0'; ov.style.top = '0'; ov.style.width = '100%'; ov.style.height = '100%';
    return;
  }
  const r = videoContentRect(v.clientWidth, v.clientHeight, v.videoWidth, v.videoHeight);
  // The video element fills the wrap (display:block, width:100%), so its
  // offset inside the wrap is 0 — included anyway in case that changes.
  ov.style.left = (v.offsetLeft + r.left) + 'px';
  ov.style.top = (v.offsetTop + r.top) + 'px';
  ov.style.width = r.width + 'px';
  ov.style.height = r.height + 'px';
}
window.addEventListener('resize', fitOverlay);

async function api(path, opts) {
  const r = await fetch(path, opts);
  if (!r.ok) { const t = await r.text(); throw new Error(t || r.statusText); }
  return r.json();
}
const fmtTime = s => { s = Math.max(0, s); const m = Math.floor(s / 60), sec = Math.floor(s % 60); return m + ':' + String(sec).padStart(2, '0'); };

async function loadStatus() {
  try {
    const st = await api('/api/status');
    if (st.version) document.querySelector('header h1').textContent = 'BlurCut v' + st.version;
    const el = $('#status');
    const ff = st.ffmpegOk ? 'ffmpeg ✓' : 'ffmpeg ✗';
    const ts = st.tesseractOk ? 'tesseract ✓' : 'tesseract ✗ (manual boxes only)';
    el.textContent = ff + ' · ' + ts + (st.hasNvenc ? ' · NVENC' : '');
    el.className = 'status ' + (st.ffmpegOk ? 'ok' : 'bad');
    if (!st.ffmpegOk) $('#uploadMsg').textContent = 'ffmpeg not found — use Tools below to download it, or put ffmpeg.exe next to blurcut.exe and click Re-check tools.';
    if (!st.tesseractOk) $('#scanMsg').textContent = 'Auto-scan is off: tesseract not found. Use Tools below to download it, or draw manual boxes.';
  } catch (e) { $('#status').textContent = 'status failed: ' + e.message; }
}

async function loadTools() {
  try {
    const t = await api('/api/tools');
    const ul = $('#toolList'); ul.innerHTML = '';
    [['ffmpeg', t.ffmpeg], ['ffprobe', t.ffprobe], ['tesseract', t.tesseract]].forEach(([name, info]) => {
      const li = document.createElement('li');
      li.textContent = (info.found ? '✓ ' : '✗ ') + name + ': ' + (info.found ? info.path : 'not found');
      li.className = info.found ? 'ok' : 'bad';
      ul.appendChild(li);
    });
    if (t.tessdata) { const li = document.createElement('li'); li.textContent = '✓ tessdata: ' + t.tessdata; li.className = 'ok'; ul.appendChild(li); }
    $('#toolFfmpeg').value = (t.ffmpeg && t.ffmpeg.path) || '';
    $('#toolTesseract').value = (t.tesseract && t.tesseract.path) || '';
    const dl = (s) => s && s.state && s.state !== 'idle' ? s.tool + ': ' + s.state + ' — ' + s.message : '';
    const msgs = [dl(t.ffmpegDownload), dl(t.tesseractDownload)].filter(Boolean);
    if (msgs.length) $('#toolMsg').textContent = msgs.join(' | ');
  } catch (e) { $('#toolMsg').textContent = 'Tools check failed: ' + e.message; }
}

let toolPoll = null;
function pollToolsWhileBusy() {
  clearInterval(toolPoll);
  toolPoll = setInterval(async () => {
    await loadTools(); await loadStatus();
    const m = $('#toolMsg').textContent;
    if (!/downloading|extracting|installing/.test(m)) clearInterval(toolPoll);
  }, 3000);
}
$('#dlFfmpegBtn').onclick = async () => { try { await api('/api/tools/download-ffmpeg', { method: 'POST' }); $('#toolMsg').textContent = 'Downloading ffmpeg…'; pollToolsWhileBusy(); } catch (e) { $('#toolMsg').textContent = 'Download failed: ' + e.message; } };
$('#dlTessBtn').onclick = async () => { try { await api('/api/tools/download-tesseract', { method: 'POST' }); $('#toolMsg').textContent = 'Downloading Tesseract installer…'; pollToolsWhileBusy(); } catch (e) { $('#toolMsg').textContent = 'Download failed: ' + e.message; } };
$('#recheckBtn').onclick = async () => { await api('/api/tools/recheck', { method: 'POST' }); $('#toolMsg').textContent = 'Re-checked.'; loadTools(); loadStatus(); };
$('#saveTools').onclick = async () => {
  try {
    await api('/api/tools', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ ffmpegPath: $('#toolFfmpeg').value.trim(), tesseractPath: $('#toolTesseract').value.trim() }) });
    $('#toolSaveMsg').textContent = 'Saved — tools re-detected.'; loadTools(); loadStatus();
  } catch (e) { $('#toolSaveMsg').textContent = 'Save failed: ' + e.message; }
};

async function loadJobs() {
  const jobs = await api('/api/jobs');
  const ul = $('#jobList'); ul.innerHTML = '';
  jobs.forEach(j => {
    const li = document.createElement('li');
    li.innerHTML = '<span>' + esc(j.name) + '</span><span>' + j.status + ' · ' + (j.regions ? j.regions.length : 0) + ' regions</span>';
    li.onclick = () => selectJob(j.id);
    ul.appendChild(li);
  });
}
const esc = s => String(s || '').replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));

function videoSrc(id, previewConverted) {
  return '/video/' + id + (previewConverted ? '?preview=1' : '');
}

async function selectJob(id) {
  currentJob = await api('/api/jobs/' + id);
  setDrawMode(false);
  $('#previewCard').hidden = false;
  $('#jobInfo').textContent = currentJob.name + ' — ' + currentJob.width + '×' + currentJob.height + ', ' + fmtTime(currentJob.duration) + ' (' + currentJob.status + ')';
  const v = $('#video'); v.src = videoSrc(id, currentJob.previewConverted);
  fitOverlay(); // metadata may not be in yet — loadedmetadata re-fits it
  $('#previewNote').hidden = !currentJob.previewConverted;
  renderRegions();
  $('#downloadLink').hidden = !currentJob.hasExport;
  if (currentJob.hasExport) $('#downloadLink').href = '/download/' + id;
  startPoll();
}

function renderRegions() {
  const ov = $('#overlay'); ov.querySelectorAll('.box').forEach(e => e.remove());
  const tbody = document.querySelector('#regionTable tbody'); tbody.innerHTML = '';
  if (!currentJob) return;
  const v = $('#video');
  (currentJob.regions || []).forEach(r => {
    const tr = document.createElement('tr');
    tr.innerHTML = '<td><input type="checkbox" ' + (r.enabled ? 'checked' : '') + ' data-id="' + r.id + '" class="toggle"></td>' +
      '<td>' + esc(r.label) + '</td><td>' + esc(r.source || 'auto') + '</td>' +
      '<td title="' + fmtTime(r.start) + ' – ' + fmtTime(r.end) + '"><input type="number" step="0.1" min="0" value="' + r.start.toFixed(1) + '" data-id="' + r.id + '" class="tstart" style="width:4.5em">–<input type="number" step="0.1" min="0" value="' + r.end.toFixed(1) + '" data-id="' + r.id + '" class="tend" style="width:4.5em"></td>' +
      '<td title="' + esc(r.text) + '">' + esc(r.text || '') + '</td>' +
      '<td><button data-id="' + r.id + '" class="del">✕</button></td>';
    tbody.appendChild(tr);
    // overlay box, shown only while the video is in its time range
    const show = v.currentTime >= r.start && v.currentTime <= r.end;
    if (show) {
      const d = document.createElement('div');
      d.className = 'box' + (r.enabled ? '' : ' disabled');
      d.style.left = (r.rx * 100) + '%'; d.style.top = (r.ry * 100) + '%';
      d.style.width = (r.rw * 100) + '%'; d.style.height = (r.rh * 100) + '%';
      d.innerHTML = '<span class="tag">' + esc(r.label) + ' ' + fmtTime(r.start) + '–' + fmtTime(r.end) + '</span>';
      d.onclick = () => { v.currentTime = r.start; };
      ov.appendChild(d);
    }
  });
  tbody.querySelectorAll('.toggle').forEach(cb => cb.onchange = async () => {
    const r = currentJob.regions.find(x => x.id === cb.dataset.id);
    await api('/api/jobs/' + currentJob.id + '/regions/' + r.id, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ enabled: cb.checked }) });
    r.enabled = cb.checked; renderRegions();
  });
  // Start/end times are editable (seconds, one decimal). Saves on
  // change; an invalid range reverts to the saved values.
  tbody.querySelectorAll('.tstart,.tend').forEach(inp => inp.onchange = async () => {
    const r = currentJob.regions.find(x => x.id === inp.dataset.id);
    const row = inp.closest('tr');
    const start = parseFloat(row.querySelector('.tstart').value);
    const end = parseFloat(row.querySelector('.tend').value);
    if (isNaN(start) || isNaN(end) || end <= start) { renderRegions(); return; }
    if (start === r.start && end === r.end) return;
    try {
      const upd = await api('/api/jobs/' + currentJob.id + '/regions/' + r.id, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ start, end }) });
      r.start = upd.start; r.end = upd.end; renderRegions();
    } catch (e) { renderRegions(); }
  });
  tbody.querySelectorAll('.del').forEach(b => b.onclick = async () => {
    await api('/api/jobs/' + currentJob.id + '/regions/' + b.dataset.id, { method: 'DELETE' });
    currentJob.regions = currentJob.regions.filter(x => x.id !== b.dataset.id); renderRegions();
  });
}

function startPoll() {
  clearInterval(pollTimer);
  pollTimer = setInterval(async () => {
    if (!currentJob) return;
    const j = await api('/api/jobs/' + currentJob.id);
    // The converted preview may finish after the upload: switch the
    // video over to it (cache-busting query) and show the note.
    if (j.previewConverted && !currentJob.previewConverted) {
      const v = $('#video');
      v.src = videoSrc(j.id, true);
      fitOverlay();
      $('#previewNote').hidden = false;
    }
    currentJob = j;
    // Scanning message carries live frame counts ("frame 120/240 (50%)");
    // exporting shows the % from the ffmpeg time= progress.
    $('#progress').textContent = j.status === 'scanning' ? (j.message || 'Scanning…')
      : j.status === 'exporting' ? 'Exporting ' + Math.round(j.progress * 100) + '%'
      : (j.message || '');
    // Don't re-render the region table while the user is typing a
    // start/end time into it — the re-render would eat their input.
    const typingTime = document.activeElement && (document.activeElement.classList.contains('tstart') || document.activeElement.classList.contains('tend'));
    if (j.status === 'scanned' || j.status === 'done' || j.status === 'error') { if (!typingTime) renderRegions(); $('#jobInfo').textContent = j.name + ' — ' + j.width + '×' + j.height + ', ' + fmtTime(j.duration) + ' (' + j.status + ': ' + (j.message || '') + ')'; }
    if (j.hasExport) { $('#downloadLink').hidden = false; $('#downloadLink').href = '/download/' + j.id; }
    if (j.status === 'done' || j.status === 'error' || j.status === 'scanned') loadJobs();
  }, 1500);
}

// upload
async function upload(file) {
  $('#uploadMsg').textContent = 'Uploading ' + file.name + '…';
  const fd = new FormData(); fd.append('video', file);
  const r = await fetch('/api/jobs', { method: 'POST', body: fd });
  if (!r.ok) { $('#uploadMsg').textContent = 'Upload failed: ' + await r.text(); return; }
  const j = await r.json();
  $('#uploadMsg').textContent = 'Uploaded. Now press Auto-scan, or draw a manual box.';
  loadJobs(); selectJob(j.id);
}
$('#browseBtn').onclick = () => $('#fileInput').click();
$('#fileInput').onchange = e => { if (e.target.files[0]) upload(e.target.files[0]); };
const dz = $('#dropZone');
['dragover', 'dragenter'].forEach(ev => dz.addEventListener(ev, e => { e.preventDefault(); dz.classList.add('dragover'); }));
['dragleave', 'drop'].forEach(ev => dz.addEventListener(ev, e => { e.preventDefault(); dz.classList.remove('dragover'); }));
dz.addEventListener('drop', e => { const f = e.dataTransfer.files[0]; if (f) upload(f); });

// scan / export
$('#scanBtn').onclick = async () => {
  if (!currentJob) return;
  try { await api('/api/jobs/' + currentJob.id + '/scan', { method: 'POST' }); $('#scanMsg').textContent = 'Scanning… this takes a while for long videos.'; }
  catch (e) { $('#scanMsg').textContent = 'Scan failed: ' + e.message; }
};
// "What does the scan see here?" — OCR one frame at the video's
// current time and show the raw words, so you can check the scan can
// actually read your screen before trusting (or blaming) it.
$('#ocrPreviewBtn').onclick = async () => {
  if (!currentJob) return;
  const box = $('#ocrPreview'); box.hidden = false;
  box.textContent = 'Reading this frame…';
  try {
    const v = $('#video');
    const d = await api('/api/jobs/' + currentJob.id + '/ocr-preview', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ time: v.currentTime || 0 }) });
    if (!d.words || !d.words.length) { box.textContent = 'At ' + fmtTime(d.time) + ' the scan sees no readable text.'; return; }
    box.textContent = 'At ' + fmtTime(d.time) + ' the scan reads: ' + d.words.map(w => w.text + ' (' + w.conf + ')').join(' · ');
  } catch (e) { box.textContent = 'Could not read this frame: ' + e.message; }
};
$('#exportBtn').onclick = async () => {
  if (!currentJob) return;
  try { await api('/api/jobs/' + currentJob.id + '/export', { method: 'POST' }); $('#progress').textContent = 'Exporting…'; }
  catch (e) { $('#progress').textContent = 'Export failed: ' + e.message; }
};
$('#deleteJobBtn').onclick = async () => {
  if (!currentJob) return;
  if (!confirm('Delete this job and its files?')) return;
  try { await api('/api/jobs/' + currentJob.id, { method: 'DELETE' }); }
  catch (e) { $('#progress').textContent = 'Delete failed: ' + e.message; return; }
  clearInterval(pollTimer);
  currentJob = null;
  $('#previewCard').hidden = true;
  loadJobs();
};

// Fullscreen preview — the whole #videoWrap (video + overlay + boxes),
// so drawing keeps working while fullscreen. Entering/leaving
// fullscreen changes the element size, so the overlay is refit.
$('#fullscreenBtn').onclick = async () => {
  const wrap = $('#videoWrap');
  if (!wrap) return;
  try {
    if (document.fullscreenElement) await document.exitFullscreen();
    else await wrap.requestFullscreen();
  } catch (e) { /* fullscreen denied — stay as-is */ }
};
document.addEventListener('fullscreenchange', () => {
  const btn = $('#fullscreenBtn');
  if (btn) btn.textContent = document.fullscreenElement ? '⛶ Exit fullscreen' : '⛶ Fullscreen';
  fitOverlay();
});

// draw a manual box on the video (only while drawing mode is on)
$('#drawToggleBtn').onclick = () => setDrawMode(!drawMode);
(function () {
  const wrap = $('#videoWrap'), v = $('#video'), ov = $('#overlay');
  let start = null, box = null;
  ov.addEventListener('mousedown', e => {
    if (!drawMode) return;
    if (e.target !== ov) return;
    const r = ov.getBoundingClientRect();
    start = { x: (e.clientX - r.left) / r.width, y: (e.clientY - r.top) / r.height };
    box = document.createElement('div'); box.id = 'drawBox'; ov.appendChild(box);
  });
  ov.addEventListener('mousemove', e => {
    if (!start || !box) return;
    const r = ov.getBoundingClientRect();
    const x = (e.clientX - r.left) / r.width, y = (e.clientY - r.top) / r.height;
    box.style.left = Math.min(start.x, x) * 100 + '%'; box.style.top = Math.min(start.y, y) * 100 + '%';
    box.style.width = Math.abs(x - start.x) * 100 + '%'; box.style.height = Math.abs(y - start.y) * 100 + '%';
  });
  ov.addEventListener('mouseup', async e => {
    if (!start || !box || !currentJob) { start = null; if (box) box.remove(); box = null; return; }
    const r = ov.getBoundingClientRect();
    const x = (e.clientX - r.left) / r.width, y = (e.clientY - r.top) / r.height;
    const rx = Math.min(start.x, x), ry = Math.min(start.y, y), rw = Math.abs(x - start.x), rh = Math.abs(y - start.y);
    box.remove(); box = null; start = null;
    if (rw < 0.01 || rh < 0.01) return;
    const t = v.currentTime;
    const reg = await api('/api/jobs/' + currentJob.id + '/regions', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ rx, ry, rw, rh, start: t, end: t + 5 }) });
    currentJob.regions.push(reg); renderRegions();
    setDrawMode(false); // box drawn — back to playback mode so play works right away
  });
  v.addEventListener('timeupdate', renderRegions);
  // The picture's size inside the element is only known once the
  // metadata is in — re-fit the overlay then, and whenever the
  // source swaps (e.g. the converted preview replaces the original).
  v.addEventListener('loadedmetadata', fitOverlay);
  v.addEventListener('loadeddata', fitOverlay);
  // If the preview file itself will not play (odd codec, bad remux, …),
  // say so plainly and offer a reload — the file is still fine for
  // scan/export, which never use the preview.
  v.addEventListener('error', () => {
    const e = $('#videoError');
    e.textContent = 'This preview could not play in the app — the file itself is fine for scan/export. Try reloading the preview.';
    e.hidden = false;
    $('#reloadPreviewBtn').hidden = false;
  });
  v.addEventListener('loadstart', () => { $('#videoError').hidden = true; $('#reloadPreviewBtn').hidden = true; });
  $('#reloadPreviewBtn').onclick = () => { const vv = $('#video'); vv.load(); };
})();

// settings
$('#settingsBtn').onclick = () => { $('#settingsCard').hidden = !$('#settingsCard').hidden; loadSettings(); };
async function loadSettings() {
  const c = await api('/api/settings');
  $('#setInterval').value = String(c.scanInterval); $('#setBlur').value = c.blurStrength; $('#blurVal').textContent = c.blurStrength;
  $('#patUrl').checked = !!c.patterns.url; $('#patEmail').checked = !!c.patterns.email;
  $('#patPassword').checked = !!c.patterns.password; $('#patApikey').checked = !!c.patterns.apikey;
  $('#patCrypto').checked = !!c.patterns.crypto;
  $('#patSuspicious').checked = !!c.patterns.suspicious;
  $('#setKeywords').value = (c.keywords || []).join('\n');
}
$('#setBlur').oninput = e => $('#blurVal').textContent = e.target.value;
$('#saveSettings').onclick = async () => {
  const cfg = { port: 8080, scanInterval: parseFloat($('#setInterval').value), blurStrength: parseInt($('#setBlur').value, 10),
    keywords: $('#setKeywords').value.split('\n').map(s => s.trim()).filter(Boolean),
    patterns: { url: $('#patUrl').checked, email: $('#patEmail').checked, password: $('#patPassword').checked, apikey: $('#patApikey').checked, crypto: $('#patCrypto').checked, suspicious: $('#patSuspicious').checked },
    ffmpegPath: ($('#toolFfmpeg') ? $('#toolFfmpeg').value.trim() : ''), tesseractPath: ($('#toolTesseract') ? $('#toolTesseract').value.trim() : '') };
  await api('/api/settings', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(cfg) });
  $('#settingsMsg').textContent = 'Saved.';
};

loadStatus(); loadTools(); loadJobs();
