# Account client

This package owns native GitHub device-login orchestration and Overtok session
storage. It calls only the Overtok API; GitHub device codes and access tokens
remain on the server side.

The persisted session contains an Overtok device credential, account and device
IDs, login display value, and expiration. It is stored in the operating-system
credential vault under an API-origin-specific account. There is intentionally
no plaintext configuration-file fallback.

Logout revokes the server-side device before deleting local state. A network
failure leaves the local credential intact so the user can retry revocation.
Credential rotation replaces the vault entry only after the server has
atomically revoked the old credential and returned its replacement.

Account deletion similarly removes the vault entry only after the hosted API
confirms permanent deletion. Failed and unauthorized requests retain the local
credential for an honest retry. Hosted deletion never removes the local SQLite
usage database or source evidence.
