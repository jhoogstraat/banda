// Reproductions of open bugs from the round-4 correctness audit (B<n>), one per harness capability.
// test.fail(): they pass while the bug is there. Fixing a bug makes its test fail: then turn it into
// a plain test() in the spec file of its domain.
import { AutoPushState, SetupState } from '../src/lib/gen/banda/v1/banda_pb.js';
import { expect, kube, listen, repo, Status, test } from './fixtures.ts';

test.fail('B1(b): a teammate’s note posted while the thread starts reaches the agent', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const t = await alice.create({ repoUrl: repo('setup-hold'), prompt: 'first e2e-echo' });
	await expect.poll(() => kube('get', 'pod', t.pod, '-o', 'jsonpath={.status.phase}'), { timeout: 60_000 }).toBe('Running');
	await t.as(bob).send('TEAMMATE-NOTE use postgres');
	await t.release('setup');
	await t.waitForStatus(Status.AWAITING_INPUT);
	await t.turn(() => t.send('@agent second e2e-echo', { mentionsAgent: true }));
	expect((await t.prompts()).join('\n')).toContain('TEAMMATE-NOTE');
});

test.fail('B2: settling while the last turn’s push is still running schedules no deletion of the unpushed work', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create({ prompt: 'e2e: commit one' });
	await t.waitFor('pushed', (i) => i.autoPush === AutoPushState.PUSHED, 30_000);
	await t.holdPush();
	await t.turn(() => t.send('commit two'));
	await alice.threads.settle({ threadId: t.id, settled: true });
	await t.podGone();
	expect(await kube('get', 'sandbox', t.pod, '-o', 'jsonpath={.spec.shutdownTime}')).toBe('');
});

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

test.fail('B6: a terminal open during Retry ends or keeps working', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create({ repoUrl: repo('setup-fails') });
	await t.waitForStatus(Status.ERRORED);
	const term = listen((signal) => alice.threads.openTerminal({ threadId: t.id, cols: 80, rows: 24 }, { signal }));
	const output = () => term.got.map((r) => Buffer.from(r.data).toString()).join('');
	await expect.poll(() => term.got.length).toBeGreaterThan(0); // the shell is up
	await alice.threads.writeTerminal({ threadId: t.id, data: Buffer.from('echo before-retry\n') });
	await expect.poll(output).toContain('before-retry');
	await alice.threads.retry({ threadId: t.id });
	const failed = async () => (await t.of('setupStep')).filter((s) => s.id === 'setup' && s.state === SetupState.FAILED).length;
	await expect.poll(failed, { timeout: 90_000 }).toBe(2); // the retried setup failed again
	await alice.threads.writeTerminal({ threadId: t.id, data: Buffer.from('echo after-retry\n') }).catch(() => {});
	await expect.poll(() => term.got.some((r) => r.exited) || output().includes('after-retry'), { timeout: 20_000 }).toBe(true);
	term.stop();
});

test.fail('B9: an API restart mid-turn tells the chat the turn was lost', async ({ as, startApi }) => {
	const api = await startApi();
	const t = await as('alice', api).create({ prompt: 'e2e-hold:forever' });
	await t.waitForEvent((e) => e.kind.case === 'agentText' && e.kind.value.text === 'Holding forever', 'the held turn');
	await api.restart();
	await t.waitForStatus(Status.AWAITING_INPUT, 180_000);
	expect(await t.of('turnEnded')).toHaveLength(1);
});
