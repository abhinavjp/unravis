import base from './playwright.config';
export default { ...base, use: { ...base.use, launchOptions: { executablePath: '/opt/pw-browsers/chromium', args: ['--no-sandbox'] } }, reporter: [['line'], ['json', { outputFile: process.env.E2E_REPORT }]] };
