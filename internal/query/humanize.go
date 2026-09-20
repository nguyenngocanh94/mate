package query

import (
	"fmt"
	"strings"
)

// HumanizeTokens renders a token count the way `matev2 usage` and the
// console's TOKENS column do: exact under 1000, one decimal place with a
// k/M/B suffix above it - "523", "96.3k", "1.2M". A trailing ".0" is
// dropped ("90k", not "90.0k"), which is also why the observer's budget
// text (internal/timeline) and this share one function: two numbers that
// read identically in one place must read identically everywhere.
func HumanizeTokens(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	var out string
	switch {
	case n < 1000:
		out = fmt.Sprintf("%d", n)
	case n < 1_000_000:
		out = trimHumanZero(float64(n)/1000) + "k"
	case n < 1_000_000_000:
		out = trimHumanZero(float64(n)/1_000_000) + "M"
	default:
		out = trimHumanZero(float64(n)/1_000_000_000) + "B"
	}
	if neg {
		return "-" + out
	}
	return out
}

// HumanizeCost renders a dollar amount: cents under $1000 ("$0.12",
// "$42.00"), one decimal with a k suffix above it ("$1.2k") - so a cost
// column never grows wider than a token one next to it.
func HumanizeCost(v float64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	var out string
	if v < 1000 {
		out = fmt.Sprintf("$%.2f", v)
	} else {
		out = "$" + trimHumanZero(v/1000) + "k"
	}
	if neg {
		return "-" + out
	}
	return out
}

func trimHumanZero(v float64) string {
	s := fmt.Sprintf("%.1f", v)
	return strings.TrimSuffix(s, ".0")
}
