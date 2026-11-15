# Translation Guide

The server and `shortner-cli` use the embedded JSON catalogs in
`src/common/i18n/locales/`. The supported locales are English (`en`), Spanish
(`es`), Chinese (`zh`), French (`fr`), Arabic (`ar`), German (`de`), and
Japanese (`ja`). English is the reference catalog and fallback language.

## Editing a catalog

Each file is UTF-8 JSON. Keep the `meta` object, including the language code,
native display name, text direction (`ltr` or `rtl`), and catalog version.
Translation keys are nested by feature; the runtime flattens these into
dot-separated names such as `common.save` and `home.submit`. Preserve every
English key in each locale and translate its value rather than renaming the
key.

Placeholders use literal braces, for example `Page {current} of {total}`.
Keep every placeholder exactly as written so runtime interpolation can supply
the value. Pluralized messages live under `plurals` and must provide the
categories required by that language's CLDR rules. Do not use printf-style
verbs or HTML in translation values.

## Review and validation

Before submitting a catalog change, check that the text fits the page or
control, keeps the original meaning, and remains understandable to screen
reader users. Arabic is right-to-left; other current catalogs are left-to-
right. Preserve product names, command names, URLs, code, and placeholders
where translating them would make them unusable.

Run the repository validator from the project root:

```sh
make i18n-validate
```

The validator checks JSON syntax, locale metadata, key parity with English,
placeholder parity, and required plural categories. The project test target
runs it as well. Keep the locale's existing formatting and end the file with
one newline.

## Contributing a language

Add a complete locale JSON file named with its language code, update the
supported-language list in `AI.md` through the template-maintainer workflow,
and ensure all locale catalogs contain the same key set. `AI.md` is the
project's read-only implementation specification; report template defects
through the project-maintainer issue process rather than editing it here.

Translation credits should be added only with the translator's agreement.
