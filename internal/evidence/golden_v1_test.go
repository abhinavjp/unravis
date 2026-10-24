package evidence

// goldenV1 is a schemaVersion 1 export written by the code before the version
// 2 change (baseInput with pseudonyms and ledger asked for; the pseudonym
// table was withheld). Verify must keep accepting it byte for byte. It is a raw
// string so a CRLF checkout cannot change its bytes: Go drops carriage returns
// inside raw string literals.
const goldenV1 = `{
  "manifest": {
    "schemaVersion": 1,
    "toolVersion": "0.13.0-test",
    "createdAt": "2026-10-24T12:00:00Z",
    "bodySha256": "a67dafd73898945cf5f736184f922d6ba461d248291cf33b1db162c943d539ea",
    "masking": {
      "statements": [
        "Left out by default: request and response payloads, raw URLs, query text, IP addresses, user agents, raw user IDs and source ids. Sources are named by a human label only.",
        "Every count below 20 is shown as \"<20\", including counts inside headlines and known gaps. A finding with a count below 20 is marked low n.",
        "Pseudonym table withheld: a cell was below 20.",
        "Only hypotheses that reference a finding in this export are included, with their text, state and evidence references; notes and guard facts are not. In hypothesis text, identifiers, email addresses, IP addresses, host names, paths and links are replaced by \"[removed]\", every number below 20 is shown as \"<20\", and text that still looks like it holds an identifier is replaced by \"[ledger text withheld]\".",
        "The key that makes pseudonyms is not in this file.",
        "bodySha256 is the SHA-256 of this file's bytes with the bodySha256 value set to an empty string, so it covers the manifest and the body."
      ]
    }
  },
  "body": {
    "records": [
      {
        "version": 1,
        "evidenceId": "p2:b1:0011223344556677",
        "source": "Audit (sandbox)",
        "windowStart": "2026-10-20T09:00:00Z",
        "windowEnd": "2026-10-20T11:00:00Z",
        "joinClass": "none",
        "tolerance": "1 s _id resolution (ObjectId time is whole seconds)",
        "provenance": {
          "kind": "query",
          "ref": "Audit (sandbox)",
          "hash": "d672df003f71579d4e66fe20e1e90ef288c56c12b45844a0862d0a2b9310b82c"
        },
        "label": "Symptom",
        "rule": "B1",
        "headline": "<20 requests took 45 s or longer.",
        "n": {
          "masked": true,
          "text": "<20"
        },
        "lowN": true,
        "knownGaps": [
          "<20 incomplete rows in the window are left out of these figures.",
          "25 invalid rows in the window are left out of these figures.",
          "Field coverage was not measured for this window. Coverage did not run (see [removed] or ask [removed] at [removed]). Run Coverage first.",
          "A read was partial (row cap reached), so its figures are lower bounds."
        ],
        "sourceQuality": {
          "threshold": "stall at or above 45 s",
          "coverageFirst": "2026-10-20T09:00:00Z",
          "coverageLast": "2026-10-20T11:00:00Z",
          "timezoneRule": "UTC; request times are stored as UTC"
        }
      },
      {
        "version": 1,
        "evidenceId": "p2:b2:0011223344556677",
        "source": "Audit (sandbox)",
        "windowStart": "2026-10-20T09:00:00Z",
        "windowEnd": "2026-10-20T11:00:00Z",
        "joinClass": "none",
        "tolerance": "1 s _id resolution (ObjectId time is whole seconds)",
        "provenance": {
          "kind": "query",
          "ref": "Audit (sandbox)",
          "hash": "a306307aa34755a9056d497a5d73babff9a6b112a1df9b576ec2c54d436e8d79"
        },
        "label": "Symptom",
        "rule": "B2",
        "headline": "Not recurring: fewer than 3 separate episodes, <20 requests in all.",
        "n": {
          "masked": true,
          "text": "<20"
        },
        "lowN": true,
        "knownGaps": [
          "<20 incomplete rows in the window are left out of these figures.",
          "25 invalid rows in the window are left out of these figures.",
          "Field coverage was not measured for this window. Coverage did not run (see [removed] or ask [removed] at [removed]). Run Coverage first.",
          "A read was partial (row cap reached), so its figures are lower bounds."
        ],
        "sourceQuality": {
          "coverageFirst": "2026-10-20T09:00:00Z",
          "coverageLast": "2026-10-20T11:00:00Z",
          "timezoneRule": "UTC; request times are stored as UTC"
        }
      },
      {
        "version": 1,
        "evidenceId": "p2:b6:0011223344556677",
        "source": "Audit (sandbox)",
        "windowStart": "2026-10-20T09:00:00Z",
        "windowEnd": "2026-10-20T11:00:00Z",
        "joinClass": "none",
        "tolerance": "1 s _id resolution (ObjectId time is whole seconds)",
        "provenance": {
          "kind": "query",
          "ref": "Audit (sandbox)",
          "hash": "bad3e2600255b97aea6b29802a36d5cda1f46fbd6ed504e37fedc4c3f5f84fe4"
        },
        "label": "Symptom",
        "rule": "B6",
        "headline": "Audit create took 7.3 s as measured from the client side. That is the longest of <20 requests at or above 5 s and 30% of observed time.",
        "n": {
          "masked": true,
          "text": "<20"
        },
        "lowN": true,
        "knownGaps": [
          "<20 incomplete rows in the window are left out of these figures.",
          "25 invalid rows in the window are left out of these figures.",
          "Field coverage was not measured for this window. Coverage did not run (see [removed] or ask [removed] at [removed]). Run Coverage first.",
          "A read was partial (row cap reached), so its figures are lower bounds."
        ],
        "sourceQuality": {
          "coverageFirst": "2026-10-20T09:00:00Z",
          "coverageLast": "2026-10-20T11:00:00Z",
          "timezoneRule": "UTC; request times are stored as UTC"
        }
      },
      {
        "version": 1,
        "evidenceId": "p2:b12:0011223344556677",
        "source": "Audit (sandbox)",
        "windowStart": "2026-10-20T09:00:00Z",
        "windowEnd": "2026-10-20T11:00:00Z",
        "joinClass": "none",
        "tolerance": "1 s _id resolution (ObjectId time is whole seconds)",
        "provenance": {
          "kind": "query",
          "ref": "Audit (sandbox)",
          "hash": "b67daa29d80031f72fe869bf6fbdbf503afa3d1c0353efffae1e27cc27eb4472"
        },
        "label": "Correlation",
        "rule": "B12",
        "headline": "<20 times, one user started 2 or more requests of 45 s or longer within 1 s of each other.",
        "n": {
          "masked": true,
          "text": "<20"
        },
        "lowN": true,
        "knownGaps": [
          "<20 incomplete rows in the window are left out of these figures.",
          "25 invalid rows in the window are left out of these figures.",
          "Field coverage was not measured for this window. Coverage did not run (see [removed] or ask [removed] at [removed]). Run Coverage first.",
          "A read was partial (row cap reached), so its figures are lower bounds."
        ],
        "sourceQuality": {
          "coverageFirst": "2026-10-20T09:00:00Z",
          "coverageLast": "2026-10-20T11:00:00Z",
          "timezoneRule": "UTC; request times are stored as UTC"
        }
      }
    ],
    "ledger": [
      {
        "text": "User [removed] at [removed] hit [removed] mail [removed] row [removed], <20 stalls.",
        "state": "supported_by_correlation",
        "evidenceIds": [
          "p2:b1:0011223344556677",
          "not in this export"
        ]
      }
    ]
  }
}
`
