// The harness's own capabilities (deploy/fake-claude directives, fixture repos, test APIs), so a
// broken capability fails here instead of in a scenario built on it.
import { AutoPushState } from '../src/lib/gen/banda/v1/banda_pb.js';
import { expect, kube, mcpDirective, repo, Status, test } from './fixtures.ts';

const said = (text: string) => (e: { kind: { case?: string; value?: unknown } }) =>
	e.kind.case === 'agentText' && (e.kind.value as { text: string }).text === text;

test('e2e-echo: the agent says the prompt it received', async ({ as }) => {
	const t = await as('alice').create({ prompt: 'e2e-echo hello there' });
	await t.waitForStatus(Status.AWAITING_INPUT);
	expect(await t.prompts()).toEqual([expect.stringContaining('e2e-echo hello there')]);
});

test('e2e-hold keeps the turn open until released; an interrupt ends a held turn', async ({ as }) => {
	const t = await as('alice').create({ prompt: 'e2e-hold:one' });
	await t.waitForEvent(said('Holding one'), 'the held turn');
	await new Promise((r) => setTimeout(r, 2000));
	expect((await t.get()).status).toBe(Status.WORKING);
	await t.release('one');
	await t.waitForEvent(said('Created hello1.txt with claude-opus-5-5 · high'), 'the released turn');
	await t.waitForStatus(Status.AWAITING_INPUT);

	await t.send('e2e-hold:two');
	await t.waitForEvent(said('Holding two'), 'the second held turn');
	await t.user.threads.interrupt({ threadId: t.id });
	await t.waitForEvent((e) => e.kind.case === 'turnEnded' && e.kind.value.isError, 'the interrupted turn');
	await t.waitForStatus(Status.AWAITING_INPUT);
	expect((await t.of('turnEnded')).length).toBe(2);
});

test('e2e-crash exits the CLI mid-turn: the container restarts', async ({ as }) => {
	const t = await as('alice').create({ prompt: 'e2e-crash:once' });
	await expect
		.poll(() => kube('get', 'pod', t.pod, '-o', 'jsonpath={.status.containerStatuses[0].restartCount}'), { timeout: 60_000 })
		.toBe('1');
});

test('e2e-mcp calls a Banda MCP tool from inside the sandbox', async ({ as }) => {
	const t = await as('alice').create({ prompt: mcpDirective('list_children', {}) });
	await t.waitForStatus(Status.AWAITING_INPUT);
	expect(await t.reply()).toMatch(/^MCP list_children: /);
	expect((await t.of('toolCall')).map((c) => c.name)).toContain('mcp__banda__list_children');
});

test('the git server holds a branch’s push until released', async ({ as }) => {
	const t = await as('alice').create({ prompt: 'e2e: commit' });
	await t.waitFor('pushed', (i) => i.autoPush === AutoPushState.PUSHED);
	await t.holdPush();
	await t.turn(() => t.send('commit again'));
	await new Promise((r) => setTimeout(r, 3000));
	const branch = (await t.get()).branch;
	expect(await t.origin(`log --format=%s ${branch}`)).not.toContain('hello 2');
	await t.releasePush();
	await expect.poll(() => t.origin(`log --format=%s ${branch}`), { timeout: 30_000 }).toContain('hello 2');
});

test('setup-hold keeps a thread starting until released', async ({ as }) => {
	const t = await as('alice').create({ repoUrl: repo('setup-hold') });
	await expect.poll(() => kube('get', 'pod', t.pod, '-o', 'jsonpath={.status.phase}'), { timeout: 60_000 }).toBe('Running');
	await new Promise((r) => setTimeout(r, 3000));
	expect((await t.get()).status).toBe(Status.STARTING);
	await t.release('setup');
	await t.waitForStatus(Status.AWAITING_INPUT);
});

test('a test’s own API restarts mid-test; the runner reconnects to it', async ({ as, startApi }) => {
	const api = await startApi();
	const t = await as('alice', api).create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await api.restart();
	await t.turn(() => t.send('after the restart'), 180_000);
	expect(await t.reply()).toContain('Created hello2.txt');
});

test('with its own database, a test’s API suspends idle threads within seconds', async ({ as, startApi }) => {
	const api = await startApi({ IDLE_SUSPEND_AFTER: '5s', IDLE_SWEEP_EVERY: '1s' }, { ownDatabase: true });
	const t = await as('alice', api).create();
	await t.waitForStatus(Status.SUSPENDED, 120_000);
});
