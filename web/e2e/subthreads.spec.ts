// The Banda MCP server, sub-threads and search.
import { expect, repo, Status, test } from './fixtures.ts';

test('the Banda MCP server is reachable from the sandbox', async ({ as }) => {
	const t = await as('alice').create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	expect(await t.mcpRaw('{"jsonrpc":"2.0","id":1,"method":"tools/list"}')).toContain('read_thread');
});

test('a teammate’s word is found by prefix from another user, with a highlighted snippet and its seq, and by the search_threads MCP tool', async ({ as }) => {
	const [bob, carol] = [as('bob'), as('carol')];
	const t = await as('alice').create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const word = `quokka${Date.now()}`;
	await t.as(bob).send(`we keep the ${word} cache in redis`);
	const seq = (await t.events()).find((e) => e.kind.case === 'message' && e.kind.value.text.includes(word))!.seq;
	const { results } = await carol.threads.search({ query: word.slice(0, 10) });
	expect(results[0]).toMatchObject({ threadId: t.id, seq, actor: bob.id });
	expect(results[0].snippet[1]).toBe(word);
	expect(await t.mcp('search_threads', { query: `${word} redis` })).toContain(`\\"seq\\":${seq},`);
});

test('an agent spawns a sub-thread over MCP; it links to its parent, which hears when it finishes; a teammate spawns one of their own', async ({ as }) => {
	const bob = as('bob');
	// An origin that refuses pushes: the parent's branch stays unpushed.
	const parent = await as('alice').create({ repoUrl: repo('readonly') });
	await parent.waitForStatus(Status.AWAITING_INPUT);
	const out = await parent.mcp('create_thread', { prompt: 'e2e: a sub-thread' });
	const childId = out.match(/thread_id[^a-z0-9]+([a-z0-9]{26})/)?.[1];
	expect(childId, out).toBeTruthy();
	const child = parent.user.thread(childId!);
	const info = await child.get();
	expect(info.parentId).toBe(parent.id);
	expect(info.parentTitle).not.toBe('');
	expect(info.baseBranch).toBe('main'); // an unpushed parent's sub-thread starts from its base
	await child.waitForStatus(Status.AWAITING_INPUT);
	const update = await parent.waitForEvent(
		(e) => e.kind.case === 'lineageUpdate' && e.kind.value.threadId === childId && e.kind.value.status === Status.AWAITING_INPUT,
		'the finish notice'
	);
	expect(update.kind.value).toMatchObject({ text: expect.stringContaining('hello') });
	expect((await parent.of('lineageUpdate')).filter((u) => u.status === Status.STARTING)).toHaveLength(1);
	expect(await parent.mcp('list_children', {})).toContain(childId);

	await test.step('a teammate spawns a sub-thread of their own; the parent lists both', async () => {
		const { thread } = await bob.threads.spawnThread({ parentId: parent.id, prompt: 'e2e: bob’s sub-thread' });
		expect([thread?.ownerId, thread?.parentId]).toEqual([bob.id, parent.id]);
		expect((await parent.user.threads.lineageChildren({ threadId: parent.id })).threads).toHaveLength(2);
	});

	await test.step('the parent agent hears of its sub-threads in its next prompt', async () => {
		const after = await parent.turn(() => parent.send('carry on'));
		expect(after.diff).toContain(`Sub-thread ${childId}`);
	});
});
