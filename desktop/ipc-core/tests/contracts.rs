//! Wire contracts (BL-445). The golden files in `contracts/v1/fixtures` are captured from the real Go handlers by
//! `internal/server/contracts_test.go`, which also checks them against the JSON Schemas in `contracts/v1/schema`.
//! Here the same files are read into strict serde structs (`deny_unknown_fields`, no defaults for required fields), so a
//! field the Go side renames, drops or adds fails this test too. The structs live in the test on purpose: the shell itself
//! must keep ignoring fields it does not know, so that an additive server change never breaks an older shell.

use serde::Deserialize;

#[derive(Deserialize, Debug, PartialEq)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
struct Limits {
    max_sources: u32,
    max_page_size: u32,
    max_summary_rows: u32,
    max_time_buckets: u32,
}

#[derive(Deserialize, Debug)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
struct Info {
    demo: bool,
    #[serde(default)]
    capture_cases: Option<bool>,
    limits: Limits,
    now: String,
}

#[derive(Deserialize, Debug)]
#[serde(deny_unknown_fields)]
struct Version {
    version: String,
    commit: String,
    date: String,
}

#[derive(Deserialize, Debug)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
struct ErrorBody {
    error: String,
    #[serde(default)]
    reason: Option<String>,
    #[serde(default)]
    column: Option<String>,
    #[serde(default)]
    import_id: Option<i64>,
    #[serde(default)]
    state: Option<String>,
    #[serde(default)]
    retry_after_seconds: Option<u32>,
    #[serde(default)]
    code: Option<String>,
    #[serde(default)]
    stage: Option<String>,
    #[serde(default)]
    collections: Option<Vec<String>>,
}

const INFO: &str = include_str!("../../../contracts/v1/fixtures/info.demo.json");
const VERSION: &str = include_str!("../../../contracts/v1/fixtures/version.json");
const UNAUTHORIZED: &str = include_str!("../../../contracts/v1/fixtures/error.unauthorized.json");
const VALIDATION: &str = include_str!("../../../contracts/v1/fixtures/error.validation.json");
const LOCKED: &str = include_str!("../../../contracts/v1/fixtures/error.locked.json");

#[test]
fn info_golden_reads_into_the_strict_struct() {
    let info: Info = serde_json::from_str(INFO).expect("info golden");
    assert!(info.demo);
    assert_eq!(info.capture_cases, None);
    assert_eq!(info.limits.max_sources, 8);
    assert_eq!(info.limits.max_page_size, 100);
    assert_eq!(info.limits.max_summary_rows, 40000);
    assert_eq!(info.limits.max_time_buckets, 120);
    assert!(info.now.starts_with("2026-10-22T12:00:00"));
}

#[test]
fn version_golden_reads_into_the_strict_struct() {
    let v: Version = serde_json::from_str(VERSION).expect("version golden");
    assert_eq!((v.version.as_str(), v.commit.as_str(), v.date.as_str()), ("1.2.3", "abc1234", "2026-10-27"));
}

#[test]
fn error_goldens_read_into_the_strict_struct() {
    let unauthorized: ErrorBody = serde_json::from_str(UNAUTHORIZED).expect("unauthorized golden");
    assert!(!unauthorized.error.is_empty());
    assert_eq!(unauthorized.reason, None);
    let validation: ErrorBody = serde_json::from_str(VALIDATION).expect("validation golden");
    assert!(!validation.error.is_empty());
    let locked: ErrorBody = serde_json::from_str(LOCKED).expect("locked golden");
    assert_eq!(locked.reason.as_deref(), Some("locked"));
    assert_eq!(locked.column, None);
    assert_eq!(locked.import_id, None);
}

/// The drift proof: a renamed, extra or wrongly typed field must be refused by the strict structs.
#[test]
fn drifted_answers_are_refused() {
    let renamed = INFO.replace("maxSources", "maxSource");
    assert!(serde_json::from_str::<Info>(&renamed).is_err(), "renamed field");
    let extra = INFO.replace("\"demo\": true", "\"demo\": true, \"surprise\": 1");
    assert!(serde_json::from_str::<Info>(&extra).is_err(), "extra field");
    let wrong_type = INFO.replace("\"maxPageSize\": 100", "\"maxPageSize\": \"100\"");
    assert!(serde_json::from_str::<Info>(&wrong_type).is_err(), "wrong type");
    // A required field that is only missing, not renamed.
    for key in ["demo", "limits", "now"] {
        let mut v: serde_json::Value = serde_json::from_str(INFO).unwrap();
        v.as_object_mut().unwrap().remove(key);
        assert!(serde_json::from_value::<Info>(v).is_err(), "dropped {key}");
    }
    // A throttled answer carries retryAfterSeconds, a source failure code, stage and collections: the error struct knows them.
    let throttled = r#"{"error":"x","reason":"wrong-key","state":"locked","retryAfterSeconds":4,"code":"c","stage":"s","collections":["a"]}"#;
    assert!(serde_json::from_str::<ErrorBody>(throttled).is_ok());
}
