# TSK-10 report: key file v2, protector selection, per-case re-wrap

Status: done. `go build ./...`, `go vet ./internal/casestore/... ./internal/masterkey/...` clean; `go test ./internal/casestore/... ./internal/masterkey/... -count=1` green (casestore, sqlite, masterkey); gofmt clean on every file I touched. Nothing committed, no git state touched. `-race` was not run (no gcc).

## Files changed
- `internal/casestore/keyfile.go` (modified): kinds, labels, `ErrLocked`, lookup/bind/resolve, v2 envelope, `FileKind`, `WriteKeyFileKind`, `ReadKeyFileAny`. `WriteKeyFile`/`ReadKeyFile` keep their signatures and behaviour (DPAPI, v1).
- `internal/casestore/rewrap.go` (new): `Rewrap`, `Sealing`, `RewrapOptions`, `ErrRewrapDirection`, `KeyFileName`, `ManifestFileName`.
- `internal/casestore/sqlite/sqlite.go` (modified): `Options.Protectors`, `Options.NewCaseKind`, `Store.CaseKind`, `Store.Rewrap`, `ErrLocked`; Create/List/readCaseFiles/CloseCase choose the protector from the kind recorded in the file. gofmt re-aligned one comment block there.
- `internal/casestore/sqlite/case.go` (modified): four call sites use `c.s.sealer(c.dir, "")` instead of `c.s.opt.Protector` for the manifest.
- Tests (new): `internal/casestore/keyfile_v2_test.go`, `internal/casestore/rewrap_test.go`, `internal/casestore/sqlite/rewrap_test.go`. The existing `keyfile_test.go` is untouched.
- `readManifest`/`writeManifest` signatures unchanged. `masterkey` unchanged. `discover/*`, `server/*` untouched.

## Layouts
- v1 (unchanged, still what a DPAPI case writes): the bare sealed blob; inner JSON `{"v":1,...}`. A build from before this task still reads these files, so rolling back works for DPAPI cases.
- v2 (master key): `{"v":2,"p":"masterkey"}\n` then the sealed blob; inner JSON `{"v":2,"p":"masterkey",...}`. `manifest.dat` uses the same envelope. Labels `case.key` and `manifest.dat` are the AAD (via `Keyring.WithLabel`). A v1-only reader hands the whole file to its protector and refuses it (tested).
- A file whose first byte is not `{`, or whose first line is not JSON, is a v1 blob. A header with an unknown version or kind, a header/inner-document mismatch, or a v2 document in a headerless file is `ErrKeyFile`.

## Exported signatures TSK-11/12 consume
```go
// package casestore
const ( KindDPAPI = "dpapi"; KindMasterKey = "masterkey"; LabelKey = "case.key"; LabelManifest = "manifest.dat" )
const ( KeyFileName = "case.key"; ManifestFileName = "manifest.dat" )
var ErrLocked = masterkey.ErrLocked          // same value: errors.Is works with either name; the 423 case
var ErrKeyFile error                         // damaged, wrong key, half-moved case (existing)
var ErrRewrapDirection error
func ValidKind(k string) bool
type ProtectorLookup func(kind string) (sealedfile.Protector, error)
func DPAPIOnly(p sealedfile.Protector) ProtectorLookup
func Bind(p sealedfile.Protector, kind, label string) sealedfile.Protector
func Resolve(lookup ProtectorLookup, kind, label string) (sealedfile.Protector, error)
func FileKind(path string) (kind string, err error)          // header only; works while locked
func WriteKeyFileKind(path, kind string, p sealedfile.Protector, k Keys) error
func ReadKeyFileAny(path string, lookup ProtectorLookup) (Keys, string, error)
func WrapSealed(kind string, blob []byte) []byte
func UnwrapSealed(raw []byte) (kind string, blob []byte, err error)
type Sealing struct{ Kind string; P sealedfile.Protector }
type RewrapOptions struct {
    Enter func() (leave func(), err error)   // runs under the folder lock, before any read
    Step  func(stage string) error           // test seam: "staged", "manifest-replaced", "key-replaced"
}
func Rewrap(dir string, from, to Sealing, opt RewrapOptions) error

// package casestore/sqlite
type Options struct { ...; Protectors casestore.ProtectorLookup; NewCaseKind func() string; ... }
var ErrLocked = casestore.ErrLocked
func (s *Store) CaseKind(id casestore.CaseID) (string, error)   // "dpapi" | "masterkey", works while locked
func (s *Store) Rewrap(ctx context.Context, id casestore.CaseID) error
```
How TSK-11 wires it:
- `Options.Protectors`: called with `casestore.KindMasterKey`. Return the unlocked `*masterkey.Keyring` (it is the `sealedfile.Protector`; the store binds the per-file label itself). While locked return `(nil, casestore.ErrLocked)` or any error. A nil `Protectors` means master-key cases are locked. `Options.Protector` stays the DPAPI protector. The lookup is called on every operation, so a lock takes effect for the next open/list/close.
- `Options.NewCaseKind`: return `casestore.KindMasterKey` once a master key exists and is unlocked to make new cases master-key sealed; nil or anything else keeps DPAPI/v1. `Create` returns `ErrLocked` (and leaves no folder) if the kind is master key and the key is locked.
- `Store.Open`, `CloseCase`, `Rewrap` return `ErrLocked` for a master-key case with no usable key, before reading the manifest. `Store.List` shows such a case as title `(locked)` with zero counts. A wrong master key is `ErrKeyFile`, never `ErrLocked`.
- `Store.Rewrap`: DPAPI to master key only, one case, atomic, safe with the case open, no-op if already master key, `ErrLocked` with nothing written if the key is locked. TSK-12 loops over `List` + `CaseKind` and calls it per case.
- `casestore.Rewrap` is the folder-level function (also what `Store.Rewrap` calls).

## Tests (new)
- keyfile: v1 file from the old writer opens; the old v1-only reader reads what is written now; v2 round trip with a real `masterkey.Keyring`; no key bytes in the file; old reader refuses v2; `case.key` blob does not open under the `manifest.dat` label; wrong key is `ErrKeyFile`, locked / nil lookup / lookup error / DPAPI-only is `ErrLocked`; writing while locked writes nothing; unknown protector, empty protector, unknown version, v1 header, missing version, header-vs-blob kind swap, inner/header mismatch, v2 document without header, truncated, header-only, flipped byte; `FileKind`; no key bytes in any error text.
- rewrap (folder level): both files move, `case.db` bytes and mtime unchanged, no temp files; second run is a no-op that calls no step; bad directions, locked or missing target write nothing; injected failure at "staged" leaves both old files byte-identical; failure at "manifest-replaced" leaves the old `case.key` untouched and a second run completes it; failure after the last replace is already moved; a broken new file is caught by the read-back before anything is replaced; wrong old protector changes nothing; `Enter` hook and missing files; no key or manifest bytes in any error.
- sqlite: re-wrapped case opens with a Store that has only the Keyring (different DPAPI protector), `case.db` hash identical, import after re-wrap works, `List` correct; DPAPI-only store gets `ErrLocked`; locked (no lookup / lookup error / locked Keyring) gives `ErrLocked` for Open/CloseCase/Rewrap and `(locked)` in List; wrong key is not `ErrLocked` and reveals no title or id; Rewrap with the case open; injected failure keeps the case openable via DPAPI, a crash between the two replaces makes Open return `ErrKeyFile` and a second Rewrap finishes it; locked Rewrap writes nothing; `NewCaseKind` creates master-key cases, title not in plain, locked Create leaves no folder, default stays DPAPI.

## Deviations
1. `casestore.Rewrap` is folder-based with `Sealing` structs and `RewrapOptions`, not the packet's `Rewrap(caseID, from, to Protector)`; the case-id form is `sqlite.(*Store).Rewrap(ctx, id)`.
2. Extra file outside the listed Write scope: `internal/casestore/sqlite/rewrap_test.go`. Also `case.go` (four call sites) and `casestore/keyfile_v2_test.go`, `casestore/rewrap_test.go` as new test files rather than extending `keyfile_test.go`.
3. `internal/casestore` now imports `internal/masterkey` (for `ErrLocked`, so there is one error value).
4. `Options.NewCaseKind` is an extra option (the packet only required injectable selection).
5. Packet wording says the case stays openable after a failure past the first replace. With the order manifest first, key last, every failure before the final replace keeps the old `case.key`, but a real crash exactly between the two replaces leaves manifest and key on different protectors. Open then returns `ErrKeyFile` (it refuses to guess) until Rewrap is run again, which finishes it. A real failure of the key replace itself puts the old manifest back. A power cut in that one window is the only way to see the mismatch.
6. `List` shows `(locked)` for a locked master-key case instead of `(unreadable case)`.
7. `Sweep` cannot read a closed master-key case while the key is locked, so it skips it that run.
8. `Store.Open` of a case already open in this Store still returns the live handle after the Keyring is locked. TSK-11 must close open handles when it locks.

## Not measured / not done
- `-race` unavailable (no gcc). Rewrap's Enter hook takes the handle lock under the folder lock; ordering reasoned, not race-tested.
- Real DPAPI sealing off the test protector was not exercised here; DPAPI still fails closed off Windows (unchanged).
- Behaviour of the Windows named folder mutex under a concurrent second process during Rewrap is not tested.
- No real power-cut test; crash points are simulated through `RewrapOptions.Step`.
- Argon2 cost of unlocking per test is not part of any timing claim.
