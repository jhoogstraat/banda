// The app's other pages in a browser: home (a race), compare, the team board, the sidebar's inbox, search and the
// command palette, and settings. The thread page is thread-page.spec.ts; sign-in and out through Dex, oidc.spec.ts.
import { randomBytes } from 'node:crypto';
import type { Page } from '@playwright/test';
import { expect, repo, Status, test, type User } from './fixtures.ts';

const word = () => `zebracorn${[...randomBytes(4)].map((b) => String.fromCharCode(97 + (b % 26))).join('')}`;


/** A column of the team board: one person's threads. */
const column = (page: Page, user: User) => page.locator('section', { has: page.getByRole('heading', { name: user.id }) });

test('home: a prompt raced two ways opens compare; picking one settles the other', async ({ as }) => {
	const alice = as('alice');
	const page = await alice.page();
	await page.goto('/');
	await page.getByRole('button', { name: 'Choose repository' }).click();
	await page.getByLabel('Repository URL').fill(repo());
	await page.getByRole('button', { name: 'Use' }).click();
	await page.getByRole('button', { name: 'Try N ways: 1 try' }).click();
	await page.getByRole('menuitemradio', { name: '2 tries' }).click();
	await page.getByPlaceholder('Describe the task.').fill('e2e: race me');
	await page.keyboard.press('Enter');
	await expect(page).toHaveURL(/\/compare\?race=/);
	await expect(page.getByRole('heading', { name: 'Compare 2 tries' })).toBeVisible();
	await expect(page.getByText('1 turn', { exact: true })).toHaveCount(2, { timeout: 120_000 });
	await expect(page.getByText(/^1 file \+2 −0$/)).toHaveCount(2);
	await page.getByRole('button', { name: 'Pick this one' }).first().click();
	await expect(page.getByText('Picked', { exact: true })).toBeVisible();
	await expect(page.getByText('Settled', { exact: true })).toBeVisible();
});

test('compare without tries to compare says so instead of loading forever', async ({ as }) => {
	const page = await as('alice').page();
	await page.goto('/compare');
	await expect(page.getByText('Nothing to compare: no such tries.')).toBeVisible();
	await page.goto('/compare?ids=nosuchthread');
	await expect(page.getByText('Nothing to compare: no such tries.')).toBeVisible();
});

test('team board: bob watches alice’s thread start, work and wait for her, live', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	await alice.users.me({}); // known to the team before bob looks
	const page = await bob.page();
	await page.goto('/team');
	await expect(page.getByRole('heading', { name: 'You' })).toBeVisible();
	const t = await alice.create({ prompt: 'e2e-hold:board' });
	const card = column(page, alice).getByRole('link', { name: /e2e-hold:board/ });
	await expect(card).toBeVisible();
	await expect(column(page, alice).getByText('1 working · 1')).toBeVisible(); // starting counts
	await expect(card).toContainText('Agent: ', { timeout: 90_000 });
	await t.release('board');
	await expect(card).toContainText(`Waiting for ${alice.id}`);
	await card.click();
	await expect(page).toHaveURL(`/t/${t.id}`);
});

test('the sidebar’s inbox: my thread waiting for me under Needs input; a teammate’s mention under Mentioned, counted in the tab title until read', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const [mine, bobs] = [await alice.create({ prompt: 'e2e: mine' }), await bob.create({ prompt: 'e2e: bob’s' })];
	await Promise.all([mine.waitForStatus(Status.AWAITING_INPUT), bobs.waitForStatus(Status.AWAITING_INPUT)]);
	await alice.users.me({});
	const page = await alice.page();
	await page.goto('/settings'); // a page that marks nothing read
	await expect(page.getByRole('region', { name: 'Needs input' }).getByRole('link', { name: /e2e: mine/ })).toBeVisible();

	await bobs.send(`@${alice.id} have a look`, { mentionedUserIds: [alice.id] });
	const mention = page.getByRole('region', { name: 'Mentioned' }).getByRole('link', { name: /e2e: bob’s/ });
	await expect(mention).toBeVisible();
	await expect(page).toHaveTitle(/^\(\d\) Banda$/);
	await mention.click();
	await expect(page.getByText(`@${alice.id} have a look`)).toBeVisible();
	await expect.poll(async () => (await bobs.as(alice).get()).unread).toBe(false);
});

test('search finds a teammate’s words and opens the thread at them; the palette finds threads, messages and actions', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const secret = word();
	const t = await bob.create({ prompt: `e2e: about the ${secret}` });
	await t.waitForStatus(Status.AWAITING_INPUT);
	await alice.users.me({});
	const page = await alice.page();

	await test.step('the search page', async () => {
		await page.goto('/search');
		await page.getByRole('textbox', { name: 'Search', exact: true }).fill(secret);
		const hit = page.getByRole('link', { name: new RegExp(secret) }).first();
		await expect(hit).toContainText(`in e2e: about the ${secret}`);
		await expect(page).toHaveURL(new RegExp(`\\?q=${secret}`)); // shareable
		await hit.click();
		await expect(page).toHaveURL(new RegExp(`/t/${t.id}#\\d+$`));
		await expect(page.getByRole('heading', { name: `e2e: about the ${secret}` })).toBeVisible();
	});
	await test.step('the palette', async () => {
		await page.keyboard.press('ControlOrMeta+k');
		const palette = page.getByRole('dialog', { name: 'Command palette' });
		await palette.getByPlaceholder('Search threads, teammates and actions').fill(secret);
		await expect(palette.getByRole('option', { name: new RegExp(`e2e: about the ${secret}`) }).first()).toBeVisible();
		await expect(palette.getByRole('option', { name: `See all results for “${secret}”` })).toBeVisible();
		await palette.getByPlaceholder('Search threads, teammates and actions').fill('go to team');
		await palette.getByRole('option', { name: /Go to Team/ }).click();
		await expect(page).toHaveURL('/team');
		await expect(palette).toBeHidden();
	});
});

test('settings: secrets are added, replaced and deleted, never shown; the account switches to an API key and back', async ({ as }) => {
	const page = await as('alice').page();
	await page.goto('/settings');
	page.on('dialog', (d) => d.accept());

	await test.step('secrets', async () => {
		const secrets = page.getByRole('region', { name: 'Secrets & variables' }); // not the team's
		await secrets.getByRole('textbox', { name: 'Name' }).fill('lower-case');
		await secrets.getByLabel('Value', { exact: true }).fill('x');
		await secrets.getByRole('button', { name: 'Add', exact: true }).click();
		await expect(secrets.getByText('Names are environment variables')).toBeVisible();
		await secrets.getByRole('textbox', { name: 'Name' }).fill('E2E_TOKEN');
		await secrets.getByRole('button', { name: 'Add', exact: true }).click();
		await expect(secrets.getByText('E2E_TOKEN', { exact: true })).toBeVisible();
		await expect(secrets.getByText(/^set · /)).toBeVisible();
		await secrets.getByRole('button', { name: 'Replace' }).click();
		await secrets.getByLabel('New value of E2E_TOKEN').fill('y');
		await secrets.getByRole('button', { name: 'Save', exact: true }).click();
		await expect(secrets.getByLabel('New value of E2E_TOKEN')).toBeHidden();
		await secrets.getByRole('button', { name: 'Delete' }).click();
		await expect(secrets.getByText('E2E_TOKEN', { exact: true })).toBeHidden();
	});
	await test.step('the account’s sign-in method', async () => {
		await page.getByRole('radio', { name: 'API key' }).check();
		const key = page.getByPlaceholder(/write-only/);
		await key.fill('sk-ant-e2e');
		await page.getByRole('button', { name: 'Save key' }).click();
		await expect(page.getByText('Connected (API key)')).toBeVisible();
		await expect(key).toHaveAttribute('placeholder', /^Replace the stored key/);
		await page.getByRole('radio', { name: 'Subscription' }).check();
		await expect(page.getByText('Connected (API key)')).toBeHidden();
		await expect(page.getByRole('radio', { name: 'Subscription' })).toBeChecked();
	});
});
