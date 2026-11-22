//! Repository policy checks that run in every `cargo test` (BL-320, BL-321, BL-322). They read the real files, not copies.

use ipc_core::floor::{TAURI_FLOOR, locked_version, locked_with_prefix, meets};
use std::path::PathBuf;

fn desktop(rel: &str) -> String {
    let p = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("..").join(rel);
    std::fs::read_to_string(&p).unwrap_or_else(|e| panic!("{}: {e}", p.display()))
}

#[test]
fn locked_tauri_meets_the_floor() {
    let v = locked_version(&desktop("Cargo.lock"), "tauri").expect("tauri is locked");
    assert!(meets(&v, TAURI_FLOOR), "locked tauri {v} is below the floor {TAURI_FLOOR:?}");
}

#[test]
fn zero_tauri_plugins_and_no_updater() {
    let lock = desktop("Cargo.lock");
    assert_eq!(locked_with_prefix(&lock, "tauri-plugin-"), Vec::<String>::new());
    assert!(locked_version(&lock, "tauri-plugin-updater").is_none());
}

#[test]
fn capabilities_have_no_shell_fs_http_or_remote() {
    let dir = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../src-tauri/capabilities");
    let mut n = 0;
    for e in std::fs::read_dir(dir).unwrap() {
        let path = e.unwrap().path();
        let v: serde_json::Value = serde_json::from_str(&std::fs::read_to_string(&path).unwrap()).unwrap();
        assert!(v.get("remote").is_none(), "{}: remote origins are not allowed", path.display());
        assert_eq!(v["windows"], serde_json::json!(["main"]), "{}: one window label", path.display());
        assert_eq!(v["local"], serde_json::json!(true));
        for p in v["permissions"].as_array().unwrap() {
            let p = p.as_str().unwrap_or_default();
            for bad in ["shell:", "fs:", "http:", "core:default", "opener:", "process:", ":allow-"] {
                assert!(!p.contains(bad), "{}: permission {p} is not allowed", path.display());
            }
        }
        n += 1;
    }
    assert_eq!(n, 1, "exactly one capability file (main-windows)");
}

#[test]
fn tauri_conf_security_block() {
    let conf: serde_json::Value = serde_json::from_str(&desktop("src-tauri/tauri.conf.json")).unwrap();
    let app = &conf["app"];
    assert_eq!(app["withGlobalTauri"], serde_json::json!(false));
    assert_eq!(app["security"]["assetProtocol"]["enable"], serde_json::json!(false));
    assert_eq!(app["security"]["dangerousDisableAssetCspModification"], serde_json::json!(false));
    assert_eq!(app["security"]["pattern"]["use"], serde_json::json!("brownfield"));
    assert_eq!(app["security"]["capabilities"], serde_json::json!(["main-windows"]));
    assert!(conf.get("plugins").is_none(), "no plugins key");
    let csp = app["security"]["csp"].as_str().expect("production csp is set");
    let script_src = csp.split(';').map(str::trim).find(|d| d.starts_with("script-src")).expect("script-src");
    for bad in ["unsafe-inline", "unsafe-eval", "data:", "blob:"] {
        assert!(!script_src.contains(bad), "script-src has {bad}");
    }
    assert!(!csp.contains("unsafe-inline") && !csp.contains("unsafe-eval"));
    assert!(app["security"].get("devCsp").is_none_or(|v| v.is_null()), "devCsp stays out of the shipped config");
}

#[test]
fn cargo_manifest_has_no_plugin_dependency() {
    let m = desktop("src-tauri/Cargo.toml");
    assert!(!m.lines().any(|l| l.trim_start().starts_with("tauri-plugin-")), "a tauri-plugin dependency is declared");
}
