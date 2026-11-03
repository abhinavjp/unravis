import { defineConfig } from '@playwright/test';
import base from './playwright.config';

// Archived guided specs only. Everything else (workers, retries, viewport, the E2E_REPORT
// reporter) comes from the base config; run-e2e.ps1 -Archive sets E2E_REPORT to report/archive.json.
export default defineConfig({ ...base, testDir: './archive/guided' });
