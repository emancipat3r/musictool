package service

import "context"

// ArtistSeed bundles the discovery inputs for one artist.
type ArtistSeed struct {
	Artist  string   `json:"artist"`
	Tags    []string `json:"tags"`
	Similar []string `json:"similar"`
	Cached  bool     `json:"cached"`
}

// ArtistTags returns tags + similar artists for an artist, served from the
// local cache when present and otherwise fetched from Last.fm and cached.
func (s *Service) ArtistTags(ctx context.Context, artist string) (ArtistSeed, error) {
	seed := ArtistSeed{Artist: artist}
	if tags, similar, ok := s.DB.GetArtistTags(ctx, artist); ok {
		seed.Tags, seed.Similar, seed.Cached = tags, similar, true
		return seed, nil
	}
	if !s.LF.Enabled() {
		return seed, nil
	}
	tags, err := s.LF.TopTags(ctx, artist, 12)
	if err != nil {
		return seed, err
	}
	similar, err := s.LF.SimilarArtists(ctx, artist, 20)
	if err != nil {
		return seed, err
	}
	seed.Tags, seed.Similar = tags, similar
	_ = s.DB.PutArtistTags(ctx, artist, tags, similar)
	return seed, nil
}

// SimilarArtists returns just the similar-artist names for an artist.
func (s *Service) SimilarArtists(ctx context.Context, artist string) ([]string, error) {
	seed, err := s.ArtistTags(ctx, artist)
	if err != nil {
		return nil, err
	}
	return seed.Similar, nil
}
