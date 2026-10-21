// The store under load: no pool deadlocks, long threads stay fast, no needless events, slow watchers catch up.
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { DB, expect, Status, test } from './fixtures.ts';

// B4: Update's closure once queried the pool while its transaction held a connection and the thread's lock.
test('with a one-connection pool, approving a teammate’s turn does not deadlock', async ({ as, startApi }) => {
	const api = await startApi({}, { query: 'pool_max_conns=1' });
	const [alice, bob] = [as('alice', api), as('bob', api)];
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await t.as(bob).send('@agent go', { mentionsAgent: true });
	await t.waitFor('a turn request', (i) => i.turnRequests === 1);
	const seq = (await t.events()).filter((e) => e.kind.case === 'turnRequest').at(-1)!.seq;
	await alice.threads.approveTurn({ threadId: t.id, requestSeq: seq }, { timeoutMs: 5000 });
	expect((await t.get()).turnRequests).toBe(0);
});

// B11: posts, approvals and forks loaded every event since the last delivery (or all of them), tool outputs included.
test('a 20k-event thread: posting, resolving turn requests and forking stay fast', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const { stdout } = await promisify(execFile)('../deploy/long-thread.sh', ['20000'], { env: { ...process.env, DB, OWNER: alice.id } });
	const t = alice.thread(stdout.trim());
	await t.get(); // connected
	// The fastest of three, so a busy machine doesn't fail it; they took 140-200 ms each.
	const fastest = async (call: (i: number) => Promise<unknown>) => {
		const ms: number[] = [];
		for (let i = 0; i < 3; i++) {
			const start = performance.now();
			await call(i);
			ms.push(performance.now() - start);
		}
		return Math.round(Math.min(...ms));
	};
	const post = await fastest(() => t.as(bob).send('a note'));
	for (let i = 0; i < 3; i++) await t.as(bob).send('@agent go', { mentionsAgent: true });
	const requests = (await t.events()).filter((e) => e.kind.case === 'turnRequest').map((e) => e.seq);
	const resolve = await fastest((i) =>
		(i < 2 ? alice.threads.declineTurn : alice.threads.approveTurn)({ threadId: t.id, requestSeq: requests[i] })
	);
	const fork = await fastest(() => alice.threads.fork({ threadId: t.id }));
	expect(Math.max(post, resolve, fork), `ms: ${JSON.stringify({ post, resolve, fork })}`).toBeLessThan(50);
});
