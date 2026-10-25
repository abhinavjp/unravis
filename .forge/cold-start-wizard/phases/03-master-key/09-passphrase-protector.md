# TSK-09: Passphrase protector (Argon2id + AEAD) and session keyring
risk: high — new cryptographic code that every protected case will depend on.

## Outcome
A new package `internal/masterkey` provides:
- `Setup(dir string, pass []byte) error` creates `masterkey.dat` (in the per-user app-data dir, next to `connections.dat`): a random 16-byte salt, the Argon2id parameters, and a verifier (a fixed constant sealed under the derived key) so a wrong passphrase is detected without touching any case. Refuses a passphrase under 12 characters.
- `Unlock(dir string, pass []byte) (*Keyring, error)` derives the key and checks the verifier. All failures return one error value `ErrWrongKey` (wrong passphrase, damaged verifier) so callers cannot tell them apart; a missing file is `ErrNotSet`.
- `Keyring` implements `sealedfile.Protector` (`Protect` / `Unprotect`, AES-256-GCM, random nonce, a version byte and a context label as AAD) and has `Lock()` that zeroes the key. Case-file keys (salt per file) are TSK-12's job, not this package's `Keyring`.
- Passphrases are normalised to Unicode NFC before derivation.

## Write scope
- `internal/masterkey/` (new package: `masterkey.go`, `keyring.go`, tests)
- `go.mod`, `go.sum` (promote `golang.org/x/crypto` from indirect to direct; add `golang.org/x/text/unicode/norm` only if it is already in the module graph, otherwise implement NFC via the standard library alternative recorded as a deviation)

## Must not change
- `internal/sealedfile` (the `Protector` interface and `DefaultProtector`); `internal/casestore` (TSK-10 owns the wiring).
- Any existing dependency version other than the promotion above.

## Changes
1. Argon2id parameters: time 3, memory 64 MiB, parallelism 4, key length 32. Store them in `masterkey.dat` so they can be raised later; refuse to load parameters outside sane bounds (a damaged file must not allocate gigabytes).
2. `masterkey.dat` is written with `sealedfile.WriteAtomic` and is not secret by itself (salt + verifier only); the passphrase and derived key are never written anywhere (I5).
3. Zero the passphrase slice and the derived key material after use; the Keyring holds only the 32-byte key while unlocked.
4. Failed unlocks are not rate limited here (TSK-11 owns throttling); this package only guarantees constant-shape errors.

## Proof
Tests: set-up then unlock round trip; wrong passphrase → `ErrWrongKey`; 11-character passphrase refused; NFC and NFD forms of the same text unlock the same key; `Protect` then `Unprotect` round trip; tampered ciphertext, truncated ciphertext and a ciphertext from another key all fail with one error; `Lock()` makes later calls fail and the key bytes are zero (inspect through an exported test hook); damaged parameters (huge memory value) are rejected without allocating; a scan of the error strings and of a captured log buffer shows no passphrase or key bytes. `go test ./internal/masterkey/...` green; `go vet` clean.

## Depends on
none
