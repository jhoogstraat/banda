// A thread's environment over time: push, suspend and resume, settle, delete and recreate.
import { AutoPushState } from '../src/lib/gen/banda/v1/banda_pb.js';
import { exists, expect, kube, png, refused, repo, Status, test, until } from './fixtures.ts';

test('push reports its result as a notice', async ({ as }) => {
	const t = await as('alice').create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await t.user.threads.push({ threadId: t.id });
	await t.waitForEvent((e) => e.kind.case === 'notice' && e.kind.value.text.startsWith('Push'), 'the push notice', 20_000);
});

test('snoozing suspends the environment; a prompt resumes it with the checkout intact, the settings changed while paused and its images', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await t.snooze();
	expect((await t.get()).status).toBe(Status.SUSPENDED);
	await t.podGone();
	expect(await exists('pvc', `home-${t.pod}`)).toBe(true); // volumes kept

	await test.step('a prompt resumes the environment with the checkout intact and the settings changed while paused', async () => {
		await alice.threads.setAgentSettings({ threadId: t.id, effort: 'max' });
		const image = await alice.attach('wake.png', 'image/png', png);
		const info = await t.turn(() => t.send('wake up', { attachments: [{ id: image }] }));
		expect(await t.reply()).toContain('with claude-opus-5-5 · max');
		expect(info.diff).toContain('hello2.txt');
	});
	await test.step('a prompt’s images wait with it while the environment resumes', async () => {
		expect((await t.of('agentText')).map((a) => a.text)).toContain('Received 1 image blocks (image/png) and 0 text files');
	});
});

const sandbox = (id: string, field: string) => kube('get', 'sandbox', `t-${id}`, '-o', `jsonpath={.spec.${field}}`);

test('auto-push and settling: commits reach origin; settling schedules deletion, unsettling cancels it; after the delay the owner recreates the environment from the pushed branch', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const t = await alice.create({ prompt: 'e2e: commit a file' });
	await t.waitForStatus(Status.AWAITING_INPUT);
	await t.waitFor('pushed', (i) => i.autoPush === AutoPushState.PUSHED, 30_000);
	expect(await t.origin(`log --format=%s ${(await t.get()).branch}`)).toMatch(/^hello 1$/m);

	await test.step('settling a pushed thread schedules its environment’s deletion; unsettling cancels it', async () => {
		await alice.threads.settle({ threadId: t.id, settled: true });
		expect(await sandbox(t.id, 'shutdownPolicy')).toBe('Delete');
		expect(await sandbox(t.id, 'shutdownTime')).not.toBe('');
		await alice.threads.settle({ threadId: t.id, settled: false });
		expect(await sandbox(t.id, 'shutdownTime')).toBe('');
		expect(await sandbox(t.id, 'shutdownPolicy')).not.toBe('Delete'); // the CRD defaults it back to Retain
	});

	await test.step('after the delay the environment and its volume are deleted; the owner recreates it from the pushed branch', async () => {
		// The run's API deletes settled environments after DELETE_SETTLED_AFTER=20s (deploy/dev.sh).
		await alice.threads.settle({ threadId: t.id, settled: true });
		await until(async () => !(await exists('sandbox', t.pod)), () => 'the settled environment is still there', 120_000);
		await until(async () => !(await exists('pvc', `home-${t.pod}`)), () => 'its volume is still there', 60_000);
		// The API goes by its own clock, the controller by the node's: they may disagree by a few seconds.
		await t.waitFor('to report its environment deleted', (i) => i.environmentDeleted, 30_000);
		await refused(bob.threads.environmentRecreate({ threadId: t.id }));
		await alice.threads.environmentRecreate({ threadId: t.id });
		const info = await t.waitForStatus(Status.AWAITING_INPUT);
		expect(info.environmentDeleted).toBe(false);
		expect((await alice.threads.listFiles({ threadId: t.id })).paths).toContain('hello1.txt');
	});
});

test('a thread that can’t push reports it, and settling it keeps its environment', async ({ as }) => {
	const t = await as('alice').create({ repoUrl: repo('readonly') });
	await t.waitFor('unpushable', (i) => i.autoPush === AutoPushState.UNAVAILABLE);
	await t.user.threads.settle({ threadId: t.id, settled: true });
	expect(await sandbox(t.id, 'shutdownTime')).toBe('');
});
