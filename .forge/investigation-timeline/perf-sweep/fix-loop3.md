# Fix loop 3 (internal/casestore/sqlite only)

1. Identity stamp: statStamp now stats an open handle (File.Stat, SameFile(fi,fi)), so Windows pins the file ID. Test: TestReplacedFileSameSizeAndTimeIsCheckedAgain (rename-replace, same size, restored mtime, quick_check reruns).
2. Damage while open: new isDamage/caseState.noteDamage calls verified.drop on IntegrityCheck, mapDBErr, Events/Each errors, bucketChunk and BucketCounts. Tests: TestIntegrityCheckDamageDropsTheCacheEntry, TestNoteDamageDropsOnlyForDamage.
3. Timeline build: failures and no-room are remembered per process and case file (timelineFailed, cleared by drop). Headroom check (quarter of size, min 16 MB, within MaxDBBytes and free disk) runs before the scan. Import no longer builds timeline indexes in its transaction for a case with earlier events (timelineLate): it creates core indexes and starts the background build afterwards. Tests: TestFailedTimelineBuildIsNotRepeated, TestNearCapCaseSkipsTheBuildAndStillImports (fails without the change).
4. BucketCounts: errors clamped to count (mergeBucketErrors); all chunks and both queries run in one read transaction. Tests: TestMergeBucketErrorsClampsToCount, TestBucketCountsReadOneSnapshot.
5. Build tracked (tlBuild). BeginArchive, BeginFree, FinishFree call stopTimelineBuild (cancel and bounded wait, before the folder lock). lockImport is one ctx-cancellable loop. Tests: TestStopTimelineBuildCancelsAndWaits, TestImportBehindTheBuildIsCancellable.
6. Events merge returns nil when empty. Test: TestEventsSeveralSourcesEmptyIsNil.

Results: go vet casestore clean; Linux vet clean; go test casestore and server pass (-count=1).
Not done: no -race run (CGO off); BeginFree/Archive wait is tested at stopTimelineBuild level, not through the full state machine.
