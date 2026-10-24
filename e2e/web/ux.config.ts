import { defineConfig, devices } from '@playwright/test';

// The UX tour (ux/): journeys through gummi web as a person takes them,
// photographed and probed at every step into ux-out/ for a UX review to
// read. It asserts nothing about the page and is not part of the e2e suite
// or `make ci`: run it with scripts/web-ux.sh.
export default defineConfig({
  globalSetup: './fixtures/global-setup.ts',
  testDir: './ux',
  testMatch: '*.ux.ts',
  timeout: 300_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [['line']],
  outputDir: 'test-results-ux',
  use: { headless: true, trace: 'off', screenshot: 'off' },
  projects: [
    { name: 'desktop', use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } } },
    { name: 'laptop', use: { ...devices['Desktop Chrome'], viewport: { width: 1024, height: 768 } } },
    {
      name: 'phone',
      use: { ...devices['Desktop Chrome'], viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, isMobile: true, hasTouch: true },
    },
  ],
});
