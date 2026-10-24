import { defineConfig, type Plugin } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig(({ command }) => {
  const env = command === 'serve' ? process.env : {};
  const token = env.AUDIT_SESSION_TOKEN ?? '';
  const apiTarget = env.AUDIT_API_TARGET ?? 'http://127.0.0.1:18765';
  // The page never carries the token; like the backend, the dev server prints a
  // launch link that passes it in the fragment, which the page reads and removes.
  const printDevLink: Plugin = {
    name: 'audit-explorer-dev-session',
    configureServer(server) {
      server.httpServer?.once('listening', () => {
        const address = server.httpServer?.address();
        if (!token || !address || typeof address === 'string') return;
        console.log('  Open Unravis at http://127.0.0.1:' + address.port + '/#t=' + encodeURIComponent(token));
      });
    }
  };
  return {
    plugins: [svelte(), ...(command === 'serve' ? [printDevLink] : [])],
    server: {
      host: '127.0.0.1',
      ...(command === 'serve' ? { proxy: { '/api': { target: apiTarget, changeOrigin: false } } } : {})
    },
    preview: { host: '127.0.0.1' }
  };
});
