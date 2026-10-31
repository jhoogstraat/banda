// Whose Claude a turn runs on: a teammate's turn never reaches the owner's subscription login, also through
// the owner's other threads; a thread can run on the team's Console identity (internal/sandbox, internal/api/turns.go).
import { expect, kexec, kube, sql, Status, test, until, userHash, type Api, type User } from './fixtures.ts';

const team = {
	CLAUDE_TEAM_RULE_ID: 'fdrl_e2e',
	CLAUDE_TEAM_ORGANIZATION_ID: '00000000-0000-0000-0000-000000000000',
	CLAUDE_TEAM_SERVICE_ACCOUNT_ID: 'svac_e2e',
	PUBLIC_URL: 'https://banda.e2e.example'
};
const tokenFile = '/run/banda/id-tokens/api.anthropic.com';
const claudeClaim = async (pod: string) =>
	JSON.parse(await kube('get', 'pod', pod, '-o', 'json')).spec.volumes.find((v: { name: string }) => v.name === 'claude').persistentVolumeClaim.claimName;
const claims = (token: string) => JSON.parse(Buffer.from(token.split('.')[1], 'base64url').toString());

async function signedIn(u: User, api: Api) {
	await u.users.me({});
	await sql(`UPDATE users SET email = '${u.id}@e2e.example' WHERE id = '${u.id}'`, api.db);
	return u;
}

test('a thread on an API key can’t reach its owner’s subscription login, and keeps its conversation on the move', { tag: '@key' }, async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await kexec(t.pod, 'echo login > "$CLAUDE_CONFIG_DIR/.credentials.json"');
	await t.turn(() => t.send('e2e-echo e2e: remember the quokka'));
	await t.as(as('bob')).send('@agent e2e: a teammate’s suggestion', { mentionsAgent: true });

	await alice.apiKey();
	await until(async () => (await t.get()).status === Status.SUSPENDED || undefined, () => 'not moved to the key');
	await t.turn(() => t.send('@agent e2e-echo e2e: on the key now', { mentionsAgent: true }));
	expect(await claudeClaim(t.pod)).toBe(`claude-key-${userHash(alice.id)}`);
	expect(await kexec(t.pod, 'ls -A "$CLAUDE_CONFIG_DIR/.credentials.json" 2>/dev/null || echo none')).toContain('none');

	const prompt = (await t.prompts()).at(-1)!;
	expect(prompt).toContain('moved to a new Claude Code session');
	expect(prompt).toContain('remember the quokka');
	expect(prompt).not.toContain('a teammate’s suggestion'); // a suggestion reaches the agent only as what its owner sends
});

test('a fork gets no teammate’s suggestion from the conversation', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await t.as(as('bob')).send('@agent e2e: a teammate’s suggestion', { mentionsAgent: true });
	await t.send('e2e: plain words by the owner', { mentionsAgent: false });
	const { threads } = await alice.threads.fork({ threadId: t.id, prompt: 'e2e-echo e2e: carry on' });
	const fork = alice.thread(threads[0].id);
	await fork.waitForStatus(Status.AWAITING_INPUT);
	expect((await fork.prompts()).join('\n')).not.toContain('a teammate’s suggestion');
});

test('a thread runs on the team’s Console identity: teammates’ turns run, its owner’s login and other threads stay out of it', { tag: '@key' }, async ({ as, startApi }) => {
	const api = await startApi({ ...team, TEST_ID_TOKEN_REFRESH: '3s' });
	const alice = await signedIn(as('alice', api), api);
	const bob = as('bob', api);
	expect((await alice.users.me({})).teamIdentity).toBe(true);
	const t = await alice.create();
	const other = await alice.create();
	await Promise.all([t.waitForStatus(Status.AWAITING_INPUT), other.waitForStatus(Status.AWAITING_INPUT)]);
	expect(await kexec(t.pod, `ls -A /run/banda/id-tokens`)).toBe('');

	await test.step('only the owner turns it on; the environment restarts on it', async () => {
		await expect(bob.threads.setTeamIdentity({ threadId: t.id, on: true })).rejects.toThrow();
		await alice.threads.setTeamIdentity({ threadId: t.id, on: true });
		await t.waitFor('the team identity', (i) => i.teamIdentity && i.teammateTurns);
		await t.turn(() => t.send('@agent e2e: on the team now', { mentionsAgent: true }));
		const spec = JSON.parse(await kube('get', 'pod', t.pod, '-o', 'json')).spec;
		const env = Object.fromEntries((spec.containers[0].env as { name: string; value: string }[]).map((e) => [e.name, e.value]));
		expect(env).toMatchObject({ BANDA_AUTH_METHOD: 'team', ANTHROPIC_FEDERATION_RULE_ID: 'fdrl_e2e', ANTHROPIC_SERVICE_ACCOUNT_ID: 'svac_e2e', ANTHROPIC_IDENTITY_TOKEN_FILE: tokenFile });
		expect(await claudeClaim(t.pod)).toBe(`claude-key-${userHash(alice.id)}`);
	});

	await test.step('its token names the owner for Anthropic’s audience, can be exchanged twice, and goes to no other thread', async () => {
		const c = claims((await kexec(t.pod, `cat ${tokenFile}`)).trim());
		expect(c).toMatchObject({ aud: 'https://api.anthropic.com', sub: `user:${alice.id}@e2e.example`, thread: t.id });
		expect(c.jti).toBeUndefined();
		expect(await kexec(other.pod, 'ls -A /run/banda/id-tokens')).toBe('');
		await expect(alice.settings.addIdTokenAudience({ audience: 'https://api.anthropic.com' })).rejects.toThrow(/only to threads on it/);
	});

	await test.step('a teammate’s post is a request the owner approves', async () => {
		await t.as(bob).send('@agent e2e-echo e2e: bob’s turn', { mentionsAgent: true });
		const { seq } = await t.waitForEvent((e) => e.kind.case === 'turnRequest', 'the request');
		await t.turn(() => alice.threads.approveTurn({ threadId: t.id, requestSeq: seq }));
		expect((await t.prompts()).at(-1)).toContain('bob’s turn');
	});

	await test.step('its agent starts no turn on the owner’s subscription: the post waits as a suggestion', async () => {
		const turns = (await other.of('turnEnded')).length;
		await t.mcp('post_message', { thread_id: other.id, text: 'e2e: run this', mention_agent: true });
		const m = await other.waitForEvent((e) => e.kind.case === 'message' && e.kind.value.text === 'e2e: run this', 'the post');
		expect(m.kind.value).toMatchObject({ proposal: true });
		expect((await other.get()).status).toBe(Status.AWAITING_INPUT);
		expect((await other.of('turnEnded')).length).toBe(turns);
	});

	await test.step('its sub-thread runs on the team too', async () => {
		const out = await t.mcp('create_thread', { prompt: 'e2e: a sub-thread' });
		const child = alice.thread(out.match(/thread_id[^a-z0-9]+([a-z0-9]{26})/)![1]);
		await child.waitForStatus(Status.AWAITING_INPUT);
		expect((await child.get()).teamIdentity).toBe(true);
		expect(await claudeClaim(child.pod)).toBe(`claude-key-${userHash(alice.id)}`);
	});

	await test.step('turning it off withdraws requests and returns to the owner’s login', async () => {
		await t.as(bob).send('@agent e2e: one more', { mentionsAgent: true });
		await t.waitFor('the request', (i) => i.turnRequests === 1);
		await alice.threads.setTeamIdentity({ threadId: t.id, on: false });
		await t.waitFor('the login', (i) => !i.teamIdentity && !i.teammateTurns && i.turnRequests === 0);
	});
});

test('without a team identity set up a thread can’t turn it on', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create();
	expect((await alice.users.me({})).teamIdentity).toBe(false);
	await expect(alice.threads.setTeamIdentity({ threadId: t.id, on: true })).rejects.toThrow(/no team identity/);
});

test('an agent whose thread is on a key can’t start a turn in a thread that still runs on the subscription', async ({ as }) => {
	const alice = as('alice');
	const sub = await alice.create();
	await sub.waitForStatus(Status.AWAITING_INPUT);
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await sql(`UPDATE threads SET env_api_key = true WHERE id = '${t.id}'`);
	const before = (await sub.of('turnEnded')).length;
	await t.mcp('post_message', { thread_id: sub.id, text: 'e2e: from the key', mention_agent: true });
	const m = await sub.waitForEvent((e) => e.kind.case === 'message' && e.kind.value.text === 'e2e: from the key', 'the post');
	expect(m.kind.value).toMatchObject({ proposal: true });
	expect((await sub.get()).status).toBe(Status.AWAITING_INPUT);
	expect((await sub.of('turnEnded')).length).toBe(before);
});
