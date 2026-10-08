package recovery

import "testing"

func TestReplacePrefix(t *testing.T) {
	for _, tc := range []struct {
		name, text, old, new, want string
	}{
		{"path under old", "cd /w/web/src and /w/web", "/w/web", "/w/shop/web", "cd /w/shop/web/src and /w/shop/web"},
		{"longer name untouched", "/w/webapp and `/w/web`", "/w/web", "/w/shop/web", "/w/webapp and `/w/shop/web`"},
		{"new under old", "repo /w/shop, file /w/shop/a.go", "/w/shop", "/w/shop/shop", "repo /w/shop/shop, file /w/shop/shop/a.go"},
		{"second pass is a no-op", "repo /w/shop/shop, file /w/shop/shop/a.go", "/w/shop", "/w/shop/shop", "repo /w/shop/shop, file /w/shop/shop/a.go"},
		{"sibling under new home is moved", "/w/shop/web/x", "/w/shop", "/w/shop/shop", "/w/shop/shop/web/x"},
		{"trailing slash", "/w/web/", "/w/web/", "/w/shop/web/", "/w/shop/web/"},
		{"nothing", "no paths here", "/w/web", "/w/shop/web", "no paths here"},
		{"sentence period", "Work in /w/web.\nThen /w/web.go and /w/web.d/x.", "/w/web", "/w/shop/web", "Work in /w/shop/web.\nThen /w/web.go and /w/web.d/x."},
		{"directory named with a trailing period", "/w/web./x and /w/web... and /w/web.", "/w/web", "/w/shop/web", "/w/web./x and /w/web... and /w/shop/web."},
		{"end of text", "cd /w/web", "/w/web", "/w/shop/web", "cd /w/shop/web"},
	} {
		if got := ReplacePrefix(tc.text, tc.old, tc.new); got != tc.want {
			t.Errorf("%s: ReplacePrefix(%q) = %q, want %q", tc.name, tc.text, got, tc.want)
		}
	}
}
