// Package shortcode encodes numeric IDs as short, URL-safe base62 strings.
package shortcode

import (
	"errors"
	"strings"
)

const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

const base = uint64(len(alphabet))

var ErrInvalidCode = errors.New("shortcode: invalid character in code")

// Encode converts a positive integer ID into a base62 string.
// IDs are monotonically increasing (allocated by idgen), so encoded
// length grows slowly: base62^6 ≈ 56.8 billion codes before a 7th
// character is ever needed.
func Encode(id uint64) string {
	if id == 0 {
		return string(alphabet[0])
	}
	var sb strings.Builder
	buf := make([]byte, 0, 11)
	for id > 0 {
		buf = append(buf, alphabet[id%base])
		id /= base
	}
	for i := len(buf) - 1; i >= 0; i-- {
		sb.WriteByte(buf[i])
	}
	return sb.String()
}

// Decode reverses Encode. Returns ErrInvalidCode for characters outside
// the base62 alphabet, so malformed/guessed URLs fail fast before any
// storage lookup.
func Decode(code string) (uint64, error) {
	var id uint64
	for _, c := range code {
		idx := strings.IndexRune(alphabet, c)
		if idx < 0 {
			return 0, ErrInvalidCode
		}
		id = id*base + uint64(idx)
	}
	return id, nil
}
