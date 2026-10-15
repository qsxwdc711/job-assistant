import { defineConfig } from '@playwright/test'
export default defineConfig({
  testDir: './tests/e2e', fullyParallel: false, workers: 1, retries: 0, timeout: 90000,
  outputDir: '../.cache/verification/s2-browser-results',
  reporter: [['list'], ['html', { outputFolder: '../.cache/verification/s2-browser-report', open: 'never' }]],
  use: { baseURL: process.env.S2_BROWSER_BASE_URL ?? 'http://127.0.0.1:19080', channel: 'msedge', headless: true, viewport: { width: 1440, height: 1000 }, timezoneId: 'Asia/Shanghai', trace: 'retain-on-failure', launchOptions: { args: ['--no-proxy-server'] } },
})
