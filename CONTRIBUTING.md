# Contributing

The cheapest, most useful contributions need no Go:

- **Your monitor**: `ddcdeck discover`, then `ddcdeck model submit` (opens a prefilled issue).
- **Did a model file work?** `ddcdeck model report works|partial|broken`.
- **A translation**: copy `internal/i18n/locales/template.toml` to `<lang>.toml` and translate
  the right-hand sides. Untranslated lines can stay or be deleted.

Code:

- `make test` (vet + race tests). After touching GUI strings or `data/models/`, run
  `make generate`; CI fails when `docs/MODELS.md` or the translation template is stale.
- GUI text goes through `i18n.T("...")` so it can be translated.
- Keep core packages OS-independent; Linux-only code lives in `internal/platform/linux`.
- Design notes and the reasons behind decisions: [docs/DESIGN.md](docs/DESIGN.md).

Contributions require agreeing to the [CLA](CLA.md).
