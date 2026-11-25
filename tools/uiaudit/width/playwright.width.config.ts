import { defineConfig } from '@playwright/test';
const exe = process.env.PW_CHROMIUM || undefined;
const common = { deviceScaleFactor: 1, timezoneId: 'UTC', locale: 'en-GB', reducedMotion: 'reduce' as const, browserName: 'chromium' as const, colorScheme: 'dark' as const,
  actionTimeout: 8_000, navigationTimeout: 15_000, launchOptions: exe ? { executablePath: exe, args: ['--no-sandbox'] } : {} };
const widths = (process.env.WIDTH_LIST ?? '1920,1440,1280,1024,390').split(',').map(Number);
export default defineConfig({
  testDir: '.', testMatch: 'width-survey.spec.ts', globalSetup: '../lib/global-setup.ts', workers: 1, fullyParallel: false, retries: 0,
  reporter: [['dot']], outputDir: '../test-results-width', timeout: 90_000,
  projects: widths.map((w) => ({ name: 'w' + w, use: { ...common, viewport: { width: w, height: 900 }, ...(w < 500 ? { hasTouch: true } : {}) } }))
});
