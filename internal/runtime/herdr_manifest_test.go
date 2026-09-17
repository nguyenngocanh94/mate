package runtime

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestLiveHerdrManifestPinsInstalledBinary(t *testing.T) {
	requireLive(t)
	t.Parallel()
	man, err := loadHerdrManifest()
	if err != nil {
		t.Fatal(err)
	}
	if man.HerdrVersion == "" || man.Protocol == 0 {
		t.Fatalf("manifest missing version/protocol: %+v", man)
	}
	err = assertInstalledHerdrMatchesManifest(man)
	if errors.Is(err, errHerdrNotInstalled) {
		t.Skip(err.Error())
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestMatchInstalledHerdrMissingIsNotDrift(t *testing.T) {
	t.Parallel()
	man := herdrManifest{HerdrVersion: "0.8.2", Protocol: 20}
	err := matchInstalledHerdr(man, func(string) (string, error) {
		return "", exec.ErrNotFound
	})
	if !errors.Is(err, errHerdrNotInstalled) {
		t.Fatalf("absent binary must be errHerdrNotInstalled, got %v", err)
	}
	if err := pinError(man, "herdr 0.0.0", 20); err == nil {
		t.Fatal("present binary at the wrong version must still fail")
	}
}

func TestHerdrManifestRejectsVersionDrift(t *testing.T) {
	t.Parallel()
	err := pinError(herdrManifest{HerdrVersion: "0.0.0", Protocol: 20}, "herdr 0.8.2", 20)
	if err == nil {
		t.Fatal("fixture taxonomy must fail closed when the installed Herdr version drifts")
	}
	err = pinError(herdrManifest{HerdrVersion: "0.8.2", Protocol: 20}, "herdr 0.8.2", 21)
	if err == nil {
		t.Fatal("fixture taxonomy must fail closed when the protocol drifts")
	}
	if err := pinError(herdrManifest{HerdrVersion: "0.8.2", Protocol: 20}, "herdr 0.8.2", 20); err != nil {
		t.Fatalf("matching pin must pass: %v", err)
	}
}

func TestLiveHerdrVersionOutputParsedFromInstalledCLI(t *testing.T) {
	requireLive(t)
	t.Parallel()
	path, err := exec.LookPath("herdr")
	if err != nil {
		t.Skip("herdr binary not installed; testdata/manifest.json version pin unverified")
	}
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseHerdrVersion(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	man, err := loadHerdrManifest()
	if err != nil {
		t.Fatal(err)
	}
	if got != man.HerdrVersion {
		t.Fatalf("installed %q, manifest pins %q; recapture fixtures and bump testdata/manifest.json", got, man.HerdrVersion)
	}
}
