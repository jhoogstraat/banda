// Pull requests through the forges' APIs: each test runs an API of its own against a fake forge (./forge.ts)
// that knows the run's git server, so a pull request needs the thread's branch really pushed.
import type { Page } from '@playwright/test';
import { test as base, expect, refused, sql, type Api, type User } from './fixtures';
import { FakeForge } from './forge';
import { AutoPushState } from '../src/lib/gen/banda/v1/banda_pb.js';

const test = base.extend<{ forge: FakeForge }>({
	forge: async ({}, use) => {
		const forge = await new FakeForge().start();
		await use(forge);
		await forge.stop();
	}
});

const repoUrl = 'https://github.com/e2e/plain.git'; // the run's git server, for git (deploy/sandbox.Dockerfile)

/** alice's thread whose agent committed and pushed, on an API that talks to the fake forge; bob is a teammate. */
async function world(as: (name: string, api?: Api) => User, startApi: (env?: Record<string, string>) => Promise<Api>, forge: FakeForge, env: Record<string, string> = {}, prompt = 'e2e: commit') {
	const api = await startApi({ TEST_FORGE_URL: forge.url, PULL_REQUEST_POLL: '1s', ...env });
	const alice = as('alice', api);
	const bob = as('bob', api);
	forge.tokens = { 'alice-token': 'alice', 'bob-token': 'bob' };
	await alice.settings.setSecret({ name: 'GIT_TOKEN', value: 'alice-token' }); // pushes over https need it too
	const t = await alice.create({ repoUrl, prompt });
	await t.waitFor('its work to be pushed', (i) => i.autoPush === AutoPushState.PUSHED);
	return { api, alice, bob, t };
}

async function open(user: User, id: string) {
	const page = await user.page();
	await page.goto(`/t/${id}`);
	return page;
}

const pane = (page: Page) => page.getByRole('tabpanel');

test('the owner opens a pull request from the dialog; the header chip, the tab and a line when its checks fail, which reaches the agent', { tag: '@key' }, async ({ as, startApi, forge }) => {
	const { alice, t } = await world(as, startApi, forge);
	const title = (await t.get()).title;
	const page = await open(alice, t.id);
	await page.getByRole('button', { name: 'Create a pull request' }).click();
	const dialog = page.getByRole('dialog');
	await expect(dialog.getByLabel('Title')).toHaveValue(title);
	await expect(dialog.getByLabel('Description')).not.toHaveValue(''); // the agent's last reply
	await dialog.getByLabel('Description').fill('Adds **hello1.txt**.');
	await dialog.getByRole('button', { name: 'Create pull request' }).click();
	await expect(dialog).toBeHidden();
	expect(forge.pull(1)).toMatchObject({ head: (await t.get()).branch, base: 'main', body: 'Adds **hello1.txt**.', author: 'alice', draft: false });

	await expect(page.getByText(`${alice.id} opened pull request #1`)).toBeVisible();
	const chip = page.getByRole('button', { name: /^PR #1 · Open/ });
	await chip.click();
	await expect(pane(page).getByRole('heading', { name: `${title} #1` })).toBeVisible();
	await expect(pane(page).getByText('hello1.txt', { exact: true })).toBeVisible(); // the description as markdown
	await expect(pane(page).getByText('Checks · No checks reported')).toBeVisible();

	// The forge reports a review comment and failing checks; the open page hears of them within a poll.
	forge.comment(1, { login: 'carol', body: 'Why `hello1`?', path: 'hello1.txt', line: 1 });
	forge.pull(1).checks = [
		{ name: 'lint', status: 'completed', conclusion: 'success' },
		{ name: 'test', status: 'completed', conclusion: 'failure' }
	];
	await expect(page.getByText('Checks failed on pull request #1')).toBeVisible({ timeout: 15_000 });
	await expect(page.getByRole('button', { name: 'PR #1 · Open · checks failed' })).toBeVisible();
	await expect(pane(page).getByText('Checks · 1 of 2 failing')).toBeVisible();
	await expect(pane(page).getByRole('article', { name: 'Comment by carol' })).toContainText('hello1.txt:1');

	// Like other quiet lines, it reaches the agent with its next prompt.
	await t.turn(() => t.send('e2e-echo e2e-noop'));
	expect((await t.prompts()).at(-1)).toContain(`[Pull request #1 "${title}" has failing checks: ${forge.url}/e2e/plain/pull/1]`);
});

// E2E_SCREENSHOTS=1 refreshes the README's picture of this: a pull request with a failing check and a review comment.
test('the README: a pull request with its checks and a comment on its diff', async ({ as, startApi, forge }) => {
	const { api, alice, t } = await world(as, startApi, forge, {}, 'Retry failed uploads with backoff');
	await sql(`INSERT INTO users (id, name) VALUES ('${alice.id}', 'Maya Lindqvist') ON CONFLICT (id) DO UPDATE SET name = excluded.name`, api.db);
	forge.tokens['alice-token'] = 'maya';
	const { title } = await t.get();
	await alice.threads.pullRequestCreate({ threadId: t.id, title, body: 'Uploads retry up to five times with exponential backoff, so a flaky connection no longer stops the nightly sync.' });
	Object.assign(forge.pull(1), { labels: ['bug'], stats: [17, 1, 2] });
	forge.comment(1, { login: 'jonas', body: 'Should `MAX_TRIES` be a setting?', path: 'src/upload.ts', line: 1, hunk: '@@ -0,0 +1,16 @@\n+const MAX_TRIES = 5;' });
	forge.pull(1).checks = [
		{ name: 'lint', status: 'completed', conclusion: 'success' },
		{ name: 'test', status: 'completed', conclusion: 'failure' }
	];

	const page = await alice.page({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark' });
	await page.goto(`/t/${t.id}`);
	await page.getByRole('button', { name: /^PR #1/ }).click();
	await expect(pane(page).getByText('Checks · 1 of 2 failing')).toBeVisible({ timeout: 15_000 });
	await expect(pane(page).getByRole('article', { name: 'Comment by jonas' })).toContainText('src/upload.ts:1');
	await expect(page.getByRole('button', { name: 'PR #1 · Open · checks failed' })).toBeVisible();
	await page.mouse.move(0, 0);
	await page.evaluate(() => document.fonts.ready);
	if (process.env.E2E_SCREENSHOTS) await page.screenshot({ path: '../docs/screenshots/pull-request.png' });
});

test('anyone comments with their own token: a teammate without one is told how, with one comments and replies as themselves', async ({ as, startApi, forge }) => {
	const { alice, bob, t } = await world(as, startApi, forge);
	await alice.threads.pullRequestCreate({ threadId: t.id, title: 'Hello', body: 'B' });
	forge.comment(1, { login: 'carol', body: 'Why this name?', path: 'hello1.txt', line: 1 });

	expect(await refused(bob.threads.pullRequestComment({ threadId: t.id, body: 'hi' }))).toContain('You have no token for github.com: add GIT_TOKEN in Settings');
	let page = await open(bob, t.id);
	await page.getByRole('button', { name: /^PR #1/ }).click();
	await expect(pane(page).getByText('Comments go out as you: connect GitHub, or add a GIT_TOKEN secret')).toBeVisible();

	await bob.settings.setSecret({ name: 'GIT_TOKEN', value: 'bob-token' });
	await page.reload();
	await page.getByRole('button', { name: /^PR #1/ }).click();
	await pane(page).getByLabel('Comment', { exact: true }).fill('Looks good to me');
	await pane(page).getByRole('button', { name: 'Comment', exact: true }).click();
	await expect(pane(page).getByRole('article', { name: 'Comment by bob' })).toContainText('Looks good to me');
	await pane(page).getByRole('button', { name: 'Reply', exact: true }).click();
	await pane(page).getByLabel('Reply to carol').fill('It greets.');
	await pane(page).getByRole('button', { name: 'Reply', exact: true }).click();
	await expect(pane(page).getByText('It greets.')).toBeVisible();
	const carols = forge.pull(1).comments.find((c) => c.login === 'carol')!;
	expect(forge.pull(1).comments.filter((c) => c.login === 'bob')).toMatchObject([{ body: 'Looks good to me' }, { body: 'It greets.', inReplyTo: carols.id }]);
});

test('the owner merges with a merge method; a teammate is refused and sees who merges', async ({ as, startApi, forge }) => {
	const { alice, bob, t } = await world(as, startApi, forge);
	await alice.threads.pullRequestCreate({ threadId: t.id, title: 'Hello', body: '', draft: true });
	expect(forge.pull(1).draft).toBe(true);
	expect(await refused(bob.threads.pullRequestMerge({ threadId: t.id, method: 'merge' }))).toContain('only the thread owner can do this');
	expect(await refused(bob.threads.pullRequestUpdate({ threadId: t.id, draft: false }))).toContain('only the thread owner can do this');

	const bobs = await open(bob, t.id);
	await bobs.getByRole('button', { name: /^PR #1 · Draft/ }).click();
	await expect(pane(bobs).getByText(`${alice.id} merges it`)).toBeVisible();
	await expect(pane(bobs).getByRole('button', { name: 'Merge', exact: true })).toHaveCount(0);

	const page = await open(alice, t.id);
	await page.getByRole('button', { name: /^PR #1 · Draft/ }).click();
	await pane(page).getByRole('button', { name: 'Ready for review' }).click();
	await expect(page.getByText(`Pull request #1 was marked ready for review by ${alice.id}`)).toBeVisible();
	await pane(page).getByLabel('Merge method').selectOption('merge');
	await pane(page).getByRole('button', { name: 'Merge', exact: true }).click();
	await expect(page.getByText(`Pull request #1 was merged by ${alice.id}`)).toBeVisible();
	await expect(page.getByRole('button', { name: 'PR #1 · Merged' })).toBeVisible();
	await expect(pane(page).getByText('Merged by alice')).toBeVisible();
	expect(forge.pull(1)).toMatchObject({ state: 'merged', mergedBy: 'alice', mergeMethod: 'merge' });
	await expect(bobs.getByRole('button', { name: 'PR #1 · Merged' })).toBeVisible(); // live for the teammate too
});

test('a self-hosted GitLab (FORGE_HOSTS): link the open merge request of the branch, an approval and the pipeline, merged squashed', async ({ as, startApi, forge }) => {
	const { alice, t } = await world(as, startApi, forge, { FORGE_HOSTS: 'github.com=gitlab' });
	const { branch } = await t.get();
	expect(await refused(alice.threads.pullRequestLink({ threadId: t.id }))).toContain(`no open pull request has ${branch} as its branch`);
	await forge.open(branch, 'From the forge');
	const { pullRequest } = await alice.threads.pullRequestLink({ threadId: t.id });
	expect(pullRequest?.summary).toMatchObject({ forge: 'gitlab', number: 1, title: 'From the forge' });

	const page = await open(alice, t.id);
	await expect(page.getByText(`${alice.id} linked merge request !1`)).toBeVisible();
	forge.pull(1).checks = [{ name: 'unit', status: 'in_progress' }];
	forge.comment(1, { login: 'carol', body: '', review: 'APPROVED' });
	await expect(page.getByText('Merge request !1 was approved')).toBeVisible({ timeout: 15_000 });
	await page.getByRole('button', { name: 'MR !1 · Open · checks running · approved' }).click();
	await expect(pane(page).getByRole('region', { name: 'Reviewers' })).toContainText('carolApproved');
	await expect(pane(page).getByText('Checks · 1 of 1 running')).toBeVisible();

	// Members and labels to pick from, labels changed, a review thread resolved: GitLab's own calls.
	expect(await alice.threads.pullRequestChoices({ threadId: t.id })).toMatchObject({ reviewers: forge.collaborators, labels: forge.labels });
	await alice.threads.pullRequestUpdate({ threadId: t.id, addLabels: ['bug'], removeLabels: ['e2e'] });
	expect(forge.pull(1).labels).toEqual(['bug']);
	forge.comment(1, { login: 'carol', body: 'Rename it?', path: 'hello1.txt', line: 1 });
	const { pullRequest: fresh } = await alice.threads.pullRequestGet({ threadId: t.id, refresh: true });
	const root = fresh!.comments.find((c) => c.path)!;
	expect(root).toMatchObject({ threadId: `d${root.id}`, resolved: false });
	const { pullRequest: resolved } = await alice.threads.pullRequestResolve({ threadId: t.id, reviewThread: root.threadId, resolved: true });
	expect(resolved!.comments.find((c) => c.path)?.resolved).toBe(true);

	await alice.threads.pullRequestMerge({ threadId: t.id, method: 'squash' });
	expect(forge.pull(1)).toMatchObject({ state: 'merged', mergeMethod: 'squash' });
	await expect(page.getByRole('button', { name: 'MR !1 · Merged' })).toBeVisible();
});

test('the create dialog asks for reviewers and adds labels from the forge’s lists', async ({ as, startApi, forge }) => {
	const { alice, t } = await world(as, startApi, forge);
	const page = await open(alice, t.id);
	await page.getByRole('button', { name: 'Create a pull request' }).click();
	const dialog = page.getByRole('dialog');
	await expect(dialog.locator('#pr-create-reviewer-choices option')).toHaveCount(4);
	await dialog.getByLabel('Reviewers to ask').fill('bob, @carol');
	await dialog.getByLabel('Add label').selectOption('bug');
	await dialog.getByLabel('Add label').selectOption('ui');
	await dialog.getByRole('button', { name: 'Remove label ui' }).click();
	await expect(dialog.getByRole('button', { name: 'Remove label bug' })).toBeVisible();
	await dialog.getByRole('button', { name: 'Create pull request' }).click();
	await expect(dialog).toBeHidden();
	expect(forge.pull(1)).toMatchObject({ reviewers: ['bob', 'carol'], labels: ['e2e', 'bug'] }); // the fake opens it with e2e
	await page.getByRole('button', { name: /^PR #1/ }).click();
	await expect(pane(page).getByRole('region', { name: 'Reviewers' })).toContainText('carolReview requested');
	await expect(pane(page).getByRole('button', { name: 'Remove label bug' })).toBeVisible();
});

test('GitLab: a comment is a plain note, a thread without a file resolves, changes requested show, and a refused merge says so plainly', async ({ as, startApi, forge }) => {
	const { alice, t } = await world(as, startApi, forge, { FORGE_HOSTS: 'github.com=gitlab' });
	await alice.threads.pullRequestCreate({ threadId: t.id, title: 'Hello', body: '', reviewers: ['bob'] });
	const page = await open(alice, t.id);
	await page.getByRole('button', { name: /^MR !1/ }).click();
	const conversation = pane(page).getByRole('region', { name: 'Conversation' });

	// A new discussion would be an unresolved thread on GitLab, which can block merging.
	await pane(page).getByLabel('Comment', { exact: true }).fill('Plain');
	await pane(page).getByRole('button', { name: 'Comment', exact: true }).click();
	await expect(conversation).toContainText('Plain');
	expect(forge.calls.filter((c) => c.endsWith('/merge_requests/1/notes'))).toEqual(['POST /api/v4/projects/e2e/plain/merge_requests/1/notes']);
	expect(forge.calls.some((c) => c.startsWith('POST') && c.endsWith('/discussions'))).toBe(false);
	await expect(conversation.getByRole('button', { name: 'Resolve', exact: true })).toHaveCount(0);

	// Someone starts a thread on GitLab, on no line: it blocks merging until resolved, here too.
	forge.comment(1, { login: 'bob', body: 'Think again', thread: true });
	forge.comment(1, { login: 'bob', body: '', review: 'CHANGES_REQUESTED' });
	forge.pull(1).blocked = 'discussions_not_resolved';
	await expect(page.getByRole('button', { name: /^MR !1 · Open · changes requested/ })).toBeVisible({ timeout: 15_000 });
	await expect(pane(page).getByText('Unresolved discussions')).toBeVisible();
	await expect(pane(page).getByRole('region', { name: 'Reviewers' })).toContainText('bobChanges requested');
	await pane(page).getByRole('button', { name: 'Merge', exact: true }).click();
	await expect(pane(page).getByText("GitLab won't merge this yet: see what blocks it above", { exact: true })).toBeVisible();
	await conversation.getByRole('button', { name: 'Reply', exact: true }).click();
	await conversation.getByLabel('Reply to bob').fill('Done');
	await conversation.getByRole('button', { name: 'Reply', exact: true }).click();
	await expect(conversation).toContainText('Done');
	await conversation.getByRole('button', { name: 'Resolve', exact: true }).click();
	await expect(conversation.getByRole('button', { name: 'Unresolve', exact: true })).toBeVisible();
	expect(forge.pull(1).comments.find((c) => c.thread)?.resolved).toBe(true);
});

test('GitLab: a merge request created with reviewers and labels gets them', async ({ as, startApi, forge }) => {
	const { alice, t } = await world(as, startApi, forge, { FORGE_HOSTS: 'github.com=gitlab' });
	await alice.threads.pullRequestCreate({ threadId: t.id, title: 'Hello', body: '', reviewers: ['bob'], labels: ['bug'] });
	expect(forge.pull(1)).toMatchObject({ reviewers: ['bob'], labels: ['e2e', 'bug'] });
});

test('the new-thread composer lists my repositories from the forge, filtered as I type and searched on the forge', async ({ as, startApi, forge }) => {
	const api = await startApi({ TEST_FORGE_URL: forge.url });
	const alice = as('alice', api);
	forge.tokens = { 'alice-token': 'alice' };
	await alice.settings.setSecret({ name: 'GIT_TOKEN', value: 'alice-token' });
	const page = await alice.page();
	await page.goto('/');
	await page.getByRole('button', { name: 'Choose repository' }).click();
	await expect(page.getByRole('menuitemradio', { name: 'e2e/profiles' })).toBeVisible();
	// Beyond the listed ones, the forge is searched within my repositories' owners.
	await page.getByLabel('Repository URL').fill('archive');
	await expect(page.getByRole('menuitemradio', { name: 'e2e/archive-2019' })).toBeVisible();
	await expect(page.getByRole('menuitemradio', { name: 'elsewhere/archive' })).toHaveCount(0);
	expect(forge.calls).toContain('GET /api/v3/search/repositories');
	await page.getByLabel('Repository URL').fill('pla');
	await expect(page.getByRole('menuitemradio', { name: 'e2e/profiles' })).toBeHidden();
	await page.getByRole('menuitemradio', { name: 'e2e/plain' }).click();
	await page.getByPlaceholder('Describe the task.').fill('e2e: from my repositories');
	await page.keyboard.press('Enter');
	await expect(page).toHaveURL(/\/t\/[a-z0-9]+$/);
	const t = alice.thread(page.url().split('/').pop()!);
	expect((await t.get()).repoUrl).toBe(repoUrl);
	await expect(page.getByText('Created hello1.txt')).toBeVisible({ timeout: 90_000 }); // cloned through the URL picked
});

test('without a token for the forge, the header keeps the link to its compare page', async ({ as }) => {
	const alice = as('alice'); // the run's API: no forge calls happen here
	await alice.settings.setSecret({ name: 'GIT_TOKEN', value: 'e2e' });
	const t = await alice.create({ repoUrl, prompt: 'e2e: commit' });
	await t.waitFor('its work to be pushed', (i) => i.autoPush === AutoPushState.PUSHED);
	const page = await open(alice, t.id);
	await expect(page.getByRole('button', { name: 'Create a pull request' })).toBeVisible();
	await alice.settings.deleteSecret({ name: 'GIT_TOKEN' });
	await page.reload();
	await expect(page.getByRole('link', { name: 'Open a pull request' })).toHaveAttribute('href', /^https:\/\/github\.com\/e2e\/plain\/compare\/main\.\.\./);
	expect((await t.get()).forgeReady).toBe(false);
});

test('a pull request the agent opened itself on the forge is linked when its turn ends, with a quiet line that reaches the agent', async ({ as, startApi, forge }) => {
	const { alice, t } = await world(as, startApi, forge);
	const { branch } = await t.get();
	expect((await alice.threads.pullRequestGet({ threadId: t.id })).pullRequest).toBeUndefined(); // nothing to spot after the first push
	await forge.open(branch, 'Opened with gh'); // as `gh pr create` in the sandbox would
	await t.turn(() => t.send('e2e-echo e2e-noop'));
	const page = await open(alice, t.id);
	await expect(page.getByText('Pull request #1 on this branch was linked')).toBeVisible({ timeout: 15_000 });
	await expect(page.getByRole('button', { name: /^PR #1 · Open/ })).toBeVisible();
	await t.turn(() => t.send('e2e-echo e2e-noop'));
	expect((await t.prompts()).at(-1)).toContain(`[Pull request #1 "Opened with gh" was linked to this thread: ${forge.url}/e2e/plain/pull/1]`);
});

test('the owner picks reviewers and labels from the forge’s lists and anyone with a token resolves review threads', async ({ as, startApi, forge }) => {
	const { alice, t } = await world(as, startApi, forge);
	await alice.threads.pullRequestCreate({ threadId: t.id, title: 'Hello', body: '' });
	forge.comment(1, { login: 'carol', body: 'Why this name?', path: 'hello1.txt', line: 1 });
	const page = await open(alice, t.id);
	await page.getByRole('button', { name: /^PR #1/ }).click();
	const p = pane(page);

	// alice opened it: the others are offered.
	await expect(p.locator('#pr-reviewer-choices option')).toHaveCount(3);
	expect(await p.locator('#pr-reviewer-choices option').evaluateAll((os) => os.map((o) => (o as HTMLOptionElement).value))).toEqual(['bob', 'carol', 'dave']);
	await p.getByLabel('Reviewers to ask').fill('bob');
	await p.getByRole('button', { name: 'Ask for review' }).click();
	await expect(p.getByRole('region', { name: 'Reviewers' })).toContainText('bobReview requested');
	expect(forge.pull(1).reviewers).toEqual(['bob']);
	await expect(p.locator('#pr-reviewer-choices option')).toHaveCount(2);

	await p.getByLabel('Add label').selectOption('bug');
	await expect(p.getByRole('button', { name: 'Remove label bug' })).toBeVisible();
	await p.getByRole('button', { name: 'Remove label e2e' }).click();
	await expect(p.getByRole('button', { name: 'Remove label e2e' })).toHaveCount(0);
	expect(forge.pull(1).labels).toEqual(['bug']);

	await p.getByRole('button', { name: 'Resolve', exact: true }).click();
	await expect(p.getByRole('article', { name: 'Comment by carol' })).toContainText('resolved');
	expect(forge.pull(1).comments[0].resolved).toBe(true);
	await p.getByRole('button', { name: 'Unresolve' }).click();
	await expect(p.getByRole('article', { name: 'Comment by carol' })).not.toContainText('resolved');
	expect(forge.pull(1).comments[0].resolved).toBe(false);
});
