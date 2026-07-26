package dashboard

// pageTemplate is the single-page dashboard. Theming is CSS-variable driven:
// data-theme on <html> selects a palette, persisted in localStorage, defaulting
// to gruvbox-coyote (accent #B49B72 — coyote tan, never the orange-gold
// yellow). Album art hot-links Spotify's image CDN (captured during sync).
const pageTemplate = `<!doctype html>
<html lang="en" data-theme="gruvbox-coyote">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · spotifytool</title>
<script>
  // Apply the stored theme before first paint to avoid a flash.
  try {
    var t = localStorage.getItem('spotifytool-theme');
    if (t) document.documentElement.dataset.theme = t;
  } catch (e) {}
</script>
<style>
  :root, [data-theme="gruvbox-coyote"] {
    --bg:#282828; --bg1:#3c3836; --bg2:#504945; --fg:#ebdbb2; --fg-dim:#a89984;
    --accent:#B49B72; --accent-fg:#1d2021; --green:#b8bb26; --red:#fb4934;
  }
  [data-theme="gruvbox-classic"] {
    --bg:#282828; --bg1:#3c3836; --bg2:#504945; --fg:#ebdbb2; --fg-dim:#a89984;
    --accent:#83a598; --accent-fg:#1d2021; --green:#b8bb26; --red:#fb4934;
  }
  [data-theme="catppuccin-mocha"] {
    --bg:#1e1e2e; --bg1:#313244; --bg2:#45475a; --fg:#cdd6f4; --fg-dim:#a6adc8;
    --accent:#cba6f7; --accent-fg:#11111b; --green:#a6e3a1; --red:#f38ba8;
  }
  [data-theme="nord"] {
    --bg:#2e3440; --bg1:#3b4252; --bg2:#434c5e; --fg:#eceff4; --fg-dim:#d8dee9;
    --accent:#88c0d0; --accent-fg:#2e3440; --green:#a3be8c; --red:#bf616a;
  }
  [data-theme="dracula"] {
    --bg:#282a36; --bg1:#343746; --bg2:#44475a; --fg:#f8f8f2; --fg-dim:#a8a8b3;
    --accent:#bd93f9; --accent-fg:#1e1f29; --green:#50fa7b; --red:#ff5555;
  }
  [data-theme="tokyo-night"] {
    --bg:#1a1b26; --bg1:#24283b; --bg2:#414868; --fg:#c0caf5; --fg-dim:#a9b1d6;
    --accent:#7aa2f7; --accent-fg:#15161e; --green:#9ece6a; --red:#f7768e;
  }
  [data-theme="catppuccin-latte"] {
    --bg:#eff1f5; --bg1:#e6e9ef; --bg2:#ccd0da; --fg:#4c4f69; --fg-dim:#6c6f85;
    --accent:#8839ef; --accent-fg:#eff1f5; --green:#40a02b; --red:#d20f39;
  }

  * { box-sizing: border-box; }
  html { scrollbar-color: var(--bg2) var(--bg); }
  body { margin: 0; background: var(--bg); color: var(--fg);
         font: 15px/1.55 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Inter", sans-serif; }
  a, a:visited { color: var(--accent); }
  a:hover { color: var(--fg); }

  header { position: sticky; top: 0; z-index: 10; display: flex; align-items: center;
           gap: 1rem; padding: .65rem 1.25rem;
           background: color-mix(in srgb, var(--bg) 82%, transparent);
           backdrop-filter: blur(10px); -webkit-backdrop-filter: blur(10px);
           border-bottom: 1px solid var(--bg2); }
  .brand { display: flex; align-items: center; gap: .55rem; margin-right: .5rem; }
  .brand .dot { width: 10px; height: 10px; border-radius: 50%; background: var(--accent);
                box-shadow: 0 0 10px var(--accent); }
  .brand h1 { margin: 0; font-size: 1.05rem; letter-spacing: .04em; color: var(--fg); }
  nav { display: flex; gap: .4rem; flex-wrap: wrap; }
  .nav-btn { display: inline-block; padding: .38rem .95rem; border-radius: 999px;
             background: var(--bg1); color: var(--fg-dim) !important;
             text-decoration: none; font-size: .85rem; font-weight: 500;
             border: 1px solid transparent; transition: all .15s ease; }
  .nav-btn:hover { color: var(--fg) !important; border-color: var(--bg2); transform: translateY(-1px); }
  .nav-btn.active { background: var(--accent); color: var(--accent-fg) !important; font-weight: 600; }
  .spacer { flex: 1; }
  .theme-pick { display: flex; align-items: center; gap: .45rem; color: var(--fg-dim); font-size: .78rem; }
  select { background: var(--bg1); color: var(--fg); border: 1px solid var(--bg2);
           border-radius: 8px; padding: .32rem .5rem; font-size: .82rem; cursor: pointer; }

  main { max-width: 1020px; margin: 0 auto; padding: 1.4rem 1.25rem 3rem; }
  main.wide { max-width: none; }

  .cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(140px, 1fr)); gap: .8rem; }
  .card { background: var(--bg1); border: 1px solid var(--bg2); border-radius: 14px;
          padding: .95rem 1rem; transition: transform .15s ease; }
  .card:hover { transform: translateY(-2px); }
  .card .n { font-size: 1.65rem; color: var(--accent); font-weight: 650; line-height: 1.2; }
  .card .l { color: var(--fg-dim); font-size: .72rem; text-transform: uppercase; letter-spacing: .08em; margin-top: .15rem; }

  section { margin-top: 2.4rem; }
  section h2 { font-size: .95rem; color: var(--fg); text-transform: uppercase;
               letter-spacing: .09em; font-weight: 700; margin-bottom: .9rem;
               display: flex; align-items: center; gap: .6rem; }
  section h2::before { content: ""; width: 4px; height: 1.1em; border-radius: 2px;
                       background: var(--accent); flex: none; }
  section h2 .sub { text-transform: none; letter-spacing: 0; font-weight: 400;
                    font-size: .78rem; color: var(--fg-dim); }

  table { width: 100%; border-collapse: collapse; background: var(--bg1);
          border: 1px solid var(--bg2); border-radius: 12px; overflow: hidden; }
  td, th { text-align: left; padding: .5rem .8rem; border-bottom: 1px solid var(--bg2); }
  tr:last-child td { border-bottom: 0; }
  th { color: var(--fg-dim); font-weight: 500; font-size: .75rem; text-transform: uppercase; letter-spacing: .06em; }

  .bar-row { display: flex; align-items: center; gap: .6rem; margin: .22rem 0; }
  .bar-row .lbl { flex: 0 0 84px; color: var(--fg-dim); font-size: .72rem; font-variant-numeric: tabular-nums; }
  /* Fixed track + inner fill: every bar shares the same baseline and scale. */
  .bar-row .track { flex: 1; height: 12px; border-radius: 6px; background: var(--bg1);
                    border: 1px solid var(--bg2); overflow: hidden; }
  .bar-row .track i { display: block; height: 100%; border-radius: 6px;
         background: linear-gradient(90deg, color-mix(in srgb, var(--accent) 65%, var(--bg)) 0%, var(--accent) 100%); }
  .bar-row .val { flex: 0 0 2rem; color: var(--fg-dim); font-size: .72rem; text-align: right; }

  /* Signals: valence-colored stat chips + a diverging balance bar. */
  .sig-chips { display: grid; grid-template-columns: repeat(auto-fit, minmax(148px, 1fr)); gap: .7rem; }
  .chip { background: var(--bg1); border: 1px solid var(--bg2); border-radius: 12px;
          padding: .7rem .85rem; display: flex; align-items: center; gap: .65rem; }
  .chip .ico { font-size: 1.05rem; width: 1.5rem; text-align: center; }
  .chip .n { font-size: 1.35rem; font-weight: 650; line-height: 1.1; }
  .chip .cl { color: var(--fg-dim); font-size: .68rem; text-transform: uppercase; letter-spacing: .06em; }
  .chip.pos .n, .chip.pos .ico { color: var(--green); }
  .chip.neg .n, .chip.neg .ico { color: var(--red); }
  .chip.zero .n, .chip.zero .ico { color: var(--fg-dim); }
  .balance { display: flex; height: 14px; border-radius: 7px; overflow: hidden; margin-top: .8rem;
             background: var(--bg1); border: 1px solid var(--bg2); }
  .balance .neg { background: var(--red); height: 100%; }
  .balance .pos { background: var(--green); height: 100%; }
  .balance-cap { color: var(--fg-dim); font-size: .74rem; margin-top: .35rem; }
  button.mini { margin: 0; padding: .32rem .9rem; font-size: .78rem; font-weight: 600;
                text-transform: none; letter-spacing: 0; }
  section h2 .spacer { flex: 1; }

  .covers { display: grid; grid-template-columns: repeat(auto-fill, minmax(104px, 1fr)); gap: .8rem; }
  .cover { text-decoration: none; }
  .cover img, .cover .ph { width: 100%; aspect-ratio: 1; border-radius: 10px; object-fit: cover;
               border: 1px solid var(--bg2); display: block;
               transition: transform .15s ease; background: var(--bg1); }
  .cover:hover img { transform: scale(1.04); }
  .cover .ph { display: flex; align-items: center; justify-content: center; color: var(--fg-dim); font-size: 1.4rem; }
  .cover .t { font-size: .72rem; color: var(--fg); margin-top: .35rem; white-space: nowrap;
              overflow: hidden; text-overflow: ellipsis; }
  .cover .a { font-size: .68rem; color: var(--fg-dim); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .votes { display: flex; gap: .35rem; margin-top: .3rem; }
  .vote { flex: 1; padding: .18rem 0; border-radius: 7px; border: 1px solid var(--bg2);
          background: var(--bg1); color: var(--fg-dim); font-size: .78rem; cursor: pointer;
          margin: 0; transition: all .12s ease; }
  .vote:hover { color: var(--fg); transform: none; }
  .vote.on-keep { background: var(--green); color: var(--accent-fg); border-color: transparent; }
  .vote.on-nope { background: var(--red); color: #fff; border-color: transparent; }

  .artists { display: flex; flex-direction: column; gap: .45rem; }
  .artist-row { display: flex; align-items: center; gap: .8rem; background: var(--bg1);
                border: 1px solid var(--bg2); border-radius: 12px; padding: .45rem .7rem; }
  .artist-row img, .artist-row .ph { width: 42px; height: 42px; border-radius: 50%; object-fit: cover;
                     background: var(--bg2); }
  .artist-row .ph { display: flex; align-items: center; justify-content: center; color: var(--fg-dim); }
  .artist-row .name { flex: 0 0 180px; font-size: .88rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .artist-row .depth { flex: 1; height: 8px; border-radius: 4px; background: var(--bg2); overflow: hidden; }
  .artist-row .depth i { display: block; height: 100%; background: var(--accent); border-radius: 4px; }
  .artist-row .cnt { color: var(--fg-dim); font-size: .78rem; width: 2.2rem; text-align: right; }

  .muted { color: var(--fg-dim); }
  .pill { display: inline-block; background: var(--bg1); border: 1px solid var(--bg2);
          border-radius: 999px; padding: .1rem .6rem; font-size: .74rem; color: var(--fg-dim); }
  textarea { width: 100%; min-height: 60vh; background: var(--bg1); color: var(--fg);
             border: 1px solid var(--bg2); border-radius: 12px; padding: 1rem;
             font: 13px/1.6 ui-monospace, SFMono-Regular, Menlo, monospace; }
  button { background: var(--accent); color: var(--accent-fg); border: 0; border-radius: 10px;
           padding: .55rem 1.2rem; font-weight: 600; cursor: pointer; margin-top: .75rem;
           transition: transform .12s ease; }
  button:hover { transform: translateY(-1px); }
  .saved { color: var(--green); margin-left: 1rem; }
  .term iframe { width: 100%; height: calc(100vh - 170px); border: 1px solid var(--bg2);
                 border-radius: 12px; background: #14151a; }
  code { background: var(--bg1); border: 1px solid var(--bg2); border-radius: 6px;
         padding: .08rem .38rem; font-size: .85em; color: var(--accent); }
</style>
</head>
<body>
<header>
  <div class="brand"><span class="dot"></span><h1>spotifytool</h1></div>
  <nav>
    <a class="nav-btn {{if eq .Page "dashboard"}}active{{end}}" href="/">Dashboard</a>
    <a class="nav-btn {{if eq .Page "profile"}}active{{end}}" href="/profile">Taste Profile</a>
    <a class="nav-btn {{if eq .Page "terminal"}}active{{end}}" href="/terminal">Terminal</a>
    {{if .TerminalURL}}<a class="nav-btn" href="{{.TerminalURL}}" target="_blank" rel="noopener">Session ↗</a>{{end}}
  </nav>
  <div class="spacer"></div>
  <label class="theme-pick">theme
    <select id="theme">
      <option value="gruvbox-coyote">Gruvbox Coyote</option>
      <option value="gruvbox-classic">Gruvbox Classic</option>
      <option value="catppuccin-mocha">Catppuccin Mocha</option>
      <option value="nord">Nord</option>
      <option value="dracula">Dracula</option>
      <option value="tokyo-night">Tokyo Night</option>
      <option value="catppuccin-latte">Catppuccin Latte</option>
    </select>
  </label>
</header>
<main {{if eq .Page "terminal"}}class="wide"{{end}}>
{{if eq .Page "terminal"}}
  {{if .TerminalURL}}
    <section class="term">
      <h2>claude session
        <span class="muted sub">
          same conversation as <code>zellij attach</code> over Tailscale SSH and the mobile app ·
          <a href="{{.TerminalURL}}" target="_blank" rel="noopener">open full-screen ↗</a>
        </span>
      </h2>
      <iframe src="{{.TerminalURL}}" title="Zellij web client (claude session)"
              allow="clipboard-read; clipboard-write"></iframe>
      <p class="muted">Auth is handled by the stack (token login injected by the proxy); if the
         frame errors, check that the sandbox container is up.</p>
    </section>
  {{else}}
    <section>
      <h2>terminal not configured</h2>
      <p class="muted">Set <code>SPOTIFYTOOL_TERMINAL_URL</code> to the terminal proxy address and
         restart <code>spotifytool serve</code>. The compose file wires this from <code>.env</code>.</p>
    </section>
  {{end}}
{{else if eq .Page "profile"}}
  <section>
    <h2>taste-profile.md {{if .Saved}}<span class="saved">saved ✓</span>{{end}}</h2>
    <p class="muted">Your edits are ground truth. Loaded at the start of curation sessions and batches.</p>
    <form method="post" action="/profile">
      <textarea name="content" spellcheck="false">{{.Profile}}</textarea>
      <div><button type="submit">Save profile</button></div>
    </form>
  </section>
{{else}}
  <div class="cards">
    <div class="card"><div class="n">{{.Stats.SavedTracks}}</div><div class="l">liked songs</div></div>
    <div class="card"><div class="n">{{.Stats.Playlists}}</div><div class="l">playlists</div></div>
    <div class="card"><div class="n">{{.Stats.Artists}}</div><div class="l">artists</div></div>
    <div class="card"><div class="n">{{.Stats.PlayEvents}}</div><div class="l">plays logged</div></div>
    <div class="card"><div class="n">{{.Stats.Keepers}}</div><div class="l">keepers</div></div>
    <div class="card"><div class="n">{{.Stats.Disliked}}</div><div class="l">disliked</div></div>
  </div>
  <p class="muted" style="margin-top:.9rem">
    last sync <span class="pill">{{if .Stats.LastSync}}{{.Stats.LastSync}}{{else}}never{{end}}</span>
    last batch <span class="pill">{{if .Stats.LastBatch}}{{.Stats.LastBatch}}{{else}}never{{end}}</span>
    <span class="pill">{{.Stats.RecentAdds30d}} saves / 30d</span>
  </p>

  {{if .BatchCovers}}
  <section>
    <h2>latest batch {{if .BatchLabel}}<span class="muted sub">{{.BatchLabel}} · tap to vote: ✓ keeper, ✗ dislike</span>{{end}}</h2>
    <div class="covers">
      {{range .BatchCovers}}
        <div class="cover" data-track="{{.ID}}">
          {{if .ImageURL}}<img src="{{.ImageURL}}" alt="" loading="lazy">{{else}}<div class="ph">♪</div>{{end}}
          <div class="t">{{.Title}}</div><div class="a">{{.Artist}}</div>
          <div class="votes">
            <button class="vote keep {{if .Keeper}}on-keep{{end}}" title="Keeper">✓</button>
            <button class="vote nope {{if .Disliked}}on-nope{{end}}" title="Dislike">✗</button>
          </div>
        </div>
      {{end}}
    </div>
  </section>
  {{end}}

  {{if .RecentSaves}}
  <section>
    <h2>recently liked <span class="muted sub">tap to vote</span></h2>
    <div class="covers">
      {{range .RecentSaves}}
        <div class="cover" data-track="{{.ID}}">
          {{if .ImageURL}}<img src="{{.ImageURL}}" alt="" loading="lazy">{{else}}<div class="ph">♪</div>{{end}}
          <div class="t">{{.Title}}</div><div class="a">{{.Artist}}</div>
          <div class="votes">
            <button class="vote keep {{if .Keeper}}on-keep{{end}}" title="Keeper">✓</button>
            <button class="vote nope {{if .Disliked}}on-nope{{end}}" title="Dislike">✗</button>
          </div>
        </div>
      {{end}}
    </div>
  </section>
  {{end}}

  <section>
    <h2>plays <span class="muted sub">last 30 days</span></h2>
    {{if .PlayHistory}}
      {{range .PlayHistory}}
        <div class="bar-row">
          <span class="lbl">{{.Label}}</span>
          <div class="track"><i style="width:{{.Pct}}%"></i></div>
          <span class="val">{{.Count}}</span>
        </div>
      {{end}}
    {{else}}<p class="muted">No play history yet; accumulates hourly once sync runs.</p>{{end}}
  </section>

  <section>
    <h2>recent signals <span class="muted sub">since {{.Signals.Since}}</span></h2>
    <div class="sig-chips">
      <div class="chip {{if .Signals.NewSaves}}pos{{else}}zero{{end}}"><span class="ico">♥</span>
        <span><span class="n">{{len .Signals.NewSaves}}</span><br><span class="cl">new saves</span></span></div>
      <div class="chip {{if .Signals.Repeats}}pos{{else}}zero{{end}}"><span class="ico">↻</span>
        <span><span class="n">{{len .Signals.Repeats}}</span><br><span class="cl">repeats</span></span></div>
      <div class="chip {{if .Signals.NewKeepers}}pos{{else}}zero{{end}}"><span class="ico">✓</span>
        <span><span class="n">{{len .Signals.NewKeepers}}</span><br><span class="cl">new keepers</span></span></div>
      <div class="chip {{if .Signals.NewDislikes}}neg{{else}}zero{{end}}"><span class="ico">✗</span>
        <span><span class="n">{{len .Signals.NewDislikes}}</span><br><span class="cl">new dislikes</span></span></div>
      <div class="chip {{if .Signals.NewSkips}}neg{{else}}zero{{end}}"><span class="ico">⏭</span>
        <span><span class="n">{{len .Signals.NewSkips}}</span><br><span class="cl">early skips</span></span></div>
      <div class="chip {{if .Signals.IgnoredFromLastBatch}}neg{{else}}zero{{end}}"><span class="ico">∅</span>
        <span><span class="n">{{len .Signals.IgnoredFromLastBatch}}</span><br><span class="cl">ignored</span></span></div>
    </div>
    {{if or .SigPos .SigNeg}}
    <div class="balance">
      <div class="neg" style="width:{{.SigNegPct}}%"></div>
      <div class="pos" style="width:{{.SigPosPct}}%"></div>
    </div>
    <div class="balance-cap">{{.SigPos}} positive · {{.SigNeg}} negative since the last batch</div>
    {{end}}
  </section>

  {{if .TopArtists}}
  <section>
    <h2>top artists <span class="muted sub">by liked-track depth</span></h2>
    <div class="artists">
      {{$max := 1}}{{range .TopArtists}}{{if gt .Count $max}}{{$max = .Count}}{{end}}{{end}}
      {{range .TopArtists}}
        <div class="artist-row">
          {{if .ImageURL}}<img src="{{.ImageURL}}" alt="" loading="lazy">{{else}}<div class="ph">♪</div>{{end}}
          <span class="name">{{.Name}}</span>
          <div class="depth"><i style="width:{{pct .Count $max}}%"></i></div>
          <span class="cnt">{{.Count}}</span>
        </div>
      {{end}}
    </div>
  </section>
  {{end}}

  <section>
    <h2>discovery batches
      <span class="spacer"></span>
      <button id="run-batch" class="mini">Run discovery batch</button>
    </h2>
    <p class="muted" id="batch-status" style="display:none"></p>
    {{if .Batches}}
    <table>
      <tr><th>label</th><th>when</th><th>tracks</th><th>digest</th></tr>
      {{range .Batches}}<tr>
        <td>{{.Label}}</td><td class="muted">{{.CreatedAt}}</td>
        <td>{{.TrackCount}}</td><td class="muted">{{.Digest}}</td>
      </tr>{{end}}
    </table>
    {{else}}<p class="muted">No batches shipped yet.</p>{{end}}
  </section>
{{end}}
</main>
<script>
  (function () {
    var sel = document.getElementById('theme');
    if (sel) {
      try { sel.value = localStorage.getItem('spotifytool-theme') || 'gruvbox-coyote'; } catch (e) {}
      sel.addEventListener('change', function () {
        document.documentElement.dataset.theme = sel.value;
        try { localStorage.setItem('spotifytool-theme', sel.value); } catch (e) {}
      });
    }
    // Voting: canonical write goes to the real Keepers/Disliked playlists.
    document.addEventListener('click', function (ev) {
      var btn = ev.target.closest('.vote');
      if (!btn) return;
      var tile = btn.closest('.cover');
      if (!tile || !tile.dataset.track) return;
      var action = btn.classList.contains('keep') ? 'keeper' : 'dislike';
      btn.disabled = true;
      fetch('/vote', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ uri: 'spotify:track:' + tile.dataset.track, action: action })
      }).then(function (r) { return r.json(); }).then(function (res) {
        btn.disabled = false;
        if (!res.ok) { alert(res.error || 'vote failed'); return; }
        var keep = tile.querySelector('.vote.keep'), nope = tile.querySelector('.vote.nope');
        keep.classList.toggle('on-keep', action === 'keeper');
        nope.classList.toggle('on-nope', action === 'dislike');
      }).catch(function () { btn.disabled = false; alert('vote failed'); });
    });
    // Manual discovery batch: types /discovery-batch into the live claude
    // session (subscription-billed, same as doing it yourself).
    var rb = document.getElementById('run-batch');
    if (rb) rb.addEventListener('click', function () {
      if (!confirm('Send /discovery-batch to the claude session?')) return;
      rb.disabled = true;
      var st = document.getElementById('batch-status');
      fetch('/batch', { method: 'POST' }).then(function (r) { return r.json(); }).then(function (res) {
        rb.disabled = false;
        st.style.display = '';
        st.textContent = res.ok
          ? 'Command sent to the claude session — watch it run in the Terminal tab.'
          : 'Failed: ' + (res.error || 'unknown');
      }).catch(function () { rb.disabled = false; st.style.display = ''; st.textContent = 'Failed: trigger unreachable'; });
    });
  })();
</script>
</body>
</html>`
