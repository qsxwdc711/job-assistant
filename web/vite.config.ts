import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  css: { postcss: { plugins: [] } },
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': { target: 'http://127.0.0.1:8080' },
      '/health': { target: 'http://127.0.0.1:8080' },
    },
  },
})
