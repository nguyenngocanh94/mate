package store_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/store"
)

func TestCrewSendReceiptSurvivesFailureAndSerializesWriters(t *testing.T) {
	w := newWorkspace(t)
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: "shop"}}}); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteCrewMeta("shop", "k3", map[string]string{"agent": "crew-k3"}); err != nil {
		t.Fatal(err)
	}
	want := store.CrewSendAttempt{Identity: "session/incarnation", Text: "hello", Source: store.SourceMate}
	failed := errors.New("Enter unconfirmed")
	err := w.WithCrewSend("shop", "k3", func(prior *store.CrewSendAttempt, save func(*store.CrewSendAttempt) error) error {
		if prior != nil {
			t.Fatal("unexpected prior attempt")
		}
		if err := save(&want); err != nil {
			t.Fatal(err)
		}
		err := w.WithCrewSend("shop", "k3", func(*store.CrewSendAttempt, func(*store.CrewSendAttempt) error) error {
			t.Fatal("another writer entered the locked send")
			return nil
		})
		if err == nil {
			t.Fatal("concurrent send not refused")
		}
		return failed
	})
	if !errors.Is(err, failed) {
		t.Fatal(err)
	}
	path := filepath.Join(w.CrewsDir("shop"), "k3.send.json")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("receipt mode: %v %v", info, err)
	}
	err = w.WithCrewSend("shop", "k3", func(prior *store.CrewSendAttempt, save func(*store.CrewSendAttempt) error) error {
		if prior == nil || *prior != want {
			t.Fatalf("lost receipt: %+v", prior)
		}
		return save(nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("receipt not cleared: %v", err)
	}
	if err := w.WithCrewSend("shop", "k3", func(prior *store.CrewSendAttempt, save func(*store.CrewSendAttempt) error) error {
		if prior != nil {
			t.Fatal("cleared receipt returned")
		}
		return save(nil)
	}); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"../k3", ""} {
		if err := w.WithCrewSend("shop", invalid, func(*store.CrewSendAttempt, func(*store.CrewSendAttempt) error) error {
			t.Fatal("invalid crew reached sender")
			return nil
		}); err == nil {
			t.Fatal("invalid crew accepted")
		}
	}
	if err := os.WriteFile(path, []byte("broken json"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := w.WithCrewSend("shop", "k3", func(*store.CrewSendAttempt, func(*store.CrewSendAttempt) error) error {
		t.Fatal("corrupt receipt must not authorize sending")
		return nil
	}); err == nil {
		t.Fatal("corrupt receipt ignored")
	}
}
