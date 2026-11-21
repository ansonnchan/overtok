// Package modelcatalog resolves observed model identifiers against a bundled
// catalog and prices token counts as API-equivalent estimates.
//
// Prices come from a pinned, checksum-verified LiteLLM snapshot written by
// cmd/pricegen, plus reviewed overrides that each cite an official provider
// page. A model without verified pricing stays unpriced with a reason; no
// price is ever guessed.
package modelcatalog
