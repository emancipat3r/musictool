// Tiny command-injection endpoint for the sandbox container. The dashboard's
// "Run discovery batch" button POSTs here; we type the slash command into the
// live zellij claude session, exactly as if the user had typed it. That keeps
// the run interactive and subscription-billed (never API/SDK).
//
// Compose-network only (never published to the LAN); the command is
// allowlisted, not free-form.
'use strict';
const http = require('http');
const { execFile } = require('child_process');

const PORT = process.env.TRIGGER_PORT || 8090;
const SESSION = process.env.ZELLIJ_SESSION || 'claude';
const ALLOWED = new Set(['/discovery-batch']);

function zellij(args, cb) {
  execFile('zellij', ['--session', SESSION, 'action'].concat(args), { timeout: 10000 }, cb);
}

http.createServer((req, res) => {
  const reply = (code, obj) => {
    res.writeHead(code, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify(obj));
  };
  if (req.method !== 'POST' || req.url !== '/run') return reply(405, { ok: false, error: 'POST /run only' });
  let body = '';
  req.on('data', (c) => { body += c; if (body.length > 4096) req.destroy(); });
  req.on('end', () => {
    let cmd;
    try { cmd = JSON.parse(body).command; } catch (e) { /* fallthrough */ }
    if (!ALLOWED.has(cmd)) return reply(400, { ok: false, error: 'command not allowed' });
    zellij(['write-chars', cmd], (err) => {
      if (err) return reply(500, { ok: false, error: 'zellij write failed: ' + err.message });
      // 13 = carriage return: submit the command.
      zellij(['write', '13'], (err2) => {
        if (err2) return reply(500, { ok: false, error: 'zellij enter failed: ' + err2.message });
        reply(200, { ok: true, message: 'sent ' + cmd + ' to session ' + SESSION });
      });
    });
  });
}).listen(PORT, '0.0.0.0', () => console.error('trigger server on :' + PORT));
