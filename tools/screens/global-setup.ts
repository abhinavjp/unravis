// The launch link works once. This opens it once, keeps the session token it
// hands back, and gives the tests the address and the token. Each test then
// opens the page at /#t=<token>, which is exactly what the launch link does.
export default async function globalSetup(): Promise<void> {
  const link = process.env.UNRAVIS_LAUNCH_URL;
  // A run of setup-window screens only (P11) needs the setup window, not the demo.
  if (!link && process.env.UNRAVIS_SETUP_URL) return;
  if (!link) {
    throw new Error('UNRAVIS_LAUNCH_URL is not set. Run tools\\screens\\run-screens.ps1, which starts the demo and passes the launch link.');
  }
  const res = await fetch(link, { redirect: 'manual' });
  const location = res.headers.get('location') ?? '';
  const token = /[#&]t=([^&]+)/.exec(location)?.[1];
  if (res.status < 300 || res.status >= 400 || !token) {
    throw new Error('The launch link did not hand over a session (status ' + res.status + '). It works once and expires after 10 minutes; start the demo again.');
  }
  process.env.UNRAVIS_BASE_URL = new URL(link).origin;
  process.env.UNRAVIS_TOKEN = token;
}
