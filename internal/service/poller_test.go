package service

import "testing"

func TestClassifyListen(t *testing.T) {
	cases := []struct {
		name     string
		progress int
		duration int
		want     string
	}{
		{"bailed in 10s", 10_000, 240_000, "skip_early"},
		{"bailed at 29s", 29_000, 240_000, "skip_early"},
		{"bailed at 45s of 4min", 45_000, 240_000, "skip_mid"},
		{"bailed just before half", 119_000, 240_000, "skip_mid"},
		{"bailed at 60%", 150_000, 240_000, "partial"},
		{"reached 80%", 192_000, 240_000, "completed"},
		{"played to end", 240_000, 240_000, "completed"},
		{"short interlude fully played", 35_000, 40_000, "completed"},
		{"unknown duration, deep listen", 200_000, 0, "partial"},
		{"unknown duration, quick bail", 12_000, 0, "skip_early"},
	}
	for _, c := range cases {
		if got := classifyListen(c.progress, c.duration); got != c.want {
			t.Errorf("%s: classifyListen(%d,%d) = %s, want %s", c.name, c.progress, c.duration, got, c.want)
		}
	}
}

func TestIsRestart(t *testing.T) {
	if !isRestart(120_000, 2_000) {
		t.Error("scrub to start after 2min should be a restart")
	}
	if isRestart(20_000, 1_000) {
		t.Error("restart before 30s is just fidgeting, not engagement")
	}
	if isRestart(120_000, 100_000) {
		t.Error("small backward scrub is not a restart")
	}
}
