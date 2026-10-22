// Collision radar: threads on one repository and base branch whose diffs change the same files see each other.
import { expect, Status, test, until } from './fixtures';

// The shared world turns overlaps off (every plain thread writes hello1.txt), so this test has an API of its own.
test('threads changing the same file see each other, in the API, chat, prompt, MCP, page and board; settling ends it', async ({ as, startApi }) => {
	const api = await startApi({ OVERLAP_WINDOW: '168h' });
	const [alice, bob] = [as('alice', api), as('bob', api)];
	const [a, b, c] = await Promise.all([
		alice.create({ prompt: 'Alice’s part\ne2e-file:src/shared.ts' }),
		bob.create({ prompt: 'Bob’s part\ne2e-file:src/shared.ts' }),
		bob.create({ prompt: 'Bob’s other part\ne2e-file:src/other.ts' })
	]);
	await Promise.all([a, b, c].map((t) => t.waitFor('to have its diff', (i) => i.status === Status.AWAITING_INPUT && i.diff !== '')));

	const seen = await a.waitFor('to overlap with b', (t) => t.overlaps.length > 0);
	expect(seen.overlaps.map((o) => [o.threadId, o.title, o.ownerId, o.paths])).toEqual([[b.id, 'Bob’s part', bob.id, ['src/shared.ts']]]);
	expect((await b.get()).overlaps.map((o) => [o.threadId, o.paths])).toEqual([[a.id, ['src/shared.ts']]]);
	expect((await c.get()).overlaps).toEqual([]);

	// One quiet line in each chat, and it reaches the agent with the next prompt.
	const notices = async (t: typeof a) => (await t.of('overlap')).map((o) => [o.threadId, o.paths]);
	await until(async () => (await notices(b)).length > 0, () => 'no overlap notice in b');
	expect(await notices(a)).toEqual([[b.id, ['src/shared.ts']]]);
	expect(await notices(b)).toEqual([[a.id, ['src/shared.ts']]]);
	await a.turn(() => a.send('e2e-echo again'));
	expect((await a.prompts()).at(-1)).toContain(`[Now overlaps with thread ${b.id} "Bob’s part" by ${bob.id}, which changes the same files: src/shared.ts]`);
	// That turn added hello1.txt, which b doesn't change: no new notice for the pair.
	await a.waitFor('to have hello1.txt', (t) => t.diff.includes('hello1.txt'));
	expect(await notices(a)).toHaveLength(1);

	// The agent sees them through read_thread, of its own thread by default.
	const read = await a.mcp('read_thread', {});
	expect(read).toContain(`${b.id} \\"Bob’s part\\" by ${bob.id} (awaiting_input): src/shared.ts`);

	const page = await alice.page();
	await page.goto(`/t/${a.id}`);
	await expect(page.getByText(/^Now overlaps with Bob’s part by/)).toBeVisible();
	await page.getByRole('button', { name: /Overlaps with 1 thread/ }).click();
	const overlaps = page.getByRole('region', { name: 'Overlaps' });
	await expect(overlaps.getByRole('link', { name: /Bob’s part/ })).toHaveAttribute('href', `/t/${b.id}`);
	await page.getByRole('button', { name: 'Toggle right pane' }).click();
	await page.getByRole('button', { name: /files? changed/ }).click();
	await expect(page.getByRole('img', { name: 'Also changed in Bob’s part' }).first()).toBeVisible();
	// The file link opens b's diff at that file.
	await overlaps.getByRole('link', { name: 'src/shared.ts' }).click();
	await expect(page).toHaveURL(`/t/${b.id}?file=src%2Fshared.ts`);
	await expect(page.getByRole('img', { name: 'Also changed in Alice’s part' }).first()).toBeVisible();

	await page.goto('/team');
	await expect(page.getByRole('link', { name: /Alice’s part/ }).getByRole('img', { name: 'Overlaps with Bob’s part' })).toBeVisible();
	await expect(page.getByRole('link', { name: /Bob’s other part/ }).getByRole('img', { name: /^Overlaps/ })).toHaveCount(0);

	await bob.threads.settle({ threadId: b.id, settled: true });
	expect((await a.get()).overlaps).toEqual([]);
	await expect(page.getByRole('link', { name: /Alice’s part/ }).getByRole('img', { name: /^Overlaps/ })).toHaveCount(0);
});
