# BL-324 Trusted Types and `freezePrototype` trial

Result (2026-11-25, Chromium, the demo exe, 103 screens): **neither flag can be turned on today.** Both stay OFF. Nothing in the app changed.

## How it was run

`cd tools/uiaudit && UNRAVIS_EXE=<demo exe> npm run tt` (config `playwright.tt.config.ts`, spec `csp/tt.spec.ts`; uses the screens, steps and chart hover sweep of the strict-CSP run). `TT_TRIAL=tt|freeze`, `UIAUDIT_ONLY=<ids>`, `TT_REPORT=<file>` (findings as lines of JSON). A self-check test proves the report-only policy reports an `innerHTML` string without blocking it, and that `Object.prototype` is really frozen.

- Trial `tt`: every page answers with the desktop policy and a second `Content-Security-Policy-Report-Only: require-trusted-types-for 'script'; trusted-types 'none'`.
- Trial `freeze`: `Object.freeze(Object.prototype)` before any page script (what Tauri's `freezePrototype: true` does); a screen fails on a script error, console error or unhandled rejection.

## Trusted Types: NO

103 of 103 screens report. Two sources only:

| Count | What | Where |
|---|---|---|
| 653 | `Element innerHTML` written with a string | ECharts (`charts-*.js`: tooltips and legend) |
| 105 | a Trusted Types policy named `svelte-trusted-html` created | Svelte's own runtime (`disclose-version-*.js`) |

Enforcing would block every ECharts tooltip. The app's own code writes no string to a script sink. Svelte creates its policy, so an enforced policy would have to allow the name `svelte-trusted-html` (`trusted-types svelte-trusted-html`) and ECharts would need tooltips that do not use `innerHTML` (a chart option change or a wrapper that sets a `TrustedHTML` through a policy of ours). Until ECharts is handled, enforcement is not possible. Next step if wanted: route the ECharts tooltip HTML through one named policy and add that name to the directive; then rerun this trial.

## `freezePrototype`: NO

Frozen `Object.prototype` breaks the ECharts bundle on load:

```
TypeError: Cannot assign to read only property 'constructor' of object '#<Object>'
    at charts-*.js   (module evaluation, ECharts/zrender class setup)
```

Every screen that needs the charts chunk (the Blackbox panel, and the screens that lazy-load it, 33 of the 103) never renders. The failure is **silent**: `LazyHost.svelte` swallows the rejected `import()` and shows only its "could not be loaded" notice, with no `pageerror` and no console line. The other 70 screens render under the freeze (the only findings there are the known 409 and 423 answers of the demo, the same as in the `tt` trial). This is the known "override mistake" class (a plain assignment to an inherited non-writable property throws in strict code). Not fixable in our code without patching or replacing ECharts; do not set `freezePrototype` until an ECharts release no longer assigns `constructor` this way.

## Owed on real WebView2/Tauri (nachi's PC)

This run is Chromium on the demo data, so it is the CI-side half only. Still owed: the same two runs against the real desktop build, a real case, all chart types. BL-324 is not ticked.
