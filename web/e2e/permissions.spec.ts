// Permission modes: the agent asks (deploy/fake-claude's e2e-ask and e2e-plan), and who may answer.
import { PermissionDecision, PermissionMode } from '../src/lib/gen/banda/v1/banda_pb.js';
import { expect, refused, repo, Status, test, type Thread, type User } from './fixtures.ts';

/** A thread in the given permission mode, after its first turn. */
async function threadIn(owner: User, mode: PermissionMode) {
	const t = await owner.create({ prompt: 'e2e: permissions' });
	await t.waitForStatus(Status.AWAITING_INPUT);
	await owner.threads.permissionSetMode({ threadId: t.id, mode });
	return t;
}

/** Sends text and waits for the agent's permission request; returns its id. */
async function ask(t: Thread, text: string) {
	await t.send(text);
	await t.waitFor('to ask for permission', (i) => i.permissionPending, 30_000);
	return (await t.of('permissionRequested')).at(-1)!.id;
}

const answer = (u: User, t: Thread, requestId: string, decision: PermissionDecision, mode?: PermissionMode) =>
	u.threads.permissionAnswer({ threadId: t.id, requestId, decision, ...(mode && { mode }) });

/** How often the permitted command ran. */
const permitted = async (t: Thread) => {
	const r = await t.user.threads.readFile({ threadId: t.id, path: 'permitted.txt' }).catch(() => undefined);
	return (r?.content.match(/e2e-permitted/g) ?? []).length;
};

test('ask mode: the agent waits for the owner’s answer (the thread shows it pending), then runs the command', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const t = await alice.create({ prompt: 'e2e: permissions' });
	await t.waitForStatus(Status.AWAITING_INPUT);
	await refused(bob.threads.permissionSetMode({ threadId: t.id, mode: PermissionMode.ASK }));
	await alice.threads.permissionSetMode({ threadId: t.id, mode: PermissionMode.ASK });
	expect((await t.get()).permissionMode).toBe(PermissionMode.ASK);
	const id = await ask(t, 'e2e-ask once');
	expect((await t.get()).status).toBe(Status.WORKING);
	await refused(answer(bob, t, id, PermissionDecision.ALLOW)); // not trusted
	await answer(alice, t, id, PermissionDecision.ALLOW);
	const info = await t.waitForStatus(Status.AWAITING_INPUT);
	expect(await permitted(t)).toBe(1);
	expect(info.permissionPending).toBe(false);
	await refused(answer(alice, t, id, PermissionDecision.ALLOW));
});

test('permission requests are redacted', async ({ as }) => {
	const alice = as('alice');
	const secret = `e2e-secret-${Date.now()}`;
	await alice.settings.setSecret({ name: 'E2E_SECRET', value: secret });
	const t = await threadIn(alice, PermissionMode.ASK);
	const id = await ask(t, 'e2e-ask once');
	expect((await t.of('permissionRequested')).at(-1)?.inputJson).toContain('[redacted]');
	await answer(alice, t, id, PermissionDecision.ALLOW);
	await t.waitForStatus(Status.AWAITING_INPUT);
	expect(JSON.stringify(await t.events(), (_, v) => (typeof v === 'bigint' ? `${v}` : v))).not.toContain(secret);
});

test('a denial reaches the agent with its reason, and the command does not run', async ({ as }) => {
	const alice = as('alice');
	const t = await threadIn(alice, PermissionMode.ASK);
	const id = await ask(t, 'e2e-ask deny');
	await alice.threads.permissionAnswer({ threadId: t.id, requestId: id, decision: PermissionDecision.DENY, message: 'not now' });
	await t.waitForStatus(Status.AWAITING_INPUT);
	expect(await permitted(t)).toBe(0);
	expect((await t.of('toolResult')).filter((r) => r.isError).at(-1)?.output).toBe('not now');
});

test('teammates answer in a trusted thread', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const t = await threadIn(alice, PermissionMode.ASK);
	await alice.threads.trust({ threadId: t.id, trusted: true });
	const id = await ask(t, 'e2e-ask by bob');
	await answer(bob, t, id, PermissionDecision.ALLOW);
	await t.waitForStatus(Status.AWAITING_INPUT);
	expect(await permitted(t)).toBe(1);
});

test('always allow: the next Bash call runs without asking', async ({ as }) => {
	const alice = as('alice');
	const t = await threadIn(alice, PermissionMode.ASK);
	await answer(alice, t, await ask(t, 'e2e-ask always'), PermissionDecision.ALLOW_ALWAYS);
	await t.waitForStatus(Status.AWAITING_INPUT);
	const asked = (await t.of('permissionRequested')).length;
	await t.turn(() => t.send('e2e-ask again'));
	expect(await permitted(t)).toBe(2);
	expect((await t.of('permissionRequested')).length).toBe(asked);
});

test('plan mode: only the owner approves the plan, which switches the agent to accept edits', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const t = await threadIn(alice, PermissionMode.PLAN);
	const id = await ask(t, 'e2e-plan');
	await refused(answer(bob, t, id, PermissionDecision.ALLOW, PermissionMode.ACCEPT_EDITS));
	await refused(answer(alice, t, id, PermissionDecision.ALLOW)); // a plan needs a mode to work in
	await answer(alice, t, id, PermissionDecision.ALLOW, PermissionMode.ACCEPT_EDITS);
	await t.waitForStatus(Status.AWAITING_INPUT);
	expect((await t.get()).permissionMode).toBe(PermissionMode.ACCEPT_EDITS);
	await t.turn(() => t.send('e2e-plan again'));
	expect(await t.reply()).toContain('Not in plan mode (acceptEdits)');
});

test('a request pending when the environment restarts is marked lost', async ({ as }) => {
	const alice = as('alice');
	const t = await threadIn(alice, PermissionMode.PLAN);
	await ask(t, 'e2e-plan lost');
	await t.deletePod();
	await t.waitForEvent((e) => e.kind.case === 'permissionResolved' && e.kind.value.decision === PermissionDecision.LOST, 'the lost request', 120_000);
	expect((await t.of('permissionResolved')).filter((r) => r.decision === PermissionDecision.LOST)).toHaveLength(1);
	expect((await t.get()).permissionPending).toBe(false);
});

test('B7: a thread or race created in ask mode asks before its first tool runs, with no second call', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create({ prompt: 'e2e-ask first', permissionMode: PermissionMode.ASK });
	expect((await t.get()).permissionMode).toBe(PermissionMode.ASK);
	await t.waitFor('to ask for permission', (i) => i.permissionPending);
	expect(await permitted(t)).toBe(0);
	const { threads } = await alice.threads.race({ repoUrl: repo(), baseBranch: 'main', prompt: 'e2e: race', permissionMode: PermissionMode.PLAN, tries: [{}, {}] });
	expect(threads.map((r) => r.permissionMode)).toEqual([PermissionMode.PLAN, PermissionMode.PLAN]);
});

test('B16: a mode change is in the chat and reaches a teammate’s open thread live', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const t = await alice.create();
	const page = await bob.page();
	await page.goto(`/t/${t.id}`);
	await expect(page.getByText('Bypass permissions')).toBeVisible();
	await alice.threads.permissionSetMode({ threadId: t.id, mode: PermissionMode.ASK });
	await expect(page.getByText(`${alice.id} switched to Ask before edits and commands`)).toBeVisible({ timeout: 5000 });
	await expect(page.getByText('Ask before edits and commands', { exact: true })).toBeVisible();
	await alice.threads.permissionSetMode({ threadId: t.id, mode: PermissionMode.ASK }); // no change, no line
	expect((await t.of('permissionModeChanged')).map((c) => c.mode)).toEqual([PermissionMode.ASK]);
});
