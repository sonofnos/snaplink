package shortcode

import "testing"

func TestEncodeDecodeRoundTrip(t *testing.T) {
	ids := []uint64{0, 1, 61, 62, 63, 12345, 999999999, 56800235583, 18446744073709551615}
	for _, id := range ids {
		code := Encode(id)
		got, err := Decode(code)
		if err != nil {
			t.Fatalf("Decode(%q) returned error: %v", code, err)
		}
		if got != id {
			t.Errorf("round trip mismatch: id=%d code=%q decoded=%d", id, code, got)
		}
	}
}

func TestEncodeIsMonotonicLength(t *testing.T) {
	// Codes must not shrink in length as IDs grow, otherwise sorted
	// storage/index assumptions elsewhere would break.
	prevLen := 0
	for id := uint64(0); id < 100000; id += 997 {
		l := len(Encode(id))
		if l < prevLen {
			t.Fatalf("code length shrank at id=%d: prev=%d now=%d", id, prevLen, l)
		}
		prevLen = l
	}
}

func TestDecodeRejectsInvalidCharacters(t *testing.T) {
	for _, bad := range []string{"has space", "emoji😀", "dash-here", "slash/here"} {
		if _, err := Decode(bad); err != ErrInvalidCode {
			t.Errorf("Decode(%q) = err %v, want ErrInvalidCode", bad, err)
		}
	}
}

func TestEncodeEmptyForZero(t *testing.T) {
	if got := Encode(0); got != "0" {
		t.Errorf("Encode(0) = %q, want %q", got, "0")
	}
}
