# Model catalog and pricing

This package resolves model metadata independently from source adapters. An
adapter reports exactly what local evidence says; the catalog separately maps
that observed string to canonical identity, provider, and pricing key.

## Resolution rules

Catalog revisions contain exact model IDs, provider-documented exact aliases,
and individually reviewed source mappings. The resolver does not use prefixes,
regular expressions, or best guesses. Ambiguous routing labels such as `auto`,
`default`, `router`, and `smart` remain unknown. Source-inferred attribution
also remains labelled `inferred` after a canonical match.

Unknown models keep their observed identifier and tokens. Canonical identity,
provider price, and cost stay unavailable until a later reviewed catalog
revision adds them.

## Bundled revision

The bundled catalog and pricing registry are revision `2026-11-17`. Standard
first-party API prices were recorded from these official sources:

- OpenAI GPT-6.2 Astra and Sol model pages, retrieved November 17, 2026:
  <https://developers.openai.com/api/docs/models/gpt-6.2-astra> and
  <https://developers.openai.com/api/docs/models/gpt-6.2-sol>.
- Anthropic pricing revision dated October 11, 2026:
  <https://platform.claude.com/docs/en/about-claude/pricing?v=20261011>.

Rates are stored as integer micro-US-dollars per million tokens. Estimates use
standard API input, cache-read, 5-minute/30-minute cache-write as applicable,
and output rates. They are always labelled `API-equivalent estimate`; they are
not subscription usage or actual spending.

Claude Haiku 5.5 changes price at a prompt-length threshold. Tokenholic resolves
its canonical metadata but intentionally leaves its price unavailable because
the normalized observation does not prove the applicable context tier.
