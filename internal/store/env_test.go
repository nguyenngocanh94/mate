package store_test

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/store"
)

func writeEnv(t *testing.T, w *store.Workspace, text string) {
	t.Helper()
	if err := os.WriteFile(w.EnvFile(), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadEnvReadsSettingsAroundCommentsAndQuotes(t *testing.T) {
	w := newWorkspace(t)
	writeEnv(t, w, "# per-machine settings\n\nexport MATE_JEV=on # today\nMATE_JEV_API_KEY_FILE=\"/keys/jev key\"\nNOTE='a=b # kept'\n SPACED = bare \nEMPTY=\n")
	env, err := w.LoadEnv()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"MATE_JEV": "on", "MATE_JEV_API_KEY_FILE": "/keys/jev key", "NOTE": "a=b # kept", "SPACED": "bare", "EMPTY": "",
	}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("env %v, want %v", env, want)
	}
}

func TestLoadEnvWithoutAFileIsEmpty(t *testing.T) {
	w := newWorkspace(t)
	env, err := w.LoadEnv()
	if err != nil || len(env) != 0 {
		t.Fatalf("env %v, err %v", env, err)
	}
}

func TestLoadEnvNamesTheLineThatIsNotASetting(t *testing.T) {
	w := newWorkspace(t)
	for _, text := range []string{"MATE_JEV=on\nthis is not a setting\n", "MATE_JEV=on\n1KEY=value\n"} {
		writeEnv(t, w, text)
		_, err := w.LoadEnv()
		if err == nil || !strings.Contains(err.Error(), ".mate/.env:2") {
			t.Fatalf("%q: err %v", text, err)
		}
	}
}
