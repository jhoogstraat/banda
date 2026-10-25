// Teammates in a thread: presence, chat mode, the inbox and team board, turn requests, trust and reviews.
import { AuthMethod, Mode } from '../src/lib/gen/banda/v1/banda_pb.js';
import { expect, listen, png, refused, repo, Status, test, until, type Thread } from './fixtures.ts';

const pause = (ms: number) => new Promise((r) => setTimeout(r, ms));
/** The seq of the thread's latest turn request. */
const lastRequest = async (t: Thread) => (await t.events()).filter((e) => e.kind.case === 'turnRequest').at(-1)!.seq;

test('watchers see who is here and who is typing, and nothing of it is stored', async ({ as }) => {
	const [bob, carol] = [as('bob'), as('carol')];
	const t = await as('alice').create();
	const bobs = t.as(bob);
	const carols = t.as(carol);
	bobs.watch();
	carols.watch();
	await bobs.waitForEvent((e) => e.kind.case === 'presenceChanged' && `${e.kind.value.userIds}` === `${bob.id},${carol.id}`, 'bob and carol present');
	await carol.threads.sendTyping({ threadId: t.id });
	await bobs.waitForEvent((e) => e.kind.case === 'typing' && e.actor === carol.id, 'carol typing');
	bobs.seen.splice(0);
	carols.unwatch();
	await bobs.waitForEvent((e) => e.kind.case === 'presenceChanged' && `${e.kind.value.userIds}` === bob.id, 'only bob present');
	expect((await t.events()).filter((e) => e.kind.case === 'presenceChanged' || e.kind.case === 'typing')).toEqual([]);
});

test('a teammate’s post switches to chat mode; only the owner ends it; the chat records who switched and why', async ({ as }) => {
	const bob = as('bob');
	const t = await as('alice').create();
	const switches = async () => (await t.events()).filter((e) => e.kind.case === 'modeChanged').map((e) => [e.actor, (e.kind.value as { reason: string }).reason]);
	await t.as(bob).send('nice, carol what do you think?', { attachments: [{ id: await bob.attach('dot.png', 'image/png', png) }] });
	expect((await t.get()).mode).toBe(Mode.CHAT);
	await refused(bob.threads.setMode({ threadId: t.id, mode: Mode.AGENT }));
	await t.user.threads.setMode({ threadId: t.id, mode: Mode.AGENT });
	expect((await t.get()).mode).toBe(Mode.AGENT);
	await t.send(`@${bob.id} have a look`, { mentionedUserIds: [bob.id] });
	expect(await switches()).toEqual([
		[bob.id, 'posted'],
		[t.user.id, ''],
		[t.user.id, 'mentioned someone']
	]);
});

test('the inbox stream pushes a new mention live; a teammate’s post makes the thread unread until it is read', { tag: '@key' }, async ({ as }) => {
	const [bob, carol] = [as('bob'), as('carol')];
	const t = await as('alice').create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await t.user.threads.inboxMarkRead({ threadId: t.id });
	expect((await t.get()).unread).toBe(false);
	const inbox = listen((signal) => carol.threads.inboxWatch({}, { signal }));
	await inbox.until(() => true, 'carol’s first inbox snapshot');
	await t.as(bob).send(`@${carol.id} over to you`, { mentionedUserIds: [carol.id] });
	await inbox.until((m) => m.mentioned.some((x) => x.id === t.id), 'the mention in carol’s inbox');
	inbox.stop();
	expect((await t.get()).unread).toBe(true);
});

test('bob’s new thread shows up live on carol’s team board, then its error', { tag: '@key' }, async ({ as }) => {
	const [bob, carol] = [as('bob'), as('carol')];
	const board = listen((signal) => carol.threads.teamWatch({}, { signal }));
	await board.until(() => true, 'carol’s first board');
	const t = await bob.create({ repoUrl: repo('setup-fails'), prompt: 'e2e: bob on the board' });
	const activity = (text: string) => (m: { threads: { id: string; activity?: { text: string } }[] }) =>
		m.threads.find((x) => x.id === t.id)?.activity?.text.startsWith(text);
	await board.until(activity('e2e: bob on the board'), 'bob’s thread on the board', 10_000);
	await board.until(activity('errored: Setup failed'), 'bob’s error on the board', 90_000);
	board.stop();
});

test('on a subscription only the owner runs the agent: a teammate’s @agent post is a plain post the owner’s next prompt carries, and there is no trust', { tag: '@key' }, async ({ as }) => {
	const bob = as('bob');
	const t = await as('alice').create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	expect((await t.get()).teammateTurns).toBe(false);
	await t.as(bob).send('@agent e2e: from bob', { mentionsAgent: true });
	await t.as(bob).send('', { mentionsAgent: true, review: [{ path: 'hello1.txt', startLine: 1, endLine: 1, body: 'e2e: bob asks in a review' }] });
	await pause(2000);
	const info = await t.get();
	expect([info.status, info.turnRequests, info.mode]).toEqual([Status.AWAITING_INPUT, 0, Mode.CHAT]);
	expect(await t.of('turnRequest')).toHaveLength(0);
	expect(await t.of('turnEnded')).toHaveLength(1);
	await refused(t.user.threads.trust({ threadId: t.id, trusted: true }));
	expect((await t.get()).trusted).toBe(false);
	const after = await t.turn(() => t.send('@agent carry on', { mentionsAgent: true }));
	expect(after.diff).toContain('e2e: from bob');
});

test('on a subscription a teammate’s @agent post is a proposal: only the owner uses it as a draft, edits it and sends it as their own', { tag: '@key' }, async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const [t, other] = [await alice.create(), await alice.create({ prompt: 'e2e: another thread' })];
	await Promise.all([t.waitForStatus(Status.AWAITING_INPUT), other.waitForStatus(Status.AWAITING_INPUT)]);
	const proposal = '@agent e2e-echo e2e-noop the login button should also show a spinner while it waits';
	await t.as(bob).send(proposal, { mentionsAgent: true });
	const [page, bobPage] = [await alice.page(), await bob.page()];
	await Promise.all([page.goto(`/t/${t.id}`), bobPage.goto(`/t/${t.id}`)]);
	const box = page.locator('textarea[name=message]');
	await expect(page.getByText('suggests a prompt')).toBeVisible();
	await expect(bobPage.getByText('suggests a prompt')).toBeVisible();
	await expect(bobPage.getByRole('button', { name: 'Use as draft' })).toHaveCount(0);
	const info = await t.get();
	expect([info.status, info.turnRequests]).toEqual([Status.AWAITING_INPUT, 0]);

	// What alice already typed stays; the proposal goes on a new line, focused, for her to edit.
	await box.fill('e2e: alice first,');
	// Each thread keeps its own draft across switching threads in the app.
	await page.locator(`a[href="/t/${other.id}"]`).first().click();
	await expect(page).toHaveURL(`/t/${other.id}`);
	await expect(box).toHaveValue('');
	await page.locator(`a[href="/t/${t.id}"]`).first().click();
	await expect(box).toHaveValue('e2e: alice first,');
	await page.getByRole('button', { name: 'Use as draft' }).click();
	await expect(box).toHaveValue(`e2e: alice first,\n${proposal}`);
	await expect(box).toBeFocused();
	await page.keyboard.type(', and e2e: alice’s edit');
	const sent = await t.turn(() => box.press('Enter'));
	expect(sent.status).toBe(Status.AWAITING_INPUT);
	const mine = (await t.events()).filter((e) => e.kind.case === 'message').at(-1)!;
	expect([mine.actor, (mine.kind.value as { text: string }).text]).toEqual([alice.id, `e2e: alice first,\n${proposal}, and e2e: alice’s edit`]);
	expect((await t.prompts()).at(-1)).toContain('e2e: alice first,');
	expect((await t.prompts()).at(-1)).toContain('e2e: alice’s edit');
	expect(await t.of('turnRequest')).toHaveLength(0);
	await expect(box).toHaveValue('');

	// The box clears in the thread a message was sent from, even when another is open by the time the send finishes.
	let release!: () => void;
	const held = new Promise<void>((r) => (release = r));
	await page.route('**/banda.v1.ThreadService/SendMessage', async (route) => {
		await held;
		await route.continue();
	});
	await box.fill('@agent e2e-echo e2e-noop e2e: alice second');
	await t.turn(async () => {
		await box.press('Enter');
		await page.locator(`a[href="/t/${other.id}"]`).first().click();
		await expect(page).toHaveURL(`/t/${other.id}`);
		await box.fill('e2e: other draft');
		const sentNow = page.waitForResponse('**/banda.v1.ThreadService/SendMessage');
		release();
		await sentNow;
	});
	await page.unrouteAll();
	await page.locator(`a[href="/t/${t.id}"]`).first().click();
	await expect(box).toHaveValue('');
	await page.locator(`a[href="/t/${other.id}"]`).first().click();
	await expect(box).toHaveValue('e2e: other draft');

	// On an API key the same post is still a turn request, with no draft button.
	const carol = await as('carol').apiKey();
	const k = await carol.create();
	await k.waitForStatus(Status.AWAITING_INPUT);
	await k.as(bob).send(proposal, { mentionsAgent: true });
	expect(await k.of('turnRequest')).toHaveLength(1);
	const carolPage = await carol.page();
	await carolPage.goto(`/t/${k.id}`);
	await expect(carolPage.getByRole('button', { name: 'Approve', exact: true })).toBeVisible();
	await expect(carolPage.getByRole('button', { name: 'Use as draft' })).toHaveCount(0);
});

test('switching to a subscription withdraws pending turn requests, saying why, and turns trust off for good', async ({ as }) => {
	const bob = as('bob');
	const alice = await as('alice').apiKey();
	const [asked, trusted] = [await alice.create(), await alice.create()];
	await Promise.all([asked, trusted].map((t) => t.waitForStatus(Status.AWAITING_INPUT)));
	await alice.threads.trust({ threadId: trusted.id, trusted: true });
	await asked.as(bob).send('@agent e2e: from bob', { mentionsAgent: true });
	await asked.waitFor('a turn request', (i) => i.turnRequests === 1);
	const seq = await lastRequest(asked);

	await alice.settings.setAuthMethod({ harness: 'claude', authMethod: AuthMethod.SUBSCRIPTION });
	await asked.waitFor('the request withdrawn', (i) => i.turnRequests === 0);
	const withdrawn = (await asked.of('turnRequestResolved')).at(-1)!;
	expect([withdrawn.requestSeq, withdrawn.approved, withdrawn.note]).toEqual([seq, false, expect.stringContaining('Fork to run it on yours')]);
	await refused(alice.threads.approveTurn({ threadId: asked.id, requestSeq: seq }));
	expect((await trusted.get()).trusted).toBe(false);

	await alice.apiKey();
	expect((await trusted.get()).trusted).toBe(false);
	await trusted.as(bob).send('@agent e2e: from bob again', { mentionsAgent: true });
	await trusted.waitFor('a turn request again', (i) => i.turnRequests === 1);
});

test('switching to an API key mid-turn: a teammate’s turn waits until the environment restarts on the key, then runs on it', { tag: '@key' }, async ({ as }) => {
	const bob = as('bob');
	const alice = as('alice');
	const t = await alice.create({ prompt: 'e2e-auth e2e-hold:sub' });
	await t.waitFor('the held turn on the subscription', (i) => i.status === Status.WORKING);
	await until(async () => (await t.of('agentText')).some((a) => a.text === 'Auth: subscription') || undefined, () => 'the agent to say it runs on the subscription');

	await alice.apiKey();
	expect((await t.get()).teammateTurns).toBe(true);
	await t.as(bob).send('@agent e2e-auth e2e: from bob', { mentionsAgent: true });
	await t.waitFor('a turn request', (i) => i.turnRequests === 1);
	const seq = await lastRequest(t);
	expect(await refused(alice.threads.approveTurn({ threadId: t.id, requestSeq: seq }))).toContain('still runs on your Claude subscription');
	expect(await refused(alice.threads.trust({ threadId: t.id, trusted: true }))).toContain('still runs on your Claude subscription');

	await t.release('sub');
	await t.waitForStatus(Status.SUSPENDED); // the turn ended: it restarts on the key with the next prompt
	await t.podGone();
	const info = await t.turn(() => alice.threads.approveTurn({ threadId: t.id, requestSeq: seq }));
	expect(info.diff).toContain(`${bob.id}: @agent e2e-auth e2e: from bob`);
	expect((await t.of('agentText')).filter((a) => a.text.startsWith('Auth: ')).map((a) => a.text)).toEqual(['Auth: subscription', 'Auth: api key']);
	await alice.threads.trust({ threadId: t.id, trusted: true });
});

test('a teammate’s @agent post waits for the owner; approving runs it as the teammate’s turn', { tag: '@key' }, async ({ as }) => {
	const bob = as('bob');
	const t = await (await as('alice').apiKey()).create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await t.as(bob).send('@agent one more', { mentionsAgent: true });
	await pause(2000);
	let info = await t.get();
	expect([info.status, info.turnRequests]).toEqual([Status.AWAITING_INPUT, 1]);
	expect(info.diff).not.toContain('hello2.txt');
	const seq = await lastRequest(t);
	await refused(bob.threads.approveTurn({ threadId: t.id, requestSeq: seq }));
	info = await t.turn(() => t.user.threads.approveTurn({ threadId: t.id, requestSeq: seq }));
	expect(info.diff).toContain(`${bob.id}: @agent one more`);
	expect(info.turnRequests).toBe(0);
	await refused(t.user.threads.approveTurn({ threadId: t.id, requestSeq: seq }));
});

test('declining a request runs no turn and keeps the note', async ({ as }) => {
	const bob = as('bob');
	const t = await (await as('alice').apiKey()).create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await t.as(bob).send('@agent not this', { mentionsAgent: true });
	await t.waitFor('a turn request', (i) => i.turnRequests === 1);
	await t.user.threads.declineTurn({ threadId: t.id, requestSeq: await lastRequest(t), note: 'not now' });
	await pause(2000);
	const info = await t.get();
	expect([info.status, info.turnRequests]).toEqual([Status.AWAITING_INPUT, 0]);
	expect(info.diff).not.toContain('hello2.txt');
	expect((await t.of('turnRequestResolved')).at(-1)?.note).toBe('not now');
});

test('in a trusted thread a teammate’s @agent post runs directly', async ({ as }) => {
	const bob = as('bob');
	const t = await (await as('alice').apiKey()).create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await refused(bob.threads.trust({ threadId: t.id, trusted: true }));
	await t.user.threads.trust({ threadId: t.id, trusted: true });
	expect((await t.get()).trusted).toBe(true);
	const info = await t.turn(() => t.as(bob).send('@agent trusted', { mentionsAgent: true }));
	expect(info.diff).toContain(`${bob.id}: @agent trusted`);
});

const review = { path: 'hello1.txt', startLine: 1, endLine: 1, code: '+hello 1', body: 'e2e: say hi instead' };

test('the owner’s review is a note until it asks @agent; then the agent gets each comment with its code', async ({ as }) => {
	const t = await as('alice').create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await refused(t.send('', { review: [{ path: 'x', startLine: 1, endLine: 1, body: ' ' }] }));
	await t.send('', { review: [review] });
	const info = await t.get();
	expect([info.status, info.mode]).toEqual([Status.AWAITING_INPUT, Mode.AGENT]);
	const after = await t.turn(() => t.send('@agent please address these review comments.', { mentionsAgent: true, review: [review] }));
	expect(after.diff).toMatch(/hello1\.txt, line 1:.*\+hello 1.*e2e: say hi instead/);
});

test('a teammate’s review asking @agent waits for approval, then reaches the agent with its comments', async ({ as }) => {
	const bob = as('bob');
	const t = await (await as('alice').apiKey()).create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await t.as(bob).send('@agent see my review', {
		mentionsAgent: true,
		review: [{ path: 'hello2.txt', startLine: 1, endLine: 1, code: '+hello 2', body: 'e2e: bob reviews' }]
	});
	await pause(2000);
	const info = await t.get();
	expect(info.turnRequests).toBe(1);
	expect(info.diff).not.toContain('e2e: bob reviews');
	const seq = await lastRequest(t);
	const after = await t.turn(() => t.user.threads.approveTurn({ threadId: t.id, requestSeq: seq }));
	expect(after.diff).toMatch(new RegExp(`${bob.id}: @agent see my review.*hello2\\.txt, line 1:.*e2e: bob reviews`));
});
