// A thread's environment over time: push, suspend and resume, settle, delete and recreate.
import { AutoPushState, PermissionDecision, PermissionMode } from '../src/lib/gen/banda/v1/banda_pb.js';
import { exists, expect, kexec, kube, png, refused, repo, sql, Status, test, until, type Thread } from './fixtures.ts';

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

// test.fail(): open bugs of the round-4 audit (B<n>), fixed in the next commit.
const settle = (t: Thread) => t.user.threads.settle({ threadId: t.id, settled: true });

test.fail('B2: settling while the last turn’s push is still running waits for it, then schedules the deletion of pushed work', async ({ as }) => {
	const t = await as('alice').create({ prompt: 'e2e: commit one' });
	await t.waitFor('pushed', (i) => i.autoPush === AutoPushState.PUSHED, 90_000);
	await t.holdPush();
	await t.turn(() => t.send('commit two'));
	const settling = settle(t);
	await t.waitForStatus(Status.SUSPENDED);
	await new Promise((r) => setTimeout(r, 2000)); // the push is held: the environment waits for it
	expect(await exists('pod', t.pod)).toBe(true);
	await t.releasePush();
	await settling;
	await t.podGone();
	expect(await t.origin(`log --format=%s ${(await t.get()).branch}`)).toMatch(/^hello 2$/m);
	expect(await sandbox(t.id, 'shutdownTime')).not.toBe('');
});

test.fail('B2: a commit made in the terminal is pushed when the thread is settled; uncommitted work keeps the environment', async ({ as }) => {
	const alice = as('alice');
	const [committed, dirty] = await Promise.all([alice.create({ prompt: 'e2e: commit one' }), alice.create({ prompt: 'e2e: commit one' })]);
	for (const t of [committed, dirty]) await t.waitFor('pushed', (i) => i.autoPush === AutoPushState.PUSHED, 150_000);
	await committed.terminal('echo by-hand > by-hand.txt && git add by-hand.txt && git commit -qm by-hand && echo done');
	await dirty.terminal('echo by-hand > by-hand.txt && echo done');
	await Promise.all([settle(committed), settle(dirty)]);
	expect(await committed.origin(`log --format=%s ${(await committed.get()).branch}`)).toMatch(/^by-hand$/m);
	expect(await sandbox(committed.id, 'shutdownTime')).not.toBe('');
	expect(await sandbox(dirty.id, 'shutdownTime')).toBe('');
	expect((await sql(`select env_delete_at is null from threads where id = '${dirty.id}'`))).toBe('t');
});

test.fail('B5: settling a thread whose agent waits for permission denies it with a reason the agent sees, then suspends', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create({ prompt: 'e2e: permissions' });
	await t.waitForStatus(Status.AWAITING_INPUT);
	await alice.threads.permissionSetMode({ threadId: t.id, mode: PermissionMode.ASK });
	await t.send('e2e-ask please');
	await t.waitFor('to ask for permission', (i) => i.permissionPending, 30_000);
	await settle(t);
	await t.waitForStatus(Status.SUSPENDED);
	await t.podGone();
	const resolved = (await t.of('permissionResolved')).at(-1)!;
	expect(resolved.decision).toBe(PermissionDecision.DENY);
	expect(resolved.message).toMatch(/settled/);
	expect(await t.reply()).toMatch(/^Not allowed: .*settled/);
	expect((await t.get()).permissionPending).toBe(false);
});

test.fail('B6: a terminal open during Retry ends; the thread is then idle-suspended with the page open; Reopen wakes it', async ({ as, startApi }) => {
	test.setTimeout(360_000);
	const api = await startApi({ IDLE_SUSPEND_AFTER: '90s', IDLE_SWEEP_EVERY: '1s' }); // longer than a start under load
	const alice = as('alice', api);
	const t = await alice.create({ repoUrl: repo('setup-fails') });
	const page = await alice.page();
	await page.goto(`/t/${t.id}`);
	await t.waitForStatus(Status.ERRORED, 120_000);
	await page.getByRole('tab', { name: 'Terminal' }).click();
	const term = page.locator('.xterm');
	await expect(term).toContainText('$', { timeout: 30_000 }); // the shell's prompt
	await term.click();
	await page.keyboard.type('echo before-$((6*7))\n');
	await expect(term).toContainText('before-42');
	await page.getByRole('button', { name: 'Retry' }).click();
	await expect(page.getByText('Terminal exited.')).toBeVisible({ timeout: 60_000 });
	await t.waitForStatus(Status.SUSPENDED, 150_000);
	await page.getByRole('button', { name: 'Reopen' }).click();
	await expect(term).toContainText('work$', { timeout: 90_000 }); // resumed, the shell's prompt
	await term.click();
	await page.keyboard.type('echo after-$((6*7))\n');
	await expect(term).toContainText('after-42', { timeout: 90_000 });
});

test.fail('B8: a prompt posted while the thread is being suspended wins: the environment resumes and the agent answers', async ({ as, startApi }) => {
	// The pause holds every suspend between the thread's status and its environment. A snooze races the post
	// here; the idle sweep suspends the same way.
	const api = await startApi({ TEST_SUSPEND_PAUSE: '5s' });
	const t = await as('alice', api).create({ repoUrl: repo('setup-hold') });
	const running = () =>
		expect.poll(() => kube('get', 'pod', t.pod, '-o', 'jsonpath={.status.phase}{.metadata.deletionTimestamp}'), { timeout: 90_000 }).toBe('Running');
	await running();
	await t.release('setup');
	await t.waitForStatus(Status.AWAITING_INPUT);
	// The resumed environment runs its setup again and holds there, past the pause.
	await kexec(t.pod, 'rm ~/.banda/setup.sha256 ~/e2e-release/setup');
	const snoozing = t.snooze();
	await t.waitForStatus(Status.SUSPENDED, 10_000);
	await t.send('e2e: wake up');
	await snoozing;
	await new Promise((r) => setTimeout(r, 2000));
	await running(); // and not suspended by the snooze
	await t.release('setup');
	await t.waitFor('to answer', (i) => i.status === Status.AWAITING_INPUT && i.diff.includes('hello2.txt'));
});

test.fail('B18: the deletion of a settled environment reaches an open page: the banner appears without a reload', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create({ prompt: 'e2e: commit a file' });
	await t.waitFor('pushed', (i) => i.autoPush === AutoPushState.PUSHED, 150_000);
	const page = await alice.page();
	await page.goto(`/t/${t.id}`);
	await settle(t);
	await expect(page.getByText('Environment paused.')).toBeVisible();
	// DELETE_SETTLED_AFTER=20s on the run's API (deploy/dev.sh).
	await expect(page.getByText('Environment deleted.')).toBeVisible({ timeout: 120_000 });
	await expect(page.getByRole('button', { name: 'Recreate environment' })).toBeVisible();
});
