// The web app end to end, one browser context per user: the template for UI tests.
import { devices } from '@playwright/test';
import { PermissionMode } from '../src/lib/gen/banda/v1/banda_pb.js';
import { expect, repo, Status, test } from './fixtures.ts';

const composer = 'Ask the agent…'; // the owner's, in agent mode

test('a thread created from the home page streams the agent’s reply in', { tag: '@key' }, async ({ as }) => {
	const page = await as('alice').page();
	await page.goto('/');
	await page.getByRole('button', { name: 'Choose repository' }).click();
	await page.getByLabel('Repository URL').fill(repo());
	await page.getByRole('button', { name: 'Use' }).click();
	await page.getByPlaceholder('Describe the task.').fill('e2e: from the browser');
	await page.keyboard.press('Enter');
	await expect(page).toHaveURL(/\/t\/[a-z0-9]+$/);
	await expect(page.getByText('Created hello1.txt with claude-opus-5-5 · high')).toBeVisible({ timeout: 90_000 });
});

test('bob opens alice’s thread and sees her next message and the agent’s reply live', { tag: '@key' }, async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const [alicePage, bobPage] = [await alice.page(), await bob.page()];
	await bobPage.goto(`/t/${t.id}`);
	await expect(bobPage.getByText('Created hello1.txt')).toBeVisible();
	await alicePage.goto(`/t/${t.id}`);
	await alicePage.getByPlaceholder(composer).fill('e2e: live for bob');
	await alicePage.keyboard.press('Enter');
	await expect(bobPage.getByText('e2e: live for bob')).toBeVisible();
	await expect(bobPage.getByText('Created hello2.txt')).toBeVisible({ timeout: 60_000 });
});

test('the owner answers a permission card in the browser', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await alice.threads.permissionSetMode({ threadId: t.id, mode: PermissionMode.ASK });
	const page = await alice.page();
	await page.goto(`/t/${t.id}`);
	await t.send('e2e-ask from the browser');
	await page.getByRole('button', { name: 'Allow once' }).click();
	await expect(page.getByText('Ran it')).toBeVisible({ timeout: 60_000 });
	expect(await t.reply()).toBe('Ran it');
});

// setOffline only stops new requests; stopping the test's own API breaks the open stream too.
test('a teammate’s page reconnects after losing the API and catches up on what it missed', async ({ as, startApi }) => {
	const api = await startApi();
	const [alice, bob] = [as('alice', api), as('bob', api)];
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const page = await bob.page();
	await page.goto(`/t/${t.id}`);
	await expect(page.getByText('Created hello1.txt')).toBeVisible();
	await page.context().setOffline(true);
	await api.restart();
	await expect(page.getByText('reconnecting…')).toBeVisible();
	await t.turn(() => t.send('e2e: while bob was away'), 180_000);
	await expect(page.getByText('e2e: while bob was away')).toBeHidden();
	await page.context().setOffline(false);
	await expect(page.getByText('e2e: while bob was away').first()).toBeVisible();
	await expect(page.getByText('Created hello2.txt')).toBeVisible();
	await expect(page.getByText('reconnecting…')).toBeHidden();
});

test('phone: the thread page fits the screen and its composer works', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const { defaultBrowserType: _, ...phone } = devices['iPhone 13'];
	const page = await alice.page(phone);
	await page.goto(`/t/${t.id}`);
	await expect(page.getByText('Created hello1.txt')).toBeVisible();
	expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
	await page.getByPlaceholder(composer).fill('e2e: from the phone');
	await page.keyboard.press('Enter');
	await expect(page.getByText('Created hello2.txt')).toBeVisible({ timeout: 60_000 });
});
