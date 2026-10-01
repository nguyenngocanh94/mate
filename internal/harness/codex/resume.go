package codex

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// codexRolloutHeadBytes bounds the read of a rollout's first record. The
// session_meta record carries Codex's base instructions and measured about
// 18 KiB on codex-cli 0.154.0 (2026-09-24); the bound leaves room for that to
// grow without ever reading a multi-megabyte rollout whole.
const codexRolloutHeadBytes = 256 * 1024

// CodexRolloutPath finds the rollout Codex wrote for sessionID under
// sessionsDir (CodexSessionsDir). Codex names every rollout
// `rollout-<local time>-<session id>.jsonl`, so the file name alone answers
// the question and no rollout is opened.
//
// It is what `mate start` asks before it launches `codex resume <id>`: an id
// with no rollout makes codex-cli 0.154.0 print "No saved session found with
// ID ..." and exit to the shell, which Herdr reports only as an agent-start
// timeout a minute later (measured 2026-09-24).
func CodexRolloutPath(sessionsDir, sessionID string) (string, bool) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionsDir == "" || sessionID == "" {
		return "", false
	}
	suffix := "-" + sessionID + ".jsonl"
	var found string
	_ = filepath.WalkDir(sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return nil //nolint:nilerr // an unreadable branch is not an answer
		}
		if !d.IsDir() && strings.HasPrefix(d.Name(), "rollout-") && strings.HasSuffix(d.Name(), suffix) {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found, found != ""
}

// CodexRolloutCandidatesSince lists the rollouts under sessionsDir whose day
// directory (`YYYY/MM/DD`, Codex's local date) is not before since's local
// date minus one day, each with its validated session_meta. The day bound is
// what keeps this cheap on a machine with months of Codex history; the extra
// day absorbs a launch just before midnight or a clock-zone mismatch. The
// candidates go to AdoptCodexRollout, which applies the real rule (cwd and a
// session_meta timestamp at or after launch).
func CodexRolloutCandidatesSince(sessionsDir string, since time.Time) ([]CodexRolloutCandidate, error) {
	floor := since.Local().AddDate(0, 0, -1)
	floorDay := time.Date(floor.Year(), floor.Month(), floor.Day(), 0, 0, 0, 0, time.Local)
	var out []CodexRolloutCandidate
	err := filepath.WalkDir(sessionsDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == sessionsDir {
				return walkErr
			}
			return nil //nolint:nilerr // an unreadable branch is skipped, not fatal
		}
		if d.IsDir() {
			if path != sessionsDir && dayDirBefore(sessionsDir, path, floorDay) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasPrefix(d.Name(), "rollout-") || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		head, err := readFileHead(path, codexRolloutHeadBytes)
		if err != nil {
			return nil //nolint:nilerr
		}
		meta, err := ParseCodexSessionMeta(head)
		if err != nil {
			return nil //nolint:nilerr
		}
		out = append(out, CodexRolloutCandidate{Path: path, Meta: meta})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Path < out[b].Path })
	return out, nil
}

// dayDirBefore reports whether dir is a `YYYY`, `YYYY/MM` or `YYYY/MM/DD`
// directory under root that ends before floor. Anything that does not parse
// as such is walked, never skipped: an unknown layout must not hide a rollout.
func dayDirBefore(root, dir string, floor time.Time) bool {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	nums := make([]int, 0, 3)
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return false
		}
		nums = append(nums, n)
	}
	switch len(nums) {
	case 1:
		return nums[0] < floor.Year()
	case 2:
		end := time.Date(nums[0], time.Month(nums[1])+1, 1, 0, 0, 0, 0, time.Local)
		return !end.After(floor)
	case 3:
		end := time.Date(nums[0], time.Month(nums[1]), nums[2]+1, 0, 0, 0, 0, time.Local)
		return !end.After(floor)
	}
	return false
}

func readFileHead(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	read, err := io.ReadFull(f, buf)
	if read == 0 && err != nil {
		return nil, err
	}
	return buf[:read], nil
}
