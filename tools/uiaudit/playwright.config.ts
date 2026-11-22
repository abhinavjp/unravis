import { defineConfig } from '@playwright/test';

// The UI audit: every screen, four looks (desktop and phone, dark and light).
// One worker: all screens share one demo app, and the launch link works once.
// A finding does not fail a test; run-audit.mjs reads the results and decides (new errors only, see baseline.json).
const exe = process.env.PW_CHROMIUM || undefined; // e.g. /opt/pw-browsers/chromium when the pinned Playwright browser is not installed
const common = {
  deviceScaleFactor: 1,
  timezoneId: 'UTC',
  locale: 'en-GB',
  reducedMotion: 'reduce' as const,
  browserName: 'chromium' as const,
  actionTimeout: 8_000,
  navigationTimeout: 15_000,
  launchOptions: exe ? { executablePath: exe, args: ['--no-sandbox'] } : {}
};
const desktop = { ...common, viewport: { width: 1440, height: 900 } };
const phone = { ...common, viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: false };

export default defineConfig({
  testDir: './specs',
  testMatch: '*.spec.ts',
  globalSetup: './lib/global-setup.ts',
  workers: 1,
  fullyParallel: false,
  retries: 0,
  reporter: [['list']],
  outputDir: './test-results',
  timeout: 90_000,
  projects: [
    { name: 'desktop-dark', use: { ...desktop, colorScheme: 'dark' } },
    { name: 'desktop-light', use: { ...desktop, colorScheme: 'light' } },
    { name: 'phone-dark', use: { ...phone, colorScheme: 'dark' } },
    { name: 'phone-light', use: { ...phone, colorScheme: 'light' } }
  ]
});
