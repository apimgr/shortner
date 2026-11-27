package security

import "sync"

// SecretKeyring holds the in-memory current and grace-period project keys.
type SecretKeyring struct {
	mu             sync.RWMutex
	cookieCurrent  []byte
	cookiePrevious []byte
	csrfCurrent    []byte
	csrfPrevious   []byte
}

// NewSecretKeyring creates a keyring with defensive copies of each key.
func NewSecretKeyring(cookieCurrent, cookiePrevious, csrfCurrent, csrfPrevious []byte) *SecretKeyring {
	keyring := &SecretKeyring{}
	keyring.Replace(cookieCurrent, cookiePrevious, csrfCurrent, csrfPrevious)
	return keyring
}

// Replace atomically installs current and grace-period key copies.
func (k *SecretKeyring) Replace(cookieCurrent, cookiePrevious, csrfCurrent, csrfPrevious []byte) {
	if k == nil {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.cookieCurrent = append(k.cookieCurrent[:0], cookieCurrent...)
	k.cookiePrevious = append(k.cookiePrevious[:0], cookiePrevious...)
	k.csrfCurrent = append(k.csrfCurrent[:0], csrfCurrent...)
	k.csrfPrevious = append(k.csrfPrevious[:0], csrfPrevious...)
}

// Refresh loads and atomically replaces all keys while excluding concurrent
// readers from a partially updated key set.
func (k *SecretKeyring) Refresh(load func() ([]byte, []byte, []byte, []byte, error)) error {
	if k == nil {
		return nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	cookieCurrent, cookiePrevious, csrfCurrent, csrfPrevious, err := load()
	if err != nil {
		return err
	}
	k.cookieCurrent = append(k.cookieCurrent[:0], cookieCurrent...)
	k.cookiePrevious = append(k.cookiePrevious[:0], cookiePrevious...)
	k.csrfCurrent = append(k.csrfCurrent[:0], csrfCurrent...)
	k.csrfPrevious = append(k.csrfPrevious[:0], csrfPrevious...)
	return nil
}

// CookieKeys returns defensive copies of the cookie signing keys.
func (k *SecretKeyring) CookieKeys() (current, previous []byte) {
	if k == nil {
		return nil, nil
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	return append([]byte(nil), k.cookieCurrent...), append([]byte(nil), k.cookiePrevious...)
}

// CSRFKeys returns defensive copies of the CSRF signing keys.
func (k *SecretKeyring) CSRFKeys() (current, previous []byte) {
	if k == nil {
		return nil, nil
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	return append([]byte(nil), k.csrfCurrent...), append([]byte(nil), k.csrfPrevious...)
}
