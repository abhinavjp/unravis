import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// The Lite page (web/lite) is built into cmd/unravis-lite/ui, which the lite program embeds. The file names carry no hash, so
// the built files committed there change only when the page changes and a clean checkout builds the Go program without npm.
export default defineConfig({
  root: 'lite',
  base: './',
  plugins: [svelte()],
  build: {
    outDir: '../../cmd/unravis-lite/ui',
    emptyOutDir: true,
    assetsInlineLimit: 0,
    rollupOptions: {
      output: {
        entryFileNames: 'assets/lite.js',
        chunkFileNames: 'assets/[name].js',
        assetFileNames: 'assets/[name][extname]'
      }
    }
  }
});
