package config

import "strings"

// Tor holds `server.tor`, per AI.md PART 31.1 "Tor Hidden Service".
//
// There is no `enabled` key by design: the hidden service is auto-enabled
// whenever a usable `tor` binary is found on the host. The only way to not
// run it is to not have Tor installed.
type Tor struct {
	// Binary is an explicit path to the `tor` executable. Empty means
	// auto-detect (well-known paths first, then $PATH).
	Binary string `yaml:"binary"`
	// UseNetwork makes the server build an outbound SOCKS dialer through
	// its own Tor instance. The hidden service itself never needs it, so
	// it is off by default.
	UseNetwork bool `yaml:"use_network"`
	// MaxCircuits caps concurrent circuits (1-128).
	MaxCircuits int `yaml:"max_circuits"`
	// CircuitTimeout is the per-circuit build timeout in seconds (10-300).
	CircuitTimeout int `yaml:"circuit_timeout"`
	// BootstrapTimeout is how long to wait for Tor to reach the network,
	// in seconds (30-600), before giving up on this start attempt.
	BootstrapTimeout int `yaml:"bootstrap_timeout"`
	// SafeLogging keeps sensitive strings out of Tor's own log. Turning it
	// off is a deanonymization risk and is warned about.
	SafeLogging bool `yaml:"safe_logging"`
	// MaxStreamsPerCircuit caps streams multiplexed onto one circuit
	// (10-500).
	MaxStreamsPerCircuit int `yaml:"max_streams_per_circuit"`
	// CloseCircuitOnStreamLimit closes a circuit that exceeds the stream
	// cap rather than silently queueing.
	CloseCircuitOnStreamLimit bool `yaml:"close_circuit_on_stream_limit"`
	// BandwidthRate is the sustained rate ("1 MB") passed to torrc.
	BandwidthRate string `yaml:"bandwidth_rate"`
	// BandwidthBurst is the burst allowance ("2 MB") passed to torrc.
	BandwidthBurst string `yaml:"bandwidth_burst"`
	// MaxMonthlyBandwidth is the accounting cap ("100 GB"). The literal
	// "unlimited" (or an empty value) disables accounting entirely.
	MaxMonthlyBandwidth string `yaml:"max_monthly_bandwidth"`
	// NumIntroPoints is the number of introduction points for the v3
	// hidden service descriptor (3-10).
	NumIntroPoints int `yaml:"num_intro_points"`
	// VirtualPort is the port the .onion address is published on. It is
	// the port Tor clients connect to, never the local backend port.
	VirtualPort int `yaml:"virtual_port"`
	// ContactEmail is the operator address published in the Tor variant of
	// security.txt (AI.md PART 12 "Tor Privacy Rules"). Advertising a
	// clearnet fallback there is forbidden, so this is the only contact a
	// Tor visitor is ever shown.
	ContactEmail string `yaml:"contact_email"`
}

// DefaultTor returns AI.md PART 31.1's `server.tor` block verbatim.
func DefaultTor() Tor {
	return Tor{
		Binary:                    "",
		UseNetwork:                false,
		MaxCircuits:               32,
		CircuitTimeout:            60,
		BootstrapTimeout:          180,
		SafeLogging:               true,
		MaxStreamsPerCircuit:      100,
		CloseCircuitOnStreamLimit: true,
		BandwidthRate:             "1 MB",
		BandwidthBurst:            "2 MB",
		MaxMonthlyBandwidth:       "100 GB",
		NumIntroPoints:            3,
		VirtualPort:               80,
		ContactEmail:              "",
	}
}

// Normalized returns a copy of c with every out-of-range or empty value
// replaced by its AI.md PART 31.1 default. Per AI.md PART 12 "Config
// Validation Rule" a bad value is never fatal — it is warned about by
// validateTor and silently replaced here, at the point of use.
func (c Tor) Normalized() Tor {
	d := DefaultTor()
	if c.MaxCircuits < 1 || c.MaxCircuits > 128 {
		c.MaxCircuits = d.MaxCircuits
	}
	if c.CircuitTimeout < 10 || c.CircuitTimeout > 300 {
		c.CircuitTimeout = d.CircuitTimeout
	}
	if c.BootstrapTimeout < 30 || c.BootstrapTimeout > 600 {
		c.BootstrapTimeout = d.BootstrapTimeout
	}
	if c.MaxStreamsPerCircuit < 10 || c.MaxStreamsPerCircuit > 500 {
		c.MaxStreamsPerCircuit = d.MaxStreamsPerCircuit
	}
	if c.NumIntroPoints < 3 || c.NumIntroPoints > 10 {
		c.NumIntroPoints = d.NumIntroPoints
	}
	if c.VirtualPort < 1 || c.VirtualPort > 65535 {
		c.VirtualPort = d.VirtualPort
	}
	if strings.TrimSpace(c.BandwidthRate) == "" {
		c.BandwidthRate = d.BandwidthRate
	}
	if strings.TrimSpace(c.BandwidthBurst) == "" {
		c.BandwidthBurst = d.BandwidthBurst
	}
	return c
}

// I2P holds `server.i2p`, per AI.md PART 31.2 "I2P Eepsite Support".
//
// Unlike Tor, I2P is strictly opt-in: with Enabled false nothing at all
// happens — no provider probe, no port allocation, no generated
// tunnels.conf, and no SAM dial.
type I2P struct {
	// Enabled is the single opt-in switch. Default false.
	Enabled bool `yaml:"enabled"`
	// Binary is an explicit path to `i2pd`. Empty means auto-detect; when
	// no i2pd is found the SAM bridge is tried instead.
	Binary string `yaml:"binary"`
	// SAMAddress is the SAMv3 bridge endpoint used by the external-router
	// provider (Model B).
	SAMAddress string `yaml:"sam_address"`
	// VirtualPort is the port the .b32.i2p address is published on.
	VirtualPort int `yaml:"virtual_port"`
	// InboundLength is the inbound tunnel hop count (0-7).
	InboundLength int `yaml:"inbound_length"`
	// OutboundLength is the outbound tunnel hop count (0-7).
	OutboundLength int `yaml:"outbound_length"`
	// InboundQuantity is the number of parallel inbound tunnels (1-16).
	InboundQuantity int `yaml:"inbound_quantity"`
	// OutboundQuantity is the number of parallel outbound tunnels (1-16).
	OutboundQuantity int `yaml:"outbound_quantity"`
	// SignatureType is the destination key signature type (7 =
	// EdDSA-SHA512-Ed25519).
	SignatureType int `yaml:"signature_type"`
	// BootstrapTimeout is how long to wait for the eepsite destination to
	// become ready, in seconds (30-600).
	BootstrapTimeout int `yaml:"bootstrap_timeout"`
}

// DefaultI2P returns AI.md PART 31.2's `server.i2p` block verbatim,
// including its `enabled: false` opt-in default.
func DefaultI2P() I2P {
	return I2P{
		Enabled:          false,
		Binary:           "",
		SAMAddress:       "127.0.0.1:7656",
		VirtualPort:      80,
		InboundLength:    3,
		OutboundLength:   3,
		InboundQuantity:  5,
		OutboundQuantity: 5,
		SignatureType:    7,
		BootstrapTimeout: 300,
	}
}

// Normalized returns a copy of c with every out-of-range or empty value
// replaced by its AI.md PART 31.2 default. Enabled is never rewritten —
// opt-in is the operator's decision alone.
func (c I2P) Normalized() I2P {
	d := DefaultI2P()
	if !strings.Contains(c.SAMAddress, ":") {
		c.SAMAddress = d.SAMAddress
	}
	if c.VirtualPort < 1 || c.VirtualPort > 65535 {
		c.VirtualPort = d.VirtualPort
	}
	if c.InboundLength < 0 || c.InboundLength > 7 {
		c.InboundLength = d.InboundLength
	}
	if c.OutboundLength < 0 || c.OutboundLength > 7 {
		c.OutboundLength = d.OutboundLength
	}
	if c.InboundQuantity < 1 || c.InboundQuantity > 16 {
		c.InboundQuantity = d.InboundQuantity
	}
	if c.OutboundQuantity < 1 || c.OutboundQuantity > 16 {
		c.OutboundQuantity = d.OutboundQuantity
	}
	if c.SignatureType < 0 {
		c.SignatureType = d.SignatureType
	}
	if c.BootstrapTimeout < 30 || c.BootstrapTimeout > 600 {
		c.BootstrapTimeout = d.BootstrapTimeout
	}
	return c
}

// validateTor reports non-fatal problems with `server.tor`. Per AI.md
// PART 12 "Config Validation Rule" an invalid value is warned about and
// replaced by its framework default at use time (see Tor.Normalized),
// never treated as a startup failure.
func validateTor(c Tor) []string {
	return ValidateTor(c)
}

// ValidateTor is validateTor exported for the `tor validate` CLI command,
// which needs the Tor warnings on their own rather than mixed into the
// whole-config result.
func ValidateTor(c Tor) []string {
	var warnings []string
	d := DefaultTor()
	if c.MaxCircuits < 1 || c.MaxCircuits > 128 {
		warnings = append(warnings, "server.tor.max_circuits: must be between 1 and 128, using the default")
	}
	if c.CircuitTimeout < 10 || c.CircuitTimeout > 300 {
		warnings = append(warnings, "server.tor.circuit_timeout: must be between 10 and 300 seconds, using the default")
	}
	if c.BootstrapTimeout < 30 || c.BootstrapTimeout > 600 {
		warnings = append(warnings, "server.tor.bootstrap_timeout: must be between 30 and 600 seconds, using the default")
	}
	if c.MaxStreamsPerCircuit < 10 || c.MaxStreamsPerCircuit > 500 {
		warnings = append(warnings, "server.tor.max_streams_per_circuit: must be between 10 and 500, using the default")
	}
	if c.NumIntroPoints < 3 || c.NumIntroPoints > 10 {
		warnings = append(warnings, "server.tor.num_intro_points: must be between 3 and 10, using the default")
	}
	if c.VirtualPort < 1 || c.VirtualPort > 65535 {
		warnings = append(warnings, "server.tor.virtual_port: must be between 1 and 65535, using the default")
	}
	if !c.SafeLogging {
		warnings = append(warnings, "server.tor.safe_logging: disabled — Tor's own log may record onion addresses and client data")
	}
	if strings.TrimSpace(c.BandwidthRate) == "" {
		warnings = append(warnings, "server.tor.bandwidth_rate: empty, using '"+d.BandwidthRate+"'")
	}
	if strings.TrimSpace(c.BandwidthBurst) == "" {
		warnings = append(warnings, "server.tor.bandwidth_burst: empty, using '"+d.BandwidthBurst+"'")
	}
	if c.ContactEmail != "" && !strings.Contains(c.ContactEmail, "@") {
		warnings = append(warnings, "server.tor.contact_email: '"+c.ContactEmail+"' is not an email address and will not be published")
	}
	return warnings
}

// validateI2P reports non-fatal problems with `server.i2p`. Every check is
// skipped while I2P is disabled, so an operator who never opted in is
// never warned about defaults they are not using.
func validateI2P(c I2P) []string {
	return ValidateI2P(c)
}

// ValidateI2P is validateI2P exported for the `i2p validate` CLI command,
// which needs the I2P warnings on their own rather than mixed into the
// whole-config result.
func ValidateI2P(c I2P) []string {
	if !c.Enabled {
		return nil
	}
	var warnings []string
	if !strings.Contains(c.SAMAddress, ":") {
		warnings = append(warnings, "server.i2p.sam_address: '"+c.SAMAddress+"' is not a host:port address, using '"+DefaultI2P().SAMAddress+"'")
	}
	if c.VirtualPort < 1 || c.VirtualPort > 65535 {
		warnings = append(warnings, "server.i2p.virtual_port: must be between 1 and 65535, using the default")
	}
	if c.InboundLength < 0 || c.InboundLength > 7 {
		warnings = append(warnings, "server.i2p.inbound_length: must be between 0 and 7, using the default")
	}
	if c.OutboundLength < 0 || c.OutboundLength > 7 {
		warnings = append(warnings, "server.i2p.outbound_length: must be between 0 and 7, using the default")
	}
	if c.InboundQuantity < 1 || c.InboundQuantity > 16 {
		warnings = append(warnings, "server.i2p.inbound_quantity: must be between 1 and 16, using the default")
	}
	if c.OutboundQuantity < 1 || c.OutboundQuantity > 16 {
		warnings = append(warnings, "server.i2p.outbound_quantity: must be between 1 and 16, using the default")
	}
	if c.SignatureType < 0 {
		warnings = append(warnings, "server.i2p.signature_type: must not be negative, using the default")
	}
	if c.BootstrapTimeout < 30 || c.BootstrapTimeout > 600 {
		warnings = append(warnings, "server.i2p.bootstrap_timeout: must be between 30 and 600 seconds, using the default")
	}
	return warnings
}
