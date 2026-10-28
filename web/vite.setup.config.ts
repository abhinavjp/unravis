import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// The setup window (web/setup) is built into cmd/unravis-setup/ui, which the setup program embeds. The file names carry no hash, so
// the built files committed there change only when the page changes and a clean checkout builds the Go program without npm.
export default defineConfig({
  root: 'setup',
  base: './',
  plugins: [svelte()],
  build: {
    outDir: '../../cmd/unravis-setup/ui',
    emptyOutDir: true,
    assetsInlineLimit: 0,
    rollupOptions: {
      output: {
        entryFileNames: 'assets/setup.js',
        chunkFileNames: 'assets/[name].js',
        assetFileNames: 'assets/[name][extname]'
      }
    }
  }
});
