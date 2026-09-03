package cmd

import (
	"fmt"
	"strings"

	clientconfig "github.com/apimgr/shortner/src/client/config"
)

// Flags holds every parsed command-line flag. AI.md PART 32 requires the
// same flag to work as both `--flag=value` and `--flag value`, and flags may
// appear before or after the positional arguments.
type Flags struct {
	Help    bool
	Version bool
	Debug   bool
	Force   bool
	Quiet   bool
	Verbose bool

	Shell    string
	ShellSet bool

	Server    string
	Token     string
	TokenFile string
	Config    string
	Color     string
	Lang      string
	Output    string

	Slug   string
	URL    string
	Expire string
	Limit  string
	Page   string

	Update    string
	UpdateSet bool

	// Args holds the positional arguments in the order they appeared.
	Args []string
}

// valueFlags lists flags that always consume the following argument.
var valueFlags = map[string]bool{
	"--server":     true,
	"--token":      true,
	"--token-file": true,
	"--config":     true,
	"--color":      true,
	"--lang":       true,
	"--output":     true,
	"--slug":       true,
	"--url":        true,
	"--expire":     true,
	"--limit":      true,
	"--page":       true,
}

// optionalValueFlags lists flags whose value may be omitted entirely.
var optionalValueFlags = map[string]bool{
	"--shell":  true,
	"--update": true,
}

// boolFlags lists flags that stand alone but also accept an explicit truthy
// or falsey word, e.g. `--debug`, `--debug=yes`, `--debug no`.
var boolFlags = map[string]bool{
	"--help":    true,
	"-h":        true,
	"--version": true,
	"-v":        true,
	"--debug":   true,
	"--force":   true,
	"--quiet":   true,
	"--verbose": true,
}

// ParseFlags parses argv (without the program name) into Flags. Unknown
// flags are a usage error; everything else becomes a positional argument.
func ParseFlags(args []string) (Flags, error) {
	var f Flags
	f.Args = []string{}

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if arg == "--" {
			f.Args = append(f.Args, args[i+1:]...)
			break
		}
		if arg == "" || !strings.HasPrefix(arg, "-") || arg == "-" {
			f.Args = append(f.Args, arg)
			continue
		}

		name := arg
		value := ""
		hasValue := false
		if idx := strings.Index(arg, "="); idx > 0 {
			name = arg[:idx]
			value = arg[idx+1:]
			hasValue = true
		}
		// A single-dash long option is accepted so `-server` behaves like
		// `--server`; only -h and -v are true short forms.
		if !strings.HasPrefix(name, "--") && len(name) > 2 {
			name = "-" + name
		}

		switch {
		case boolFlags[name]:
			if !hasValue && i+1 < len(args) && isBooleanWord(args[i+1]) {
				value = args[i+1]
				hasValue = true
				i++
			}
			enabled := true
			if hasValue {
				enabled = clientconfig.ParseBool(value, true)
			}
			if err := f.setBool(name, enabled); err != nil {
				return f, err
			}

		case valueFlags[name]:
			if !hasValue {
				if i+1 >= len(args) {
					return f, fmt.Errorf("flag %s requires a value", name)
				}
				value = args[i+1]
				i++
			}
			if err := f.setValue(name, value); err != nil {
				return f, err
			}

		case optionalValueFlags[name]:
			if !hasValue && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				value = args[i+1]
				i++
			}
			if err := f.setValue(name, value); err != nil {
				return f, err
			}

		default:
			return f, fmt.Errorf("unknown flag: %s", arg)
		}
	}

	return f, nil
}

// setBool assigns a parsed boolean flag.
func (f *Flags) setBool(name string, enabled bool) error {
	switch name {
	case "--help", "-h":
		f.Help = enabled
	case "--version", "-v":
		f.Version = enabled
	case "--debug":
		f.Debug = enabled
	case "--force":
		f.Force = enabled
	case "--quiet":
		f.Quiet = enabled
	case "--verbose":
		f.Verbose = enabled
	default:
		return fmt.Errorf("unknown flag: %s", name)
	}
	return nil
}

// setValue assigns a parsed value flag.
func (f *Flags) setValue(name, value string) error {
	switch name {
	case "--server":
		f.Server = value
	case "--token":
		f.Token = value
	case "--token-file":
		f.TokenFile = value
	case "--config":
		f.Config = value
	case "--color":
		f.Color = value
	case "--lang":
		f.Lang = value
	case "--output":
		f.Output = value
	case "--slug":
		f.Slug = value
	case "--url":
		f.URL = value
	case "--expire":
		f.Expire = value
	case "--limit":
		f.Limit = value
	case "--page":
		f.Page = value
	case "--shell":
		f.Shell = value
		f.ShellSet = true
	case "--update":
		f.Update = value
		f.UpdateSet = true
	default:
		return fmt.Errorf("unknown flag: %s", name)
	}
	return nil
}

// isBooleanWord reports whether an argument is one of the project's truthy
// or falsey words, which lets `--debug yes` and `--debug no` both work.
func isBooleanWord(arg string) bool {
	switch strings.ToLower(arg) {
	case "true", "yes", "on", "1", "enable", "enabled",
		"false", "no", "off", "0", "disable", "disabled", "none":
		return true
	default:
		return false
	}
}
