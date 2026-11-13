package replay

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// sealVersion prefixes every ciphertext, so the key can be rotated later.
const sealVersion = 1

// ErrOpen means override values could not be decrypted (wrong key, another
// row's ciphertext, or tampering). It carries no detail.
var ErrOpen = errors.New("replay overrides could not be decrypted")

// Sealer encrypts header override values at rest with AES-256-GCM
// (REPLAY_OVERRIDE_ENC_KEY). The replay id is additional authenticated data,
// so a ciphertext cannot be moved to another replay.
type Sealer struct{ aead cipher.AEAD }

// NewSealer returns a Sealer for a 32-byte key.
func NewSealer(key []byte) (*Sealer, error) {
	if len(key) != 32 {
		return nil, errors.New("replay override key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// Seal encrypts the overrides for replay id.
func (s *Sealer) Seal(overrides []Override, id uuid.UUID) ([]byte, error) {
	plain, err := json.Marshal(overrides)
	if err != nil {
		return nil, fmt.Errorf("encode overrides: %w", err)
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	out := append([]byte{sealVersion}, nonce...)
	return s.aead.Seal(out, nonce, plain, id[:]), nil
}

// Open decrypts overrides sealed for replay id.
func (s *Sealer) Open(ct []byte, id uuid.UUID) ([]Override, error) {
	n := s.aead.NonceSize()
	if len(ct) < 1+n || ct[0] != sealVersion {
		return nil, ErrOpen
	}
	plain, err := s.aead.Open(nil, ct[1:1+n], ct[1+n:], id[:])
	if err != nil {
		return nil, ErrOpen
	}
	var out []Override
	if err := json.Unmarshal(plain, &out); err != nil {
		return nil, ErrOpen
	}
	return out, nil
}
