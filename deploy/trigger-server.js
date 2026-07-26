// Tiny command-injection endpoint for the sandbox container. The dashboard's
// "Run discovery batch" button POSTs here; we type the slash command into the
// live zellij claude session, exactly as if the user had typed it. That keeps
// the run interactive and subscription-billed (never API/SDK).
//
// Compose-network only (never published to the LAN); the command is
// allowlisted, not free-form.
//
// HARD-WON zellij 0.43 facts (all verified by reproduction):
//   1. `zellij action ...` exits 0 but silently does NOTHING when the session
//      has no attached client — so we attach our own short-lived pty client
//      (via `script`) around the injection.
//   2. write-chars types into the FOCUSED pane; on fresh sessions that is the
//      release-notes plugin popup, which swallows the input. Config now sets
//      show_release_notes=false; we still close any focused plugin pane.
//   3. Exit codes prove nothing. Delivery is verified by dump-screen showing
//      the typed text BEFORE we press Enter; otherwise we report failure.
'use strict';
const http = require('http');
const fs = require('fs');
const { execFile, spawn } = require('child_process');

const PORT = process.env.TRIGGER_PORT || 8090;
const SESSION = process.env.ZELLIJ_SESSION || 'claude';
const ALLOWED = new Set(['/discovery-batch']);

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function zellij(args) {
  return new Promise((resolve, reject) => {
    execFile('zellij', ['--session', SESSION, 'action'].concat(args),
      { timeout: 10000 }, (err, stdout) => err ? reject(err) : resolve(stdout));
  });
}

// Attach a headless client so actions actually apply. `script` allocates the
// pty; stdin is held open (EOF would detach the client). Caller must kill().
function attachTempClient() {
  const child = spawn('script',
    ['-qc', 'stty rows 50 cols 160 2>/dev/null; exec zellij attach ' + SESSION, '/dev/null'],
    { stdio: ['pipe', 'ignore', 'ignore'] });
  child.on('error', () => { /* surfaced via verification failing */ });
  return child;
}

// True if any attached client currently focuses a plugin pane (e.g. a popup).
async function pluginFocused() {
  const out = await zellij(['list-clients']).catch(() => '');
  return out.split('\n').slice(1).some((l) => (l.trim().split(/\s+/)[1] || '').startsWith('plugin_'));
}

// dump-screen with retries; resolves the screen text ('' if zellij never
// wrote the file — which is what happens when a plugin pane holds focus).
async function dumpScreen() {
  const path = '/tmp/trigger-verify-' + process.pid + '.txt';
  for (let i = 0; i < 3; i++) {
    try { fs.unlinkSync(path); } catch (e) { /* absent is fine */ }
    await zellij(['dump-screen', path]).catch(() => {});
    await sleep(700);
    try { return fs.readFileSync(path, 'utf8'); } catch (e) { /* retry */ }
  }
  return '';
}

async function inject(cmd) {
  const client = attachTempClient();
  try {
    await sleep(1500); // client registration
    // Dismiss focus-stealing plugin popups (max 3, e.g. release notes).
    for (let i = 0; i < 3 && await pluginFocused(); i++) {
      await zellij(['close-pane']);
      await sleep(300);
    }
    await zellij(['write-chars', cmd]);
    await sleep(500);
    // Prove the text reached a terminal pane before submitting.
    if (!(await dumpScreen()).includes(cmd)) {
      throw new Error('typed text never appeared on screen (pane not accepting input?)');
    }
    await zellij(['write', '13']); // carriage return: submit
    return true;
  } finally {
    client.kill();
  }
}

http.createServer((req, res) => {
  const reply = (code, obj) => {
    res.writeHead(code, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify(obj));
  };
  if (req.method !== 'POST' || req.url !== '/run') return reply(405, { ok: false, error: 'POST /run only' });
  let body = '';
  req.on('data', (c) => { body += c; if (body.length > 4096) req.destroy(); });
  req.on('end', async () => {
    let cmd;
    try { cmd = JSON.parse(body).command; } catch (e) { /* fallthrough */ }
    if (!ALLOWED.has(cmd)) return reply(400, { ok: false, error: 'command not allowed' });
    try {
      await inject(cmd);
      reply(200, { ok: true, message: 'sent and verified ' + cmd + ' in session ' + SESSION });
    } catch (err) {
      reply(500, { ok: false, error: 'zellij injection failed: ' + err.message });
    }
  });
}).listen(PORT, '0.0.0.0', () => console.error('trigger server on :' + PORT));
