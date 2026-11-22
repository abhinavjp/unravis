//! Pure logic of the desktop shell: nothing here calls the OS, so `cargo test` runs on Linux and Windows alike.

pub mod elevation;
pub mod floor;
pub mod locator;
pub mod policy;
pub mod webview2_policy;
