package dashboard

// pageTemplate is the single gruvbox-dark page. Accent is coyote tan (#B49B72),
// never the orange-gold yellow.
const pageTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} — spotifytool</title>
<style>
  :root {
    --bg: #282828; --bg1: #3c3836; --bg2: #504945;
    --fg: #ebdbb2; --fg-dim: #a89984;
    --accent: #B49B72; /* coyote tan */
    --green: #b8bb26; --red: #fb4934;
  }
  * { box-sizing: border-box; }
  body { margin: 0; background: var(--bg); color: var(--fg);
         font: 15px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
  header { border-bottom: 2px solid var(--accent); padding: 1rem 1.5rem;
           display: flex; align-items: center; gap: 1.5rem; }
  header h1 { margin: 0; font-size: 1.2rem; color: var(--accent); letter-spacing: .04em; }
  header a { color: var(--fg-dim); text-decoration: none; }
  header a:hover, header a.active { color: var(--accent); }
  main { max-width: 960px; margin: 0 auto; padding: 1.5rem; }
  .cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(140px, 1fr)); gap: .75rem; }
  .card { background: var(--bg1); border-radius: 8px; padding: 1rem; }
  .card .n { font-size: 1.7rem; color: var(--accent); font-weight: 600; }
  .card .l { color: var(--fg-dim); font-size: .8rem; text-transform: uppercase; letter-spacing: .05em; }
  section { margin-top: 2rem; }
  section h2 { font-size: 1rem; color: var(--fg-dim); text-transform: uppercase;
               letter-spacing: .06em; border-bottom: 1px solid var(--bg2); padding-bottom: .4rem; }
  table { width: 100%; border-collapse: collapse; }
  td, th { text-align: left; padding: .35rem .5rem; border-bottom: 1px solid var(--bg1); }
  th { color: var(--fg-dim); font-weight: 500; font-size: .8rem; }
  .bar-row { display: flex; align-items: center; gap: .6rem; margin: .15rem 0; }
  .bar-row .lbl { width: 90px; color: var(--fg-dim); font-size: .75rem; font-variant-numeric: tabular-nums; }
  .bar { height: 14px; background: var(--accent); border-radius: 3px; min-width: 2px; }
  .bar-row .val { color: var(--fg-dim); font-size: .75rem; }
  .muted { color: var(--fg-dim); }
  textarea { width: 100%; min-height: 60vh; background: var(--bg1); color: var(--fg);
             border: 1px solid var(--bg2); border-radius: 8px; padding: 1rem;
             font: 13px/1.6 ui-monospace, SFMono-Regular, Menlo, monospace; }
  button { background: var(--accent); color: #1d2021; border: 0; border-radius: 6px;
           padding: .5rem 1.1rem; font-weight: 600; cursor: pointer; margin-top: .75rem; }
  .saved { color: var(--green); margin-left: 1rem; }
  .pill { display:inline-block; background: var(--bg2); border-radius: 999px;
          padding: .05rem .5rem; font-size: .75rem; color: var(--fg-dim); }
  main.wide { max-width: none; }
  main a { color: var(--accent); }
  .term iframe { width: 100%; height: calc(100vh - 180px); border: 1px solid var(--bg2);
                 border-radius: 8px; background: #1d2021; }
  code { background: var(--bg1); border-radius: 4px; padding: .1rem .35rem;
         font-size: .85em; color: var(--accent); }
</style>
</head>
<body>
<header>
  <h1>spotifytool</h1>
  <nav>
    <a href="/" {{if eq .Page "dashboard"}}class="active"{{end}}>dashboard</a>
    &nbsp;·&nbsp;
    <a href="/profile" {{if eq .Page "profile"}}class="active"{{end}}>taste profile</a>
    &nbsp;·&nbsp;
    <a href="/terminal" {{if eq .Page "terminal"}}class="active"{{end}}>terminal</a>
    {{if .TerminalURL}}&nbsp;·&nbsp;<a href="{{.TerminalURL}}" target="_blank" rel="noopener">open session ↗</a>{{end}}
  </nav>
</header>
<main {{if eq .Page "terminal"}}class="wide"{{end}}>
{{if eq .Page "terminal"}}
  {{if .TerminalURL}}
    <section class="term">
      <h2>claude session
        <span class="muted" style="font-size:.8rem;text-transform:none">
          same conversation as <code>zellij attach</code> over Tailscale SSH and the mobile app —
          <a href="{{.TerminalURL}}" target="_blank" rel="noopener">open full-screen ↗</a>
        </span>
      </h2>
      <iframe src="{{.TerminalURL}}" title="Zellij web client (claude session)"
              allow="clipboard-read; clipboard-write"></iframe>
      <p class="muted">Blank frame? The Zellij web client may refuse embedding or need its login
         token first — use the full-screen link once, then reload this page.</p>
    </section>
  {{else}}
    <section>
      <h2>terminal not configured</h2>
      <p class="muted">Set <code>SPOTIFYTOOL_TERMINAL_URL</code> to the Zellij web client address
         (e.g. <code>https://homelab.your-tailnet.ts.net:8082</code>) and restart
         <code>spotifytool serve</code>. The compose file wires this from <code>.env</code>.</p>
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
    <div class="card"><div class="n">{{.Stats.RecentAdds30d}}</div><div class="l">saves / 30d</div></div>
  </div>
  <p class="muted" style="margin-top:.8rem">
    last sync: <span class="pill">{{if .Stats.LastSync}}{{.Stats.LastSync}}{{else}}never{{end}}</span>
    last batch: <span class="pill">{{if .Stats.LastBatch}}{{.Stats.LastBatch}}{{else}}never{{end}}</span>
  </p>

  <section>
    <h2>plays — last 30 days</h2>
    {{if .PlayHistory}}
      {{range .PlayHistory}}
        <div class="bar-row">
          <span class="lbl">{{.Label}}</span>
          <div class="bar" style="width:{{.Pct}}%"></div>
          <span class="val">{{.Count}}</span>
        </div>
      {{end}}
    {{else}}<p class="muted">No play history yet — accumulates hourly once sync runs.</p>{{end}}
  </section>

  <section>
    <h2>recent signals <span class="muted" style="font-size:.8rem;text-transform:none">{{.Signals.Summary}}</span></h2>
    <table>
      <tr><th>new saves</th><td>{{len .Signals.NewSaves}}</td></tr>
      <tr><th>repeats</th><td>{{len .Signals.Repeats}}</td></tr>
      <tr><th>new keepers</th><td>{{len .Signals.NewKeepers}}</td></tr>
      <tr><th>ignored from last batch</th><td>{{len .Signals.IgnoredFromLastBatch}}</td></tr>
    </table>
  </section>

  <section>
    <h2>top artists</h2>
    {{if .Stats.TopArtists}}
    <table>
      <tr><th>artist</th><th>liked tracks</th></tr>
      {{range .Stats.TopArtists}}<tr><td>{{.Name}}</td><td>{{.Count}}</td></tr>{{end}}
    </table>
    {{else}}<p class="muted">No data yet.</p>{{end}}
  </section>

  <section>
    <h2>discovery batches</h2>
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
</body>
</html>`
