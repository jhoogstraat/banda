// The app's other pages in a browser: home (a race), compare, the team board, the sidebar's inbox, search and the
// command palette, and settings. The thread page is thread-page.spec.ts; sign-in and out through Dex, oidc.spec.ts.
import { randomBytes } from 'node:crypto';
import { devices, type Page } from '@playwright/test';
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

test('compare without a race lists mine and teammates’ races; a bad link says there is no such race', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const prompt = `e2e-noop ${word()}`;
	const { threads } = await alice.threads.race({ repoUrl: repo(), baseBranch: 'main', prompt, tries: [{}, {}] });
	for (const [user, section] of [[alice, 'Your races'], [bob, 'Teammates’ races']] as const) {
		const page = await user.page();
		await page.goto('/compare');
		const race = page.locator('section', { has: page.getByRole('heading', { name: section }) }).getByRole('link', { name: new RegExp(prompt) });
		await expect(race).toContainText('2 tries');
		await race.click();
		await expect(page).toHaveURL(`/compare?race=${threads[0].raceId}`);
		await expect(page.getByRole('heading', { name: 'Compare 2 tries' })).toBeVisible();
	}
	const page = await alice.page();
	for (const link of ['/compare?race=nosuchrace', '/compare?ids=nosuchthread']) {
		await page.goto(link);
		await expect(page.getByRole('heading', { name: 'No such race' })).toBeVisible();
	}
	await page.getByRole('link', { name: 'Recent races' }).click();
	await expect(page.getByRole('heading', { name: 'Your races' })).toBeVisible();
});

test('compare: tries go by model, update live without polling, show replies and what differs, line files up; a teammate asks an agent to judge; a phone switches tries', async ({ as }) => {
	test.setTimeout(360_000);
	const [alice, bob] = [as('alice'), as('bob')];
	const { threads } = await alice.threads.race({ repoUrl: repo(), baseBranch: 'main', prompt: 'e2e-hold:cmp', tries: [{}, { model: 'claude-sonnet-5-5' }] });
	const [a, b] = threads.map((t) => alice.thread(t.id));
	const page = await alice.page();
	let compares = 0;
	page.on('request', (r) => void (r.url().endsWith('/RaceCompare') && compares++));
	await page.goto(`/compare?race=${threads[0].raceId}`);
	const [opus, sonnet] = [page.getByRole('region', { name: 'opus-5-5 · high', exact: true }), page.getByRole('region', { name: 'sonnet-5-5 · high', exact: true })];
	await expect(opus.getByText('Working', { exact: true })).toBeVisible({ timeout: 120_000 });
	await expect(sonnet.getByText('Working', { exact: true })).toBeVisible({ timeout: 120_000 });
	// Held turns change nothing for a while: nothing is fetched.
	await page.waitForTimeout(2000);
	const before = compares;
	await page.waitForTimeout(5000);
	expect(compares).toBe(before);
	await Promise.all([a.release('cmp'), b.release('cmp')]);
	await expect(page.getByRole('region', { name: 'Last reply of sonnet-5-5 · high' })).toContainText('with claude-sonnet-5-5 · high', { timeout: 60_000 });
	await expect(opus.getByText('Needs input')).toBeVisible();
	await expect(opus.getByText('1 turn', { exact: true })).toBeVisible();
	// Different second turns, and a third in one try only.
	await Promise.all([a.turn(() => a.send('e2e: alpha')), b.turn(() => b.send('e2e: beta'))]);
	await a.turn(() => a.send('e2e: gamma'));
	await expect(page.getByText('What differs: 3 files')).toBeVisible();
	const row = (path: string) => page.getByRole('group', { name: 'What differs' }).getByRole('button', { name: new RegExp(`^${path.replace('.', '\\.')}`) });
	await expect(row('hello2.txt')).toContainText('Differs');
	await expect(row('hello3.txt')).toContainText('Only in 1 of 2');
	await expect(row('hello1.txt')).not.toContainText('Only in');
	await expect(opus.getByText('3 turns', { exact: true })).toBeVisible();
	await row('hello1.txt').click();
	await expect(opus.locator('[data-file="hello1.txt"]')).toBeInViewport();
	await expect(sonnet.locator('[data-file="hello1.txt"]')).toBeInViewport();

	// Bob judges alice's race: a thread of his under a try, with every try's reply and diff.
	const bobs = await bob.page();
	await bobs.goto(`/compare?race=${threads[0].raceId}`);
	await bobs.getByRole('button', { name: 'Ask an agent to judge' }).click();
	await expect(bobs).toHaveURL(/\/t\/\w+$/);
	const judge = bob.thread(bobs.url().split('/').pop()!);
	const info = await judge.get();
	expect([info.ownerId, info.parentId, info.title]).toEqual([bob.id, a.id, 'Judge the 2 tries of "e2e-hold:cmp"']);
	const [first] = await judge.of('message');
	for (const s of ['## Try 1: opus-5-5 · high', '## Try 2: sonnet-5-5 · high', 'with claude-sonnet-5-5', 'hello3.txt', 'e2e: beta']) expect(first.text).toContain(s);

	// A phone stacks the tries under a switcher.
	const { defaultBrowserType: _, ...iphone } = devices['iPhone 13'];
	const phone = await alice.page(iphone);
	await phone.goto(`/compare?race=${threads[0].raceId}`);
	const tries = phone.getByRole('navigation', { name: 'Tries' });
	await expect(tries.getByRole('button', { name: 'opus-5-5 · high' })).toHaveAttribute('aria-current', 'true');
	await tries.getByRole('button', { name: 'sonnet-5-5 · high' }).click();
	await expect(tries.getByRole('button', { name: 'sonnet-5-5 · high' })).toHaveAttribute('aria-current', 'true');
	await expect(tries).toBeInViewport();
	await expect(phone.getByRole('region', { name: 'sonnet-5-5 · high', exact: true }).getByRole('link', { name: 'sonnet-5-5 · high' })).toBeInViewport();
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
	await expect(card).toContainText(`Ready for ${alice.id}`);
	await card.click();
	await expect(page).toHaveURL(`/t/${t.id}`);
});

test('the sidebar’s inbox: my finished thread under Ready; a teammate’s mention under Mentioned, counted in the tab title until read', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const [mine, bobs] = [await alice.create({ prompt: 'e2e: mine' }), await bob.create({ prompt: 'e2e: bob’s' })];
	await Promise.all([mine.waitForStatus(Status.AWAITING_INPUT), bobs.waitForStatus(Status.AWAITING_INPUT)]);
	await alice.users.me({});
	const page = await alice.page();
	await page.goto('/settings'); // a page that marks nothing read
	await expect(page.getByRole('region', { name: 'Ready' }).getByRole('link', { name: /e2e: mine/ })).toBeVisible();

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
		await page.getByRole('textbox', { name: 'Name' }).fill('lower-case');
		await page.getByLabel('Value', { exact: true }).fill('x');
		await page.getByRole('button', { name: 'Add', exact: true }).click();
		await expect(page.getByText('Names are environment variables')).toBeVisible();
		await page.getByRole('textbox', { name: 'Name' }).fill('E2E_TOKEN');
		await page.getByRole('button', { name: 'Add', exact: true }).click();
		await expect(page.getByText('E2E_TOKEN', { exact: true })).toBeVisible();
		await expect(page.getByText(/^set · /)).toBeVisible();
		await page.getByRole('button', { name: 'Replace' }).click();
		await page.getByLabel('New value of E2E_TOKEN').fill('y');
		await page.getByRole('button', { name: 'Save', exact: true }).click();
		await expect(page.getByLabel('New value of E2E_TOKEN')).toBeHidden();
		await page.getByRole('button', { name: 'Delete' }).click();
		await expect(page.getByText('E2E_TOKEN', { exact: true })).toBeHidden();
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
