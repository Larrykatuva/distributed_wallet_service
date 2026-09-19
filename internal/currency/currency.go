// Package currency is the registry of ISO 4217 currencies the service accepts.
// The list lives in currencies.json (embedded at build time) so it can be
// reviewed and extended without touching Go code. Every wallet, ledger row
// and transaction carries exactly one of these codes, and a single
// transaction never mixes them.
package currency

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Currency describes a supported ISO 4217 code. Exponent is the number of
// minor-unit digits (2 for KES/USD, 0 for JPY, 3 for KWD): amounts are stored
// as int64 minor units, so KES 1.00 == 100 and KWD 1.000 == 1000.
type Currency struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Exponent int    `json:"exponent"`
}

//go:embed currencies.json
var source []byte

var (
	registry map[string]Currency
	ordered  []Currency
)

func init() {
	var list []Currency
	if err := json.Unmarshal(source, &list); err != nil {
		panic(fmt.Sprintf("currency: currencies.json is invalid: %v", err))
	}

	registry = make(map[string]Currency, len(list))
	for _, c := range list {
		code := Normalize(c.Code)
		if len(code) != 3 {
			panic(fmt.Sprintf("currency: %q is not a 3-letter ISO 4217 code", c.Code))
		}
		if _, dup := registry[code]; dup {
			panic(fmt.Sprintf("currency: %s listed twice in currencies.json", code))
		}
		c.Code = code
		registry[code] = c
	}

	ordered = make([]Currency, 0, len(registry))
	for _, c := range registry {
		ordered = append(ordered, c)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Code < ordered[j].Code })
}

// Normalize upper-cases and trims a code so "kes " and "KES" compare equal.
func Normalize(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

// Lookup returns the currency for a code (case-insensitive).
func Lookup(code string) (Currency, bool) {
	c, ok := registry[Normalize(code)]
	return c, ok
}

// Supported reports whether the code is in the registry.
func Supported(code string) bool {
	_, ok := Lookup(code)
	return ok
}

// All returns every supported currency sorted by code.
func All() []Currency {
	out := make([]Currency, len(ordered))
	copy(out, ordered)
	return out
}

// Codes lists every supported code, sorted.
func Codes() []string {
	out := make([]string, 0, len(ordered))
	for _, c := range ordered {
		out = append(out, c.Code)
	}
	return out
}
