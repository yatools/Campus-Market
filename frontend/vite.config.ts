import { configDefaults, defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://127.0.0.1:8000',
      '/uploads': 'http://127.0.0.1:8000',
      '/health': 'http://127.0.0.1:8000',
      '/app-config.json': 'http://127.0.0.1:8000',
    },
  },
  test: {
    environment: 'jsdom',
    exclude: [...configDefaults.exclude, 'e2e/**'],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'json-summary'],
      include: ['src/**/*.{ts,vue}'],
      exclude: ['src/generated/**', 'src/main.ts', 'src/env.d.ts', 'src/types.ts'],
      thresholds: { lines: 70, statements: 70, functions: 27, branches: 60 },
    },
  },
})
