// Package profile manages taste-profile.md — the living, human-readable
// distillation of the listener. It is a plain file both sides read and Claude
// (or the user, whose edits are ground truth) edits. This package only does
// file I/O and default scaffolding; it makes no model calls.
package profile

import (
	"os"
	"path/filepath"
)

// defaultTemplate is written when no profile exists yet, so curation sessions
// always have a starting structure to refine.
const defaultTemplate = `# Taste Profile

*Living distillation of the listener. Directly editable — user edits are ground
truth. Regenerated periodically from accumulated data + conversations.*

## Pillars
- (e.g. cali reggae / dub)
- (e.g. 90s boom-bap)
- (e.g. bass / dubstep)
- (e.g. death metal)
- (e.g. indie / psych)
- (e.g. country / Americana + cinematic)

## Texture preferences
- atmospheric layering; chime-over-bass; lush-not-sparse
- the cross-pillar atmospheric thread

## Artist tiers
### Core
-

### Quality but worn out
-

### No (with exceptions)
- e.g. "Tribal Seeds: mostly no, except In Your Eyes"

## Context notes
- deck speakers vs car vs headphones
`

// Read returns the profile contents, creating the default template on first
// access so the file always exists.
func Read(path string) (string, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if werr := Write(path, defaultTemplate); werr != nil {
			return "", werr
		}
		return defaultTemplate, nil
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Write saves the profile atomically (0644 — human-readable, not a secret).
func Write(path, content string) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
