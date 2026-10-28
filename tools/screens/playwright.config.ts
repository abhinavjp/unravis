import { defineConfig } from '@playwright/test';

// One Chromium, a fixed viewport, a fixed time zone and language, and no motion,
// so a screenshot only changes when the screen changes. The reference renderer is
// the CI Windows runner: baselines that go into git come from there.
const common = {
  viewport: { width: 1440, height: 900 },
  deviceScaleFactor: 1,
  timezoneId: 'UTC',
  locale: 'en-GB',
  reducedMotion: 'reduce' as const,
  browserName: 'chromium' as const
};
const narrow = { ...common, viewport: { width: 390, height: 844 } };

export default defineConfig({
  testDir: '.',
  testMatch: 'capture.spec.ts',
  globalSetup: './global-setup.ts',
  // The launch link works once, and every test starts from the same page, so the tests run one at a time.
  workers: 1,
  fullyParallel: false,
  retries: 0,
  reporter: [['list']],
  outputDir: './test-results',
  timeout: 60_000,
  snapshotPathTemplate: '../../docs/user/screenshots/{arg}-{projectName}{ext}',
  expect: {
    toHaveScreenshot: { maxDiffPixelRatio: 0.001, animations: 'disabled', caret: 'hide' }
  },
  projects: [
    { name: 'dark', use: { ...common, colorScheme: 'dark' } },
    { name: 'light', use: { ...common, colorScheme: 'light' } },
    { name: 'dark-narrow', use: { ...narrow, colorScheme: 'dark' } },
    { name: 'light-narrow', use: { ...narrow, colorScheme: 'light' } }
  ]
});
