// A thread from start to its first turn: checkout, setup, the agent's work, settings, failures.
import { SetupState } from '../src/lib/gen/banda/v1/banda_pb.js';
import { DB, expect, kube, refused, repo, sql, Status, test, type Thread } from './fixtures.ts';

const steps = async (t: Thread, id: string, state: SetupState) => (await t.of('setupStep')).filter((s) => s.id === id && s.state === state);

test('a thread starts its sandbox, runs the first turn and has its file in the diff', { tag: '@key' }, async ({ as }) => {
	const t = await as('alice').create({ prompt: 'e2e: add a file' });
	const info = await t.waitForStatus(Status.AWAITING_INPUT);
	expect(info.diff).toContain('hello1.txt');
	expect(await steps(t, 'checkout', SetupState.DONE)).toHaveLength(1);
	expect(await steps(t, 'setup', SetupState.SKIPPED)).toHaveLength(1); // the plain repo has no .banda/setup.sh
});

test('plans (the task tools) are stored as the whole list; a subagent’s tool calls carry their Agent call, its text is dropped', async ({ as }) => {
	const t = await as('alice').create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const calls = await t.of('toolCall');
	expect(calls.filter((c) => c.name.startsWith('Task')).map((c) => c.name)).toEqual(['TaskCreate', 'TaskCreate', 'TaskUpdate', 'TaskUpdate', 'TaskUpdate']);
	const plans = await t.of('planChanged');
	expect(plans).toHaveLength(5);
	expect(plans.at(-1)!.items.map((i) => `${i.status} ${i.content}`)).toEqual(['completed Look around', 'completed Write hello1.txt']);
	expect(calls.filter((c) => c.parentToolCallId === 'toolu_agent1').map((c) => c.name)).toEqual(['Bash']);
	expect((await t.of('agentText')).map((a) => a.text)).not.toContain('Two files.');
});

test('a repository URL with credentials is refused', async ({ as }) => {
	await refused(as('alice').create({ repoUrl: 'https://x-access-token:ghp_e2e@github.com/acme/app.git' }));
});

test('the owner switches model and effort mid-turn: the turn finishes as it was, the next one runs on the new settings, the chat records it', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	expect(await t.reply()).toContain('with claude-opus-5-5 · high');
	await refused(t.as(as('bob')).user.threads.setAgentSettings({ threadId: t.id, model: 'claude-haiku-5-5' }));
	// Hold the turn open, so the settings change while it runs.
	await t.send('keep going e2e-hold:settings');
	await t.waitForEvent((e) => e.kind.case === 'agentText' && e.kind.value.text === 'Holding settings', 'the held turn');
	await alice.threads.setAgentSettings({ threadId: t.id, model: 'claude-sonnet-5-5', effort: 'medium' });
	const info = await t.get();
	expect([info.model, info.effort]).toEqual(['claude-sonnet-5-5', 'medium']);
	await t.turn(() => t.release('settings'));
	expect(await t.reply()).toContain('with claude-opus-5-5 · high');
	await t.turn(() => t.send('and again'));
	expect(await t.reply()).toContain('with claude-sonnet-5-5 · medium');
	const changes = (await t.events()).filter((e) => e.kind.case === 'agentSettings' && e.kind.value.model === 'claude-sonnet-5-5');
	expect(changes.map((e) => e.actor)).toEqual([alice.id]);
});

test('a failing .banda/setup.sh errors the thread with its redacted output; the runner stays up for the terminal', async ({ as }) => {
	const alice = as('alice');
	await alice.settings.setSecret({ name: 'E2E_SECRET', value: `e2e-secret-${Date.now()}` });
	const t = await alice.create({ repoUrl: repo('setup-fails'), prompt: 'e2e: never runs' });
	await t.waitForStatus(Status.ERRORED);
	const failed = await steps(t, 'setup', SetupState.FAILED);
	expect(failed).toHaveLength(1);
	expect(failed[0].output).toContain('setup sees [redacted]');
	expect((await t.of('statusChanged')).at(-1)?.reason).toMatch(/^Setup failed/);
	await new Promise((r) => setTimeout(r, 5000));
	expect((await t.get()).status).toBe(Status.ERRORED);
	expect(await kube('get', 'pod', t.pod, '-o', 'jsonpath={.status.containerStatuses[0].restartCount}')).toBe('0');
	expect(await t.terminal('echo still-up')).toContain('still-up');
});

test('a .banda/setup.sh that leaves a background process running succeeds', async ({ as }) => {
	const t = await as('alice').create({ repoUrl: repo('setup-bg'), prompt: 'e2e: setup in the background' });
	await t.waitForStatus(Status.AWAITING_INPUT);
	expect(await steps(t, 'setup', SetupState.DONE)).toHaveLength(1);
});

test('a bad base branch errors once, without a crash loop', async ({ as }) => {
	const t = await as('alice').create({ baseBranch: 'no-such-branch', prompt: 'e2e: bad base' });
	await t.waitForStatus(Status.ERRORED);
	await new Promise((r) => setTimeout(r, 15_000));
	expect(await kube('get', 'pod', t.pod, '-o', 'jsonpath={.status.containerStatuses[0].restartCount}')).toBe('0');
	expect(await steps(t, 'checkout', SetupState.FAILED)).toHaveLength(1);
});

test('APIs starting together on a fresh database all come up: they apply the schema one at a time', async ({ startApi }) => {
	const db = `${DB}_${Date.now()}`;
	await sql(`CREATE DATABASE ${db} OWNER banda_api`);
	try {
		const url = process.env.DATABASE_URL!.replace(/\/[^/?]*(\?|$)/, `/${db}$1`);
		await Promise.all([1, 2, 3, 4].map(() => startApi({ DATABASE_URL: url }, { sharedDatabase: true })));
	} finally {
		await sql(`DROP DATABASE ${db} WITH (FORCE)`);
	}
});
