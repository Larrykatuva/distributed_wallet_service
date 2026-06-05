package utils

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

func generateTransactionNumber() string {
	ticks := time.Now().UnixNano()
	bytes := make([]byte, 8)
	for i := 0; i < 8; i++ {
		bytes[i] = byte(ticks >> (i * 8))
	}
	id := base64.StdEncoding.EncodeToString(bytes)
	id = strings.ReplaceAll(id, "+", " ")
	id = strings.ReplaceAll(id, "/", " ")
	id = strings.TrimRight(id, "=")
	return id
}

func removeSpecialChars(refNo string) string {
	reg := regexp.MustCompile("[!@#$%\\^&*\\(\\)_+=\\/\\\\{\\}\\[\\]\\|/:;/\"'<>,.\\?\\~`;]")
	return reg.ReplaceAllString(refNo, "")
}

func reverseString(s string) string {
	runes := []rune(s)
	n := len(runes)
	for i := 0; i < n/2; i++ {
		runes[i], runes[n-1-i] = runes[n-1-i], runes[i]
	}
	return string(runes)
}

func GenerateRrn() string {
	noSpaces := strings.ReplaceAll(removeSpecialChars(generateTransactionNumber()), " ", "")
	upper := strings.ToUpper(noSpaces)
	return reverseString(upper)
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
