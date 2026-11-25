import { defineConfig } from '@playwright/test';

// BL-324: the Trusted Types report-only and freezePrototype trials (csp/tt.spec.ts). Same demo app and screens as the strict-CSP run, one look (desktop, dark). A trial finding fails the screen.
const exe = process.env.PW_CHROMIUM || undefined;
export default defineConfig({
  testDir: './csp',
  testMatch: 'tt.spec.ts',
  globalSetup: './lib/global-setup.ts',
  workers: 1,
  fullyParallel: false,
  retries: 0,
  reporter: [['list']],
  outputDir: './test-results-tt',
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
