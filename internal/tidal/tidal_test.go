package tidal

import (
	"encoding/json"
	"testing"
)

func TestParseISODurationMs(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"PT3M35S", 215_000},
		{"PT1H2M3S", 3_723_000},
		{"PT45S", 45_000},
		{"PT3.5S", 3_500},
		{"PT4M", 240_000},
		{"", 0},
		{"3:35", 0},
		{"PT", 0},
		{"PT3", 0}, // trailing digits without a unit
	}
	for _, c := range cases {
		if got := parseISODurationMs(c.in); got != c.want {
			t.Errorf("parseISODurationMs(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestPopularity100(t *testing.T) {
	cases := []struct {
		in   float64
		want int
	}{{0, 0}, {0.247, 25}, {1, 100}, {1.5, 100}, {-0.1, 0}}
	for _, c := range cases {
		if got := popularity100(c.in); got != c.want {
			t.Errorf("popularity100(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

// A trimmed real-shape /tracks response: one track with artist and album
// side-loaded. buildTrack must produce a complete model.Track, fold the
// version variant into the title, and scale popularity.
const trackDocJSON = `{
  "data": [{
    "id": "236492000",
    "type": "tracks",
    "attributes": {
      "title": "Smoke Stack",
      "isrc": "USHM90935395",
      "duration": "PT3M35S",
      "popularity": 0.247,
      "version": "Live"
    },
    "relationships": {
      "artists": {"data": [{"id": "a1", "type": "artists"}]},
      "albums": {"data": [{"id": "b1", "type": "albums"}]}
    }
  }],
  "included": [
    {"id": "a1", "type": "artists", "attributes": {"name": "Stick Figure"}},
    {"id": "b1", "type": "albums", "attributes": {"title": "Smoke Stack", "releaseDate": "2009-10-01"}}
  ]
}`

func TestBuildTrackFromDocument(t *testing.T) {
	var doc jaDocument
	if err := json.Unmarshal([]byte(trackDocJSON), &doc); err != nil {
		t.Fatal(err)
	}
	res := doc.dataResources()
	if len(res) != 1 {
		t.Fatalf("resources = %d, want 1", len(res))
	}
	tr := buildTrack(res[0], includedIndex(&doc))

	if tr.ID != "236492000" || tr.URI != "tidal:track:236492000" {
		t.Fatalf("id/uri = %s / %s", tr.ID, tr.URI)
	}
	if tr.Title != "Smoke Stack (Live)" {
		t.Fatalf("title = %q; version must fold into the title for variant scoring", tr.Title)
	}
	if tr.ISRC != "USHM90935395" {
		t.Fatalf("isrc = %q", tr.ISRC)
	}
	if tr.DurationMs != 215_000 {
		t.Fatalf("duration = %d", tr.DurationMs)
	}
	if tr.Popularity != 25 {
		t.Fatalf("popularity = %d, want 25 (0.247 scaled to 0..100)", tr.Popularity)
	}
	if tr.ArtistName() != "Stick Figure" {
		t.Fatalf("artist = %q", tr.ArtistName())
	}
	if tr.Album.Name != "Smoke Stack" || tr.Album.ReleaseDate != "2009-10-01" {
		t.Fatalf("album = %+v", tr.Album)
	}
}

func TestTrackIDRoundTrip(t *testing.T) {
	c := &Client{}
	id, ok := c.TrackID("tidal:track:522691303")
	if !ok || id != "522691303" {
		t.Fatalf("TrackID = %q, %v", id, ok)
	}
	if _, ok := c.TrackID("spotify:track:abc"); ok {
		t.Fatal("foreign URI scheme must not parse")
	}
	if _, ok := c.TrackID("tidal:track:"); ok {
		t.Fatal("empty id must not parse")
	}
}

// A single-resource data document (create playlist response) must decode too.
func TestDataResourcesSingle(t *testing.T) {
	var doc jaDocument
	err := json.Unmarshal([]byte(`{"data": {"id": "p1", "type": "playlists", "attributes": {"name": "x", "accessType": "UNLISTED"}}}`), &doc)
	if err != nil {
		t.Fatal(err)
	}
	res := doc.dataResources()
	if len(res) != 1 || res[0].ID != "p1" {
		t.Fatalf("resources = %+v", res)
	}
	c := &Client{userID: "u1"}
	pl := c.playlistFromResource(res[0])
	if pl.Name != "x" || pl.Public || pl.OwnerID != "u1" {
		t.Fatalf("playlist = %+v", pl)
	}
}
