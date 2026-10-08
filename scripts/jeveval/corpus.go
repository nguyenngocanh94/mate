package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/screen/jev"
	"github.com/nguyenngocanh94/mate/internal/send"
)

// Axis is the part of the observation a screen's deterministic label speaks
// to: one of the three questions every request asks Jev. A screen is scored
// on its own axis.
type Axis = jev.Axis

const (
	AxisComposer = jev.AxisComposer
	AxisDialog   = jev.AxisDialog
	AxisNotice   = jev.AxisNotice
)

// Screen is one corpus entry: a captured (or, for injections, hand-built)
// pane and the label the repo's own classifier gives it.
type Screen struct {
	// File is module-relative; for a notice fixture it is
	// notices.json#<name>.
	File   string
	Source string
	// Kind is the harness whose profile labelled the screen; empty for a
	// notice fixture, which belongs to no harness.
	Kind harness.Kind
	Axis Axis
	// Det is the deterministic classifier's own output on Axis
	// (send.ComposerState, harness.StartupScreen or a notice label).
	Det string
	// Composer is send.ClassifyComposer's state for every screen with a
	// harness, whatever its Axis: the Empty-when-Draft/Busy gate reads it.
	Composer send.ComposerState
	Text     string
}

// Sources, in the order the corpus is listed.
const (
	sourceSend      = "send"
	sourceStartup   = "startup"
	sourceScreens   = "harness-screens"
	sourceNotice    = "notice"
	sourceInjection = "injection"
)

// loadCorpus gathers every screen in the repo that has a deterministic
// label. root is the module root.
func loadCorpus(root string) ([]Screen, error) {
	reg := catalog.Default()
	kinds := reg.Kinds()
	profile := func(k harness.Kind) harness.ScreenProfile {
		p, err := reg.Lookup(k)
		if err != nil {
			panic(err) // k came from reg.Kinds()
		}
		return p.Screen()
	}
	// prefixKind names the harness a send/injection capture belongs to
	// from its file name, the way internal/send's tests do.
	prefixKind := func(name string) (harness.Kind, bool) {
		for _, k := range kinds {
			if strings.HasPrefix(name, string(k)+"_") {
				return k, true
			}
		}
		return "", false
	}

	var corpus []Screen
	add := func(rel, source string, k harness.Kind, axis Axis) error {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return err
		}
		text := string(raw)
		sp := profile(k)
		s := Screen{File: rel, Source: source, Kind: k, Axis: axis, Text: text,
			Composer: send.ClassifyComposer(sp, text).State}
		switch axis {
		case AxisComposer:
			s.Det = string(s.Composer)
		case AxisDialog:
			s.Det = string(sp.ClassifyStartup(send.StripSGR(text)))
		}
		corpus = append(corpus, s)
		return nil
	}
	files := func(dir string) ([]string, error) {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			return nil, err
		}
		var out []string
		for _, e := range entries {
			if e.IsDir() || strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			out = append(out, e.Name())
		}
		sort.Strings(out)
		return out, nil
	}

	// 1. internal/screen/fixture's composer captures: ClassifyComposer.
	sendDir := "internal/screen/fixture/testdata/screens"
	names, err := files(sendDir)
	if err != nil {
		return nil, err
	}
	for _, n := range names {
		k, ok := prefixKind(n)
		if !ok {
			return nil, fmt.Errorf("%s/%s: no registered harness prefixes its name", sendDir, n)
		}
		if err := add(sendDir+"/"+n, sourceSend, k, AxisComposer); err != nil {
			return nil, err
		}
	}

	// 2. Each harness's startup captures: ClassifyStartup. 3. Each
	// harness's screens/ captures: ClassifyStartup, as the brief asks for
	// the pi fixtures, except a harness whose captures are all composer
	// screens with no startup dialog of its own (see screenAxis).
	for _, k := range kinds {
		for _, sub := range []string{"startup", "screens"} {
			dir := "internal/harness/" + string(k) + "/testdata/" + sub
			names, err := files(dir)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			source, axis := sourceStartup, AxisDialog
			if sub == "screens" {
				source, axis = sourceScreens, screenAxis(profile(k))
			}
			for _, n := range names {
				if err := add(dir+"/"+n, source, k, axis); err != nil {
					return nil, err
				}
			}
		}
	}

	// 4. The notice pilot's fixtures, labelled in the file.
	noticeFile := "internal/notice/testdata/notices.json"
	raw, err := os.ReadFile(filepath.Join(root, noticeFile))
	if err != nil {
		return nil, err
	}
	var notices []struct{ Name, Screen, Want string }
	if err := json.Unmarshal(raw, &notices); err != nil {
		return nil, fmt.Errorf("%s: %w", noticeFile, err)
	}
	for _, n := range notices {
		corpus = append(corpus, Screen{File: noticeFile + "#" + n.Name, Source: sourceNotice,
			Axis: AxisNotice, Det: n.Want, Text: n.Screen})
	}

	// 5. The injection screens built for this eval.
	injDir := "scripts/jeveval/testdata/screens"
	names, err = files(injDir)
	if err != nil {
		return nil, err
	}
	for _, n := range names {
		k, ok := prefixKind(n)
		if !ok {
			return nil, fmt.Errorf("%s/%s: no registered harness prefixes its name", injDir, n)
		}
		if err := add(injDir+"/"+n, sourceInjection, k, AxisComposer); err != nil {
			return nil, err
		}
	}
	return corpus, nil
}

// screenAxis is the axis a harness's screens/ captures are scored on. They
// are scored by ClassifyStartup (the brief's rule for the pi fixtures)
// when the harness draws a startup dialog at all; a harness that draws none
// has only composer captures there, which ClassifyComposer labels.
func screenAxis(sp harness.ScreenProfile) Axis {
	for _, d := range []harness.StartupScreen{harness.StartupScreenTrustDialog, harness.StartupScreenUpdateDialog,
		harness.StartupScreenHooksReview, harness.StartupScreenBypassDialog} {
		if _, err := sp.StartupAnswer(d); err == nil {
			return AxisDialog
		}
	}
	return AxisComposer
}

// moduleRoot walks up from the working directory to go.mod.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("go.mod not found above the working directory")
		}
		dir = parent
	}
}
