// CLI output language, per AI.md PART 30 "CLI/Agent/Server Output
// Translation". Every human-facing string the server binary prints goes
// through cliT/cliTF so `--lang`, the config file's default language, and
// the LANG/LC_ALL environment all change the output language.
package main

import (
	"github.com/apimgr/shortner/src/common/i18n"
)

// cliLang is the resolved output language for this process. It starts as
// English so any message printed before flag parsing is still valid, and
// is narrowed by setCLILanguage as soon as the flags (and later the
// config file) are known.
var cliLang = i18n.DefaultLanguage

// setCLILanguage applies AI.md PART 30's priority chain: the --lang flag
// wins, then the config file's language, then LC_ALL/LANG, then English.
// An unsupported value silently resolves to English rather than erroring.
func setCLILanguage(flagLang, configLang string) {
	cliLang = i18n.CLILanguage(flagLang, configLang)
}

// cliT returns the translation of key in the process output language.
func cliT(key string) string {
	return i18n.Translate(cliLang, key)
}

// cliTF returns the translation of key with its {token} placeholders
// replaced by args.
func cliTF(key string, args map[string]string) string {
	return i18n.TranslateFormat(cliLang, key, args)
}
