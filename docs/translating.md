# Translation Guide

The server and `shortner-cli` embed the same locale catalogs from
`src/common/i18n/locales/`. The eight supported languages are English (`en`),
Spanish (`es`), Chinese (`zh`), French (`fr`), Arabic (`ar`), German (`de`),
Italian (`it`), and Japanese (`ja`). English is the reference and fallback.

## Editing a catalog

Locale files are UTF-8 JSON with two-space indentation. Preserve the key names
and structure from `en.json`; translate every user-facing value. Keep the
`meta` language code, English name, native name, direction, and version in
sync with the registry in `src/common/i18n/i18n.go`.

Keep every interpolation token exactly as written, including braces and
spelling, for example `{current}` and `{total}`. Do not add printf verbs,
HTML, or omit text. Plural messages under `plurals` use the CLDR categories
registered for that language: English, Spanish, French, and German use `one`
and `other`; Italian uses `one`, `many`, and `other`; Arabic uses `zero`,
`one`, `two`, `few`, `many`, and `other`; Chinese and Japanese use `other`.
An explicit `zero` form is also allowed for another locale.

## Glossary

Reuse these terms consistently when translating the corresponding concepts:

| Language | Token | Rotate | Revoke | Linked tokens |
|----------|-------|--------|--------|---------------|
| English | token | Rotate | Revoke | Linked tokens |
| Spanish | token | Rotar | Revocar | Tokens vinculados |
| French | jeton | Renouveler | Révoquer | Jetons liés |
| German | Token | Erneuern | Widerrufen | Verknüpfte Tokens |
| Italian | token | Ruotare | Revocare | Token collegati |
| Arabic | رمز | تدوير | إلغاء | الرموز المرتبطة |
| Chinese | 令牌 | 轮换 | 撤销 | 关联令牌 |
| Japanese | トークン | 更新 | 取り消す | リンクトークン |

Keep product and brand names, protocol and technology names (API, Tor,
`.onion`, I2P, JSON), code, file names, configuration keys, URLs, and literal
values entered by users (such as `dark` and `auto`) unchanged. Preserve the
formal, neutral tone used by the existing catalog. Arabic uses right-to-left
layout; the other listed languages use left-to-right layout.

## Review and validation

Review translations for meaning, natural phrasing, screen-reader clarity, and
fit at 320, 768, and 1280 pixels. Keep the key set, placeholders, metadata,
and plural forms aligned. The package tests check embedded catalog parity and
registry requirements as part of the normal test suite. Run the suite with
`make test`; locale validation is part of the package test suite.

## Contributing a language

Add a complete locale file, a registry entry with native name, direction, and
CLDR categories, and update the supported-language documentation in the same
change. `AI.md` is read-only; template changes belong to the template
maintainer. Update existing translations whenever English source text changes.
Translations can be refined by human contributors. Add a translator credit
only with that translator's agreement.
