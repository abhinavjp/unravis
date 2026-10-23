import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig(({ command }) => {
  const env = command === 'serve' ? process.env : {};
  const token = env.AUDIT_SESSION_TOKEN ?? '';
  const apiTarget = env.AUDIT_API_TARGET ?? 'http://127.0.0.1:18765';
  const injectDevToken = {
    name: 'audit-explorer-dev-session',
    transformIndexHtml(html: string) {
      if (!token) return html;
      const safeToken = token.replaceAll('&', '&amp;').replaceAll('"', '&quot;').replaceAll('<', '&lt;');
      return html.replace('<meta name="audit-session" content="">', '<meta name="audit-session" content="' + safeToken + '">');
    }
  };
  return {
    plugins: [svelte(), ...(command === 'serve' ? [injectDevToken] : [])],
    server: {
      host: '127.0.0.1',
      ...(command === 'serve' ? { proxy: { '/api': { target: apiTarget, changeOrigin: false } } } : {})
    },
    preview: { host: '127.0.0.1' }
  };
});
