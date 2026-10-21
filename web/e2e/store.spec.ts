// The store under load: no pool deadlocks, long threads stay fast, no needless events, slow watchers catch up.
import { expect, Status, test } from './fixtures.ts';

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
