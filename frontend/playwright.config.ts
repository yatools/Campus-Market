import { defineConfig, devices } from '@playwright/test'

const projects = [
  { name: 'desktop-chromium', use: { ...devices['Desktop Chrome'] } },
  { name: 'desktop-webkit', use: { ...devices['Desktop Safari'] } },
  { name: 'mobile-chromium', use: { ...devices['Pixel 7'] } },
]

// Playwright Firefox 151 在当前 Windows 运行时无法创建 page；Linux CI 仍执行 Firefox 验收。
if (process.platform !== 'win32') projects.splice(1, 0, { name: 'desktop-firefox', use: { ...devices['Desktop Firefox'] } })

export default defineConfig({
  testDir: './e2e',
  use: { baseURL: 'http://127.0.0.1:5173', trace: 'retain-on-failure' },
  projects,
  webServer: process.env.PLAYWRIGHT_NO_WEBSERVER ? undefined : { command: 'npm run dev', url: 'http://127.0.0.1:5173', reuseExistingServer: true },
})
