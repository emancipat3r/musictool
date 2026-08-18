package store

// schemaSQL is the full DDL, applied idempotently on Open. Only musictool
// writes here (Claude via MCP tools, the dashboard via this same package).
const schemaSQL = `
CREATE TABLE IF NOT EXISTS artists (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    updated_at TEXT
);

CREATE TABLE IF NOT EXISTS albums (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    release_date TEXT,
    image_url    TEXT
);

CREATE TABLE IF NOT EXISTS tracks (
    id             TEXT PRIMARY KEY,
    uri            TEXT NOT NULL,
    title          TEXT NOT NULL,
    album_id       TEXT,
    primary_artist TEXT,
    duration_ms    INTEGER,
    popularity     INTEGER,
    isrc           TEXT,
    updated_at     TEXT,
    FOREIGN KEY (album_id) REFERENCES albums(id)
);

CREATE TABLE IF NOT EXISTS track_artists (
    track_id  TEXT NOT NULL,
    artist_id TEXT NOT NULL,
    position  INTEGER NOT NULL,
    PRIMARY KEY (track_id, position),
    FOREIGN KEY (track_id) REFERENCES tracks(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS saved_tracks (
    track_id TEXT PRIMARY KEY,
    saved_at TEXT,
    FOREIGN KEY (track_id) REFERENCES tracks(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS playlists (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT,
    public      INTEGER,
    owner_id    TEXT,
    snapshot_id TEXT,
    track_count INTEGER,
    updated_at  TEXT
);

CREATE TABLE IF NOT EXISTS playlist_tracks (
    playlist_id TEXT NOT NULL,
    track_id    TEXT NOT NULL,
    position    INTEGER NOT NULL,
    PRIMARY KEY (playlist_id, position),
    FOREIGN KEY (playlist_id) REFERENCES playlists(id) ON DELETE CASCADE
);

-- Append-only local history: Spotify only exposes the last 50 plays and no
-- historical counts, so real repeat behavior must be accumulated here.
CREATE TABLE IF NOT EXISTS recently_played (
    track_id  TEXT NOT NULL,
    played_at TEXT NOT NULL,
    PRIMARY KEY (track_id, played_at)
);
CREATE INDEX IF NOT EXISTS idx_recent_played_at ON recently_played(played_at);

-- Deterministic resolver cache: same input key -> same URI. track_json holds
-- the full chosen track so cache hits are not hollow (URI-only) objects.
CREATE TABLE IF NOT EXISTS resolution_cache (
    query_key  TEXT PRIMARY KEY,
    uri        TEXT NOT NULL,
    bucket     TEXT,
    track_json TEXT,
    created_at TEXT
);

CREATE TABLE IF NOT EXISTS artist_tags (
    artist_name  TEXT PRIMARY KEY,
    tags_json    TEXT,
    similar_json TEXT,
    fetched_at   TEXT
);

-- What each discovery batch shipped, with per-track outcome.
CREATE TABLE IF NOT EXISTS batches (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    label         TEXT,
    playlist_id   TEXT,
    created_at    TEXT,
    track_count   INTEGER,
    digest        TEXT,
    outcomes_json TEXT
);

-- Keepers = the explicit-positive vote channel. Membership of the "Keepers"
-- playlist is snapshotted here with first-seen time so new votes since the last
-- batch can be surfaced (playlist_tracks has no per-row added_at).
CREATE TABLE IF NOT EXISTS keepers (
    track_id   TEXT PRIMARY KEY,
    first_seen TEXT
);

-- Disliked = the explicit-negative vote channel (the PRD's optional "Nope"
-- playlist). Same snapshot semantics as keepers; builds refuse to re-add
-- anything in here.
CREATE TABLE IF NOT EXISTS disliked (
    track_id   TEXT PRIMARY KEY,
    first_seen TEXT
);

-- Listen telemetry from the currently-playing poller: one row per listen with
-- how far the user got and the classified outcome (completed / skip_early /
-- skip_mid / partial / restart). Richer than recently_played, which only logs
-- completions.
CREATE TABLE IF NOT EXISTS listen_events (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    track_id        TEXT NOT NULL,
    started_at      TEXT,
    ended_at        TEXT,
    max_progress_ms INTEGER,
    duration_ms     INTEGER,
    outcome         TEXT
);
CREATE INDEX IF NOT EXISTS idx_listen_track ON listen_events(track_id);
CREATE INDEX IF NOT EXISTS idx_listen_ended ON listen_events(ended_at);

-- Bookkeeping (last sync time, last batch time, etc.).
CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT
);
`
