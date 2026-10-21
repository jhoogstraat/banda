// Turns: every message reaches the agent, in order, and every turn ends, also when the agent folds a
// prompt into its turn, is interrupted, crashes or loses the API.
import { expect, kube, refused, repo, sql, Status, test, until, type Thread } from './fixtures.ts';

const said = (text: string) => (e: { kind: { case?: string; value?: unknown } }) =>
	e.kind.case === 'agentText' && (e.kind.value as { text: string }).text === text;
const holding = (t: Thread, name: string) => t.waitForEvent(said(`Holding ${name}`), `the held turn ${name}`, 120_000); // a pod may take a while
const notices = async (t: Thread) => (await t.of('notice')).map((n) => n.text).join('\n');
const turnsEnded = (t: Thread, n: number) =>
	until(async () => (await t.of('turnEnded')).length >= n, () => `${n} turns to end in ${t.id}`, 120_000);

test('B1(b): a teammate’s note posted while the thread starts reaches the agent', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const t = await alice.create({ repoUrl: repo('setup-hold'), prompt: 'first e2e-echo' });
	await expect.poll(() => kube('get', 'pod', t.pod, '-o', 'jsonpath={.status.phase}'), { timeout: 60_000 }).toBe('Running');
	await t.as(bob).send('TEAMMATE-NOTE use postgres');
	await t.release('setup');
	await t.waitForStatus(Status.AWAITING_INPUT);
	await t.turn(() => t.send('@agent second e2e-echo', { mentionsAgent: true }));
	const prompts = await t.prompts();
	expect(prompts[0]).toContain('first e2e-echo');
	expect(prompts.join('\n')).toContain('TEAMMATE-NOTE');
});

test('B1(a): teammates’ notes posted while the owner starts turns all reach the agent', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await t.as(bob).send('NOTE-chat'); // chat mode: alice mentions the agent to start a turn
	const go = (text: string) => t.send(`@agent e2e-echo ${text}`, { mentionsAgent: true });
	const n = 10;
	await Promise.all(Array.from({ length: n }, (_, i) => [t.as(bob).send(`NOTE-${i}`), go(`go ${i}`)]).flat());
	await turnsEnded(t, n + 1);
	await t.turn(() => go('flush'));
	const prompts = (await t.prompts()).join('\n');
	for (let i = 0; i < n; i++) expect(prompts).toContain(`NOTE-${i}\n`);
});

test('B21: concurrent owner prompts reach the agent in the order they were posted', async ({ as }) => {
	const t = await as('alice').create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const n = 10;
	await Promise.all(Array.from({ length: n }, (_, i) => t.send(`e2e-echo order ${i}`)));
	await turnsEnded(t, n + 1);
	await t.waitForStatus(Status.AWAITING_INPUT);
	const posted = (await t.of('message')).map((m) => m.text).filter((m) => m.startsWith('e2e-echo order'));
	expect(await t.prompts()).toEqual(posted);
});

test('B3: a prompt folded into the running turn ends with it, and a model change then applies', async ({ as }) => {
	const t = await as('alice').create({ prompt: 'e2e-hold:h e2e-fold' });
	await holding(t, 'h');
	await t.send('folded e2e-echo');
	await t.user.threads.setAgentSettings({ threadId: t.id, model: 'claude-sonnet-5-5' });
	await t.release('h');
	await t.waitForEvent(said('Folded a message into this turn'), 'the folded prompt');
	await t.waitForStatus(Status.AWAITING_INPUT, 30_000);
	expect(await t.of('turnEnded')).toHaveLength(1);
	expect(await t.prompts()).toEqual(['folded e2e-echo']);
	await t.turn(() => t.send('after the fold'));
	expect(await t.reply()).toContain('with claude-sonnet-5-5');
});

test('B13: an interrupt ends only the running turn; the prompt queued behind it runs and ends', async ({ as }) => {
	const t = await as('alice').create({ prompt: 'e2e-hold:one' });
	await holding(t, 'one');
	await t.send('e2e-hold:two');
	await t.user.threads.interrupt({ threadId: t.id });
	await holding(t, 'two');
	expect((await t.get()).status).toBe(Status.WORKING);
	await t.release('two');
	await t.waitForStatus(Status.AWAITING_INPUT);
	expect((await t.of('turnEnded')).map((e) => e.isError)).toEqual([true, false]);
});

test('B9: an API restart mid-turn tells the chat and the parent that the turn and the prompt queued behind it were lost', async ({ as, startApi }) => {
	const api = await startApi();
	const alice = as('alice', api);
	const parent = await alice.create();
	await parent.waitForStatus(Status.AWAITING_INPUT);
	const { thread } = await alice.threads.spawnThread({ parentId: parent.id, prompt: 'e2e-hold:forever' });
	const t = alice.thread(thread!.id);
	await holding(t, 'forever');
	await t.send('queued behind');
	await api.restart();
	await t.waitForStatus(Status.AWAITING_INPUT, 180_000);
	expect((await t.of('turnEnded')).map((e) => e.isError)).toEqual([true]);
	expect(await notices(t)).toMatch(/turn was lost, and 1 prompt queued behind it/);
	await parent.waitForEvent(
		(e) => e.kind.case === 'lineageUpdate' && e.kind.value.threadId === t.id && e.kind.value.text.startsWith('Turn failed'),
		'the parent hearing of the lost turn'
	);
	await t.turn(() => t.send('after the restart'));
});

test('B9: a CLI crash mid-turn tells the chat its turn and the prompt queued behind it were lost; the thread recovers', async ({ as }) => {
	const t = await as('alice').create({ prompt: 'e2e-hold:h' });
	await holding(t, 'h');
	await t.send('e2e-crash:c');
	await t.send('queued behind the crash');
	await t.release('h');
	await until(async () => /turn was lost/.test(await notices(t)), () => `a lost-turn notice in ${t.id}`, 60_000);
	await t.waitForStatus(Status.AWAITING_INPUT, 180_000);
	expect(await notices(t)).toMatch(/turn was lost, and 1 prompt queued behind it/);
	expect((await t.of('turnEnded')).map((e) => e.isError)).toEqual([false, true]);
	await t.turn(() => t.send('after the crash'));
	expect(await t.reply()).toContain('Created hello');
});

test('B14: a blank first prompt is refused; an agent’s post leaves no read state for its pseudo-user', async ({ as }) => {
	const alice = as('alice');
	expect(await refused(alice.create({ prompt: '   ' }))).toContain('prompt is required');
	const [a, b] = [await alice.create(), await alice.create()];
	await a.waitForStatus(Status.AWAITING_INPUT);
	expect(await a.mcp('post_message', { thread_id: b.id, text: 'from thread a' })).toContain('Message posted');
	expect((await b.of('message')).map((m) => m.text)).toContain('from thread a');
	expect(await sql(`select count(*) from thread_views where user_id like 'agent:%' and thread_id = '${b.id}'`)).toBe('0');
});

test('B21: settings changes during a long setup leave the terminal working', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create({ repoUrl: repo('setup-hold') });
	await until(async () => (await t.terminal('echo up', 5000)).includes('up'), () => `a terminal in ${t.id}`, 90_000);
	for (let i = 0; i < 20; i++) await alice.threads.setAgentSettings({ threadId: t.id, effort: i % 2 ? 'high' : 'low' });
	expect(await t.terminal('echo still-up', 20_000)).toContain('still-up');
	await t.release('setup');
	await t.waitForStatus(Status.AWAITING_INPUT);
});
