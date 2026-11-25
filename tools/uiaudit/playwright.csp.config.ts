import { defineConfig } from '@playwright/test';

// BL-323: the strict-CSP run. Same demo app and the same screens as the UI audit, one look (desktop, dark; the phone and light looks add no
// script or style to the page), but every page document is answered with the Content-Security-Policy of the desktop shell
// (desktop/src-tauri/tauri.conf.json) and any `securitypolicyviolation` fails the test. See csp/csp.spec.ts.
const exe = process.env.PW_CHROMIUM || undefined;
export default defineConfig({
  testDir: './csp',
  testMatch: '*.spec.ts',
  globalSetup: './lib/global-setup.ts',
  workers: 1,
  fullyParallel: false,
  retries: 0,
  reporter: [['list']],
  outputDir: './test-results-csp',
  timeout: 90_000,
  use: {
    browserName: 'chromium',
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 1,
    timezoneId: 'UTC',
    locale: 'en-GB',
    reducedMotion: 'reduce',
    colorScheme: 'dark',
    actionTimeout: 8_000,
    navigationTimeout: 15_000,
    launchOptions: exe ? { executablePath: exe, args: ['--no-sandbox'] } : {}
  }
});
