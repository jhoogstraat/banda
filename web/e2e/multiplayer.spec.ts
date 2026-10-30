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

test('on a subscription only the owner runs the agent: a teammate’s @agent post never reaches it by itself, their chat does, and there is no trust', { tag: '@key' }, async ({ as }) => {
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
	await t.as(bob).send('e2e: chat with alice');
	await t.turn(() => t.send('@agent e2e-echo e2e-noop carry on', { mentionsAgent: true }));
	const prompt = (await t.prompts()).at(-1)!;
	expect(prompt).toContain('e2e: chat with alice');
	expect(prompt).not.toContain('e2e: from bob');
	expect(prompt).not.toContain('e2e: bob asks in a review');
});

test('on a subscription a teammate’s @agent post is a proposal: only the owner uses it as a draft, edits it and sends it as their own', { tag: '@key' }, async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const [t, other] = [await alice.create(), await alice.create({ prompt: 'e2e: another thread' })];
	await Promise.all([t.waitForStatus(Status.AWAITING_INPUT), other.waitForStatus(Status.AWAITING_INPUT)]);
	const proposal = '@agent e2e-echo e2e-noop the login button should also show a spinner while it waits';
	await t.as(bob).send(proposal, { mentionsAgent: true, proposal: false });
	expect((await t.of('message')).at(-1)!.proposal).toBe(true); // the API's fact, whatever a client says
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
	expect((await t.prompts()).at(-1)).not.toContain('Messages posted in this thread'); // only what she sent: the agent gets it once
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

test('a client can’t mark a post a proposal: the owner’s and a plain post aren’t, whatever they send', async ({ as }) => {
	const bob = as('bob');
	const t = await as('alice').create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await t.send('e2e: mine', { proposal: true });
	await t.as(bob).send('e2e: no agent mentioned', { proposal: true });
	await t.send('@agent e2e-noop mine too', { mentionsAgent: true, proposal: true });
	expect((await t.of('message')).map((m) => m.proposal)).toEqual([false, false, false, false]); // with the first prompt
});

test('a proposal’s files and comments come along as Use as draft: chips and comments to remove or edit, sent as the owner’s, and the agent gets them', { tag: '@key' }, async ({ as }) => {
	const [alice, bob, carol] = [as('alice'), as('bob'), as('carol')];
	const [t, other] = [await alice.create(), await alice.create({ prompt: 'e2e: another thread' })];
	await Promise.all([t.waitForStatus(Status.AWAITING_INPUT), other.waitForStatus(Status.AWAITING_INPUT)]);
	const [image, notes, gone, plain] = [
		await bob.attach('dot.png', 'image/png', png),
		await bob.attach('notes.md', '', '# e2e: bob’s notes\n'),
		await bob.attach('gone.md', '', '# e2e: removed\n'),
		await bob.attach('plain.png', 'image/png', png)
	];
	const comments = [
		{ path: 'hello1.txt', startLine: 1, endLine: 1, code: '+hello 1', body: 'e2e: bob’s first comment' },
		{ path: 'hello2.txt', startLine: 1, endLine: 1, code: '+hello 2', body: 'e2e: bob’s second comment' }
	];
	await t.as(bob).send('@agent e2e-echo please look', { mentionsAgent: true, attachments: [{ id: image }, { id: notes }, { id: gone }], review: comments });
	await t.as(bob).send('e2e: not a proposal', { attachments: [{ id: plain }] });

	// Only the owner may post a proposal’s files, and only that thread’s proposals’.
	await refused(t.send('e2e: not proposed', { attachments: [{ id: plain }] }));
	await refused(other.send('e2e: another thread', { attachments: [{ id: image }] }));
	await refused(t.as(carol).send('e2e: not the owner', { attachments: [{ id: image }] }));

	const page = await alice.page();
	await page.goto(`/t/${t.id}`);
	const box = page.locator('textarea[name=message]');
	const form = page.locator('form').filter({ has: box });
	await page.getByRole('button', { name: 'Use as draft' }).click();
	await expect(box).toHaveValue('@agent e2e-echo please look');
	await expect(form.getByRole('img', { name: 'dot.png' })).toBeVisible();
	await expect(form.getByText('notes.md')).toBeVisible();
	await expect(form.getByText('gone.md')).toBeVisible();
	const draft = page.getByRole('group', { name: 'Pending review comments' });
	await expect(draft.getByText('2 pending comments')).toBeVisible();

	// A second click adds nothing twice; the draft survives switching threads, for this thread only.
	await page.getByRole('button', { name: 'Use as draft' }).click();
	await expect(draft.getByText('2 pending comments')).toBeVisible();
	await expect(form.getByText('notes.md')).toHaveCount(1);
	await page.locator(`a[href="/t/${other.id}"]`).first().click();
	await expect(page).toHaveURL(`/t/${other.id}`);
	await expect(form.getByText('notes.md')).toHaveCount(0);
	await page.locator(`a[href="/t/${t.id}"]`).first().click();
	await expect(form.getByText('notes.md')).toBeVisible();

	// She removes a file and a comment, and edits the other comment.
	await form.getByText('gone.md').hover();
	await form.getByRole('button', { name: 'Remove file' }).nth(1).click();
	await expect(form.getByText('gone.md')).toHaveCount(0);
	await draft.getByRole('button', { name: 'Remove the comment on hello2.txt:1' }).click();
	await draft.getByRole('textbox', { name: 'Comment on hello1.txt:1' }).fill('e2e: alice’s version');
	await box.fill('@agent e2e-echo please look, says alice');
	await t.turn(() => box.press('Enter'));
	await expect(draft).toBeHidden();
	await expect(form.getByText('notes.md')).toHaveCount(0);

	const events = (await t.events()).filter((e) => e.kind.case === 'message');
	const mine = events.at(-1)!;
	const sent = mine.kind.value as { attachments: { id: string; name: string }[]; review: { body: string }[]; proposal: boolean };
	expect(mine.actor).toBe(alice.id);
	expect([sent.attachments.map((a) => a.name), sent.review.map((c) => c.body), sent.proposal]).toEqual([['dot.png', 'notes.md'], ['e2e: alice’s version'], false]);
	const prompt = (await t.prompts()).at(-1)!;
	expect(prompt).toContain(`${alice.id}: @agent e2e-echo please look, says alice`);
	expect(prompt).toContain('e2e: not a proposal'); // chat is context, a proposal only what she sent of it
	expect(prompt).toMatch(/hello1\.txt, line 1:.*\+hello 1.*e2e: alice’s version/s);
	expect(prompt).toContain('# e2e: bob’s notes');
	for (const removed of ['e2e: bob’s first comment', 'e2e: bob’s second comment', '# e2e: removed', '@agent e2e-echo please look\n']) expect(prompt).not.toContain(removed);
	expect((await t.of('agentText')).map((a) => a.text)).toContain('Received 2 image blocks (image/png image/png) and 1 text files');
	const proposal = events.find((e) => e.actor === bob.id)!.kind.value as { attachments: unknown[]; review: { body: string }[] };
	expect([proposal.attachments.length, proposal.review[0].body]).toEqual([3, 'e2e: bob’s first comment']); // the proposal stays as posted
});

test('a teammate’s review asking @agent on a subscription is a proposal: the owner sends it from the composer; a review without text works too', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const [page, bobPage] = [await alice.page(), await bob.page()];
	await Promise.all([page.goto(`/t/${t.id}`), bobPage.goto(`/t/${t.id}`)]);

	await bobPage.getByRole('button', { name: 'View diff' }).click();
	await bobPage.locator('[data-file="hello1.txt"]').getByRole('button', { name: 'Comment on line 1' }).click();
	await bobPage.getByPlaceholder('Comment on this line').fill('e2e-echo e2e-noop e2e: bob reviews');
	await bobPage.getByRole('button', { name: 'Add to review' }).click();
	await bobPage.getByRole('checkbox', { name: 'Ask @agent' }).first().check();
	await bobPage.getByRole('button', { name: 'Send review' }).click();
	await expect(bobPage.getByText('suggests a prompt')).toBeVisible();
	const posted = (await t.of('message')).at(-1)!;
	expect([posted.proposal, posted.text, (await t.get()).status]).toEqual([true, '@agent please address these review comments.', Status.AWAITING_INPUT]);

	await page.getByRole('button', { name: 'Use as draft' }).click();
	const box = page.locator('textarea[name=message]');
	const draft = page.getByRole('group', { name: 'Pending review comments' });
	await expect(draft.getByRole('textbox', { name: 'Comment on hello1.txt:1' })).toHaveValue('e2e-echo e2e-noop e2e: bob reviews');
	await t.turn(() => box.press('Enter'));
	await expect(draft).toBeHidden();
	let mine = (await t.events()).filter((e) => e.kind.case === 'message').at(-1)!;
	expect(mine.actor).toBe(alice.id);
	let prompt = (await t.prompts()).at(-1)!;
	expect(prompt).toContain('e2e: bob reviews');
	expect(prompt).not.toContain('Messages posted in this thread');

	// Through the API a review needs no text: the chip still shows, and only the comment goes to the composer.
	await t.as(bob).send('', { mentionsAgent: true, review: [{ path: 'hello1.txt', startLine: 1, endLine: 1, code: '+hello 1', body: 'e2e-echo e2e-noop e2e: bob again' }] });
	await expect(page.getByText('The comments below')).toBeVisible();
	await page.getByRole('button', { name: 'Use as draft' }).nth(1).click();
	await expect(draft.getByText('1 pending comment')).toBeVisible();
	await expect(box).toHaveValue('');
	await t.turn(() => box.press('Enter'));
	mine = (await t.events()).filter((e) => e.kind.case === 'message').at(-1)!;
	expect([mine.actor, (mine.kind.value as { text: string }).text]).toEqual([alice.id, '@agent please address these review comments.']);
	prompt = (await t.prompts()).at(-1)!;
	expect(prompt).toContain('e2e: bob again');
	expect(prompt).not.toContain('Messages posted in this thread');
});

test('a proposal’s file survives the thread’s suspend: the owner’s send wakes it and the agent gets the teammate’s upload', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await t.as(bob).send('@agent e2e-echo see this', { mentionsAgent: true, attachments: [{ id: await bob.attach('dot.png', 'image/png', png) }] });
	await t.snooze();
	await t.podGone();
	const page = await alice.page();
	await page.goto(`/t/${t.id}`);
	await page.getByRole('button', { name: 'Use as draft' }).click();
	const box = page.locator('textarea[name=message]');
	await expect(box).toHaveValue('@agent e2e-echo see this');
	await t.turn(() => box.press('Enter'));
	expect((await t.of('agentText')).map((a) => a.text)).toContain('Received 1 image blocks (image/png) and 0 text files');
	expect((await t.prompts()).at(-1)).toBe('@agent e2e-echo see this\n[attached image: dot.png]');
});

test('a post stays what it was when the thread’s auth changes: a turn that ran isn’t a proposal on a subscription, a proposal stays one on a key', { tag: '@key' }, async ({ as }) => {
	const bob = as('bob');
	const alice = await as('alice').apiKey();
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await alice.threads.trust({ threadId: t.id, trusted: true });
	await t.turn(() => t.as(bob).send('@agent e2e-noop e2e: a real turn', { mentionsAgent: true }));

	await alice.settings.setAuthMethod({ harness: 'claude', authMethod: AuthMethod.SUBSCRIPTION });
	await t.waitFor('the subscription', (i) => !i.teammateTurns);
	await t.as(bob).send('@agent e2e: a proposal', { mentionsAgent: true });
	expect((await t.of('message')).filter((m) => m.text.startsWith('@agent')).map((m) => m.proposal)).toEqual([false, true]);
	const page = await alice.page();
	await page.goto(`/t/${t.id}`);
	const says = (text: string) => page.getByText(text, { exact: true }).locator('xpath=ancestor::div[contains(@class, "gap-3")][1]');
	await expect(says('@agent e2e: a proposal').getByText('suggests a prompt')).toBeVisible();
	await expect(says('@agent e2e-noop e2e: a real turn').getByText('suggests a prompt')).toHaveCount(0);
	await expect(page.getByRole('button', { name: 'Use as draft' })).toHaveCount(1);

	await alice.apiKey();
	await t.waitFor('the key', (i) => i.teammateTurns);
	await page.reload();
	await expect(says('@agent e2e: a proposal').getByText('suggests a prompt')).toBeVisible();
	await expect(says('@agent e2e-noop e2e: a real turn').getByText('suggests a prompt')).toHaveCount(0);
	await expect(page.getByRole('button', { name: 'Use as draft' })).toHaveCount(1);
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
	expect((await t.of('turnRequest'))[0].message!.proposal).toBe(false); // the owner decides on it, so it proposes nothing
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
