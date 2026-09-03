// Package pgp implements the project-level OpenPGP keypair AI.md PART 11
// "GPG Keypair Management" prescribes: an Ed25519 signing key with a
// Curve25519 encryption subkey, used to encrypt incoming security reports
// at rest, to encrypt outbound maintainer notifications, and to seed the
// `Encryption:` line in /.well-known/security.txt.
//
// The implementation is pure Go (github.com/ProtonMail/go-crypto), so it
// builds with CGO_ENABLED=0 like the rest of the project.
package pgp

import (
	"bytes"
	"crypto"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

// KeyLifetimeYears is the validity period AI.md PART 11 requires:
// "Expires 2 years from generation."
const KeyLifetimeYears = 2

// ErrNoKeypair is returned when an operation needs a keypair and none has
// been generated yet.
var ErrNoKeypair = errors.New("pgp: no keypair has been generated")

// ErrNoEntity is returned when armored key material parses but contains no
// OpenPGP entity.
var ErrNoEntity = errors.New("pgp: armored input contains no OpenPGP key")

// Keypair is one generated project keypair together with the metadata
// AI.md PART 11 "Keypair properties stored in DB" tracks.
type Keypair struct {
	Entity      *openpgp.Entity
	Fingerprint string
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// Identity renders the AI.md PART 11 user id for the project keypair:
// `{app_name} Security <{security_contact}>`.
func Identity(appName, contactEmail string) string {
	return fmt.Sprintf("%s Security <%s>", strings.TrimSpace(appName), strings.TrimSpace(contactEmail))
}

// IdentityName is the user-id name half — everything before the address.
func IdentityName(appName string) string {
	return strings.TrimSpace(appName) + " Security"
}

// Generate creates the Ed25519 (signing) + Curve25519 (encryption)
// keypair AI.md PART 11 "Generate" specifies, valid for two years from
// now.
//
// The generated key is an OpenPGP v4 key, so its fingerprint is the
// 160-bit fingerprint the format defines rather than a SHA-256 digest;
// AI.md's "Full SHA-256 fingerprint" wording describes the field, and the
// value stored is always whatever fingerprint the key actually has, since
// a fabricated digest would not match what any OpenPGP client computes.
func Generate(appName, contactEmail string, now time.Time) (*Keypair, error) {
	appName = strings.TrimSpace(appName)
	contactEmail = strings.TrimSpace(contactEmail)
	if appName == "" {
		return nil, errors.New("pgp: application name is required for the key identity")
	}
	if contactEmail == "" {
		return nil, errors.New("pgp: a security contact address is required for the key identity")
	}

	now = now.UTC().Truncate(time.Second)
	expires := now.AddDate(KeyLifetimeYears, 0, 0)

	cfg := &packet.Config{
		Algorithm:       packet.PubKeyAlgoEdDSA,
		Curve:           packet.Curve25519,
		DefaultHash:     crypto.SHA256,
		DefaultCipher:   packet.CipherAES256,
		KeyLifetimeSecs: uint32(expires.Sub(now).Seconds()),
		Time:            func() time.Time { return now },
	}

	entity, err := openpgp.NewEntity(IdentityName(appName), "", contactEmail, cfg)
	if err != nil {
		return nil, fmt.Errorf("pgp: generate keypair: %w", err)
	}

	return &Keypair{
		Entity:      entity,
		Fingerprint: FingerprintOf(entity),
		CreatedAt:   now,
		ExpiresAt:   expires,
	}, nil
}

// FingerprintOf returns the uppercase hex fingerprint of an entity's
// primary key.
func FingerprintOf(entity *openpgp.Entity) string {
	if entity == nil || entity.PrimaryKey == nil {
		return ""
	}
	return strings.ToUpper(hex.EncodeToString(entity.PrimaryKey.Fingerprint))
}

// ArmorPublic returns the ASCII-armored public key block — the exact bytes
// written to `{config_dir}/security/pgp.pub.asc` and served at
// /.well-known/pgp-key.asc.
func (k *Keypair) ArmorPublic() (string, error) {
	if k == nil || k.Entity == nil {
		return "", ErrNoKeypair
	}
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		return "", fmt.Errorf("pgp: armor public key: %w", err)
	}
	if err := k.Entity.Serialize(w); err != nil {
		return "", fmt.Errorf("pgp: serialize public key: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("pgp: close public key armor: %w", err)
	}
	return buf.String() + "\n", nil
}

// ArmorPrivate returns the ASCII-armored private key block. It is never
// written to disk unprotected: the caller either seals it with SealPrivate
// or hands it straight to the operator through
// `--maintenance pgp export private`.
func (k *Keypair) ArmorPrivate() (string, error) {
	if k == nil || k.Entity == nil {
		return "", ErrNoKeypair
	}
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PrivateKeyType, nil)
	if err != nil {
		return "", fmt.Errorf("pgp: armor private key: %w", err)
	}
	// SerializePrivateWithoutSigning preserves the self-signatures created
	// at generation, including their key-expiry subpacket; re-signing here
	// would silently rewrite the two-year lifetime.
	if err := k.Entity.SerializePrivateWithoutSigning(w, nil); err != nil {
		return "", fmt.Errorf("pgp: serialize private key: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("pgp: close private key armor: %w", err)
	}
	return buf.String() + "\n", nil
}

// PrimaryEmail returns the address in the entity's first user id, used to
// validate an imported key against the project identity.
func (k *Keypair) PrimaryEmail() string {
	if k == nil || k.Entity == nil {
		return ""
	}
	for _, id := range k.Entity.Identities {
		if id.UserId != nil {
			return id.UserId.Email
		}
	}
	return ""
}

// ParsePrivate reads an ASCII-armored private key block back into a
// Keypair, recovering its creation and expiry times from the key's own
// self-signature.
func ParsePrivate(armored string) (*Keypair, error) {
	entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(armored))
	if err != nil {
		return nil, fmt.Errorf("pgp: read private key: %w", err)
	}
	if len(entities) == 0 {
		return nil, ErrNoEntity
	}
	entity := entities[0]
	if entity.PrivateKey == nil {
		return nil, errors.New("pgp: armored input is a public key, not a private key")
	}
	created := entity.PrimaryKey.CreationTime.UTC()
	return &Keypair{
		Entity:      entity,
		Fingerprint: FingerprintOf(entity),
		CreatedAt:   created,
		ExpiresAt:   expiryOf(entity, created),
	}, nil
}

// ParsePublic reads one or more ASCII-armored public keys. Both the
// project's own pubkey and a researcher's submitted key go through here.
func ParsePublic(armored string) (openpgp.EntityList, error) {
	entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(armored))
	if err != nil {
		return nil, fmt.Errorf("pgp: read public key: %w", err)
	}
	if len(entities) == 0 {
		return nil, ErrNoEntity
	}
	return entities, nil
}

// expiryOf resolves an entity's key expiry from its primary self-signature,
// falling back to the two-year default when the key carries no lifetime.
func expiryOf(entity *openpgp.Entity, created time.Time) time.Time {
	for _, id := range entity.Identities {
		if id.SelfSignature != nil && id.SelfSignature.KeyLifetimeSecs != nil {
			return created.Add(time.Duration(*id.SelfSignature.KeyLifetimeSecs) * time.Second)
		}
	}
	return created.AddDate(KeyLifetimeYears, 0, 0)
}

// SignPublicKeyOf certifies newKey's identities with old's primary key,
// implementing AI.md PART 11 "Rotate": "signs the new pubkey with the old
// key". The certification lets anyone holding the retired key verify that
// the replacement really came from the same operator.
func SignPublicKeyOf(newKey, old *Keypair, now time.Time) error {
	if newKey == nil || newKey.Entity == nil || old == nil || old.Entity == nil {
		return ErrNoKeypair
	}
	if old.Entity.PrivateKey == nil {
		return errors.New("pgp: the previous private key is required to sign the new public key")
	}
	cfg := &packet.Config{
		DefaultHash: crypto.SHA256,
		Time:        func() time.Time { return now.UTC() },
	}
	for name := range newKey.Entity.Identities {
		if err := newKey.Entity.SignIdentity(name, old.Entity, cfg); err != nil {
			return fmt.Errorf("pgp: sign new key with previous key: %w", err)
		}
	}
	return nil
}
