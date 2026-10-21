// Reproductions of open bugs from the round-4 correctness audit (B<n>), one per harness capability.
// test.fail(): they pass while the bug is there. Fixing a bug makes its test fail: then turn it into
// a plain test() in the spec file of its domain.
import { expect, Status, test } from './fixtures.ts';

test.fail('B4: with a one-connection pool, approving a teammate’s turn does not deadlock', async ({ as, startApi }) => {
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
