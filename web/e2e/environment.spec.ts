// A thread's environment over time: push, suspend and resume, settle, delete and recreate.
import net from 'node:net';
import { AutoPushState, LoginAction, PermissionDecision, PermissionMode } from '../src/lib/gen/banda/v1/banda_pb.js';
import { exists, expect, freePort, HOST, kexec, kube, png, refused, repo, sql, Status, test, until, type Thread } from './fixtures.ts';

test('push reports its result as a notice', async ({ as }) => {
	const t = await as('alice').create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await t.user.threads.push({ threadId: t.id });
	await t.waitForEvent((e) => e.kind.case === 'notice' && e.kind.value.text.startsWith('Push'), 'the push notice', 20_000);
});

test('snoozing suspends the environment; a prompt resumes it with the checkout intact, the settings changed while paused and its images', { tag: '@key' }, async ({ as }) => {
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

test('a start that can’t succeed (an image that can’t be pulled) ends as errored with the reason and Retry; the login environment says the same', { tag: '@key' }, async ({ as, startApi }) => {
	const api = await startApi({ SANDBOX_IMAGE: 'localhost/banda-sandbox:e2e-missing', SANDBOX_LOGIN_IMAGE: 'localhost/banda-login:e2e-missing' }); // its own database: its threads only
	const alice = as('alice', api);
	const t = await alice.create({ prompt: 'e2e: never runs' });
	const page = await alice.page();
	await page.goto(`/t/${t.id}`);
	await t.waitForStatus(Status.ERRORED, 90_000);
	const card = page.getByRole('alert').filter({ hasText: "Can't pull the sandbox image" });
	await expect(card).toContainText('banda-sandbox:e2e-missing');
	await expect(card.getByRole('button', { name: 'Retry' })).toBeVisible();
	const login = alice.terminal((signal) => alice.settings.openLoginTerminal({ harness: 'claude', action: LoginAction.LOGIN, cols: 80, rows: 24 }, { signal }), 90_000);
	expect(await refused(login)).toContain("Can't pull the sandbox image");
});

test('a login terminal says which step its environment is at: the runner it waits for has started and is connecting', async ({ as, startApi }) => {
	// Takes the runners' connections and never answers, so the runner stays up, connecting.
	const port = await freePort();
	const held = new Set<net.Socket>();
	const server = net.createServer((c) => held.add(c)).listen(port, '0.0.0.0');
	try {
		const alice = as('alice', await startApi({ RUNNER_API_URL: `http://${HOST}:${port}` }));
		const seen: string[] = [];
		for await (const r of alice.settings.openLoginTerminal({ harness: 'claude', action: LoginAction.LOGIN, cols: 80, rows: 24 }, { signal: AbortSignal.timeout(90_000) })) {
			if (r.waiting) seen.push(r.waiting);
			if (r.waiting.startsWith('The runner started')) break;
		}
		expect(seen.at(-1)).toBe('The runner started and is connecting to Banda');
	} finally {
		held.forEach((c) => c.destroy());
		server.close();
	}
});

// Parks t and makes its next start wait for a node that never comes.
async function unschedulable(t: Thread) {
	await t.snooze();
	await t.podGone();
	// Only this test's Sandbox: a start keeps fields of its pod template that Banda doesn't set.
	await kube('patch', 'sandbox', t.pod, '--type', 'merge', '-p', '{"spec":{"podTemplate":{"spec":{"nodeSelector":{"banda.dev/e2e":"nowhere"}}}}}');
}

test('a start that waits on Kubernetes (no node fits) says why on the thread, the sidebar and the team board, and keeps waiting; once a node fits it starts', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await unschedulable(t);
	const page = await alice.page();
	await page.goto(`/t/${t.id}`);
	const phone = await alice.page(); // no hover there, so the board shows the reason whole, live
	await phone.setViewportSize({ width: 390, height: 800 });
	await phone.goto('/team');
	await t.send('wake up');
	const why = /Waiting for room on a node: 0\/1 nodes are available/;
	await expect(page.locator('[aria-live=polite]').getByText(why)).toBeVisible({ timeout: 30_000 });
	const row = page.locator(`a[href="/t/${t.id}"]`).filter({ hasText: why });
	await page.locator('details', { has: row }).locator('summary').click(); // the sidebar's folded section
	await expect(row).toBeVisible();
	const card = phone.getByRole('link').filter({ hasText: why });
	await expect(card).toBeVisible();
	await expect.poll(() => card.getByText(why).evaluate((p) => p.scrollWidth <= p.clientWidth && p.scrollHeight <= p.clientHeight)).toBe(true);
	const opened = await bob.page(); // opens the board mid-start
	await opened.goto('/team');
	await expect(opened.getByRole('link').filter({ hasText: why })).toBeVisible();
	await page.waitForTimeout(6000); // several looks at the pod: still waiting, not errored
	expect((await t.get()).status).toBe(Status.STARTING);

	await kube('patch', 'sandbox', t.pod, '--type', 'merge', '-p', '{"spec":{"podTemplate":{"spec":{"nodeSelector":null}}}}');
	await kube('delete', 'pod', t.pod, '--wait=false');
	await t.waitForStatus(Status.AWAITING_INPUT, 120_000);
	await expect(page.getByText(/Waiting for room on a node/)).toHaveCount(0);
	await expect(opened.getByText(/Waiting for room on a node/)).toHaveCount(0);
});

test('a start without a runner after START_DEADLINE ends errored and a late runner still rescues it; a login terminal gives up the same way', async ({ as, startApi }) => {
	const api = await startApi({ START_DEADLINE: '1ms' }); // past at the first look, if no runner beat it there
	const alice = as('alice', api);
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT, 120_000);
	await unschedulable(t); // this start has no runner to beat it
	await t.send('wake up');
	await t.waitForStatus(Status.ERRORED, 30_000);
	expect((await t.of('statusChanged')).findLast((e) => e.status === Status.ERRORED)?.reason).toMatch(/^The environment didn't start within 1ms: /);
	await kube('patch', 'sandbox', t.pod, '--type', 'merge', '-p', '{"spec":{"podTemplate":{"spec":{"nodeSelector":null}}}}');
	await kube('delete', 'pod', t.pod, '--wait=false');
	await t.waitForStatus(Status.AWAITING_INPUT, 120_000);
	const login = alice.terminal((signal) => alice.settings.openLoginTerminal({ harness: 'claude', action: LoginAction.LOGIN, cols: 80, rows: 24 }, { signal }), 90_000);
	expect(await refused(login)).toContain("The environment didn't start within 1ms");
});

test('the start deadline counts from the thread’s start across an API restart, and ends it with Kubernetes’ last reason and Retry', async ({ as, startApi }) => {
	const api = await startApi({ START_DEADLINE: '30m', IDLE_SWEEP_EVERY: '1s' });
	const alice = as('alice', api);
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await unschedulable(t);
	const page = await alice.page();
	await page.goto(`/t/${t.id}`);
	await t.send('wake up');
	await expect(page.locator('[aria-live=polite]').getByText(/Waiting for room on a node/)).toBeVisible({ timeout: 30_000 });
	await api.stop();
	await sql(`update threads set env_started_at = now() - interval '2 hours' where id = '${t.id}'`, api.db);
	await api.start();
	await t.waitForStatus(Status.ERRORED, 30_000);
	const card = page.getByRole('alert').filter({ hasText: "The environment didn't start within 30m: Waiting for room on a node" });
	await expect(card.getByRole('button', { name: 'Retry' })).toBeVisible({ timeout: 30_000 });
});

const sandbox = (id: string, field: string) => kube('get', 'sandbox', `t-${id}`, '-o', `jsonpath={.spec.${field}}`);

test('auto-push and settling: commits reach origin; settling schedules deletion, unsettling cancels it; after the delay the owner recreates the environment from the pushed branch', { tag: '@key' }, async ({ as }) => {
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

const settle = (t: Thread) => t.user.threads.settle({ threadId: t.id, settled: true });

test('B2: settling while the last turn’s push is still running waits for it, then schedules the deletion of pushed work', async ({ as }) => {
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

test('B2: a commit made in the terminal is pushed when the thread is settled; uncommitted work keeps the environment', async ({ as }) => {
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

test('B5: settling a thread whose agent waits for permission denies it with a reason the agent sees, then suspends', async ({ as }) => {
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

for (const [how, prompt, mode] of [
	['snoozing', 'e2e-ask please', PermissionMode.ASK],
	['settling', 'e2e-plan please', PermissionMode.PLAN],
] as const) {
	test(`${how} a thread whose agent waits for ${prompt.startsWith('e2e-plan') ? 'plan approval' : 'permission'} denies it, interrupts the turn and suspends`, async ({ as }) => {
		const alice = as('alice');
		const t = await alice.create({ prompt: 'e2e: permissions' });
		await t.waitForStatus(Status.AWAITING_INPUT);
		await alice.threads.permissionSetMode({ threadId: t.id, mode });
		await t.send(prompt);
		await t.waitFor('to ask', (i) => i.permissionPending, 30_000);
		await (how === 'snoozing' ? t.snooze() : settle(t));
		await t.waitForStatus(Status.SUSPENDED, 60_000);
		await t.podGone();
		const resolved = (await t.of('permissionResolved')).at(-1)!;
		expect(resolved.decision).toBe(PermissionDecision.DENY);
		expect(resolved.message).toMatch(how === 'snoozing' ? /snoozed/ : /settled/);
		expect((await t.get()).permissionPending).toBe(false);
	});
}

test('settling a working thread whose agent asks afterwards denies the request, interrupts the turn and suspends', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create({ prompt: 'e2e: permissions' });
	await t.waitForStatus(Status.AWAITING_INPUT);
	await alice.threads.permissionSetMode({ threadId: t.id, mode: PermissionMode.ASK });
	await t.send('e2e-hold:late e2e-ask please');
	await t.waitForStatus(Status.WORKING);
	await settle(t);
	await t.release('late');
	await t.waitForStatus(Status.SUSPENDED, 60_000);
	await t.podGone();
	expect((await t.of('permissionResolved')).at(-1)!.message).toMatch(/settled/);
});

test('B6: a terminal open during Retry ends; the thread is then idle-suspended with the page open; Reopen wakes it', async ({ as, startApi }) => {
	test.setTimeout(360_000);
	const api = await startApi({ IDLE_SUSPEND_AFTER: '90s', IDLE_SWEEP_EVERY: '1s' }); // longer than a start under load
	const alice = as('alice', api);
	const t = await alice.create({ repoUrl: repo('setup-fails') });
	const page = await alice.page();
	await page.goto(`/t/${t.id}`);
	await t.waitForStatus(Status.ERRORED, 120_000);
	await page.getByRole('button', { name: 'Toggle right pane' }).click();
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

test('B8: a prompt posted while the thread is being suspended wins: the environment resumes and the agent answers', async ({ as, startApi }) => {
	// The pause holds every suspend between the thread's status and its environment. A snooze races the post
	// here; the idle sweep suspends the same way.
	const api = await startApi({ TEST_SUSPEND_PAUSE: '5s' });
	const t = await as('alice', api).create({ repoUrl: repo('setup-hold') });
	await t.podRunning(90_000);
	await t.release('setup');
	await t.waitForStatus(Status.AWAITING_INPUT);
	// The resumed environment runs its setup again and holds there, past the pause.
	await kexec(t.pod, 'rm ~/.banda/setup.sha256 ~/e2e-release/setup');
	const snoozing = t.snooze();
	await t.waitForStatus(Status.SUSPENDED, 10_000);
	await t.send('e2e: wake up');
	await snoozing;
	await new Promise((r) => setTimeout(r, 2000));
	await t.podRunning(90_000); // and not suspended by the snooze
	await t.release('setup');
	await t.waitFor('to answer', (i) => i.status === Status.AWAITING_INPUT && i.diff.includes('hello2.txt'));
});

test('B18: the deletion of a settled environment reaches an open page: the banner appears without a reload', async ({ as }) => {
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

test('an unsettled environment unused for DELETE_IDLE_AFTER with all its work on origin is deleted with its runner token, and the open page shows it; one with uncommitted work stays', async ({ as, startApi }) => {
	test.setTimeout(360_000);
	const api = await startApi({ DELETE_IDLE_AFTER: '20s', IDLE_SWEEP_EVERY: '1s' });
	const alice = as('alice', api);
	const [pushed, dirty] = await Promise.all([alice.create({ prompt: 'e2e: commit one' }), alice.create({ prompt: 'e2e: commit one' })]);
	for (const t of [pushed, dirty]) await t.waitFor('pushed', (i) => i.autoPush === AutoPushState.PUSHED, 150_000);
	await dirty.terminal('echo by-hand > by-hand.txt && echo done');
	expect(await kube('get', 'secret', `${pushed.pod}-token`, '-o', 'jsonpath={.metadata.ownerReferences[0].kind}')).toBe('Sandbox');
	const page = await alice.page();
	await page.goto(`/t/${pushed.id}`);
	await Promise.all([pushed.snooze(), dirty.snooze()]);
	await expect(page.getByText('Environment paused.')).toBeVisible();

	await test.step('the pushed environment is deleted, its volume and token with it; the chat and the open page say so', async () => {
		await expect(page.getByText('Environment deleted.')).toBeVisible({ timeout: 120_000 });
		await expect(page.getByRole('button', { name: 'Recreate environment' })).toBeVisible();
		await pushed.waitForEvent((e) => e.kind.case === 'notice' && e.kind.value.text.startsWith('The unused environment was deleted'), 'the notice');
		await until(async () => !(await exists('sandbox', pushed.pod)), () => 'the environment is still there', 60_000);
		await until(async () => !(await exists('pvc', `home-${pushed.pod}`)), () => 'its volume is still there', 60_000);
		await until(async () => !(await exists('secret', `${pushed.pod}-token`)), () => 'its runner token is still there', 60_000);
	});

	await test.step('the environment with uncommitted work, unused as long, stays', async () => {
		await until(
			async () => (await sql(`select updated_at < now() - interval '25 seconds' from threads where id = '${dirty.id}'`, api.db)) === 't',
			() => 'the unpushed thread is not idle long enough yet'
		);
		expect(await exists('sandbox', dirty.pod)).toBe(true);
		expect(await sandbox(dirty.id, 'shutdownTime')).toBe('');
		expect(await sql(`select env_delete_at is null from threads where id = '${dirty.id}'`, api.db)).toBe('t');
	});
});

// Monitoring: METRICS_ADDR serves Prometheus' text format on a port of its own; the API port has no /metrics.
test('the metrics port reports threads by status, sandboxes and connected runners', async ({ as, startApi }) => {
	const port = await freePort();
	const api = await startApi({ METRICS_ADDR: `:${port}` }); // a database of its own: its threads only
	const t = await as('alice', api).create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const res = await fetch(`http://localhost:${port}/metrics`);
	expect(res.headers.get('content-type')).toContain('text/plain; version=0.0.4');
	const text = await res.text();
	expect(text).toContain('banda_threads{status="awaiting_input"} 1\n');
	expect(text).toContain('banda_threads{status="working"} 0\n');
	expect(text).toMatch(/^banda_sandboxes\{mode="running"\} [1-9]\d*$/m); // the run's namespace: other tests' too
	expect(text).toContain('banda_runners_connected 1\n');
	expect(text).toMatch(/^banda_internal_errors_total \d+$/m);
	expect(text).toMatch(/^banda_uptime_seconds \d+$/m);
	expect(await (await fetch(`${api.url}/metrics`)).text()).not.toContain('banda_threads');
});
