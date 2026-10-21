// What needs me: the decisions queue, the honest sidebar sections and the blocked status.
import { PermissionDecision, PermissionMode, type InboxWatchResponse } from '../src/lib/gen/banda/v1/banda_pb.js';
import { expect, listen, repo, Status, test, until, type User } from './fixtures.ts';

/** The decisions in an inbox snapshot: tool names, or "turn" for a turn request. */
const asked = (r: InboxWatchResponse) =>
	r.decisions.map((d) => (d.request?.kind.case === 'permissionRequested' ? d.request.kind.value.toolName : 'turn')).join(',');

/** The user's inbox stream; latest() waits until its newest snapshot is as wanted. */
function inbox(u: User) {
	const l = listen((signal) => u.threads.inboxWatch({}, { signal }));
	const latest = (want: string, timeout = 30_000) =>
		until(async () => (l.got.length && asked(l.got.at(-1)!) === want) || undefined, () => `${u.id}'s decisions "${want}", got "${l.got.length && asked(l.got.at(-1)!)}"`, timeout);
	return { ...l, latest };
}

test('the inbox lists what I can decide: my agent’s permission requests and plans, teammates’ turn requests; trusted teammates get the permission requests, never the plan', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const t = await alice.create({ prompt: 'e2e: permissions', permissionMode: PermissionMode.ASK });
	await t.waitForStatus(Status.AWAITING_INPUT);
	const [mine, theirs] = [inbox(alice), inbox(bob)];
	await mine.latest('');

	await t.send('e2e-ask please');
	await mine.latest('Bash');
	expect((await t.get()).permissionTools).toEqual(['Bash']);
	const d = mine.got.at(-1)!.decisions[0];
	expect([d.thread?.id, d.thread?.title]).toEqual([t.id, (await t.get()).title]);
	await theirs.latest('');

	await alice.threads.trust({ threadId: t.id, trusted: true });
	await theirs.latest('Bash');
	const id = (await t.of('permissionRequested')).at(-1)!.id;
	await bob.threads.permissionAnswer({ threadId: t.id, requestId: id, decision: PermissionDecision.ALLOW });
	await Promise.all([mine.latest(''), theirs.latest('')]);
	await t.waitForStatus(Status.AWAITING_INPUT);

	await alice.threads.permissionSetMode({ threadId: t.id, mode: PermissionMode.PLAN });
	await t.send('e2e-plan please');
	await mine.latest('ExitPlanMode');
	await new Promise((r) => setTimeout(r, 1000));
	expect(asked(theirs.got.at(-1)!)).toBe(''); // a plan only the owner approves
	await alice.threads.permissionAnswer({ threadId: t.id, requestId: (await t.of('permissionRequested')).at(-1)!.id, decision: PermissionDecision.DENY });
	await mine.latest('');
	await t.waitForStatus(Status.AWAITING_INPUT);

	await alice.threads.trust({ threadId: t.id, trusted: false });
	await t.as(bob).send('@agent e2e: from bob', { mentionsAgent: true });
	await mine.latest('turn');
	await theirs.latest('');
	const seq = mine.got.at(-1)!.decisions[0].request!.seq;
	await alice.threads.declineTurn({ threadId: t.id, requestSeq: seq });
	await mine.latest('');
	mine.stop();
	theirs.stop();
});

test('the Decisions page: requests appear live; y allows, n denies with a note, a always allows, o opens the thread at the card; the header says what the thread waits for', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const t = await alice.create({ prompt: 'e2e: permissions', permissionMode: PermissionMode.ASK });
	await t.waitForStatus(Status.AWAITING_INPUT);
	const page = await alice.page();
	await page.goto('/decisions');
	await expect(page.getByText('Nothing to decide')).toBeVisible();
	const card = page.getByRole('article', { name: /^Decision in / });
	const badge = page.getByRole('link', { name: /^Decisions/ });

	await test.step('y allows once', async () => {
		await t.send('e2e-ask once');
		await expect(card.getByText('echo e2e-permitted >> permitted.txt')).toBeVisible({ timeout: 30_000 });
		await expect(badge).toContainText('1');
		await page.keyboard.press('y');
		await expect(card).toHaveCount(0);
		await t.waitForStatus(Status.AWAITING_INPUT);
		expect(await t.reply()).toBe('Ran it');
	});

	await test.step('n denies with a note', async () => {
		await t.send('e2e-ask twice');
		await expect(card).toHaveCount(1, { timeout: 30_000 });
		await page.keyboard.press('n');
		await expect(card.getByRole('textbox', { name: 'Reason' })).toBeFocused();
		await page.keyboard.type('not today');
		await page.keyboard.press('Enter');
		await expect(card).toHaveCount(0);
		await t.waitForStatus(Status.AWAITING_INPUT);
		expect(await t.reply()).toMatch(/^Not allowed: .*not today/);
	});

	await test.step('a teammate’s turn request, declined with a note from the keyboard', async () => {
		await t.as(bob).send('@agent e2e: from bob', { mentionsAgent: true });
		await expect(card.getByText(`${bob.id} asks the agent:`)).toBeVisible();
		await page.keyboard.press('n');
		await page.keyboard.type('later');
		await page.keyboard.press('Enter');
		await expect(card).toHaveCount(0);
		const resolved = (await t.of('turnRequestResolved')).at(-1)!;
		expect([resolved.approved, resolved.note]).toEqual([false, 'later']);
	});

	await test.step('a always allows; the header says the thread waits for me meanwhile', async () => {
		await t.send('e2e-ask thrice');
		await expect(card).toHaveCount(1, { timeout: 30_000 });
		const tab = await alice.page();
		await tab.goto(`/t/${t.id}`);
		await expect(tab.getByText('Waiting for you · Bash')).toBeVisible();
		await page.bringToFront();
		await page.keyboard.press('a');
		await expect(card).toHaveCount(0);
		await expect(tab.getByText('Waiting for you · Bash')).toBeHidden();
		await t.waitForStatus(Status.AWAITING_INPUT);
		expect((await t.of('permissionResolved')).at(-1)!.decision).toBe(PermissionDecision.ALLOW_ALWAYS);
	});

	await test.step('j/k move between decisions, o opens the selected one in its thread', async () => {
		const other = await alice.create({ prompt: 'e2e: permissions', permissionMode: PermissionMode.ASK });
		await other.waitForStatus(Status.AWAITING_INPUT);
		await t.as(bob).send('@agent e2e: first', { mentionsAgent: true });
		await other.as(bob).send('@agent e2e: second', { mentionsAgent: true });
		await expect(card).toHaveCount(2);
		await page.keyboard.press('j');
		await expect(card.nth(1)).toHaveAttribute('aria-current', 'true');
		await page.keyboard.press('o');
		await expect(page).toHaveURL(new RegExp(`/t/${other.id}#\\d+$`));
	});
});

test('the Decisions page works on a phone: the cards’ own buttons answer', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const page = await alice.page({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true });
	await page.goto('/decisions');
	await t.as(bob).send('@agent e2e: from bob', { mentionsAgent: true });
	const card = page.getByRole('article', { name: /^Decision in / });
	await card.getByRole('button', { name: 'Approve', exact: true }).tap();
	await expect(card).toHaveCount(0);
	await t.waitFor('the approved turn', (i) => i.turnRequests === 0);
});

test('the sidebar: a finished thread is Ready, one blocked on me Needs input and waits for me; a race is one row; the title counts them', async ({ as }) => {
	const alice = as('alice');
	const [done, blocked] = [await alice.create({ prompt: 'e2e: done' }), await alice.create({ prompt: 'e2e: permissions', permissionMode: PermissionMode.ASK })];
	const { threads } = await alice.threads.race({ repoUrl: repo(), baseBranch: 'main', prompt: 'e2e: race', tries: [{}, {}] });
	await Promise.all([done, blocked, ...threads.map((t) => alice.thread(t.id))].map((t) => t.waitForStatus(Status.AWAITING_INPUT)));
	const page = await alice.page();
	await page.goto('/settings'); // a page that marks nothing read
	const section = (name: string) => page.getByRole('region', { name });
	await expect(section('Ready').getByRole('link', { name: /e2e: done/ })).toBeVisible();
	await expect(section('Needs input')).toHaveCount(0);

	await blocked.send('e2e-ask please');
	const row = section('Needs input').getByRole('link', { name: /e2e: permissions/ });
	await expect(row).toBeVisible({ timeout: 30_000 });
	await expect(row).toContainText('Bash');
	await expect(page).toHaveTitle(/^\(\d+\) Banda$/);

	const race = page.getByRole('link', { name: /^e2e: race · 2 tries/ });
	await expect(race).toBeVisible();
	await expect(race).toHaveAttribute('href', `/compare?race=${threads[0].raceId}`);
	await page.getByRole('button', { name: 'Unfold 2 tries' }).click();
	for (const t of threads) await expect(page.getByRole('link', { name: new RegExp(t.title.replace(/[()]/g, '\\$&')) })).toBeVisible();
});
