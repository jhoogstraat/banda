// Reproductions of open bugs from the round-4 correctness audit (B<n>), one per harness capability.
// test.fail(): they pass while the bug is there. Fixing a bug makes its test fail: then turn it into
// a plain test() in the spec file of its domain.
import { AutoPushState, SetupState } from '../src/lib/gen/banda/v1/banda_pb.js';
import { expect, kube, listen, repo, Status, test } from './fixtures.ts';

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
