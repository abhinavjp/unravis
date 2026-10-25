# TSK-09 passphrase protector - DONE

Package `api-audit-explorer/internal/masterkey` (masterkey.go, keyring.go, masterkey_test.go). go.mod: `golang.org/x/crypto v0.57.0` moved indirect -> direct (go.sum unchanged; hashes already present). `golang.org/x/text v0.42.0` was already a direct dependency and in the module cache, so NFC uses `golang.org/x/text/unicode/norm` (no deviation, no network needed).

## Exported API (consumed by TSK-10/11/12)
```go
const FileName = "masterkey.dat"; MinPassphraseLen = 12; KeyLen = 32
var ErrWrongKey, ErrNotSet, ErrAlreadySet, ErrTooShort, ErrLocked, ErrCiphertext error
type Params struct{ Time, Memory uint32; Threads uint8 }   // Memory in KiB
func DefaultParams() Params                  // t=3, 64 MiB, p=4
func (Params) Validate() bool                // t 1..10, mem 16..256 MiB, p 1..16
func Path(dir string) string
func Exists(dir string) bool
func Normalize(pass []byte) []byte           // NFC copy; caller clears
func DeriveKey(pass, salt []byte, p Params) ([]byte, error) // NFC + Argon2id, 32 bytes, caller clears; does not zero pass; salt 8..64 bytes; bad params -> ErrWrongKey
func SealWith(key []byte, label string, plain []byte) ([]byte, error)  // AES-256-GCM: ver(1)|nonce(12)|ct+tag, AAD = ver||label
func OpenWith(key []byte, label string, blob []byte) ([]byte, error)   // any failure -> ErrCiphertext
func Setup(dir string, pass []byte) error    // zeroes pass; <12 runes -> ErrTooShort; existing file -> ErrAlreadySet
func Unlock(dir string, pass []byte) (*Keyring, error) // zeroes pass; wrong/damaged -> ErrWrongKey; missing -> ErrNotSet
type Keyring struct{...}                     // implements sealedfile.Protector
func (*Keyring) Protect/Unprotect([]byte) ([]byte, error)   // label "unravis/masterkey/protect/v1"
func (*Keyring) Seal/Open(label string, b []byte) ([]byte, error)
func (*Keyring) WithLabel(label string) sealedfile.Protector // e.g. "case.key", "manifest.dat" for TSK-10
func (*Keyring) Locked() bool
func (*Keyring) Lock()                       // zeroes key, idempotent; later calls -> ErrLocked
```
masterkey.dat = JSON `{v,kdf:"argon2id",t,m,p,salt(hex 16B),verifier(hex)}`, written with sealedfile.WriteAtomic; verifier = fixed constant sealed under the derived key with its own label.

## Tests (15 funcs, all green; go vet clean, gofmt clean, `go build ./...` ok)
Round trip, passphrase slice zeroed, not-set, wrong passphrase, 11-char refused (also rune-vs-byte), no overwrite, NFC==NFD key (both directions), Protect/Unprotect round trip + random nonce, tampered/truncated/empty/bad-version/other-label/other-key all -> single ErrCiphertext, Lock zeroes key bytes (inspected in-package via the backing slice) and later calls ErrLocked, concurrent use vs Lock, 11 damaged-parameter variants (huge memory etc.) -> ErrWrongKey with <8 MiB allocated, damaged/oversized file, stored params are the defaults, no secret/key in error strings (%v/%+v/%#v) or captured log (package logs nothing).

## Deviations / decisions to note
- No exported key-inspection test hook: tests are in-package and read `kr.key` directly (exporting would expose the key).
- Setup refuses to overwrite an existing masterkey.dat (ErrAlreadySet); changing the passphrase is a re-wrap job for TSK-10/11, which would need its own function (not in this packet).
- All damaged-file cases (bad JSON, bad params, oversize, bad salt/verifier) map to ErrWrongKey (constant shape); only OS errors other than not-exist are returned raw (path/permission text, no secrets).
- Extra exports beyond the packet (Seal/Open, WithLabel, DeriveKey, SealWith/OpenWith, Params, Exists, Path, Normalize) so TSK-10 can use distinct AAD labels and TSK-12 can derive per-file keys with its own salt without reimplementing the primitives.

## Concerns
- Go cannot guarantee no residual copies of the key/passphrase (GC, stack); zeroing narrows the window only (same caveat as casestore.Keys.Zero).
- Setup's exists-check then write is not atomic across processes (single-user local app; WriteAtomic replace would win last).
- -race was not run (unavailable).
