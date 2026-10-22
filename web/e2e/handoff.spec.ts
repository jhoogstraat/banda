// Sub-threads started with create_thread's wake_parent (the Specification agent's handoff to an Engineer):
// their reports also start a turn of the parent's agent, as a request from the sub-thread's agent, guarded so
// that agents don't keep each other busy without a person. The fake agent writes each prompt it gets into a
// file (deploy/fake-claude), so the parent's diff holds the prompt that woke it.
import { expect, repo, Status, test, until, type Thread } from './fixtures.ts';

/** Starts a sub-thread of parent as its agent does (create_thread). */
async function spawn(parent: Thread, args: object) {
	const out = await parent.mcp('create_thread', { prompt: 'e2e: build it', ...args });
	const id = out.match(/thread_id[^a-z0-9]+([a-z0-9]{26})/)?.[1];
	expect(id, out).toBeTruthy();
	return parent.user.thread(id!);
}

/** The messages child's reports posted in parent to wake its agent. */
const wakes = async (parent: Thread, child: Thread) =>
	(await parent.events()).filter((e) => e.kind.case === 'message' && e.actor === `agent:${child.id}`).map((e) => e.kind.value);

/** In the woken prompt, as the fake's file holds it (JSON, so a newline is \n). */
const asked = (child: Thread) => `agent in thread ${child.id}: [Sub-thread ${child.id} `;
const readIt = (child: Thread) => `\\nYou started this sub-thread with wake_parent. Read it with read_thread (thread_id ${child.id}, include_diff`;

test('a Specification thread hands its spec to an Engineer sub-thread with wake_parent; its finished turn wakes the parent’s agent', async ({ as }) => {
	const parent = await as('alice').create({ profile: 'spec' });
	await parent.waitForStatus(Status.AWAITING_INPUT);
	let child!: Thread;
	const woken = await parent.turn(async () => (child = await spawn(parent, { profile: 'engineer', wake_parent: true })), 180_000);
	expect((await child.get()).profile).toBe('engineer');
	expect(await child.reply()).toBe('Created hello1.txt with claude-opus-5-5 · high'); // the engineer's profile adds no text
	expect(await wakes(parent, child)).toEqual([
		expect.objectContaining({ mentionsAgent: true, text: expect.stringContaining('finished] Created hello1.txt with claude-opus-5-5 · high\nYou started') })
	]);
	// The report still shows as the lineage line, before the request.
	expect((await parent.of('lineageUpdate')).filter((u) => u.threadId === child.id).map((u) => u.status)).toEqual([Status.STARTING, Status.AWAITING_INPUT]);
	expect(woken.diff).toContain(asked(child));
	expect(woken.diff).toContain(`finished] Created hello1.txt with claude-opus-5-5`);
	expect(woken.diff).toContain(readIt(child));
});

test('a failed sub-thread wakes its parent too; one without wake_parent only reports', async ({ as }) => {
	const parent = await as('alice').create();
	await parent.waitForStatus(Status.AWAITING_INPUT);
	const quiet = await spawn(parent, {});
	await parent.waitForEvent(
		(e) => e.kind.case === 'lineageUpdate' && e.kind.value.threadId === quiet.id && e.kind.value.status === Status.AWAITING_INPUT,
		'the report of the sub-thread without wake_parent',
		120_000
	);
	let failing!: Thread;
	const woken = await parent.turn(async () => (failing = await spawn(parent, { repo_url: repo('setup-fails'), wake_parent: true })), 180_000);
	expect((await failing.get()).status).toBe(Status.ERRORED);
	expect(woken.diff).toContain(asked(failing));
	expect(woken.diff).toContain('failed] Setup failed');
	expect(woken.diff).toContain(readIt(failing));
	// The quiet one reported long before and started no turn: the parent ran its first turn and the woken one.
	expect(await wakes(parent, quiet)).toEqual([]);
	expect(await parent.of('turnEnded')).toHaveLength(2);
});

test('loop guard: a sub-thread wakes its parent again only once a person posted in either thread since its last wake', async ({ as }) => {
	const parent = await as('alice').create();
	await parent.waitForStatus(Status.AWAITING_INPUT);
	let child!: Thread;
	await parent.turn(async () => (child = await spawn(parent, { wake_parent: true })), 180_000); // the first report wakes
	const reports = async () => (await parent.of('lineageUpdate')).filter((u) => u.threadId === child.id && u.status === Status.AWAITING_INPUT).length;
	/** The parent's agent asks the sub-thread for more; returns once the sub-thread reported back. */
	const ask = async () => {
		const n = await reports();
		await child.turn(() => parent.mcp('post_message', { thread_id: child.id, text: 'e2e: more', mention_agent: true }));
		await until(async () => (await reports()) > n, () => `report ${n + 1} of ${child.id} in ${parent.id}`);
	};
	await ask(); // agents only since the wake: no turn
	await parent.turn(() => child.turn(() => child.send('e2e: alice in the sub-thread'))); // a person posted in the child
	await ask();
	await parent.turn(() => parent.send('e2e: alice in the parent')); // a person posted in the parent (her own turn)
	await parent.turn(ask);
	// Every report shows as a line; only those after a person's post (and the first) woke the parent's agent, once each.
	const seq = (await parent.events()).flatMap((e) =>
		e.kind.case === 'lineageUpdate' && e.kind.value.threadId === child.id && e.kind.value.status === Status.AWAITING_INPUT
			? ['report']
			: e.kind.case === 'message' && e.actor === `agent:${child.id}`
				? ['wake']
				: []
	);
	expect(seq).toEqual(['report', 'wake', 'report', 'report', 'wake', 'report', 'report', 'wake']);
});

test('a paused parent wakes for its sub-thread’s report', async ({ as }) => {
	const parent = await as('alice').create();
	await parent.waitForStatus(Status.AWAITING_INPUT);
	// The directive on a line of its own: the title (the first line) is quoted in the woken prompt.
	const child = await spawn(parent, { prompt: 'e2e: build it\ne2e-hold:h', wake_parent: true });
	await child.waitForEvent((e) => e.kind.case === 'agentText' && e.kind.value.text === 'Holding h', 'the held turn', 120_000);
	await parent.snooze();
	await parent.waitForStatus(Status.SUSPENDED);
	await parent.podGone();
	const woken = await parent.turn(() => child.release('h'), 180_000);
	expect(woken.diff).toContain(asked(child));
	expect(woken.diff).toContain(readIt(child));
});
