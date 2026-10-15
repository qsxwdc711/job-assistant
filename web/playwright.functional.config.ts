import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './tests/functional',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 60000,
  outputDir: '../.cache/verification/full-browser-results',
  reporter: [
    ['list'],
    ['json', { outputFile: '../.cache/verification/full-browser-results.json' }],
    ['html', { outputFolder: '../.cache/verification/full-browser-report', open: 'never' }],
  ],
  use: {
    baseURL: process.env.S2_BROWSER_BASE_URL ?? 'http://127.0.0.1:18081',
    channel: 'msedge', headless: true,
    viewport: { width: 1440, height: 1000 },
    timezoneId: 'Asia/Shanghai',
    screenshot: 'only-on-failure', trace: 'retain-on-failure',
    launchOptions: { args: ['--no-proxy-server'] },
  },
})
