// Every command the frontend may invoke is listed here (BL-321). Empty in the skeleton: BL-314 adds the read-only relay commands.
fn main() {
    let app = tauri_build::AppManifest::new().commands(&[]);
    tauri_build::try_build(tauri_build::Attributes::new().app_manifest(app)).expect("tauri build failed");
}
