import { defineConfig } from 'vite'

export default defineConfig({
  root: 'renderer',
  build: {
    outDir: '../dist',
    emptyOutDir: true,
    license: { fileName: 'assets/licenses.txt' },
    rolldownOptions: { output: { postBanner: '/* Bundled dependency licenses at /assets/licenses.txt */' } },
  },
})
