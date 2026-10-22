// What runs in a thread's environment: the terminal, secrets, files and the preview.
import { request } from '@playwright/test';
import { collect, expect, refused, sql, Status, test, type User } from './fixtures.ts';

test('terminal runs commands in the sandbox', async ({ as }) => {
	const t = await as('alice').create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	expect(await t.terminal('echo e2e-terminal-$((6*7))')).toContain('e2e-terminal-42');
});

test('the owner’s secrets are in the environment (no API key), and redacted from files, the diff and the database', async ({ as }) => {
	const alice = as('alice');
	const secret = `e2e-secret-${Date.now()}`;
	await alice.settings.setSecret({ name: 'E2E_SECRET', value: secret });
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const out = await t.terminal('printf %s $E2E_SECRET > leak.txt; echo secret-$E2E_SECRET key-${ANTHROPIC_API_KEY:-none}');
	expect(out).toContain(`secret-${secret} key-none`);
	expect((await alice.threads.readFile({ threadId: t.id, path: 'leak.txt' })).content).toBe('[redacted]');
	await test.step('secrets are redacted from the diff', async () => {
		const info = await t.turn(() => t.send('keep going'));
		expect(info.diff).not.toContain(secret);
		expect(info.diff).toContain('[redacted]');
	});
	await test.step('no secret value reached the database', async () => {
		expect(await sql(`select count(*) from events where body::text like '%${secret}%'`)).toBe('0');
	});
});

test('files are listed; paths outside the checkout are refused', async ({ as }) => {
	const t = await as('alice').create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	expect((await t.user.threads.listFiles({ threadId: t.id })).paths).toContain('README');
	await refused(t.user.threads.readFile({ threadId: t.id, path: '../.claude/.credentials.json' }));
});

const ports = (u: User, threadId: string, wake = false) => collect((signal) => u.threads.previewWatch({ threadId, wake }, { signal }));

test('the preview lists the dev server the agent started, to teammates too, and serves it only with the cookie from PreviewOpen', async ({ as }) => {
	const bob = as('bob');
	const t = await as('alice').create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await t.turn(() => t.send('start the preview'));
	await expect
		.poll(async () => (await ports(bob, t.id)).at(-1), { timeout: 60_000 })
		.toMatchObject({ running: true, ports: [{ port: 5173, process: 'http.server' }] });
	// The chat says so once: a port that starts listening while the runner is connected.
	await t.waitForEvent((e) => e.kind.case === 'portOpened', 'portOpened');
	expect(await t.of('portOpened')).toMatchObject([{ port: 5173, process: 'http.server' }]);

	await test.step('the preview serves the page and its absolute-path asset, only with the cookie from PreviewOpen', async () => {
		const { url } = await bob.threads.previewOpen({ threadId: t.id, port: 5173, path: '/' });
		const origin = url.split('/__banda/')[0];
		const browser = await request.newContext(); // keeps the cookie
		expect(await (await browser.get(url)).text()).toContain('e2e-preview-page');
		expect(await (await browser.get(`${origin}/assets/app.js`)).text()).toContain('e2e-preview-asset');
		expect((await fetch(`${origin}/assets/app.js`)).status).toBe(401);
		expect((await fetch(`${origin}/__banda/open?token=forged&path=/`)).status).toBe(401);
		await browser.dispose();
	});
});

test('the preview of a paused environment says so and does not wake it; teammates can’t wake it', async ({ as }) => {
	const bob = as('bob');
	const t = await as('alice').create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await t.turn(() => t.send('start the preview'));
	await expect.poll(async () => (await ports(t.user, t.id)).at(-1)?.ports.length, { timeout: 60_000 }).toBe(1);
	const { url } = await t.user.threads.previewOpen({ threadId: t.id, port: 5173, path: '/' });
	const browser = await request.newContext();
	await browser.get(url);
	await t.snooze();
	expect((await t.get()).status).toBe(Status.SUSPENDED);
	await t.podGone();
	expect((await ports(t.user, t.id)).at(-1)).toMatchObject({ running: false, ports: [] });
	expect((await browser.get(`${url.split('/__banda/')[0]}/`)).status()).toBe(503);
	await browser.dispose();
	expect(await refused(ports(bob, t.id, true))).toContain('permission_denied');
	expect((await t.get()).status).toBe(Status.SUSPENDED);
});
