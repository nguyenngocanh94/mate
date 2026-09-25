package host

import "testing"

func TestDetect(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		env  map[string]string
		want Kind
	}{
		{name: "empty", want: None},
		{
			name: "wezterm pane",
			env:  map[string]string{"WEZTERM_PANE": "12"},
			want: WezTerm,
		},
		{
			name: "wezterm wins over ghostty",
			env:  map[string]string{"WEZTERM_PANE": "1", "TERM_PROGRAM": "ghostty"},
			want: WezTerm,
		},
		{
			name: "ghostty",
			env:  map[string]string{"TERM_PROGRAM": "ghostty"},
			want: Ghostty,
		},
		{
			name: "iterm",
			env:  map[string]string{"TERM_PROGRAM": "iTerm.app"},
			want: ITerm,
		},
		{
			name: "blank wezterm pane is ignored",
			env:  map[string]string{"WEZTERM_PANE": "  ", "TERM_PROGRAM": "ghostty"},
			want: Ghostty,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Detect(func(k string) string { return tc.env[k] })
			if got != tc.want {
				t.Fatalf("Detect = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOpenNilForNoneAndITerm(t *testing.T) {
	t.Parallel()
	if Open(None, Options{}) != nil {
		t.Fatal("Open(None) must be nil")
	}
	if Open(ITerm, Options{}) != nil {
		t.Fatal("Open(ITerm) is reserved and must be nil")
	}
}
