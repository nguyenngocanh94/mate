package query

import "testing"

func TestHumanizeTokens(t *testing.T) {
	cases := map[int64]string{
		0:             "0",
		523:           "523",
		999:           "999",
		1000:          "1k",
		96300:         "96.3k",
		1_200_000:     "1.2M",
		2_000_000:     "2M",
		1_500_000_000: "1.5B",
		-96300:        "-96.3k",
	}
	for n, want := range cases {
		if got := HumanizeTokens(n); got != want {
			t.Errorf("HumanizeTokens(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestHumanizeCost(t *testing.T) {
	cases := map[float64]string{
		0:       "$0.00",
		0.12:    "$0.12",
		42:      "$42.00",
		999.995: "$1000.00", // rounds at the printf level, still under the k cutoff branch
		1200:    "$1.2k",
	}
	for v, want := range cases {
		if got := HumanizeCost(v); got != want {
			t.Errorf("HumanizeCost(%v) = %q, want %q", v, got, want)
		}
	}
}
