// Package token creates endpoint receiving tokens and protects them at rest (D4).
//
// A token is 32 bytes (256 bits) from crypto/rand, encoded as a fixed-length,
// 43-character base62 string. It is never stored: the database keeps
//
//   - Hash(token) = HMAC-SHA256(hmacKey, token), unique, for O(1) lookup during
//     ingestion without comparing plaintext; and
//   - Seal(token) = AES-256-GCM(encKey, token) with a random 12-byte nonce and
//     the project and endpoint ids as additional authenticated data, so the
//     owner can see the URL again and a ciphertext cannot be moved to another row.
package token

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/google/uuid"
)

const (
	// RawBytes is the token's entropy: 256 bits.
	RawBytes = 32
	// Length is the encoded token length. 62^43 > 2^256, so every 32-byte value
	// fits; shorter encodings are left-padded with '0'.
	Length = 43
	// KeyBytes is the size of both keys (HMAC-SHA256 and AES-256).
	KeyBytes = 32

	alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// ErrDecrypt is returned when a ciphertext cannot be opened (wrong key, wrong
// row, or tampering). It deliberately carries no detail.
var ErrDecrypt = errors.New("endpoint token could not be decrypted")

// Keys protects tokens at rest.
type Keys struct {
	hmacKey []byte
	aead    cipher.AEAD
	version int16
}

// NewKeys builds Keys from the HMAC and encryption keys (32 bytes each).
// version is stored with each ciphertext to allow key rotation later.
func NewKeys(hmacKey, encKey []byte, version int16) (*Keys, error) {
	if len(hmacKey) < KeyBytes {
		return nil, fmt.Errorf("HMAC key must be at least %d bytes", KeyBytes)
	}
	if len(encKey) != KeyBytes {
		return nil, fmt.Errorf("encryption key must be exactly %d bytes", KeyBytes)
	}
	block, err := aes.NewCipher(encKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Keys{hmacKey: append([]byte(nil), hmacKey...), aead: aead, version: version}, nil
}

// Version is the key version written with new ciphertexts.
func (k *Keys) Version() int16 { return k.version }

// Generate returns a new random token.
func Generate() (string, error) {
	raw := make([]byte, RawBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return encode(raw), nil
}

func encode(raw []byte) string {
	n := new(big.Int).SetBytes(raw)
	base := big.NewInt(int64(len(alphabet)))
	mod := new(big.Int)
	out := make([]byte, 0, Length)
	for n.Sign() > 0 {
		n.DivMod(n, base, mod)
		out = append(out, alphabet[mod.Int64()])
	}
	for len(out) < Length {
		out = append(out, alphabet[0])
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

// WellFormed reports whether s has the shape of a token. Ingestion uses it to
// reject garbage before hashing and querying.
func WellFormed(s string) bool {
	if len(s) != Length {
		return false
	}
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(alphabet, s[i]) < 0 {
			return false
		}
	}
	return true
}

// Hash returns the lookup hash HMAC-SHA256(hmacKey, token).
func (k *Keys) Hash(token string) []byte {
	mac := hmac.New(sha256.New, k.hmacKey)
	mac.Write([]byte(token))
	return mac.Sum(nil)
}

// AAD binds a ciphertext to its endpoint row.
func AAD(projectID, endpointID uuid.UUID) []byte {
	aad := make([]byte, 0, 21+32)
	aad = append(aad, "relaylab:endpoint:v1:"...)
	aad = append(aad, projectID[:]...)
	return append(aad, endpointID[:]...)
}

// Seal encrypts token for storage: nonce || ciphertext || tag.
func (k *Keys) Seal(token string, aad []byte) ([]byte, error) {
	nonce := make([]byte, k.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("read nonce: %w", err)
	}
	return k.aead.Seal(nonce, nonce, []byte(token), aad), nil
}

// Open decrypts a stored token. Any failure returns ErrDecrypt.
func (k *Keys) Open(sealed, aad []byte) (string, error) {
	ns := k.aead.NonceSize()
	if len(sealed) < ns+k.aead.Overhead() {
		return "", ErrDecrypt
	}
	plain, err := k.aead.Open(nil, sealed[:ns], sealed[ns:], aad)
	if err != nil || !WellFormed(string(plain)) {
		return "", ErrDecrypt
	}
	return string(plain), nil
}
