import { defineConfig } from '@playwright/test';

// Same browser settings as the screenshot harness: one Chromium, a fixed viewport,
// UTC, en-GB, no motion. One worker and no retries: a test that needs a retry to pass
// is a flaky test and has to be fixed, not hidden.
export default defineConfig({
  testDir: './specs',
  testMatch: '*.spec.ts',
  workers: 1,
  fullyParallel: false,
  retries: 0,
  forbidOnly: true,
  reporter: [['list'], ['json', { outputFile: process.env.E2E_REPORT || 'report/results.json' }]],
  outputDir: './test-results',
  timeout: 60_000,
  expect: { timeout: 10_000 },
  use: {
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 1,
    timezoneId: 'UTC',
    locale: 'en-GB',
    reducedMotion: 'reduce',
    colorScheme: 'dark',
    acceptDownloads: true,
    browserName: 'chromium',
    trace: 'off'
  }
});
