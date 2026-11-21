# Model catalog and pricing

This package resolves model metadata independently from source adapters. An
adapter reports exactly what local evidence says; the catalog separately maps
that observed string to canonical identity, provider, and pricing key, and the
price registry turns token counts into an API-equivalent estimate.

Both the CLI and the API build the same bundled catalog. The server's result is
authoritative; the client's is an offline preview.

## Where prices come from

| File | Role | Edited by |
| --- | --- | --- |
| `data/litellm.lock.json` | Pins the upstream file: repository, path, full commit, SHA-256, retrieval date, and the providers and modes to keep | A reviewer, when moving the pin |
| `data/litellm_prices.json` | The vendored subset of that file | `make prices` only |
| `data/overrides.json` | Reviewed additions: prices the snapshot lacks, and labels deliberately left unpriced | A reviewer, with a citation |
| `data/LICENSE-litellm.txt` | MIT notice for the vendored data | Nobody |

The upstream file is LiteLLM's `model_prices_and_context_window.json`.
`cmd/pricegen` downloads the pinned commit, refuses content whose SHA-256
differs from the pin, and writes a deterministic snapshot. It keeps an entry
only when it is:

- a bare first-party model ID for a pinned provider and mode, not a prefixed
  route through another vendor;
- priced per token, with an input or output rate;
- not an `alias_of` entry. A moving alias such as a `-latest` name would
  reprice history whenever the provider repoints it.

For each kept model it stores five rates and nothing else: uncached input,
cache read, base cache write, 1-hour cache write, and output. Rates are integer
micro-US-dollars per million tokens, converted exactly from the upstream
decimal. A price that is not a whole number in that unit fails generation
instead of being rounded.

### Moving the pin

1. Choose an upstream commit and set `commit`, `sha256`, and `retrieved` in
   `data/litellm.lock.json`. The digest is of the raw file at that commit.
2. Run `make prices`. It needs network access; `go run ./cmd/pricegen -input
   <file>` reads a local copy instead and applies the same checksum.
3. Review the diff of `data/litellm_prices.json`.
4. Update `bundledRevision` in `bundled_test.go` to the new value the failing
   test prints. The revision is derived from the content, so it cannot be
   forgotten. The next API start reprices every stored bucket to it in one
   transaction; see [the server guide](../../server/README.md).

### Overrides

`overrides.json` exists for models newer than the snapshot. Every price entry
must cite the provider's own pricing page over HTTPS and the date it was read;
`Build` rejects any other host. Copy the published numbers. Never estimate a
price from a similar model, and never add an entry without reading the page.
When no official price exists, leave the model out so it stays unpriced.

The `unpriced` list records identifiers that are understood and intentionally
have no model, such as a client routing label. Each carries a reason, a note,
and the page that was checked, so it reads as a decision rather than a gap.

The bundled overrides currently add no prices. The snapshot's rates for the
Claude models in the regression test were compared with
<https://platform.claude.com/docs/en/about-claude/pricing> on November 21, 2026
and matched.

## Resolution rules

Resolution tries an exact catalog ID first. Two explicit suffix rules follow,
and each requires an exact match for what remains:

| Rule | Example | Recorded match |
| --- | --- | --- |
| Strip a client context-window marker | `claude-opus-5-5[1m]` → `claude-opus-5-5` | `reviewed` |
| Strip a trailing calendar date, `-YYYYMMDD` or `-YYYY-MM-DD` | a dated snapshot ID → its undated model | `alias` |

A rule never guesses a model family, version, or size, and it only ever weakens
the recorded match kind. `[1m]` is safe to strip because a model whose price
changes with prompt length is already unpriced (see below); for every other
model the provider charges the same rates across the whole context window.

Routing labels `auto`, `default`, `router`, and `smart` stay unknown, with or
without a suffix. Source-inferred attribution stays labelled `inferred` after a
canonical match.

Changing a rule changes what stored usage resolves to. Increment
`aliasRulesVersion` in `bundled.go` in the same change so the revision moves.

## Why a model can be unpriced

Every resolution without a pricing key carries a reason:

| Reason | Meaning |
| --- | --- |
| `unknown_model` | The identifier is not in the catalog |
| `model_unavailable` | The source reported no model |
| `ambiguous_routing_alias` | A routing alias that names no single model |
| `routing_label` | A reviewed client label with no identifiable model |
| `context_tiered` | The provider charges more above a prompt length |
| `no_published_price` | A known model with no reviewed price |

`context_tiered` is detected from upstream fields such as
`input_cost_per_token_above_272k_tokens`. The applicable tier depends on each
request's prompt length, and usage buckets do not carry that, so the model
resolves canonically but is not priced. Pricing it at the base tier would be a
guess.

Unpriced tokens still count toward token totals. They are reported as unpriced
so cost coverage stays visible.

## Estimates

`EstimateTokens` prices five categories:

| Tokens | Rate |
| --- | --- |
| Uncached input | Input |
| Cache read | Cache read |
| Cache write, 5-minute lifetime | Base cache write |
| Cache write, 1-hour lifetime | 1-hour cache write |
| Output, including reasoning | Output |

A cache write whose lifetime the source did not report is priced at the base
rate and marks the estimate a lower bound. An estimate is also a lower bound
when a billable category is unavailable or when known tokens have no rate; the
latter are counted in `UnpricedTokens`. Arithmetic is exact big-integer math
with one floor to whole micro-dollars per estimate.

Estimates use standard-tier, global-routing rates. Batch, fast-mode,
data-residency, and other service-tier multipliers are not applied, because the
normalized record does not carry that evidence.

Every value is labelled an `API-equivalent estimate`. It is not subscription
usage or actual spending.
