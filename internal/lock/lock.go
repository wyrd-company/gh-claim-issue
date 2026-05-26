// Package lock provides a cross-process file mutex used to serialise
// claim attempts by multiple agents running on the same machine against
// the same repository.
package lock

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// Acquire takes an exclusive flock on a path derived from repoSlug
// (e.g. "owner/name"). It retries until ctx is cancelled, polling at
// retry. Caller must invoke the returned release function.
func Acquire(ctx context.Context, repoSlug string, retry time.Duration) (release func() error, path string, err error) {
	path, err = lockPath(repoSlug)
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, "", fmt.Errorf("create lock dir: %w", err)
	}
	fl := flock.New(path)
	ok, err := fl.TryLockContext(ctx, retry)
	if err != nil {
		return nil, path, fmt.Errorf("acquire lock %s: %w", path, err)
	}
	if !ok {
		return nil, path, fmt.Errorf("could not acquire lock %s", path)
	}
	return fl.Unlock, path, nil
}

func lockPath(repoSlug string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate user cache dir: %w", err)
	}
	// Hash the slug so we never produce a path with awkward characters.
	sum := sha256.Sum256([]byte(repoSlug))
	name := hex.EncodeToString(sum[:8]) + ".lock"
	return filepath.Join(cache, "gh-claim-issue", name), nil
}
