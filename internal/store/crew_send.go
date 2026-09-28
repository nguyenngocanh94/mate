package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// CrewSendAttempt records ownership of text that may still be in a composer.
// It is not an inbox or proof of delivery. Identity includes the crew launch
// and the runtime session, so a replacement cannot inherit a draft receipt.
type CrewSendAttempt struct {
	Identity string `json:"identity"`
	Text     string `json:"text"`
	Source   string `json:"source"`
}

// WithCrewSend serializes sends across CLI/console processes. Save is atomic
// and must be called before typing; nil clears a confirmed attempt. A process
// crash releases the lock and leaves the receipt for a conservative retry.
func (w *Workspace) WithCrewSend(project, crew string, fn func(*CrewSendAttempt, func(*CrewSendAttempt) error) error) error {
	if err := validatePair(project, crew); err != nil {
		return err
	}
	path := filepath.Join(w.CrewsDir(project), crew+".send.json")
	if _, err := w.resolve(path); err != nil {
		return err
	}
	lockPath, err := w.resolve(path + ".lock")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	ok, err := tryLockFile(f, true)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("a send to crew %s is already in progress", crew)
	}
	defer unlockFile(f)
	var attempt *CrewSendAttempt
	data, err := os.ReadFile(path)
	if err == nil {
		if err = json.Unmarshal(data, &attempt); err != nil {
			return fmt.Errorf("read send attempt: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	save := func(next *CrewSendAttempt) error {
		if next == nil {
			err := os.Remove(path)
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		data, err := json.Marshal(next)
		if err != nil {
			return err
		}
		return w.writeFile(path, data, 0600)
	}
	return fn(attempt, save)
}
