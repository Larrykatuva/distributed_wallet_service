package utils

import (
	"crypto/rand"
	"encoding/json"
	"net"
	"net/http"
	"strings"
)

const rrnAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// GenerateRrn returns a random 12-character reference (about 62 bits).
func GenerateRrn() string {
	out := make([]byte, 0, 12)
	buf := make([]byte, 24)
	for len(out) < cap(out) {
		_, _ = rand.Read(buf)
		for _, c := range buf {
			if c < 252 && len(out) < cap(out) { // 252 = 7*36 keeps every symbol equally likely
				out = append(out, rrnAlphabet[c%36])
			}
		}
	}
	return string(out)
}

// CastData is a utility function that marshals the source data (src) into JSON,
// then unmarshals it into the destination (dest). This is used to copy data
// from one structure to another, ensuring that the types are compatible.
func CastData(src, dest interface{}) error {
	// Marshal src into JSON format with indentation for readability.
	jsonData, err := json.MarshalIndent(src, "", "    ")
	if err != nil {
		return err // Return the error if marshaling fails.
	}

	// Unmarshal the JSON data into the dest interface.
	if err = json.Unmarshal(jsonData, &dest); err != nil {
		return err // Return the error if unmarshaling fails.
	}

	return nil // Return nil if everything succeeds.
}

func GetIPAddress(r *http.Request) string {
	// Check X-Forwarded-For (for reverse proxies/load balancers)
	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded != "" {
		// May contain multiple IPs, take the first
		parts := strings.Split(forwarded, ",")
		return strings.TrimSpace(parts[0])
	}

	// Fallback to RemoteAddr
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

func SafeString(s *string) string {
	if s != nil {
		return *s
	}
	return ""
}

func SafeFloat(f *float64) float64 {
	if f != nil {
		return *f
	}
	return 0.00
}
