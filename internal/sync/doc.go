// Package sync defines the privacy-allowlisted absolute-bucket protocol and
// native outbox client.
//
// Protocol v2 uploads observed evidence only: the model identifier a source
// recorded, how it was attributed, and token counts. It has no field for a
// canonical model, price, or estimate, because the server derives those. A
// client and server with different catalogs therefore never disagree about a
// request, and show-data prints the exact request body.
package sync
