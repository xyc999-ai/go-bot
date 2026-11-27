# Client languages

Web, Desktop and mobile share these language definitions and translation
catalogues. Supported languages are English (`en`), Simplified Chinese (`zh-CN`),
Traditional Chinese (`zh-TW`), Spanish (`es`), Japanese (`ja`), Brazilian Portuguese
(`pt-BR`), French (`fr`), German (`de`) and Korean (`ko`). The picker shows each
language's own name.

On first launch, clients select the first supported system/browser preference.
Chinese script tags take precedence over regions: `zh-Hant`, Taiwan, Hong Kong
and Macao select Traditional Chinese; `zh-Hans` and other Chinese preferences
select Simplified Chinese. Portuguese preferences select Brazilian Portuguese.
Unsupported preferences fall back to English. A saved manual choice takes
precedence and survives restarts in each client.

Translations ship with the app; switching languages makes no translation service
request. User messages, Agent responses and model settings are not translated
or changed. The initial welcome message uses the chosen language when a main chat is first
created. It is stored in chat history and is not rewritten by later language
changes. Server welcome text lives in `internal/clientlocale/welcome.json`.
Plugin-provided names, descriptions and tutorials use an exact language match
when available, otherwise English.

## Updating translations

- Use English source messages as keys. Add new messages to `en.json` and each
  translated catalogue, preserving placeholders such as `{count}`, `{name}` and
  `${credential:name}` exactly. Keep service names, API identifiers and code intact.
- Simplified Chinese retains its existing feature dictionaries through
  `chinese.ts`. `mobile-aliases.json` maps the mobile client's existing Chinese
  source labels to shared English keys; `mobile-notices.json` retains its notice
  wording. Add mobile-only messages here too. Missing translations return the
  English key, with placeholder substitution still applied.
- Review translations in context, especially action verbs, permission choices
  and longer instructions. Traditional Chinese has its own catalogue and wording.
- Native Desktop menu and notification keys are listed in `native-zh-CN.json`.
  `desktop/scripts/sync-locales.cjs` packages the shared matcher and the relevant
  translations during Desktop builds and tests; do not edit generated
  `desktop/electron/locales/` files.
- Keep `mobile/app.json`'s `expo-localization.supportedLocales` aligned with
  `languages.json`. Native configuration changes apply on the next Expo Prebuild
  and native build; they do not require a version bump during development.

Run `npm test` in all three client directories, build Web and Desktop, and run
`npm run typecheck` in mobile. The shared locale tests verify coverage,
placeholders, preference matching, English fallback and date/unit formatting.
