package names

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofrs/flock"
)

// HistorySize is the number of recent names remembered to suppress repeats.
const HistorySize = 20

// Generate returns a random "adjective-noun" identifier. It avoids any
// name still inside the rolling history window of size HistorySize and
// records the new name in the history before returning. Concurrent
// callers are serialised by a per-file flock.
func Generate() (string, error) {
	path, err := historyPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create history dir: %w", err)
	}
	fl := flock.New(path + ".lock")
	if err := fl.Lock(); err != nil {
		return "", fmt.Errorf("lock history: %w", err)
	}
	defer func() { _ = fl.Unlock() }()

	hist, err := readHistory(path)
	if err != nil {
		return "", err
	}
	used := make(map[string]struct{}, len(hist))
	for _, n := range hist {
		used[n] = struct{}{}
	}

	// 2500 combos vs at most HistorySize used → terminates in well under
	// 100 tries with overwhelming probability, but cap to be safe.
	const maxTries = 1000
	var name string
	for i := 0; i < maxTries; i++ {
		name = pickPair()
		if _, taken := used[name]; !taken {
			break
		}
	}
	if _, taken := used[name]; taken {
		return "", errors.New("could not find an unused name; widen the dictionary or shrink history")
	}

	hist = append(hist, name)
	if len(hist) > HistorySize {
		hist = hist[len(hist)-HistorySize:]
	}
	if err := writeHistory(path, hist); err != nil {
		return "", err
	}
	return name, nil
}

// GenerateStateless returns a random name without consulting or updating
// the history. Useful for tests.
func GenerateStateless() string {
	return pickPair()
}

// Counts exposes the size of each list for tests and help text.
func Counts() (adj, noun int) {
	return len(adjectives), len(nouns)
}

// HistoryPath returns the on-disk location of the rolling name history.
func HistoryPath() (string, error) { return historyPath() }

func pickPair() string {
	a := pick(len(adjectives))
	n := pick(len(nouns))
	return fmt.Sprintf("%s-%s", adjectives[a], nouns[n])
}

func historyPath() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate user cache dir: %w", err)
	}
	return filepath.Join(cache, "gh-claim-issue", "name-history"), nil
}

func readHistory(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read history: %w", err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			out = append(out, line)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read history: %w", err)
	}
	if len(out) > HistorySize {
		out = out[len(out)-HistorySize:]
	}
	return out, nil
}

func writeHistory(path string, names []string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".name-history-*")
	if err != nil {
		return fmt.Errorf("write history: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		_ = os.Remove(tmp.Name())
	}()
	w := bufio.NewWriter(tmp)
	for _, n := range names {
		if _, err := fmt.Fprintln(w, n); err != nil {
			return fmt.Errorf("write history: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("write history: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write history: %w", err)
	}
	closed = true
	return os.Rename(tmp.Name(), path)
}
