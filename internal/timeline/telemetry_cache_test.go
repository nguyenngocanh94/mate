package timeline

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

func TestCodexTranscriptCacheTailMatchesFullParse(t *testing.T) {
	data, err := os.ReadFile("testdata/codex-0.154-rollout.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	// Stop in the middle of a source record. The cached parser must resume
	// from ConsumedBytes, retaining the fragment and cumulative usage state.
	cut := len(data) / 2
	if err := os.WriteFile(path, data[:cut], 0o600); err != nil {
		t.Fatal(err)
	}
	ing := &Ingester{}
	loc := Located{Path: path, Kind: harness.KindCodex}
	first, err := ing.cachedTranscript(loc)
	if err != nil {
		t.Fatal(err)
	}
	if first.ConsumedBytes >= int64(cut) {
		t.Fatal("fixture cut did not leave a partial line")
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ing.cachedTranscript(loc)
	if err != nil {
		t.Fatal(err)
	}
	want := (harness.Codex{}).ParseTranscript(harness.TranscriptParseState{}, data)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("append cache differs from full parse: usage, offsets, prompt attribution or cursor changed")
	}
}

func TestCodexTranscriptCacheHandlesGrowthThenTruncation(t *testing.T) {
	data, err := os.ReadFile("testdata/codex-0.154-rollout.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	cut := len(data) / 2
	if err := os.WriteFile(path, data[:cut], 0o600); err != nil {
		t.Fatal(err)
	}
	// Simulate stat seeing 10 bytes, ReadFile seeing the later complete file,
	// then truncation before the next stat. The next tail length is negative.
	full := (harness.Codex{}).ParseTranscript(harness.TranscriptParseState{}, data)
	ing := &Ingester{transcripts: map[string]transcriptCache{path: {Size: 10, Batch: full, HeaderBytes: 5, HeaderSHA256: fmt.Sprintf("%x", sha256.Sum256(data[:5]))}}}
	got, err := ing.cachedTranscript(Located{Path: path, Kind: harness.KindCodex})
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalBytes != int64(cut) {
		t.Fatalf("did not replay truncated source: %d", got.TotalBytes)
	}
}
