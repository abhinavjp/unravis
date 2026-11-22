import { defineConfig } from '@playwright/test';

// Checks the checker: lib/page-checks.js must find the faults planted in fixtures/bad.html and nothing in fixtures/good.html.
const exe = process.env.PW_CHROMIUM || undefined;
export default defineConfig({
  testDir: './tests',
  testMatch: '*.spec.ts',
  workers: 1,
  reporter: [['list']],
  outputDir: './test-results-selftest',
  use: { browserName: 'chromium', viewport: { width: 600, height: 700 }, colorScheme: 'light', launchOptions: exe ? { executablePath: exe, args: ['--no-sandbox'] } : {} }
});
