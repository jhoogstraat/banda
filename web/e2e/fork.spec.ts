// Forks and races: threads started from another one, or together.
import { SetupState } from '../src/lib/gen/banda/v1/banda_pb.ts';
import { expect, refused, repo, Status, test } from './fixtures.ts';

test('a teammate forks a thread with commits: the fork is theirs, starts from the source’s changes on another model, and both link each other', async ({ as }) => {
	const bob = as('bob');
	const src = await as('alice').create();
	await src.waitForStatus(Status.AWAITING_INPUT);
	expect(await src.terminal('git add -A && git commit -qm e2e-commit && echo e2e-committed')).toContain('e2e-committed');
	const { threads } = await bob.threads.fork({ threadId: src.id, prompt: 'e2e: carry on', tries: [{ model: 'claude-haiku-5-5' }] });
	const fork = bob.thread(threads[0].id);
	let info = await fork.get();
	expect([info.ownerId, info.forkedFrom?.threadId]).toEqual([bob.id, src.id]);
	info = await fork.waitForStatus(Status.AWAITING_INPUT);
	expect((await fork.of('setupStep')).filter((s) => s.id === 'fork' && s.state === SetupState.DONE)).toHaveLength(1);
	expect(info.diff).toContain('hello1.txt'); // the source's commit
	expect(await fork.reply()).toContain('with claude-haiku-5-5');
	expect((await src.of('forkNote')).filter((n) => n.threadId === fork.id)).toHaveLength(1);
});

test('a race of 2 runs both tries on their models; compare shows them; picking one settles and pauses the other and notes it in both', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const { threads } = await alice.threads.race({ repoUrl: repo(), baseBranch: 'main', prompt: 'e2e: race', tries: [{}, { model: 'claude-sonnet-5-5' }] });
	const [a, b] = threads.map((t) => alice.thread(t.id));
	await a.waitForStatus(Status.AWAITING_INPUT);
	expect(await a.reply()).toContain('with claude-opus-5-5');
	await b.waitForStatus(Status.AWAITING_INPUT);
	expect(await b.reply()).toContain('with claude-sonnet-5-5');
	const { entries } = await alice.threads.raceCompare({ raceId: threads[0].raceId });
	expect(entries).toHaveLength(2);
	for (const e of entries) {
		expect(e.turns).toBe(1);
		expect(e.costUsd).toBeGreaterThan(0);
		expect(e.thread?.diff).toContain('hello1.txt');
	}
	await refused(bob.threads.racePick({ threadId: a.id }));
	await alice.threads.racePick({ threadId: a.id });
	const other = await b.get();
	expect([other.settled, other.status]).toEqual([true, Status.SUSPENDED]);
	expect((await a.get()).settled).toBe(false);
	for (const t of [a, b]) expect((await t.of('forkNote')).filter((n) => n.picked && n.threadId === a.id)).toHaveLength(1);
});
