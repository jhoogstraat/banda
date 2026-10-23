// Teammates in a thread: presence, chat mode, the inbox and team board, turn requests, trust and reviews.
import { Mode } from '../src/lib/gen/banda/v1/banda_pb.js';
import { expect, listen, png, refused, repo, Status, test, type Thread } from './fixtures.ts';

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

test('a teammate’s @agent post waits for the owner; approving runs it as the teammate’s turn', { tag: '@key' }, async ({ as }) => {
	const bob = as('bob');
	const t = await as('alice').create();
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
	const t = await as('alice').create();
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
	const t = await as('alice').create();
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
	const t = await as('alice').create();
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
