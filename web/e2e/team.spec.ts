// The team board at scale (each person's threads by what matters, quiet people hidden, the phone list) and the
// morning brief at its top. The board's live updates are in pages.spec.ts.
import { devices, type Page } from '@playwright/test';
import { expect, sql, Status, test, until, type User } from './fixtures.ts';

/** A person on the team board. */
const person = (page: Page, user: User | 'You') =>
	page.locator('section', { has: page.getByRole('heading', { name: user === 'You' ? /^\S+ You\b/ : user.id }) });

/** Opens the team board and waits until it marked the brief seen. */
async function visit(page: Page) {
	const seen = page.waitForResponse(/BriefSeen/);
	await page.goto('/team');
	await seen;
}

test('team board: alice’s threads grouped by what needs her, paused ones folded; quiet carol only when asked', async ({ as }) => {
	const [alice, bob, carol] = [as('alice'), as('bob'), as('carol')];
	await carol.users.me({}); // on the team, without threads
	const [working, waiting, done, older] = await Promise.all([
		alice.create({ prompt: 'e2e-hold:grouped' }),
		alice.create({ prompt: 'e2e: waits for alice' }),
		alice.create({ prompt: 'e2e: done today' }),
		alice.create({ prompt: 'e2e: done last week' })
	]);
	await Promise.all([waiting, done, older].map((t) => t.waitForStatus(Status.AWAITING_INPUT)));
	await Promise.all([done, older].map((t) => t.snooze()));
	await Promise.all([done, older].map((t) => t.waitForStatus(Status.SUSPENDED)));
	// Paused 3 days ago; again if the suspend's last events (the push report) land after it.
	await older.podGone();
	await until(async () => {
		await sql(`UPDATE threads SET activity = NULL, updated_at = now() - interval '3 days' WHERE id = '${older.id}'`);
		await new Promise((r) => setTimeout(r, 3000));
		return (await sql(`SELECT updated_at < now() - interval '1 day' FROM threads WHERE id = '${older.id}'`)) === 't';
	}, () => 'the thread kept changing');

	const page = await bob.page();
	await page.goto('/team');
	await expect(page.getByRole('heading', { level: 2 }).filter({ has: page.getByRole('button') }).first()).toContainText('You');
	const col = person(page, alice);
	const group = (name: string) => col.getByRole('region', { name });
	await expect(group('Needs them').getByRole('link', { name: /waits for alice/ })).toBeVisible();
	await expect(group('Working').getByRole('link', { name: /e2e-hold:grouped/ })).toBeVisible();
	await expect(group('Done recently').getByRole('link', { name: /done today/ })).toBeVisible();
	await expect(col.getByRole('link', { name: /done last week/ })).toBeHidden();
	await expect(col.getByRole('heading', { level: 2 })).toContainText('1 waiting · 1 working · 1 done');
	await col.getByRole('button', { name: '1 paused earlier' }).click();
	await expect(group('Paused earlier').getByRole('link', { name: /done last week/ })).toBeVisible();

	await expect(person(page, carol)).toBeHidden();
	await page.getByRole('button', { name: /^Show \d+ quiet$/ }).click();
	await expect(person(page, carol).getByText('Nothing this week.')).toBeVisible();
	await page.getByRole('button', { name: 'Hide quiet' }).click();
	await expect(person(page, carol)).toBeHidden();
	await working.release('grouped');
});

test('brief: bob reads what alice’s agent finished and what she settled, once; alice sees what waits for her', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	await bob.users.me({});
	const page = await bob.page();
	await visit(page); // bob was here: the brief starts now
	const [t, settled] = await Promise.all([alice.create({ prompt: 'e2e: for the brief' }), alice.create({ prompt: 'e2e: settled for the brief' })]);
	await Promise.all([t, settled].map((x) => x.waitForStatus(Status.AWAITING_INPUT)));
	await alice.threads.settle({ threadId: settled.id, settled: true });
	const words = (await t.reply()).split('\n')[0];

	await visit(page);
	const brief = page.getByRole('region', { name: 'Brief' });
	const finished = brief.getByRole('region', { name: 'Finished' }).getByRole('link', { name: /e2e: for the brief/ });
	await expect(finished).toContainText(alice.id);
	await expect(finished).toContainText(words);
	await expect(brief.getByRole('region', { name: 'Settled' }).getByRole('link', { name: /settled for the brief/ })).toBeVisible();
	await finished.click();
	await expect(page).toHaveURL(`/t/${t.id}`);

	await visit(page);
	await expect(brief).toBeVisible();
	await expect(brief.getByRole('link', { name: /e2e: for the brief/ })).toBeHidden(); // seen

	const own = await alice.page();
	await own.goto('/team');
	await expect(own.getByRole('region', { name: 'Waiting on you' }).getByRole('link', { name: /e2e: for the brief/ })).toBeVisible();
});

test('phone: the team board is a list of people, each unfolding to their threads', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const [mine, theirs] = await Promise.all([bob.create({ prompt: 'e2e: bob on the phone' }), alice.create({ prompt: 'e2e: alice on the phone' })]);
	await Promise.all([mine, theirs].map((t) => t.waitForStatus(Status.AWAITING_INPUT)));
	const { defaultBrowserType: _, ...phone } = devices['iPhone 13'];
	const page = await bob.page(phone);
	await page.goto('/team');
	await expect(person(page, 'You').getByRole('link', { name: /bob on the phone/ })).toBeVisible(); // mine unfolded
	const fold = person(page, alice).getByRole('button', { name: new RegExp(alice.id) });
	await expect(fold).toHaveAttribute('aria-expanded', 'false');
	const card = person(page, alice).getByRole('link', { name: /alice on the phone/ });
	await expect(card).toBeHidden();
	await fold.click();
	await expect(card).toBeVisible();
	const [me, her] = await Promise.all([person(page, 'You').boundingBox(), person(page, alice).boundingBox()]);
	expect(her!.y).toBeGreaterThan(me!.y); // below me, not beside
	expect(her!.x).toBe(me!.x);
	expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});
