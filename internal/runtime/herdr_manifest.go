package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// errHerdrNotInstalled means the pin could not run. An absent binary is not
// drift: tests skip rather than fail. CI has no herdr, so it never verifies
// this pin (ADR 0007).
var errHerdrNotInstalled = errors.New("herdr binary not installed; testdata/manifest.json version pin unverified")

type herdrManifest struct {
	HerdrVersion string `json:"herdr_version"`
	Protocol     int    `json:"protocol"`
}

func loadHerdrManifest() (herdrManifest, error) {
	raw, err := os.ReadFile(filepath.Join("testdata", "manifest.json"))
	if err != nil {
		return herdrManifest{}, fmt.Errorf("herdr manifest: %w", err)
	}
	var man herdrManifest
	if err := json.Unmarshal(raw, &man); err != nil {
		return herdrManifest{}, fmt.Errorf("herdr manifest: %w", err)
	}
	if strings.TrimSpace(man.HerdrVersion) == "" || man.Protocol == 0 {
		return herdrManifest{}, fmt.Errorf("herdr manifest: herdr_version and protocol are required")
	}
	return man, nil
}

func parseHerdrVersion(out string) (string, error) {
	out = strings.TrimSpace(out)
	out = strings.TrimPrefix(out, "herdr")
	out = strings.TrimSpace(out)
	if out == "" {
		return "", fmt.Errorf("herdr version: empty")
	}
	if f := strings.Fields(out); len(f) > 0 {
		return f[0], nil
	}
	return out, nil
}

func pinError(man herdrManifest, versionOutput string, protocol int) error {
	got, err := parseHerdrVersion(versionOutput)
	if err != nil {
		return err
	}
	if got != man.HerdrVersion {
		return fmt.Errorf("installed herdr %s, testdata/manifest.json pins %s; recapture fixtures before trusting the taxonomy", got, man.HerdrVersion)
	}
	if protocol != man.Protocol {
		return fmt.Errorf("herdr protocol %d, testdata/manifest.json pins %d; recapture fixtures before trusting the taxonomy", protocol, man.Protocol)
	}
	return nil
}

func protocolFromStatusFixture() (int, error) {
	raw, err := os.ReadFile(filepath.Join("testdata", "status.json"))
	if err != nil {
		return 0, err
	}
	st, err := parseStatus(raw)
	if err != nil {
		return 0, err
	}
	if st.Protocol == 0 {
		return 0, fmt.Errorf("testdata/status.json missing protocol")
	}
	return st.Protocol, nil
}

func matchInstalledHerdr(man herdrManifest, lookPath func(string) (string, error)) error {
	path, err := lookPath("herdr")
	if err != nil {
		return errHerdrNotInstalled
	}
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return fmt.Errorf("herdr --version: %w", err)
	}
	proto, err := protocolFromStatusFixture()
	if err != nil {
		return err
	}
	if proto != man.Protocol {
		return fmt.Errorf("testdata/status.json protocol %d disagrees with manifest %d", proto, man.Protocol)
	}
	return pinError(man, strings.TrimSpace(string(out)), proto)
}

func assertInstalledHerdrMatchesManifest(man herdrManifest) error {
	return matchInstalledHerdr(man, exec.LookPath)
}
