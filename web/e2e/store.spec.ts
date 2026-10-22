// The store under load: no pool deadlocks, long threads stay fast, no needless events, slow watchers catch up.
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { DB, expect, listen, sql, Status, test } from './fixtures.ts';

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

// B14: the runner reports the diff after every turn, and each report was a diffChanged that made every viewer fetch it.
test('turns that change no file store no diffChanged', async ({ as }) => {
	const t = await as('alice').create(); // writes hello1.txt
	await t.waitForStatus(Status.AWAITING_INPUT);
	await t.turn(() => t.send('e2e-noop'));
	await t.turn(() => t.send('e2e-noop'));
	await t.turn(() => t.send('write another')); // its diff comes after the noops' on the runner's stream
	await expect.poll(async () => (await t.events()).filter((e) => e.kind.case === 'diffChanged').length).toBe(2);
	await new Promise((r) => setTimeout(r, 1000));
	expect((await t.events()).filter((e) => e.kind.case === 'diffChanged')).toHaveLength(2);
});

// B20: a watcher whose buffer overflowed was dropped and reconnected, flickering presence and losing the stream.
test('a watcher that falls behind catches up from the store: the same events as a fresh load, no duplicates or gaps', async ({ as, startApi }) => {
	const api = await startApi({ WATCH_BUFFER: '4' });
	const alice = as('alice', api);
	const t = await alice.create({ prompt: 'e2e-noop' });
	await t.waitForStatus(Status.AWAITING_INPUT);
	const watch = listen((signal) => alice.threads.watchThread({ threadId: t.id }, { signal }));
	const seen = () => watch.got.flatMap((r) => r.event ?? []);
	await t.turn(() => t.send('e2e-deltas:2000 go'));
	const stored = (await t.events()).map((e) => e.seq);
	await watch.until(() => seen().at(-1)?.seq === stored.at(-1), 'the last stored event');
	watch.stop();
	expect(seen().filter((e) => e.seq > 0n).map((e) => e.seq)).toEqual(stored);
	expect(seen().filter((e) => e.kind.case === 'agentTextDelta').length).toBeLessThan(2000); // it did fall behind
});

// Applying the schema takes table locks even when nothing changes: a starting API waited for (or deadlocked with) live traffic.
test('an API starts while another holds a lock on its tables: a current schema is not applied again', async ({ startApi }) => {
	const api = await startApi();
	void sql(`BEGIN; LOCK TABLE threads IN ACCESS SHARE MODE; SELECT pg_sleep(120); COMMIT`, api.db).catch(() => {});
	const locked = `SELECT count(*) FROM pg_locks l JOIN pg_class c ON c.oid = l.relation WHERE c.relname = 'threads' AND l.granted`;
	await expect.poll(() => sql(locked, api.db)).not.toBe('0');
	await api.restart(); // gives up after 30 s
	await sql(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE query LIKE '%pg_sleep(120)%' AND pid <> pg_backend_pid()`, api.db);
});
