import { defineConfig, devices } from '@playwright/test';

// Run through `mise run e2e`, which starts the API this points at (see deploy/dev.sh).
export default defineConfig({
	testDir: 'e2e',
	fullyParallel: true,
	// Each test runs a few sandbox pods; the kind node has room for about 110, shared with other runs.
	workers: Number(process.env.E2E_WORKERS || 4),
	timeout: 180_000,
	expect: { timeout: 30_000 },
	reporter: [['list'], ['html', { open: 'never' }]],
	use: {
		...devices['Desktop Chrome'],
		baseURL: process.env.E2E_API,
		trace: 'retain-on-failure',
		screenshot: 'only-on-failure'
	}
});
