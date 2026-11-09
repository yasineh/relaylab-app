package token

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func testKeys(t *testing.T, fill byte) *Keys {
	t.Helper()
	k, err := NewKeys(bytes.Repeat([]byte{fill}, 32), bytes.Repeat([]byte{fill + 1}, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestGenerateIsUniqueAndWellFormed(t *testing.T) {
	seen := make(map[string]struct{}, 10_000)
	for range 10_000 {
		tok, err := Generate()
		if err != nil {
			t.Fatal(err)
		}
		if len(tok) != Length || len(tok) < 43 || !WellFormed(tok) {
			t.Fatalf("malformed token %q", tok)
		}
		if _, dup := seen[tok]; dup {
			t.Fatalf("duplicate token %q", tok)
		}
		seen[tok] = struct{}{}
	}
}

func TestEncodeBoundaries(t *testing.T) {
	zero := encode(make([]byte, RawBytes))
	max := encode(bytes.Repeat([]byte{0xff}, RawBytes))
	if zero != strings.Repeat("0", Length) || len(max) != Length || !WellFormed(max) {
		t.Fatalf("zero=%q max=%q", zero, max)
	}
	if encode([]byte{1}) == encode([]byte{2}) {
		t.Fatal("distinct inputs encoded the same")
	}
}

func TestWellFormed(t *testing.T) {
	good, _ := Generate()
	for _, bad := range []string{"", good[:42], good + "0", good[:42] + "-", good[:42] + "/", good[:42] + "é"[:1], strings.Repeat(" ", 43)} {
		if WellFormed(bad) {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestHash(t *testing.T) {
	k1, k2 := testKeys(t, 1), testKeys(t, 7)
	tok, _ := Generate()
	other, _ := Generate()
	if !bytes.Equal(k1.Hash(tok), k1.Hash(tok)) || len(k1.Hash(tok)) != 32 {
		t.Fatal("hash not deterministic or wrong size")
	}
	if bytes.Equal(k1.Hash(tok), k1.Hash(other)) || bytes.Equal(k1.Hash(tok), k2.Hash(tok)) {
		t.Fatal("hash collisions across tokens or keys")
	}
	if bytes.Contains(k1.Hash(tok), []byte(tok)) {
		t.Fatal("hash contains the token")
	}
}

func TestSealOpen(t *testing.T) {
	k := testKeys(t, 1)
	tok, _ := Generate()
	p, e := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	aad := AAD(p, e)

	sealed, err := k.Seal(tok, aad)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte(tok)) {
		t.Fatal("ciphertext contains the token")
	}
	again, _ := k.Seal(tok, aad)
	if bytes.Equal(sealed, again) {
		t.Fatal("nonce reused: identical ciphertexts")
	}
	got, err := k.Open(sealed, aad)
	if err != nil || got != tok {
		t.Fatalf("open: %q %v", got, err)
	}
}

func TestOpenRejectsTamperingWrongRowAndWrongKey(t *testing.T) {
	k := testKeys(t, 1)
	tok, _ := Generate()
	p, e := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	sealed, _ := k.Seal(tok, AAD(p, e))

	for i := range sealed {
		bad := bytes.Clone(sealed)
		bad[i] ^= 0x01
		if _, err := k.Open(bad, AAD(p, e)); err != ErrDecrypt {
			t.Fatalf("tampered byte %d: %v", i, err)
		}
	}
	other := uuid.Must(uuid.NewV7())
	if _, err := k.Open(sealed, AAD(p, other)); err != ErrDecrypt {
		t.Fatalf("swapped endpoint id: %v", err)
	}
	if _, err := k.Open(sealed, AAD(other, e)); err != ErrDecrypt {
		t.Fatalf("swapped project id: %v", err)
	}
	if _, err := testKeys(t, 9).Open(sealed, AAD(p, e)); err != ErrDecrypt {
		t.Fatalf("wrong key: %v", err)
	}
	if _, err := k.Open(sealed[:10], AAD(p, e)); err != ErrDecrypt {
		t.Fatalf("truncated: %v", err)
	}
}

func TestNewKeysValidation(t *testing.T) {
	good := bytes.Repeat([]byte{1}, 32)
	if _, err := NewKeys(good[:31], good, 1); err == nil {
		t.Error("short HMAC key accepted")
	}
	if _, err := NewKeys(good, good[:16], 1); err == nil {
		t.Error("AES-128 key accepted")
	}
	if _, err := NewKeys(good, append(good, 1), 1); err == nil {
		t.Error("33-byte encryption key accepted")
	}
}
