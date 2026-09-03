package pgp

import (
	"bytes"
	"crypto"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

// messageBlockType is the armor header of an OpenPGP encrypted message.
const messageBlockType = "PGP MESSAGE"

// ErrNoRecipients is returned when EncryptArmored is given no usable
// recipient key.
var ErrNoRecipients = errors.New("pgp: no recipient key to encrypt to")

// EncryptArmored encrypts plaintext to every recipient and returns a
// single ASCII-armored `-----BEGIN PGP MESSAGE-----` block.
//
// AI.md PART 11 "Submission Flow" step 4 calls for a PGP-encrypted
// maintainer email; a self-contained inline armored block is used rather
// than a PGP/MIME multipart body because the project ships no MIME
// composer (PART 17's mailer writes a single RFC 5322 text body), and an
// inline block is decryptable by every OpenPGP implementation.
func EncryptArmored(recipients openpgp.EntityList, plaintext []byte) (string, error) {
	if len(recipients) == 0 {
		return "", ErrNoRecipients
	}

	var buf bytes.Buffer
	armorWriter, err := armor.Encode(&buf, messageBlockType, nil)
	if err != nil {
		return "", fmt.Errorf("pgp: armor message: %w", err)
	}

	cfg := &packet.Config{
		DefaultHash:            crypto.SHA256,
		DefaultCipher:          packet.CipherAES256,
		DefaultCompressionAlgo: packet.CompressionZLIB,
	}
	cipherWriter, err := openpgp.Encrypt(armorWriter, recipients, nil, nil, cfg)
	if err != nil {
		return "", fmt.Errorf("pgp: encrypt message: %w", err)
	}
	if _, err := cipherWriter.Write(plaintext); err != nil {
		return "", fmt.Errorf("pgp: write message: %w", err)
	}
	if err := cipherWriter.Close(); err != nil {
		return "", fmt.Errorf("pgp: finish message: %w", err)
	}
	if err := armorWriter.Close(); err != nil {
		return "", fmt.Errorf("pgp: close message armor: %w", err)
	}
	return buf.String() + "\n", nil
}

// DecryptArmored reverses EncryptArmored using the keypair's private key.
// It exists so an operator (and the test suite) can confirm that what the
// server sealed is actually recoverable with the exported key.
func DecryptArmored(key *Keypair, armored string) ([]byte, error) {
	if key == nil || key.Entity == nil || key.Entity.PrivateKey == nil {
		return nil, ErrNoKeypair
	}
	block, err := armor.Decode(strings.NewReader(armored))
	if err != nil {
		return nil, fmt.Errorf("pgp: decode message armor: %w", err)
	}
	if block.Type != messageBlockType {
		return nil, fmt.Errorf("pgp: expected %q, got %q", messageBlockType, block.Type)
	}
	md, err := openpgp.ReadMessage(block.Body, openpgp.EntityList{key.Entity}, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("pgp: read message: %w", err)
	}
	body, err := io.ReadAll(md.UnverifiedBody)
	if err != nil {
		return nil, fmt.Errorf("pgp: read message body: %w", err)
	}
	return body, nil
}

// IsArmoredMessage reports whether s is an ASCII-armored PGP message. The
// security-report store uses it to tell a PGP-encrypted body apart from
// the AES-256-GCM base64 fallback without needing a schema column.
func IsArmoredMessage(s string) bool {
	return strings.HasPrefix(strings.TrimSpace(s), "-----BEGIN "+messageBlockType+"-----")
}
