package types

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Money is a decimal amount in major units as it appears on the API
// ("110.00", 110.00 or 110). Internally the service stores int64 minor units;
// Minor converts using the currency's exponent and rejects excess precision.
type Money struct {
	units int64 // value = units / 10^scale
	scale int
	set   bool
}

// MoneyFromMinor builds a Money from minor units with the given exponent.
func MoneyFromMinor(minor int64, exponent int) Money {
	return Money{units: minor, scale: exponent, set: true}
}

// ParseMoney parses a plain decimal such as "110", "110.5" or "-3.250".
func ParseMoney(s string) (Money, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Money{}, errors.New("amount is empty")
	}
	neg := false
	if s[0] == '-' || s[0] == '+' {
		neg = s[0] == '-'
		s = s[1:]
	}
	intPart, fracPart, _ := strings.Cut(s, ".")
	if intPart == "" && fracPart == "" {
		return Money{}, errors.New("amount is not a number")
	}
	if intPart == "" {
		intPart = "0"
	}
	for _, r := range intPart + fracPart {
		if r < '0' || r > '9' {
			return Money{}, fmt.Errorf("amount %q is not a plain decimal number", s)
		}
	}
	if len(fracPart) > 8 {
		return Money{}, fmt.Errorf("amount %q has too many decimal places", s)
	}
	units, err := strconv.ParseInt(intPart+fracPart, 10, 64)
	if err != nil {
		return Money{}, fmt.Errorf("amount %q is out of range", s)
	}
	if neg {
		units = -units
	}
	return Money{units: units, scale: len(fracPart), set: true}, nil
}

// UnmarshalJSON accepts a JSON number or a quoted decimal string.
func (m *Money) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "null" {
		return nil
	}
	if strings.ContainsAny(s, "eE") {
		return fmt.Errorf("amount %s must be a plain decimal, not exponent notation", s)
	}
	parsed, err := ParseMoney(s)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

// MarshalJSON emits a JSON number with exactly `scale` decimals, e.g. 110.00.
func (m Money) MarshalJSON() ([]byte, error) {
	return []byte(m.String()), nil
}

// String formats the amount with its scale, e.g. "110.00" or "1500" (scale 0).
func (m Money) String() string {
	neg := m.units < 0
	abs := m.units
	if neg {
		abs = -abs
	}
	digits := strconv.FormatInt(abs, 10)
	if m.scale == 0 {
		if neg {
			return "-" + digits
		}
		return digits
	}
	for len(digits) <= m.scale {
		digits = "0" + digits
	}
	out := digits[:len(digits)-m.scale] + "." + digits[len(digits)-m.scale:]
	if neg {
		return "-" + out
	}
	return out
}

// IsSet reports whether a value was supplied.
func (m Money) IsSet() bool { return m.set }

// IsZero reports whether the amount is zero.
func (m Money) IsZero() bool { return m.units == 0 }

// Negative reports whether the amount is below zero.
func (m Money) Negative() bool { return m.units < 0 }

// Float is used only by the validator for gt/gte tags; never for arithmetic.
func (m Money) Float() float64 {
	return float64(m.units) / math.Pow10(m.scale)
}

// Minor converts to minor units for a currency with the given exponent.
// "110.00" with exponent 2 → 11000; "110.005" with exponent 2 → error.
func (m Money) Minor(exponent int) (int64, error) {
	switch {
	case m.scale == exponent:
		return m.units, nil
	case m.scale < exponent:
		factor := int64(math.Pow10(exponent - m.scale))
		if m.units > math.MaxInt64/factor || m.units < math.MinInt64/factor {
			return 0, errors.New("amount is out of range")
		}
		return m.units * factor, nil
	default:
		factor := int64(math.Pow10(m.scale - exponent))
		if m.units%factor != 0 {
			return 0, fmt.Errorf("amount %s has more than %d decimal places", m.String(), exponent)
		}
		return m.units / factor, nil
	}
}
