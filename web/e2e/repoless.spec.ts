// Threads without a repository: the agent works in an empty folder that lives only in the thread's volume.
import { expect, kube, refused, repo, sql, Status, test, type Thread } from './fixtures.ts';

const files = async (t: Thread) => (await t.user.threads.listFiles({ threadId: t.id })).paths;

test('a thread without a repository: no branches, setup or pushes; its turn writes files in an empty folder that settling keeps and a prompt resumes', async ({ as }) => {
	const alice = as('alice');
	const word = `quokka${Date.now()}`;
	const t = await alice.create({ repoUrl: '', baseBranch: 'main', prompt: `e2e: write a file ${word}` });
	let info = await t.waitForStatus(Status.AWAITING_INPUT);
	expect([info.repoUrl, info.branch, info.baseBranch, info.diff]).toEqual(['', '', '', '']);
	expect(await files(t)).toEqual(['hello1.txt']);
	expect(await t.terminal('pwd; test -e .git || echo no-git')).toMatch(/\/home\/banda\/work\s+no-git/);
	expect(await t.of('setupStep')).toEqual([]); // no checkout, tools or setup.sh; the fake is logged in
	expect(await refused(alice.threads.push({ threadId: t.id }))).toContain('no repository');
	expect(await t.mcp('read_thread', { thread_id: t.id })).toContain('Repo: none');
	expect((await alice.threads.search({ query: word, repoUrl: '' })).results.map((r) => r.threadId)).toEqual([t.id]); // the “No repository” project
	expect((await alice.threads.search({ query: word, repoUrl: repo() })).results).toEqual([]);

	await test.step('settling suspends it and never schedules its deletion: the work lives only in the volume', async () => {
		await alice.threads.settle({ threadId: t.id, settled: true });
		expect((await t.get()).status).toBe(Status.SUSPENDED);
		await t.podGone();
		expect(await kube('get', 'sandbox', t.pod, '-o', 'jsonpath={.spec.shutdownTime}')).toBe('');
		expect(await sql(`SELECT env_pushed FROM threads WHERE id = '${t.id}'`)).toBe('f');
	});

	await test.step('a prompt resumes it with its files', async () => {
		info = await t.turn(() => t.send('e2e: and another'));
		expect(await files(t)).toEqual(['hello1.txt', 'hello2.txt']);
		expect(info.diff).toBe('');
		expect(await t.of('autoPush')).toHaveLength(0); // no push lines in the chat
	});
});

test('a fork of a thread without a repository carries the conversation, not the files', async ({ as }) => {
	const bob = as('bob');
	const src = await as('alice').create({ repoUrl: '', prompt: 'e2e: the source’s ask' });
	await src.waitForStatus(Status.AWAITING_INPUT);
	await src.turn(() => src.send('e2e: one more'));
	const { threads } = await bob.threads.fork({ threadId: src.id, prompt: 'e2e-echo carry on' });
	const fork = bob.thread(threads[0].id);
	const info = await fork.waitForStatus(Status.AWAITING_INPUT);
	expect([info.repoUrl, info.branch, info.forkedFrom?.threadId]).toEqual(['', '', src.id]);
	const prompt = (await fork.prompts())[0];
	expect(prompt).toContain('you start in an empty folder');
	expect(prompt).toContain('e2e: the source’s ask');
	expect(await files(fork)).toEqual(['hello1.txt']); // its own turn's, not the source's two
	expect((await fork.of('setupStep')).filter((s) => s.id === 'fork')).toHaveLength(0);
});

test('sub-threads of a thread without a repository have none either, from MCP and from a teammate; a race needs no repository', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const parent = await alice.create({ repoUrl: '' });
	await parent.waitForStatus(Status.AWAITING_INPUT);
	const out = await parent.mcp('create_thread', { prompt: 'e2e: a sub-thread' });
	const child = alice.thread(out.match(/thread_id[^a-z0-9]+([a-z0-9]{26})/)![1]);
	const info = await child.waitForStatus(Status.AWAITING_INPUT);
	expect([info.parentId, info.repoUrl, info.branch, info.baseBranch]).toEqual([parent.id, '', '', '']);
	expect(await files(child)).toEqual(['hello1.txt']);
	const { thread } = await bob.threads.spawnThread({ parentId: parent.id, prompt: 'e2e: bob’s sub-thread' });
	expect([thread?.repoUrl, thread?.branch]).toEqual(['', '']);
	const { threads } = await alice.threads.race({ repoUrl: '', prompt: 'e2e: race', tries: [{}, { model: 'claude-sonnet-5-5' }] });
	expect(threads.map((t) => [t.repoUrl, t.branch])).toEqual([['', ''], ['', '']]);
});

test('browser: a thread started with “No repository” shows its files, no diff or push, and has a project of its own in the sidebar', async ({ as }) => {
	const alice = as('alice');
	const page = await alice.page();
	await page.goto('/');
	await page.getByRole('button', { name: 'Choose repository' }).click();
	await page.getByRole('menuitemradio', { name: 'No repository' }).click();
	await expect(page.getByLabel('Base branch')).toHaveCount(0);
	await page.getByPlaceholder('Describe the task.').fill('e2e: notes for the launch');
	await page.keyboard.press('Enter');
	await expect(page).toHaveURL(/\/t\/[a-z0-9]{26}$/);
	await expect(page.getByText('No repository', { exact: true })).toBeVisible();
	await expect(page.getByText('Created hello1.txt')).toBeVisible({ timeout: 90_000 });

	await page.getByRole('button', { name: 'Toggle right pane' }).click();
	const pane = page.getByRole('tabpanel', { name: 'Files' }); // the pane opens on Files: there is no Diff tab
	await expect(pane.getByRole('button', { name: 'hello1.txt', exact: true })).toBeVisible();
	await expect(page.getByRole('tab', { name: 'Diff' })).toHaveCount(0);
	await expect(page.getByRole('button', { name: 'Push the branch to origin' })).toHaveCount(0);
	const composer = page.getByPlaceholder('Ask the agent…');
	await composer.fill('e2e: another file');
	await composer.press('Enter');
	await expect(pane.getByRole('button', { name: 'hello2.txt', exact: true })).toBeVisible({ timeout: 90_000 }); // listed once the turn ends

	await test.step('the sidebar’s project filter files it under “No repository”, apart from a thread with one', async () => {
		await (await alice.create({ repoUrl: repo(), prompt: 'e2e: with a repository' })).waitForStatus(Status.AWAITING_INPUT);
		const sidebar = page.getByRole('region', { name: 'Needs input' });
		await expect(sidebar.getByRole('link', { name: /e2e: with a repository/ })).toBeVisible();
		await page.getByRole('button', { name: 'Filter by project' }).click();
		await page.getByRole('menuitemradio', { name: 'No repository' }).click();
		await expect(page.getByRole('button', { name: 'Project: No repository' })).toBeVisible();
		await expect(sidebar.getByRole('link', { name: /e2e: notes for the launch/ })).toBeVisible();
		await expect(page.getByRole('link', { name: /e2e: with a repository/ })).toHaveCount(0);
	});
});
