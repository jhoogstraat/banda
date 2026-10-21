// Streams after a lost connection: the thread page (WatchThread from its last seq, presence), the sidebar's inbox and
// the team board reconnect and end up showing what a fresh load shows. setOffline only stops new requests, so the
// test's own API restarts to break the open streams while the page is offline.
import type { Page } from '@playwright/test';
import { expect, Status, test, type User } from './fixtures.ts';

/** The chat as the page shows it: each item's key and text. */
const chat = (page: Page) =>
	page.locator('[data-key]').evaluateAll((els) => els.map((e) => `${e.getAttribute('data-key')} ${e.textContent?.replace(/\s+/g, ' ').trim()}`));

/** 'same' once both pages show the same chat, else both chats. */
async function same(a: Page, b: Page) {
	const [x, y] = [await chat(a), await chat(b)];
	return x.length && JSON.stringify(x) === JSON.stringify(y) ? 'same' : `${x.join('\n')}\n---\n${y.join('\n')}`;
}

const here = (page: Page, user: User) => page.getByRole('group', { name: 'Here now' }).getByTitle(user.id);

test('a thread page catches up after losing the API: no duplicates, no gaps, presence back', async ({ as, startApi }) => {
	const api = await startApi();
	const [alice, bob] = [as('alice', api), as('bob', api)];
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const [alicePage, bobPage] = [await alice.page(), await bob.page()];
	for (const p of [alicePage, bobPage]) {
		await p.goto(`/t/${t.id}`);
		await expect(p.getByText('Created hello1.txt')).toBeVisible();
	}
	await expect(here(alicePage, bob)).toBeVisible();
	await expect(here(bobPage, alice)).toBeVisible();

	await bobPage.context().setOffline(true);
	await api.restart();
	await expect(bobPage.getByText('reconnecting…')).toBeVisible();
	await t.turn(() => t.send('e2e: while bob was away'), 180_000);
	await bobPage.context().setOffline(false);
	await expect(bobPage.getByText('reconnecting…')).toBeHidden();
	await expect(bobPage.getByText('Created hello2.txt')).toBeVisible();
	await expect(here(alicePage, bob)).toBeVisible();
	await expect(here(bobPage, alice)).toBeVisible();

	const fresh = await bob.page();
	await fresh.goto(`/t/${t.id}`);
	await expect(fresh.getByText('Created hello2.txt')).toBeVisible();
	await expect.poll(() => same(bobPage, fresh)).toBe('same');
	await expect.poll(() => same(alicePage, fresh)).toBe('same');
});

test('the sidebar’s inbox and the team board catch up after losing the API', async ({ as, startApi }) => {
	const api = await startApi();
	const [alice, bob] = [as('alice', api), as('bob', api)];
	const t = await alice.create({ prompt: 'e2e: the first' });
	await t.waitForStatus(Status.AWAITING_INPUT);
	await bob.users.me({});
	const page = await bob.page();
	await page.goto('/team');
	const board = page.locator('section', { has: page.getByRole('heading', { name: alice.id }) });
	await expect(board.getByRole('link', { name: /e2e: the first/ })).toBeVisible();

	await page.context().setOffline(true);
	await api.restart();
	await expect(page.getByText('reconnecting…')).toBeVisible();
	await alice.create({ prompt: 'e2e: the second' });
	await t.send(`@${bob.id} look`, { mentionedUserIds: [bob.id] });
	await page.context().setOffline(false);
	await expect(page.getByText('reconnecting…')).toBeHidden();
	await expect(board.getByRole('link', { name: /e2e: the second/ })).toBeVisible();
	await expect(page.getByRole('region', { name: 'Mentioned' }).getByRole('link', { name: /e2e: the first/ })).toBeVisible();
});
