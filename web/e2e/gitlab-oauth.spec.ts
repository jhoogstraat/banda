// GitLab through the team's OAuth application (internal/api/gitlab.go): set up from Settings by pasting the ID and
// secret of an application made by hand, users connect with two grants of it (api for Banda, write_repository for
// git), and git in sandboxes gets the git-only one. Each test runs an API of its own against the fake forge
// (./forge.ts), with application Secrets of its own: the run's other APIs share the namespace.
import type { Page } from '@playwright/test';
import { randomBytes } from 'node:crypto';
import { test as base, expect, kube, kexec, refused, sql, until, userHash, type Api, type User } from './fixtures';
import { FakeForge } from './forge';
import { toJsonString } from '@bufbuild/protobuf';
import { AutoPushState, GetGitLabResponseSchema } from '../src/lib/gen/banda/v1/banda_pb.js';

const test = base.extend<{ forge: FakeForge }>({
	forge: async ({}, use) => {
		const forge = await new FakeForge().start();
		await use(forge);
		await forge.stop();
	}
});

const repoUrl = 'https://gitlab.com/e2e/plain.git'; // the run's git server, for git (deploy/sandbox.Dockerfile)
const appId = 'b4nd4e2e0123456789abcdef';
const appSecret = `gloas-${randomBytes(16).toString('hex')}`;

async function startWorld(startApi: (env?: Record<string, string>) => Promise<Api>, forge: FakeForge, env: Record<string, string> = {}) {
	const prefix = `banda-gitlab-app-${randomBytes(3).toString('hex')}-`;
	const api = await startApi({ TEST_FORGE_URL: forge.url, TEST_GITLAB_APP_SECRET: prefix, PULL_REQUEST_POLL: '1s', ...env });
	// The application someone made on GitLab by hand, as Settings tells them to.
	forge.gitlabApp = { clientId: appId, clientSecret: appSecret, redirect: `${api.url}/gitlab/callback`, scopes: ['api', 'write_repository'] };
	return { api, secret: `${prefix}gitlab.com` };
}

/** Follows a browser flow's redirects as user, with their header only on the API: returns where it lands in Banda (Settings or a thread). */
async function follow(api: Api, user: User, url: string) {
	for (let i = 0; i < 10; i++) {
		const own = url.startsWith(api.url);
		const r = await fetch(url, { redirect: 'manual', headers: own ? { 'X-Banda-User': user.id } : {} });
		const to = r.headers.get('location');
		if (!to) throw new Error(`${url} answered ${r.status} without a redirect: ${await r.text()}`);
		url = new URL(to, url).href;
		if (url.startsWith(api.url) && /^\/(settings$|t\/)/.test(new URL(url).pathname)) return new URL(url);
	}
	throw new Error('too many redirects');
}

const setUp = (user: User) => user.settings.setGitLabApp({ host: 'gitlab.com', applicationId: appId, secret: appSecret });

/** Connects user's GitLab account (forge login): both grants, one after the other. */
async function connect(api: Api, user: User, forge: FakeForge, login: string, outcome = 'connected') {
	forge.signedIn = login;
	const landed = await follow(api, user, `${api.url}/gitlab/connect?host=gitlab.com`);
	expect(landed.searchParams.get('gitlab')).toBe(outcome);
}

async function open(user: User, path: string) {
	const page = await user.page();
	await page.goto(path);
	return page;
}

const section = (page: Page, name: string) => page.getByRole('region', { name, exact: true });

/** What the user's forge Secret holds, decoded; empty if it is gone. */
async function stored(user: User) {
	const out = await kube('get', 'secret', `banda-forge-${userHash(`${user.id} gitlab.com`)}`, '--ignore-not-found', '-o', 'jsonpath={.data}');
	return Object.fromEntries(Object.entries(out ? (JSON.parse(out) as Record<string, string>) : {}).map(([k, v]) => [k, Buffer.from(v, 'base64').toString()]));
}

const credentialFill = (pod: string) => kexec(pod, `printf 'protocol=https\\nhost=gitlab.com\\npath=someone/else.git\\n\\n' | git credential fill 2>&1 || true`);

test('team setup from Settings: where to create the application, the exact values, its ID and secret checked with GitLab', async ({ as, startApi, forge }) => {
	const { api, secret } = await startWorld(startApi, forge, { FORGE_HOSTS: 'git.corp.example=gitlab' });
	const alice = as('alice', api);
	const page = await open(alice, '/settings#gitlab-app');
	const app = section(page, 'GitLab application');
	// A self-hosted GitLab (FORGE_HOSTS) is offered too, and there the instance's admin can own it.
	const host = app.getByRole('combobox', { name: 'Host' });
	await expect(host).toHaveValue('gitlab.com');
	await host.selectOption('git.corp.example');
	await app.getByRole('radio', { name: /The instance/ }).check();
	await expect(app.getByRole('link', { name: 'Open GitLab’s applications page' })).toHaveAttribute('href', `${forge.url}/admin/applications`);
	if (process.env.E2E_SCREENSHOTS) await shoot(page, 'gitlab-app-self-hosted', '#gitlab-app', false);
	await host.selectOption('gitlab.com');
	await expect(app.getByRole('listitem').filter({ hasText: 'Create an application on GitLab' })).toHaveAttribute('aria-current', 'step');
	await expect(section(page, 'GitLab')).toHaveCount(0); // nothing to connect: Accounts offers GitLab once the team has it
	// The values GitLab needs, each to copy: the redirect URI exactly, and only the two scopes.
	const values = app.getByRole('definition');
	await expect(app.getByLabel('Values for GitLab')).toContainText(`${api.url}/gitlab/callback`);
	await expect(app.getByLabel('Values for GitLab')).toContainText(/Scopes\s*api\s*write_repository/);
	await expect(values.getByRole('button', { name: 'Copy the redirect uri' })).toBeVisible();
	// Where: a group (recommended, once named); gitlab.com has no instance admin. Never a person's account: whoever
	// can edit the application can get everyone's tokens, which the form says.
	const openGitLab = app.getByRole('link', { name: 'Open GitLab’s applications page' });
	await expect(openGitLab).toHaveCount(0);
	await expect(app.getByRole('radio', { name: /The instance/ })).toHaveCount(0);
	await expect(app.getByRole('radio', { name: /My account/ })).toHaveCount(0);
	await expect(app).toContainText('Whoever can edit the application on GitLab (the group’s Owners) can get tokens for every teammate who connects');
	if (process.env.E2E_SCREENSHOTS) await shoot(page, 'gitlab-app-create');
	await app.getByLabel('Group path').fill('e2e/team');
	await expect(openGitLab).toHaveAttribute('href', `${forge.url}/groups/e2e/team/-/settings/applications`);
	const [tab] = await Promise.all([page.waitForEvent('popup'), openGitLab.click()]);
	await tab.close();
	await expect(app.getByRole('listitem').filter({ hasText: 'Paste its ID and secret here' })).toHaveAttribute('aria-current', 'step');

	// A wrong secret is caught before anything is kept.
	await app.getByLabel('Application ID').fill(appId);
	await app.getByLabel('Secret').fill('not-the-secret');
	await app.getByRole('button', { name: 'Save and check' }).click();
	await expect(app.getByRole('alert')).toContainText("GitLab doesn't know that Application ID and Secret on gitlab.com");
	expect(await kube('get', 'secret', secret, '--ignore-not-found')).toBe('');
	if (process.env.E2E_SCREENSHOTS) await shoot(page, 'gitlab-app-wrong-secret', '#gitlab-app li:nth-child(2)', false);

	await app.getByLabel('Application ID').fill(appId); // again after the screenshots' reloads
	await app.getByLabel('Secret').fill(appSecret);
	await app.getByRole('button', { name: 'Save and check' }).click();
	await expect(app).toContainText(`Set up by ${alice.id}`);
	await expect(app).toContainText('Everyone connects their own GitLab account once, under Accounts. You haven’t yet.');
	// The ID and secret are in the application's Secret only, and the secret never comes back.
	const data = JSON.parse(await kube('get', 'secret', secret, '-o', 'jsonpath={.data}')) as Record<string, string>;
	expect(Object.keys(data).sort()).toEqual(['application-id', 'host', 'secret']);
	expect(Buffer.from(data.secret, 'base64').toString()).toBe(appSecret);
	expect(await page.content()).not.toContain(appSecret);
	expect(toJsonString(GetGitLabResponseSchema, await alice.settings.getGitLab({}))).not.toContain(appSecret);
	await expect(section(page, 'GitLab').getByRole('link', { name: 'Connect GitLab' })).toBeVisible();
	if (process.env.E2E_SCREENSHOTS) {
		await shoot(page, 'gitlab-app-connect');
		await shoot(page, 'gitlab-not-connected', '#gitlab');
	}

	// Connecting from the personal section: GitLab asks for both grants, and both sections say @alice.
	forge.signedIn = 'alice';
	await section(page, 'GitLab').getByRole('link', { name: 'Connect GitLab' }).click();
	await expect(section(page, 'GitLab')).toContainText('@alice');
	await expect(section(page, 'GitLab')).toContainText('Connected as @alice');
	await expect(app).not.toContainText('You haven’t yet');
	expect(forge.gitlabAuthorized).toEqual(['api', 'write_repository']);
	if (process.env.E2E_SCREENSHOTS) {
		await shoot(page, 'gitlab-connected', '#gitlab');
		await shoot(page, 'gitlab-app-done');
	}
});

test('a team without GitLab sees one quiet line, not the walk-through, until someone asks for it', async ({ as, startApi, forge }) => {
	const { api } = await startWorld(startApi, forge);
	const page = await open(as('alice', api), '/settings');
	const app = section(page, 'GitLab application');
	await expect(app).toContainText('Not set up. Only needed if the team’s repositories are on GitLab');
	await expect(app.getByRole('list')).toHaveCount(0);
	await app.getByRole('button', { name: 'Set up GitLab' }).click();
	await expect(app.getByRole('listitem').filter({ hasText: 'Create an application on GitLab' })).toHaveAttribute('aria-current', 'step');
	await app.getByRole('button', { name: 'Cancel' }).click();
	await expect(app.getByRole('button', { name: 'Set up GitLab' })).toBeVisible();
});

test('connected: a merge request goes out as the user, and git in the pod gets the git-only token, which can’t call the API', { tag: '@key' }, async ({ as, startApi, forge }) => {
	const { api } = await startWorld(startApi, forge);
	const alice = as('alice', api);
	await setUp(alice);

	// Cancelling the second consent connects nothing, and the first grant is revoked at once.
	forge.gitlabDeny = 'write_repository';
	await connect(api, alice, forge, 'alice', 'denied');
	expect(forge.grants.map((g) => [g.scope, !!g.revoked])).toEqual([['api', true]]);
	expect(await stored(alice)).toEqual({});
	forge.gitlabDeny = '';
	await connect(api, alice, forge, 'alice');
	const [apiGrant, gitGrant] = [forge.grant('alice', 'api')!, forge.grant('alice', 'write_repository')!];
	expect(Object.keys(await stored(alice)).sort()).toEqual(['access-token', 'expires-at', 'git-access-token', 'git-expires-at', 'git-refresh-token', 'refresh-token']);

	const t = await alice.create({ repoUrl, prompt: 'e2e: commit' });
	await t.waitFor('its work to be pushed', (i) => i.autoPush === AutoPushState.PUSHED);
	await alice.threads.pullRequestCreate({ threadId: t.id, title: 'From GitLab', body: '' });
	expect(forge.pull(1)).toMatchObject({ author: 'alice', title: 'From GitLab' });

	// The helper asks the API, which answers with the owner's git grant whatever the pod names.
	const out = await credentialFill(t.pod);
	expect(out).toContain(`username=oauth2\npassword=${gitGrant.access}\npassword_expiry_utc=`);
	const expiry = Number(out.match(/password_expiry_utc=(\d+)/)![1]);
	expect(expiry * 1000 - Date.now()).toBeGreaterThan(110 * 60_000); // GitLab's two hours, less what passed
	// That token does git only: GitLab's API refuses it. The api token never enters the pod.
	const asPod = await fetch(`${forge.url}/api/v4/user`, { headers: { Authorization: `Bearer ${gitGrant.access}` } });
	expect(asPod.status).toBe(403);
	expect(await asPod.json()).toMatchObject({ error: 'insufficient_scope' });
	const env = await kexec(t.pod, 'env');
	for (const v of [apiGrant.access, apiGrant.refresh, gitGrant.refresh, gitGrant.access, appSecret]) expect(env).not.toContain(v);
});

test('on GitLab itself the header opens a merge request: created by the connected owner, a link to GitLab’s form for everyone else', async ({ as, startApi, forge }) => {
	const { api } = await startWorld(startApi, forge);
	const [alice, bob] = [as('alice', api), as('bob', api)];
	await setUp(alice);
	await connect(api, alice, forge, 'alice');
	const t = await alice.create({ repoUrl, prompt: 'e2e: commit' });
	await t.waitFor('its work to be pushed', (i) => i.autoPush === AutoPushState.PUSHED);
	const { branch } = await t.get();

	const bobs = await open(bob, `/t/${t.id}`);
	const q = new URLSearchParams({ 'merge_request[source_branch]': branch, 'merge_request[target_branch]': 'main' });
	await expect(bobs.getByRole('link', { name: 'Open a merge request' })).toHaveAttribute('href', `${forge.url}/e2e/plain/-/merge_requests/new?${q}`);

	const page = await open(alice, `/t/${t.id}`);
	await page.getByRole('button', { name: 'Create a merge request' }).click();
	const dialog = page.getByRole('dialog');
	await dialog.getByLabel('Title').fill('From the header');
	await dialog.getByRole('button', { name: 'Create merge request' }).click();
	await expect(dialog).toBeHidden();
	expect(forge.pull(1)).toMatchObject({ author: 'alice', title: 'From the header' });
	await expect(page.getByRole('button', { name: /^MR !1/ })).toBeVisible();
});

test('a merge request already open for the branch: the dialog says so in GitLab’s words, and links it', async ({ as, startApi, forge }) => {
	const { api } = await startWorld(startApi, forge);
	const alice = as('alice', api);
	await setUp(alice);
	await connect(api, alice, forge, 'alice');
	const t = await alice.create({ repoUrl, prompt: 'e2e: commit' });
	await t.waitFor('its work to be pushed', (i) => i.autoPush === AutoPushState.PUSHED);
	const page = await open(alice, `/t/${t.id}`);
	await page.getByRole('button', { name: 'Create a merge request' }).click();
	const dialog = page.getByRole('dialog');
	await forge.open((await t.get()).branch, 'Someone was first');
	await dialog.getByLabel('Title').fill('Too late');
	await dialog.getByRole('button', { name: 'Create merge request' }).click();
	await expect(dialog.getByText('GitLab: Another open merge request already exists for this source branch: !1', { exact: true })).toBeVisible();
	await dialog.getByRole('button', { name: 'Link the open one for this branch' }).click();
	await expect(page.getByRole('button', { name: /^MR !1/ })).toBeVisible();
});

test('the repository chip lists the projects of a connected GitLab account and searches GitLab', async ({ as, startApi, forge }) => {
	const { api } = await startWorld(startApi, forge);
	const alice = as('alice', api);
	await setUp(alice);
	await connect(api, alice, forge, 'alice');
	forge.gitlabProjects = ['e2e/plain', 'e2e/archive-2019'];
	const page = await open(alice, '/');
	await page.getByRole('button', { name: /Choose repository|main$/ }).first().click();
	await expect(page.getByRole('menuitemradio', { name: 'e2e/plain' })).toBeVisible();
	await page.getByLabel('Repository URL').fill('archive');
	await expect(page.getByRole('menuitemradio', { name: 'e2e/archive-2019' })).toBeVisible();
	await expect(page.getByRole('menuitemradio', { name: 'e2e/plain' })).toHaveCount(0);
	await expect.poll(() => forge.calls.includes('GET /api/v4/projects')).toBe(true);
});

test('no application, then not connected: each says what to do, with the button for it', async ({ as, startApi, forge }) => {
	const { api } = await startWorld(startApi, forge);
	const [alice, bob] = [as('alice', api), as('bob', api)];

	// No application on gitlab.com: the push line says to set one up.
	const t = await bob.create({ repoUrl, prompt: 'e2e: commit' });
	await t.waitFor('unpushable', (i) => i.autoPush === AutoPushState.UNAVAILABLE);
	expect((await t.of('autoPush')).at(-1)?.detail).toContain('set up GitLab for gitlab.com in Settings');
	let page = await open(bob, `/t/${t.id}`);
	await expect(page.getByRole('link', { name: 'Set up GitLab' })).toHaveAttribute('href', '/settings#gitlab-app');
	if (process.env.E2E_SCREENSHOTS) await shootThread(page, 'gitlab-thread-no-app');

	// Set up, but bob hasn't connected: his thread can't push, his merge requests can't open, his projects aren't listed.
	await setUp(alice);
	const u = await bob.create({ repoUrl, prompt: 'e2e: commit' });
	await u.waitFor('unpushable', (i) => i.autoPush === AutoPushState.UNAVAILABLE);
	expect((await u.of('autoPush')).at(-1)?.detail).toContain('connect GitLab in Settings, so Banda can push as you');
	expect(await refused(bob.threads.pullRequestCreate({ threadId: u.id, title: 'x' }))).toContain('Connect GitLab in Settings to do this on gitlab.com');
	expect(await credentialFill(u.pod)).toContain('Banda: connect GitLab in Settings');
	page = await open(bob, `/t/${u.id}`);
	await expect(page.getByRole('link', { name: 'Connect GitLab' })).toHaveAttribute('href', `/gitlab/connect?host=gitlab.com&back=${encodeURIComponent(`/t/${u.id}`)}`);
	if (process.env.E2E_SCREENSHOTS) await shootThread(page, 'gitlab-thread-not-connected');
	await page.goto('/');
	await page.getByRole('button', { name: 'e2e/plain main' }).click();
	await page.getByRole('menuitem', { name: 'Connect GitLab to list your repositories on gitlab.com' }).click();
	await expect(page).toHaveURL(/\/settings#gitlab$/);
	await expect(section(page, 'GitLab').getByRole('link', { name: 'Connect GitLab' })).toHaveAttribute('href', '/gitlab/connect?host=gitlab.com');

	// Connected from the thread's button: back on the thread, which says so, and the next push goes through.
	forge.signedIn = 'bob';
	const landed = await follow(api, bob, `${api.url}/gitlab/connect?host=gitlab.com&back=${encodeURIComponent(`/t/${u.id}`)}`);
	expect(landed.pathname + landed.search).toBe(`/t/${u.id}`);
	page = await open(bob, `/t/${u.id}`);
	await expect(page.getByText('Connected as @bob')).toBeVisible();
	await u.turn(() => u.send('e2e: commit'));
	await u.waitFor('its work to be pushed', (i) => i.autoPush === AutoPushState.PUSHED);
});

test('refresh tokens rotate per grant: one refresh each, the new pairs saved; a revoked application shows as “Reconnect GitLab”', async ({ as, startApi, forge }) => {
	const { api } = await startWorld(startApi, forge);
	const alice = as('alice', api);
	await setUp(alice);
	forge.gitlabTokenTTL = 60; // within the 5 minutes before expiry: the first use of each refreshes
	await connect(api, alice, forge, 'alice');
	const [apiFirst, gitFirst] = [forge.grant('alice', 'api')!.refresh, forge.grant('alice', 'write_repository')!.refresh];
	await Promise.all([alice.threads.listForgeRepos({}), alice.threads.listForgeRepos({})]);
	expect(forge.gitlabRefreshed).toEqual([apiFirst]); // once, though two calls needed it; git's grant untouched
	const t = await alice.create({ repoUrl, prompt: 'e2e: commit' });
	await t.waitFor('its work to be pushed', (i) => i.autoPush === AutoPushState.PUSHED);
	await Promise.all([credentialFill(t.pod), credentialFill(t.pod)]);
	expect(forge.gitlabRefreshed).toEqual([apiFirst, gitFirst]); // git's once too, by whichever asked first
	const [apiGrant, gitGrant] = [forge.grant('alice', 'api')!, forge.grant('alice', 'write_repository')!];
	expect(apiGrant.refresh).not.toBe(apiFirst);
	expect(gitGrant.refresh).not.toBe(gitFirst);
	expect(await stored(alice)).toMatchObject({ 'access-token': apiGrant.access, 'refresh-token': apiGrant.refresh,
		'git-access-token': gitGrant.access, 'git-refresh-token': gitGrant.refresh });
	expect(await credentialFill(t.pod)).toContain(`password=${gitGrant.access}`);

	// alice revokes Banda in her GitLab settings: the next call is refused, and Settings says so honestly.
	forge.gitlabRevokeAll('alice');
	await alice.threads.listForgeRepos({});
	await until(async () => (await alice.settings.getGitLab({})).hosts[0].connection?.broken, () => 'the connection to show as broken');
	expect(await stored(alice)).toEqual({});
	expect(await credentialFill(t.pod)).toContain('Banda: reconnect GitLab in Settings');
	const page = await open(alice, '/settings#gitlab');
	await expect(section(page, 'GitLab')).toContainText('GitLab no longer accepts your connection as @alice');
	await expect(section(page, 'GitLab').getByRole('link', { name: 'Reconnect GitLab' })).toBeVisible();
	if (process.env.E2E_SCREENSHOTS) await shoot(page, 'gitlab-broken', '#gitlab');
});

test('an access token GitLab calls expired before Banda thought it would is refreshed, not taken for a revocation', async ({ as, startApi, forge }) => {
	const { api } = await startWorld(startApi, forge);
	const alice = as('alice', api);
	await setUp(alice);
	await connect(api, alice, forge, 'alice');
	const t = await alice.create({ repoUrl, prompt: 'e2e: commit' });
	await t.waitFor('its work to be pushed', (i) => i.autoPush === AutoPushState.PUSHED);
	const [apiFirst, gitFirst] = [forge.grant('alice', 'api')!.refresh, forge.grant('alice', 'write_repository')!.refresh];

	// The api grant, as a call hears of it: the one call fails, the next one works, and nothing was revoked.
	forge.gitlabExpire('alice', 'api');
	await alice.threads.listForgeRepos({});
	await until(async () => forge.gitlabRefreshed.includes(apiFirst), () => 'the api grant to be refreshed');
	expect(forge.grants.every((g) => !g.revoked)).toBe(true);
	expect((await alice.settings.getGitLab({})).hosts[0].connection?.broken).toBe(false);
	await until(async () => (await stored(alice))['refresh-token'] === forge.grant('alice', 'api')!.refresh, () => 'the new pair to be saved');
	expect(forge.grant('alice', 'api')!.refresh).not.toBe(apiFirst);

	// The git grant, which nothing but handing it out tests.
	forge.gitlabExpire('alice', 'write_repository');
	expect(await credentialFill(t.pod)).toContain(`password=${forge.grant('alice', 'write_repository')!.access}`);
	expect(forge.gitlabRefreshed).toEqual([apiFirst, gitFirst]);
});

test('revoked on GitLab with nothing polling: a thread’s next push says to reconnect, with the button, not git’s “Access denied”', async ({ as, startApi, forge }) => {
	const { api } = await startWorld(startApi, forge);
	const alice = as('alice', api);
	await setUp(alice);
	await connect(api, alice, forge, 'alice');
	const t = await alice.create({ repoUrl, prompt: 'e2e: commit' });
	await t.waitFor('its work to be pushed', (i) => i.autoPush === AutoPushState.PUSHED);

	forge.gitlabRevokeAll('alice');
	expect(await credentialFill(t.pod)).toContain('Banda: reconnect GitLab in Settings');
	await t.turn(() => t.send('e2e: commit'));
	await t.waitFor('unpushable', (i) => i.autoPush === AutoPushState.UNAVAILABLE);
	expect((await t.of('autoPush')).at(-1)?.detail).toContain('reconnect GitLab');
	const page = await open(alice, `/t/${t.id}`);
	await expect(page.getByRole('link', { name: 'Reconnect GitLab' }).first()).toBeVisible();
	await expect(page.getByText('Access denied')).toHaveCount(0);
});

test('disconnect revokes both grants on GitLab and forgets the tokens', async ({ as, startApi, forge }) => {
	const { api } = await startWorld(startApi, forge);
	const alice = as('alice', api);
	await setUp(alice);
	await connect(api, alice, forge, 'alice');
	const page = await open(alice, '/settings#gitlab');
	const gitlab = section(page, 'GitLab');
	await expect(gitlab).toContainText('@alice');
	page.once('dialog', (d) => d.accept());
	await gitlab.getByRole('button', { name: 'Disconnect' }).click();
	await expect(gitlab.getByRole('link', { name: 'Connect GitLab' })).toBeVisible();
	await expect(gitlab).toContainText('Not connected');
	expect(forge.grants.map((g) => [g.scope, !!g.revoked])).toEqual([['api', true], ['write_repository', true]]);
	expect(await stored(alice)).toEqual({});
});

test('with the application set up, a GITLAB_TOKEN or GIT_TOKEN secret still works for whoever hasn’t connected, and Settings calls it deprecated', async ({ as, startApi, forge }) => {
	const { api } = await startWorld(startApi, forge);
	const carol = as('carol', api);
	await setUp(carol);
	forge.tokens = { 'carol-token': 'carol' };
	await carol.settings.setSecret({ name: 'GIT_TOKEN', value: 'carol-token' });

	const t = await carol.create({ repoUrl, prompt: 'e2e: commit' });
	await t.waitFor('its work to be pushed', (i) => i.autoPush === AutoPushState.PUSHED);
	await carol.threads.pullRequestCreate({ threadId: t.id, title: 'Legacy', body: '' });
	expect(forge.pull(1).author).toBe('carol');
	// git asks Banda first, which says why not, then GIT_TOKEN's helper answers.
	const out = await credentialFill(t.pod);
	expect(out).toContain('Banda: connect GitLab in Settings');
	expect(out).toContain('password=carol-token');

	const page = await open(carol, '/settings#gitlab');
	await expect(section(page, 'GitLab')).toContainText('Until you connect, Banda uses your GITLAB_TOKEN or GIT_TOKEN secret');
});

test('replacing the application is asked for, ends every connection through the old one, revoked with its credentials, and everyone sees who did it', async ({ as, startApi, forge }) => {
	const { api, secret } = await startWorld(startApi, forge);
	const [alice, bob, carol] = [as('alice', api), as('bob', api), as('carol', api)];
	await setUp(alice);
	await connect(api, bob, forge, 'bob');
	const old = forge.gitlabApp!;
	const next = { ...old, clientId: 'b4nd4e2e-second', clientSecret: `gloas-${randomBytes(16).toString('hex')}` };
	forge.gitlabApp = next; // made on GitLab by carol

	// Not without asking for it.
	expect(await refused(carol.settings.setGitLabApp({ host: 'gitlab.com', applicationId: next.clientId, secret: next.clientSecret }))).toContain(
		'the team already has a GitLab application for gitlab.com: replacing it ends everyone\'s connection, so it has to be asked for'
	);
	expect(forge.grants.every((g) => !g.revoked)).toBe(true);

	// Settings says what replacing does before the form.
	const page = await open(carol, '/settings#gitlab-app');
	const app = section(page, 'GitLab application');
	await expect(app).toContainText(`Set up by ${alice.id}`);
	await app.getByRole('button', { name: 'Replace the application on gitlab.com' }).click();
	await expect(app.getByRole('group', { name: 'Replace the application' })).toContainText('ends everyone’s GitLab connection on gitlab.com');
	await app.getByLabel('Application ID').fill(next.clientId);
	await app.getByLabel('Secret').fill(next.clientSecret);
	if (process.env.E2E_SCREENSHOTS) await shoot(page, 'gitlab-app-replace', '#gitlab-app li:nth-child(2)', false);
	await app.getByRole('button', { name: 'Replace and check' }).click();
	await expect(app).toContainText(`Replaced by ${carol.id} · just now, was ${old.clientId.slice(0, 12)}…`);
	if (process.env.E2E_SCREENSHOTS) await shoot(page, 'gitlab-app-replaced');

	// bob's grants were revoked with the old application's credentials, and his tokens and connection are gone.
	expect(forge.grants.map((g) => [g.login, g.scope, g.clientId, !!g.revoked])).toEqual([
		['bob', 'api', old.clientId, true],
		['bob', 'write_repository', old.clientId, true]
	]);
	expect(await stored(bob)).toEqual({});
	const host = (await bob.settings.getGitLab({})).hosts[0];
	expect(host.connection).toBeUndefined();
	expect(host.lastChange).toMatchObject({ action: 'replaced', by: carol.id, applicationId: next.clientId, wasApplicationId: old.clientId });
	expect(await sql(`select action || ' ' || by_user || ' ' || application_id || ' ' || was_application_id from gitlab_app_changes order by id`, api.db)).toBe(
		`created ${alice.id} ${old.clientId} \nreplaced ${carol.id} ${next.clientId} ${old.clientId}`
	);
	const bobPage = await open(bob, '/settings#gitlab');
	const ended = `${carol.id} replaced the team’s GitLab application for gitlab.com just now, which ended every connection to the old one. Connect again to keep working as you.`;
	await expect(section(bobPage, 'GitLab')).toContainText(ended);
	if (process.env.E2E_SCREENSHOTS) await shoot(bobPage, 'gitlab-replaced-reconnect', '#gitlab');
	await connect(api, bob, forge, 'bob');
	await bobPage.reload();
	await expect(section(bobPage, 'GitLab')).toContainText('@bob');
	await expect(section(bobPage, 'GitLab')).not.toContainText('replaced the team’s GitLab application');

	// Removing asks first, ends the connections through it and is recorded too.
	page.once('dialog', (d) => {
		expect(d.message()).toContain('Settings shows everyone that you removed it');
		d.accept();
	});
	await page.reload();
	await app.getByRole('button', { name: 'Remove the application on gitlab.com from Banda' }).click();
	await expect(app).toContainText(`${carol.id} removed the application on gitlab.com · just now, was ${next.clientId.slice(0, 12)}…`);
	expect(await kube('get', 'secret', secret, '--ignore-not-found')).toBe('');
	expect(forge.grants.filter((g) => g.clientId === next.clientId).map((g) => [g.login, !!g.revoked])).toEqual([['bob', true], ['bob', true]]);
	await bobPage.reload();
	await expect(section(bobPage, 'GitLab')).toContainText(`${carol.id} removed the team’s GitLab application for gitlab.com just now, which ended every connection to it.`);
});

test('an application made by hand can’t be replaced from Settings, and another ID in its Secret ends the connections through the old one', async ({ as, startApi, forge }) => {
	test.setTimeout(240_000); // Banda reads a Secret made by hand every 30 s
	const { api, secret } = await startWorld(startApi, forge);
	const alice = as('alice', api);
	await kube('create', 'secret', 'generic', secret, `--from-literal=application-id=${appId}`, `--from-literal=secret=${appSecret}`);
	await until(async () => (await alice.settings.getGitLab({})).hosts[0].app?.applicationId === appId, () => 'the application made by hand to show', 45_000);
	expect(await refused(alice.settings.setGitLabApp({ host: 'gitlab.com', applicationId: appId, secret: appSecret, replace: true }))).toContain(
		'the GitLab application for gitlab.com comes from the deployment'
	);
	const page = await open(alice, '/settings#gitlab-app');
	const app = section(page, 'GitLab application');
	await expect(app).toContainText('Set up by the deployment');
	await expect(app.getByRole('button', { name: 'Replace the application on gitlab.com' })).toHaveCount(0);

	await connect(api, alice, forge, 'alice');
	const old = forge.gitlabApp!;
	const next = { ...old, clientId: 'b4nd4e2e-by-hand', clientSecret: `gloas-${randomBytes(16).toString('hex')}` };
	forge.gitlabApp = next;
	await kube('patch', 'secret', secret, '--type', 'merge', '-p', JSON.stringify({ stringData: { 'application-id': next.clientId, secret: next.clientSecret } }));
	await until(
		async () => (await alice.settings.getGitLab({})).hosts[0].app?.applicationId === next.clientId && forge.grants.every((g) => g.revoked),
		() => `the old application's connections to end: ${JSON.stringify(forge.grants)}`,
		45_000
	);
	expect(forge.grants.map((g) => g.clientId)).toEqual([old.clientId, old.clientId]);
	expect((await alice.settings.getGitLab({})).hosts[0].connection).toBeUndefined();
	expect(await stored(alice)).toEqual({});
});

test('a refresh refused for the application’s credentials, a rate limit or an outage keeps the connection; Settings says the application needs attention', async ({ as, startApi, forge }) => {
	const { api } = await startWorld(startApi, forge);
	const alice = as('alice', api);
	await setUp(alice);
	forge.gitlabTokenTTL = 60; // the first use of each grant refreshes
	await connect(api, alice, forge, 'alice');
	const kept = await stored(alice);

	// Someone renewed the secret on GitLab: alice revoked nothing, so her tokens stay.
	forge.gitlabRefreshFails = 'invalid_client';
	const t = await alice.create({ repoUrl, prompt: 'e2e: commit' });
	await t.waitFor('unpushable', (i) => i.autoPush === AutoPushState.UNAVAILABLE);
	expect((await t.of('autoPush')).at(-1)?.detail).toContain('the team\'s GitLab application for gitlab.com needs attention');
	expect(await credentialFill(t.pod)).toContain('needs attention, so your connection can\'t be renewed: see GitLab in Settings');
	let host = (await alice.settings.getGitLab({})).hosts[0];
	expect(host.app?.needsAttention).toBe(true);
	expect(host.connection?.broken).toBe(false);
	expect(await stored(alice)).toEqual(kept);
	const page = await open(alice, '/settings#gitlab');
	await expect(section(page, 'GitLab')).toContainText('The team’s GitLab application for gitlab.com needs attention: GitLab refused its ID or secret');
	await expect(section(page, 'GitLab application')).toContainText('GitLab refused this application’s ID or secret when renewing a connection');
	if (process.env.E2E_SCREENSHOTS) {
		await shoot(page, 'gitlab-needs-attention', '#gitlab');
		await shoot(page, 'gitlab-app-needs-attention');
	}

	for (const fails of [429, 503, 'invalid_request'] as const) {
		forge.gitlabRefreshFails = fails;
		expect(await credentialFill(t.pod)).toContain('GitLab couldn\'t renew your connection just now');
		expect((await alice.settings.getGitLab({})).hosts[0].connection?.broken).toBe(false);
		expect(await stored(alice)).toEqual(kept);
	}

	// Fixed: the kept refresh token works, and the warning goes.
	forge.gitlabRefreshFails = undefined;
	expect(await credentialFill(t.pod)).toContain(`password=${forge.grant('alice', 'write_repository')!.access}`);
	host = (await alice.settings.getGitLab({})).hosts[0];
	expect(host.app?.needsAttention).toBe(false);
});

test('both grants must come from one GitLab account: otherwise both are revoked, and Settings says which account to sign in as', async ({ as, startApi, forge }) => {
	const { api } = await startWorld(startApi, forge);
	const alice = as('alice', api);
	await setUp(alice);
	forge.gitlabSignedInFor = { write_repository: 'mallory' }; // the browser's GitLab session changed between the two
	await connect(api, alice, forge, 'alice', 'account');
	expect(forge.grants.map((g) => [g.login, g.scope, !!g.revoked])).toEqual([
		['alice', 'api', true],
		['mallory', 'write_repository', true]
	]);
	expect(await stored(alice)).toEqual({});
	expect((await alice.settings.getGitLab({})).hosts[0].connection).toBeUndefined();
	const page = await open(alice, '/settings?gitlab=account&login=alice#gitlab');
	await expect(section(page, 'Accounts').getByRole('status')).toContainText('Both approvals have to come from one GitLab account');
	await expect(section(page, 'Accounts').getByRole('status')).toContainText('sign in to GitLab as @alice');
	if (process.env.E2E_SCREENSHOTS) await shoot(page, 'gitlab-other-account', '#gitlab', false);
});

test('connecting starts only from Banda’s own pages: another site can’t make a signed-in teammate reconnect', { tag: '@key' }, async ({ as, startApi, forge }) => {
	const { api } = await startWorld(startApi, forge);
	const alice = as('alice', api);
	await setUp(alice);
	for (const [path, param] of [['/gitlab/connect?host=gitlab.com', 'gitlab'], ['/github/connect', 'github']]) {
		for (const site of ['cross-site', 'same-site']) {
			const r = await fetch(`${api.url}${path}`, { redirect: 'manual', headers: { 'X-Banda-User': alice.id, 'Sec-Fetch-Site': site } });
			expect(new URL(r.headers.get('location')!, api.url).searchParams.get(param)).toBe('cross-site');
		}
	}
	expect(forge.gitlabAuthorized).toEqual([]);
	for (const site of ['same-origin', 'none']) {
		const r = await fetch(`${api.url}/gitlab/connect?host=gitlab.com`, { redirect: 'manual', headers: { 'X-Banda-User': alice.id, 'Sec-Fetch-Site': site } });
		expect(r.headers.get('location')).toContain(`${forge.url}/oauth/authorize`);
	}
	// From Settings itself (same-origin) it goes through, as in the first test; coming back refused says why.
	const page = await open(alice, '/settings?gitlab=cross-site#gitlab');
	await expect(section(page, 'Accounts').getByRole('status')).toContainText('Start connecting from Settings');
});

test('the git token is redacted from what the pod sends: terminal output, the agent’s text, and so the database', { tag: '@key' }, async ({ as, startApi, forge }) => {
	const { api } = await startWorld(startApi, forge);
	const alice = as('alice', api);
	await setUp(alice);
	await connect(api, alice, forge, 'alice');
	const t = await alice.create({ repoUrl, prompt: 'e2e: commit' });
	await t.waitFor('its work to be pushed', (i) => i.autoPush === AutoPushState.PUSHED);
	const token = forge.grant('alice', 'write_repository')!.access; // what GitCredential handed the pod; no shape the runner knows
	const out = await t.terminal(`printf 'protocol=https\\nhost=gitlab.com\\npath=e2e/plain.git\\n\\n' | git credential fill`);
	expect(out).toContain('password=[redacted]');
	expect(out).not.toContain(token);
	await t.turn(() => t.send(`e2e-echo e2e-noop the token is ${token}`));
	const said = (await t.of('agentText')).map((x) => x.text).join('\n');
	expect(said).toContain('the token is [redacted]');
	expect(said).not.toContain(token);
	expect(await sql(`select count(*) from events where thread_id = '${t.id}' and body->>'actor' = 'agent' and body::text like '%${token}%'`, api.db)).toBe('0');
});

/**
 * Settings in light and dark, for docs and review (E2E_SCREENSHOTS=1), as the theme setting picks them; without
 * reload, by the theme's class only, so what is on screen (an error) stays.
 */
async function shoot(page: Page, name: string, at = '#gitlab-app', reload = true) {
	for (const mode of ['light', 'dark'] as const) {
		await page.evaluate((m) => localStorage.setItem('mode-watcher-mode', m), mode);
		if (reload) await page.reload();
		else {
			await page.addStyleTag({ content: '*, *::before, *::after { transition: none !important; }' });
			await page.evaluate((m) => {
				document.documentElement.classList.toggle('dark', m === 'dark');
				document.documentElement.style.colorScheme = m;
			}, mode);
		}
		await page.getByRole('region', { name: 'GitLab application', exact: true }).getByRole('button').first().waitFor();
		await page.locator(at).evaluate((e) => e.scrollIntoView());
		await page.screenshot({ path: `../docs/screenshots/${name}-${mode}.png` });
	}
}

async function shootThread(page: Page, name: string) {
	for (const mode of ['light', 'dark'] as const) {
		await page.evaluate((m) => localStorage.setItem('mode-watcher-mode', m), mode);
		await page.reload();
		await page.getByRole('link', { name: /Set up GitLab|Connect GitLab/ }).first().waitFor();
		await page.screenshot({ path: `../docs/screenshots/${name}-${mode}.png` });
	}
}
