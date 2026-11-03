# Progress: Phase 02 earliest-red-guards

## Done

(nothing yet)

## Evidence

(command + PASS/FAIL line per task)

## Not fixed

(anything left, with reason)

## UNMEASURED

(checks that could not run, with reason)
TSK-08 DONE: pairSep added in origin.go, used by ComputeOrigin and colour; go build ./... and go test ./internal/... PASS (goldens untouched this packet; baseline golden hashes snapshotted, Phase 01 diffs pre-existing)
TSK-09 DONE: TestTiersSixtySecondsApartAreTogether and TestTiersTwoMinutesApartAreSeparable appended, both PASS
TSK-10 DONE: Input.LaneUnread (omitempty) added in types.go; applyLaneRuns fills it; TestLaneUnreadIsSet + TestLaneUnreadNotSetWhenAnswered added; go build ./... and go test ./internal/server ./internal/investigate PASS
TSK-11 DONE: tiers() adds unread lane tiers from Input.LaneUnread (sorted); TestUnreadLaneBlocksALaterOrigin + TestUnreadLaneNeedsAConfiguredLane added; go test ./internal/investigate PASS, golden hashes unchanged
TSK-12 DONE: persistShare added in facts_parts.go with TestPersistShare (facts_parts_test.go created). Deviation: takes the per-minute status func(*bin) minStatus (same as findRun) instead of hit func(bin) bool, so thin minutes are skipped exactly as metricOnset skips them
TSK-13 DONE: metricOnset and loadOnset drop an onset with persistShare < OriginPersistShare-floatEps (nil, 'moved briefly but did not stay up', tested=false); lane onsets untouched; TestOnsetDroppedWhenItDoesNotPersist + TestOnsetKeptWhenItPersists added; go test ./internal/investigate PASS, golden hashes unchanged, no existing test fixture changed
TSK-14 DONE: isLane + pairSep (lane margin 5 s when either tier is sql/mongo/host); ComputeOrigin compares first vs each tier with pairSep(first, other); colour tracks earliestTier and uses near() helper; clock note appends 'Lane clocks (SQL, Mongo, host) are read at +/-5 s.' when a lane tier is dated; tests TestLaneAndAppTierThreeSecondsApartAreTogether, TestLaneMarginIsWiderThanTheAppMargin (63 s), TestTwoAppTiersThreeSecondsApartStayOrdered (app onsets are minute-bucketed so 3 s is not constructible; uses 2 minutes apart + pairSep checks) added; go test ./internal/investigate PASS, golden hashes unchanged
TSK-15 DONE: guard added in ComputeOrigin after the decision switch (Named + tierLoad + SigRetriesUp/SigErrorsPrecede Yes -> OriginNone, Tier/Label/At cleared, near[load]=true); signals were already a parameter (s Signals), no caller change. Note: the Note is REPLACED by the retry sentence (appending would leave 'moved first' contradicting the None status). Tests TestRetriesBeforeLoadAreNotNamedAsLoad + TestLoadStillNamedWithoutRetries (load first at 10:10, c at 10:20, clients 5000); go test ./internal/investigate PASS, golden hashes unchanged. Second reviewer still required
TSK-16 DONE: colour(sps, in, f, sig, th) - only caller Evaluate updated; the packet's 'L-group Red case' does not exist as such (Red is the generic 'tier moved first' case), so a new case before it gives Orange when sp.Tier == load (L1 and L3) and retries/errors precede, Why 'Its tier moved first, but load follows retries; not new demand.'; TestRetriesBeforeLoadGiveOrangeNotRed + TestLoadSuspectStillRedWithoutRetries added (mutation-checked); go test ./internal/investigate PASS, golden hashes unchanged. Second reviewer still required
TSK-15/16 review fixes: (1) errors half of load-follows-retries now requires first incident server error at or before the load onset (by the minute; unknown onset keeps old behaviour) via shared loadFollowsRetries in origin.go, used by ComputeOrigin and colour; tests TestErrorsBeforeLoadAreNotNamedAsLoad, TestErrorsAfterLoadOnsetStillNameLoad, TestRetryStormSuspectIsNeverRed, TestLoadFollowsRetriesErrorsHalf appended. (3) heldBackTiers/heldBackClause split dropped onsets ('had its onset set aside because it did not stay up') from unread tiers ('could not be read in every minute') in origin and colour notes; unread wording unchanged so goldens are unchanged; tests TestHeldBackWordingSeparatesDroppedFromUnread, TestDroppedOnsetNoteSaysSetAside appended.
