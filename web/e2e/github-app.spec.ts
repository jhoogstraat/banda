// The team's GitHub App (internal/api/github.go): set up from Settings through the manifest flow, users connect
// through its OAuth flow, git in sandboxes gets installation tokens for the thread's repository only. Each test
// runs an API of its own against the fake forge (./forge.ts), with an app Secret of its own: the run's other APIs
// share the namespace and must not see it.
import type { Page } from '@playwright/test';
import { randomBytes } from 'node:crypto';
import { test as base, expect, kube, kexec, refused, until, userHash, type Api, type User } from './fixtures';
import { FakeForge } from './forge';
import { createClient } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';
import { AutoPushState, RunnerService } from '../src/lib/gen/banda/v1/banda_pb.js';

const test = base.extend<{ forge: FakeForge }>({
	forge: async ({}, use) => {
		const forge = await new FakeForge().start();
		await use(forge);
		await forge.stop();
	}
});

const repoUrl = 'https://github.com/e2e/plain.git'; // the run's git server, for git (deploy/sandbox.Dockerfile)

async function startWorld(startApi: (env?: Record<string, string>) => Promise<Api>, forge: FakeForge) {
	const secret = `banda-github-app-${randomBytes(3).toString('hex')}`;
	const api = await startApi({ TEST_FORGE_URL: forge.url, TEST_GITHUB_APP_SECRET: secret, PULL_REQUEST_POLL: '1s' });
	return { api, secret };
}

/** Follows a browser flow's redirects as user, with their header only on the API: returns where it lands in Banda (Settings or a thread). */
async function follow(api: Api, user: User, url: string, init: RequestInit = {}) {
	for (let i = 0; i < 10; i++) {
		const own = url.startsWith(api.url);
		const r = await fetch(url, { ...init, redirect: 'manual', headers: { ...(own && { 'X-Banda-User': user.id }), ...(init.headers ?? {}) } });
		init = {};
		const to = r.headers.get('location');
		if (!to) throw new Error(`${url} answered ${r.status} without a redirect: ${await r.text()}`);
		url = new URL(to, url).href;
		if (url.startsWith(api.url) && /^\/(settings$|t\/)/.test(new URL(url).pathname)) return new URL(url);
	}
	throw new Error('too many redirects');
}

/** Creates the team's app as user, as the Settings button does, without a browser. */
async function createApp(api: Api, user: User) {
	const m = await user.settings.gitHubAppManifest({});
	const landed = await follow(api, user, m.url, { method: 'POST', body: new URLSearchParams({ manifest: m.manifest }) });
	expect(landed.searchParams.get('github-app')).toBe('created');
}

/** Connects user's GitHub account (forge login) through the app's OAuth flow. */
async function connect(api: Api, user: User, forge: FakeForge, login: string) {
	forge.signedIn = login;
	const landed = await follow(api, user, `${api.url}/github/connect`);
	expect(landed.searchParams.get('github')).toBe('connected');
}

async function open(user: User, path: string) {
	const page = await user.page();
	await page.goto(path);
	return page;
}

const section = (page: Page, name: string) => page.getByRole('region', { name, exact: true });

/** What a forge Secret of the user holds (internal/sandbox ForgeSecret), decoded; empty if it is gone. */
async function stored(user: User) {
	const out = await kube('get', 'secret', `banda-forge-${userHash(`${user.id} github.com`)}`, '--ignore-not-found', '-o', 'jsonpath={.data}');
	return Object.fromEntries(Object.entries(out ? (JSON.parse(out) as Record<string, string>) : {}).map(([k, v]) => [k, Buffer.from(v, 'base64').toString()]));
}

test('team setup from Settings: create the app on GitHub, install it, and every step says where it stands', async ({ as, startApi, forge }) => {
	const { api, secret } = await startWorld(startApi, forge);
	const alice = as('alice', api);
	const page = await open(alice, '/settings#github-app');
	const app = section(page, 'GitHub App');
	await expect(app.getByRole('listitem').filter({ hasText: 'Create the app on GitHub' })).toHaveAttribute('aria-current', 'step');
	await expect(section(page, 'GitHub')).toContainText('Nothing to connect yet: the team’s GitHub App isn’t set up.');
	await expect(section(page, 'GitLab')).toHaveCount(0); // a forge the team doesn't use isn't offered
	if (process.env.E2E_SCREENSHOTS) await shoot(page, 'github-app-create');

	// An organization by default; a personal app says what its owner gets.
	await expect(app.getByRole('radio', { name: 'An organization' })).toBeChecked();
	await app.getByRole('radio', { name: 'My GitHub account' }).check();
	await expect(app).toContainText('Owned by your account: you’ll be able to reach every repository it’s installed on, whatever your own access.');
	if (process.env.E2E_SCREENSHOTS) await shootHere(page, 'github-app-create-personal');
	await app.getByRole('radio', { name: 'An organization' }).check();
	await expect(app).not.toContainText('Owned by your account');
	await app.getByRole('textbox', { name: 'Organization' }).fill('e2e');

	forge.signedIn = 'alice';
	await app.getByRole('button', { name: 'Create on GitHub' }).click();
	await expect(page).toHaveURL(/\/settings#github-app$/); // back from GitHub, the outcome said and dropped
	await expect(app).toContainText('banda-e2e on github.com, owned by e2e');
	await expect(app).not.toContainText('personal account');
	expect(forge.app).toMatchObject({ owner: 'e2e', ownerType: 'Organization' });
	await expect(app).toContainText(`Set up by ${alice.id}`);
	await expect(app).toContainText('Not installed yet');
	expect(forge.app?.manifest).toMatchObject({ url: api.url, public: false, redirect_url: `${api.url}/github/app`, callback_urls: [`${api.url}/github/callback`],
		hook_attributes: { active: false }, default_events: [],
		default_permissions: { contents: 'write', metadata: 'read', pull_requests: 'write', checks: 'read', statuses: 'read', workflows: 'write' } });
	// The keys are in the app's Secret only; Postgres knows who and when.
	expect(Object.keys(JSON.parse(await kube('get', 'secret', secret, '-o', 'jsonpath={.data}'))).sort()).toEqual(['app-id', 'client-id', 'client-secret', 'host', 'private-key', 'slug']);
	expect(await app.textContent()).not.toContain('PRIVATE KEY');

	await app.getByRole('link', { name: 'Install on GitHub' }).click();
	await expect(app.getByRole('list', { name: 'Installed on' })).toContainText(/e2e\s*2 repositories/);
	await expect(app.getByRole('link', { name: 'Manage on GitHub: e2e' })).toHaveAttribute('href', `${forge.url}/organizations/e2e/settings/installations/1`);
	await expect(app).toContainText('Everyone connects their own GitHub account once, under Accounts. You haven’t yet.');
	await expect(page.getByRole('navigation', { name: 'Settings sections' }).getByRole('link', { name: 'Accounts (needs you)' })).toBeVisible();
	if (process.env.E2E_SCREENSHOTS) await shoot(page, 'github-app-installed');

	// Connecting from the personal section: back as @alice in both.
	await section(page, 'GitHub').getByRole('link', { name: 'Connect GitHub' }).click();
	await expect(section(page, 'GitHub')).toContainText('@alice');
	await expect(section(page, 'GitHub')).toContainText('Connected as @alice');
	await expect(app).not.toContainText('You haven’t yet');
	await expect(page.getByRole('navigation', { name: 'Settings sections' }).getByRole('link', { name: 'Accounts', exact: true })).toBeVisible();
	if (process.env.E2E_SCREENSHOTS) await shoot(page, 'github-connected', '#github');
});

test('connected, a pull request goes out as the user, and git in the pod gets a token for the thread’s repository only', { tag: '@key' }, async ({ as, startApi, forge }) => {
	const { api } = await startWorld(startApi, forge);
	const alice = as('alice', api);
	await createApp(api, alice);
	forge.installedOn.push('e2e');
	await connect(api, alice, forge, 'alice');

	const t = await alice.create({ repoUrl, prompt: 'e2e: commit' });
	await t.waitFor('its work to be pushed', (i) => i.autoPush === AutoPushState.PUSHED); // https pushes with the app's token now
	await alice.threads.pullRequestCreate({ threadId: t.id, title: 'From the app', body: '' });
	expect(forge.pull(1)).toMatchObject({ author: 'alice', title: 'From the app' });

	// The helper asks the API, which answers for the thread's repository whatever the pod names.
	const out = await kexec(t.pod, `printf 'protocol=https\\nhost=github.com\\npath=someone/else.git\\n\\n' | git credential fill`);
	const token = forge.minted.at(-1)!;
	expect(out).toContain(`username=x-access-token\npassword=${token.token}\npassword_expiry_utc=`);
	expect(token).toMatchObject({ repositories: ['plain'], permissions: { contents: 'write', metadata: 'read', workflows: 'write' } });
	// Neither the user's token nor the app's key is in the pod's environment.
	const env = await kexec(t.pod, 'env');
	expect(env).not.toContain(forge.userToken('alice')!);
	expect(env).not.toMatch(/PRIVATE KEY|ghu_|ghr_/);

	// Only pull access: a read-only token, no workflows.
	forge.access.alice = 'pull';
	await alice.settings.disconnectGitHub({});
	await connect(api, alice, forge, 'alice'); // a new connection asks GitHub afresh
	await kexec(t.pod, `printf 'protocol=https\\nhost=github.com\\n\\n' | git credential fill`);
	expect(forge.minted.at(-1)).toMatchObject({ repositories: ['plain'], permissions: { contents: 'read', metadata: 'read' } });
	expect(forge.minted.at(-1)!.permissions.workflows).toBeUndefined();

	// A runner token taken from the pod gets nothing once the pod is gone.
	const stolen = (await kexec(t.pod, 'printf %s "$BANDA_RUNNER_TOKEN"')).trim();
	await t.snooze();
	await t.podGone();
	const minted = forge.minted.length;
	const runner = createClient(RunnerService, createConnectTransport({ baseUrl: api.url }));
	expect(await refused(runner.gitCredential({ threadId: t.id, token: stolen }))).toContain('the environment is not running');
	expect(forge.minted).toHaveLength(minted);
});

test('not connected, and the app not installed: each says what to do, with the button for it', async ({ as, startApi, forge }) => {
	const { api } = await startWorld(startApi, forge);
	const [alice, bob] = [as('alice', api), as('bob', api)];
	await createApp(api, alice);
	forge.tokens = { 'alice-token': 'alice' };

	// bob hasn't connected: his thread can't push, his pull requests can't open, his repositories aren't listed.
	const t = await bob.create({ repoUrl, prompt: 'e2e: commit' });
	await t.waitFor('unpushable', (i) => i.autoPush === AutoPushState.UNAVAILABLE);
	expect(await refused(bob.threads.pullRequestCreate({ threadId: t.id, title: 'x' }))).toContain('Connect GitHub in Settings to do this on github.com');
	let page = await open(bob, `/t/${t.id}`);
	const connectButton = page.getByRole('link', { name: 'Connect GitHub' });
	// Straight to GitHub, and back to the thread once connected.
	await expect(connectButton).toHaveAttribute('href', `/github/connect?back=${encodeURIComponent(`/t/${t.id}`)}`);
	await expect(page.getByText('connect GitHub, so Banda can push as you')).toBeVisible(); // the button says what
	await page.goto('/');
	await page.getByRole('button', { name: 'e2e/plain main' }).click(); // the composer starts on bob's project
	await page.getByRole('menuitem', { name: 'Connect GitHub to list your repositories on github.com' }).click();
	await expect(page).toHaveURL(/\/settings#github$/);
	await expect(section(page, 'GitHub').getByRole('link', { name: 'Connect GitHub' })).toBeVisible();
	forge.signedIn = 'bob';
	// Back only to a thread's page: anywhere else is Settings.
	expect((await follow(api, bob, `${api.url}/github/connect?back=${encodeURIComponent('//evil.example/t/x')}`)).searchParams.get('github')).toBe('connected');
	const landed = await follow(api, bob, `${api.url}/github/connect?back=${encodeURIComponent(`/t/${t.id}`)}`);
	expect(landed.pathname + landed.search).toBe(`/t/${t.id}`);
	page = await open(bob, `/t/${t.id}`);
	await expect(page.getByText('Connected as @bob')).toBeVisible();
	await expect(page.getByRole('link', { name: 'Connect GitHub' })).toHaveCount(0);

	// alice has connected, but the app isn't installed on e2e.
	await connect(api, alice, forge, 'alice');
	const u = await alice.create({ repoUrl, prompt: 'e2e: commit' });
	const info = await u.waitFor('unpushable', (i) => i.autoPush === AutoPushState.UNAVAILABLE);
	page = await open(alice, `/t/${u.id}`);
	await expect(page.getByRole('link', { name: 'Install the GitHub App' })).toHaveAttribute('href', `${forge.url}/apps/banda-e2e/installations/new`);
	expect((await u.of('autoPush')).at(-1)?.detail).toContain("the team's GitHub App isn't installed for e2e/plain: install it at");
	expect(info.autoPush).toBe(AutoPushState.UNAVAILABLE);

	// Installed: the next push goes through.
	forge.installedOn.push('e2e');
	await u.turn(() => u.send('e2e: commit'));
	await u.waitFor('its work to be pushed', (i) => i.autoPush === AutoPushState.PUSHED);
});

test('refresh tokens rotate: one refresh, the new pair saved; a token GitHub revokes shows as “Reconnect GitHub”', async ({ as, startApi, forge }) => {
	const { api } = await startWorld(startApi, forge);
	const alice = as('alice', api);
	await createApp(api, alice);
	forge.installedOn.push('e2e');
	forge.userTokenTTL = 60; // within the 5 minutes before expiry: the first use refreshes
	await connect(api, alice, forge, 'alice');
	const first = forge.refreshTokens.alice;
	expect((await stored(alice))['refresh-token']).toBe(first);

	const [a, b] = await Promise.all([alice.threads.listForgeRepos({}), alice.threads.listForgeRepos({})]);
	expect(forge.refreshed).toEqual([first]); // once, though two calls needed it
	expect(forge.refreshTokens.alice).not.toBe(first);
	expect((await stored(alice))['refresh-token']).toBe(forge.refreshTokens.alice);
	expect((await stored(alice))['access-token']).toBe(forge.userToken('alice'));
	for (const r of [a, b]) expect(r.repos.map((x) => x.name)).toEqual(['e2e/plain', 'e2e/profiles']); // through the app's installations
	await alice.threads.listForgeRepos({ query: 'plain' });
	expect(forge.refreshed).toHaveLength(1); // the new token lasts 8 hours

	// GitHub refusing the app's own credentials, or down, isn't alice revoking: her connection is kept.
	for (const [fails, says] of [['incorrect_client_credentials', "GitHub refused the team's GitHub App"], [503, "GitHub couldn't renew your connection just now"]] as const) {
		forge.refreshFails = fails;
		forge.userTokenTTL = 60;
		await connect(api, alice, forge, 'alice'); // a token about to expire again
		const kept = await stored(alice);
		expect(await refused(alice.settings.disconnectGitHub({}))).toContain(says); // it needs her token to revoke it
		expect((await alice.settings.getGitHub({})).connection?.broken).toBe(false);
		expect(await stored(alice)).toEqual(kept);
	}
	forge.refreshFails = undefined;

	// alice revokes Banda on GitHub: the next call is refused, and Settings says so honestly.
	forge.revokeAll('alice');
	await alice.threads.listForgeRepos({});
	await until(async () => (await alice.settings.getGitHub({})).connection?.broken, () => 'the connection to show as broken');
	const page = await open(alice, '/settings#github');
	await expect(section(page, 'GitHub')).toContainText('GitHub no longer accepts your connection as @alice');
	await expect(section(page, 'GitHub').getByRole('link', { name: 'Reconnect GitHub' })).toBeVisible();
	expect(await stored(alice)).toEqual({});
});

test('disconnect revokes the grant on GitHub and forgets the tokens', async ({ as, startApi, forge }) => {
	const { api } = await startWorld(startApi, forge);
	const alice = as('alice', api);
	await createApp(api, alice);
	await connect(api, alice, forge, 'alice');
	const token = forge.userToken('alice')!;
	const page = await open(alice, '/settings#github');
	const github = section(page, 'GitHub');
	await expect(github).toContainText('@alice');
	page.once('dialog', (d) => d.accept());
	await github.getByRole('button', { name: 'Disconnect' }).click();
	await expect(github.getByRole('link', { name: 'Connect GitHub' })).toBeVisible();
	await expect(github).toContainText('Not connected');
	expect(forge.revoked).toEqual([token]);
	expect(forge.userToken('alice')).toBeUndefined();
	expect(await stored(alice)).toEqual({});
});

test('with the app set up, a GIT_TOKEN secret still works for whoever hasn’t connected, and Settings calls it deprecated', async ({ as, startApi, forge }) => {
	const { api } = await startWorld(startApi, forge);
	const carol = as('carol', api);
	await createApp(api, carol);
	forge.installedOn.push('e2e');
	forge.tokens = { 'carol-token': 'carol' };
	await carol.settings.setSecret({ name: 'GIT_TOKEN', value: 'carol-token' });

	const t = await carol.create({ repoUrl, prompt: 'e2e: commit' });
	await t.waitFor('its work to be pushed', (i) => i.autoPush === AutoPushState.PUSHED);
	await carol.threads.pullRequestCreate({ threadId: t.id, title: 'Legacy', body: '' });
	expect(forge.pull(1).author).toBe('carol');
	// git asks the app first, which says why not, then GIT_TOKEN's helper answers.
	const out = await kexec(t.pod, `printf 'protocol=https\\nhost=github.com\\n\\n' | git credential fill 2>&1`);
	expect(out).toContain('Banda: connect GitHub in Settings');
	expect(out).toContain('password=carol-token');

	const page = await open(carol, '/settings#github');
	await expect(section(page, 'GitHub')).toContainText('Until you connect, Banda uses your GIT_TOKEN secret on github.com');
});

test('with an app in place, another needs asking for; replacing it revokes everyone’s connections with the old app and tells them who did it', { tag: '@key' }, async ({ as, startApi, forge }) => {
	const { api, secret } = await startWorld(startApi, forge);
	const [alice, bob] = [as('alice', api), as('bob', api)];
	forge.signedIn = 'alice';
	await createApp(api, alice); // alice's own account
	forge.installedOn.push('e2e');
	await connect(api, alice, forge, 'alice');
	await connect(api, bob, forge, 'bob');
	const tokens = [forge.userToken('alice')!, forge.userToken('bob')!];
	expect(await refused(bob.settings.gitHubAppManifest({ organization: 'e2e' }))).toContain('the team already has the GitHub App banda-e2e');

	const page = await open(bob, '/settings#github-app');
	const app = section(page, 'GitHub App');
	await expect(app).toContainText('Owned by alice’s personal account: they reach every repository it’s installed on, whatever their own access.');
	if (process.env.E2E_SCREENSHOTS) await shoot(page, 'github-app-personal');
	await app.getByRole('button', { name: 'Replace the app…' }).click();
	const confirm = app.getByRole('group', { name: 'Replace the app' });
	await expect(confirm).toContainText('Replacing banda-e2e ends everyone’s GitHub connection.');
	await expect(confirm).toContainText('each teammate connects again, and an owner of e2e installs the new app there');
	await confirm.getByRole('textbox', { name: 'Organization' }).fill('e2e');
	if (process.env.E2E_SCREENSHOTS) await shootHere(page, 'github-app-replace');
	forge.signedIn = 'bob';
	await confirm.getByRole('button', { name: 'Replace on GitHub' }).click();
	await expect(page).toHaveURL(/\/settings#github-app$/);
	await expect(app).toContainText('banda-e2e-2 on github.com, owned by e2e');
	await expect(app).toContainText(`Replaced by ${bob.id} · just now, was banda-e2e owned by alice`);
	if (process.env.E2E_SCREENSHOTS) await shoot(page, 'github-app-replaced');

	// Every grant of the old app was revoked with its own keys, and its tokens and connections are gone.
	expect([...forge.revoked].sort()).toEqual([...tokens].sort());
	expect(forge.userToken('alice')).toBeUndefined();
	for (const u of [alice, bob]) {
		expect(await stored(u)).toEqual({});
		expect((await u.settings.getGitHub({})).connection).toBeUndefined();
	}
	expect(JSON.parse(await kube('get', 'secret', secret, '-o', 'jsonpath={.data}'))['app-id']).toBe(Buffer.from('4243').toString('base64'));
	// alice sees who replaced it, and connects again.
	const alicePage = await open(alice, '/settings#github');
	const github = section(alicePage, 'GitHub');
	await expect(github).toContainText(`${bob.id} replaced the team’s GitHub App just now (it was banda-e2e, owned by alice), which ended every connection to the old one.`);
	await expect(github.getByRole('link', { name: 'Connect GitHub' })).toBeVisible();
	if (process.env.E2E_SCREENSHOTS) await shoot(alicePage, 'github-reconnect-after-replace', '#github');

	// A replace started before the app changed again isn't taken.
	const stale = await alice.settings.gitHubAppManifest({ organization: 'e2e', replace: true });
	alicePage.once('dialog', (d) => d.accept());
	await section(alicePage, 'GitHub App').getByRole('button', { name: 'Remove from Banda' }).click();
	await expect(section(alicePage, 'GitHub App')).toContainText(`Removed by ${alice.id} · just now, was banda-e2e-2 owned by e2e`);
	await expect(github).toContainText(`${alice.id} removed the team’s GitHub App just now (banda-e2e-2)`);
	const landed = await follow(api, alice, stale.url, { method: 'POST', body: new URLSearchParams({ manifest: stale.manifest }) });
	expect(landed.searchParams.get('github-app')).toBe('changed');
	expect(await kube('get', 'secret', secret, '--ignore-not-found', '-o', 'name')).toBe('');
});

test('a personal app transferred to an organization on GitHub no longer warns', async ({ as, startApi, forge }) => {
	const { api } = await startWorld(startApi, forge);
	const alice = as('alice', api);
	forge.signedIn = 'alice';
	await createApp(api, alice);
	const page = await open(alice, '/settings#github-app');
	const app = section(page, 'GitHub App');
	await expect(app).toContainText('Owned by alice’s personal account');
	Object.assign(forge.app!, { owner: 'e2e', ownerType: 'Organization' }); // Transfer ownership, on GitHub
	await alice.settings.listGitHubInstallations({ fresh: true }); // Settings asks GitHub
	await page.reload();
	await expect(app).toContainText('banda-e2e on github.com, owned by e2e');
	await expect(app).not.toContainText('personal account');
});

test('an app GitHub made other than Banda asked for isn’t kept; one made by hand can’t be replaced here, and another in its place ends its connections', async ({ as, startApi, forge }) => {
	test.setTimeout(240_000); // Banda reads a Secret made by hand every 30 s
	const { api, secret } = await startWorld(startApi, forge);
	const alice = as('alice', api);
	const permissions = { contents: 'write', metadata: 'read', pull_requests: 'write', checks: 'read', statuses: 'read', workflows: 'write' };
	const cases: [FakeForge['tamper'], string, string][] = [
		[{ public: true, permissions: { ...permissions, administration: 'write' } }, '', 'other permissions, public'],
		[{ events: ['push'], hookUrl: 'https://evil.example/hook' }, '', 'webhook events, a webhook to https://evil.example/hook'],
		[{ owner: 'mallory', ownerType: 'Organization' }, 'e2e', 'owned by mallory, not the organization e2e']
	];
	for (const [tamper, organization, reason] of cases) {
		forge.tamper = tamper;
		const m = await alice.settings.gitHubAppManifest({ organization });
		const landed = await follow(api, alice, m.url, { method: 'POST', body: new URLSearchParams({ manifest: m.manifest }) });
		expect(landed.searchParams.get('github-app')).toBe('refused');
		expect(await kube('get', 'secret', secret, '--ignore-not-found', '-o', 'name')).toBe('');
		const res = await alice.settings.getGitHub({});
		expect(res.app).toBeUndefined();
		expect(res.lastChange).toBeUndefined();
		expect(res.refusal).toMatchObject({ slug: forge.app!.slug, reason });
	}
	const deleteUrl = `${forge.url}/organizations/mallory/settings/apps/${forge.app!.slug}`;
	expect((await alice.settings.getGitHub({})).refusal?.deleteUrl).toBe(deleteUrl);
	const page = await open(alice, '/settings?github-app=refused#github-app');
	const app = section(page, 'GitHub App');
	await expect(app.getByRole('alert')).toContainText(`GitHub made ${forge.app!.slug} other than Banda asked for (owned by mallory, not the organization e2e), so Banda didn’t keep it.`);
	await expect(app.getByRole('link', { name: 'Delete it on GitHub' })).toHaveAttribute('href', deleteUrl);
	if (process.env.E2E_SCREENSHOTS) await shootHere(page, 'github-app-refused');

	// Made by hand (Helm, GitOps): it is changed there.
	forge.tamper = {};
	const manifest = { setup_url: `${api.url}/github/installed`, hook_attributes: {}, default_permissions: permissions, default_events: [] };
	const first = forge.makeApp(manifest, 'e2e', 'Organization');
	await kube('create', 'secret', 'generic', secret, ...Object.entries(first).map(([k, v]) => `--from-literal=${k}=${v}`));
	await until(async () => (await alice.settings.getGitHub({})).app?.slug === first.slug, () => 'the app made by hand to show', 45_000);
	expect(await refused(alice.settings.gitHubAppManifest({ organization: 'e2e', replace: true }))).toContain(`the GitHub App ${first.slug} comes from the deployment`);
	await page.reload();
	await expect(app).toContainText('Set up by the deployment (the Secret banda-github-app): it is changed there, not here.');
	await expect(app.getByRole('button', { name: 'Replace the app…' })).toHaveCount(0);

	// Another app in the Secret's place: the old one's connections end, revoked with its keys.
	await connect(api, alice, forge, 'alice');
	const token = forge.userToken('alice')!;
	const second = forge.makeApp(manifest, 'e2e', 'Organization');
	await kube('patch', 'secret', secret, '--type', 'merge', '-p', JSON.stringify({ stringData: second }));
	await until(async () => (await alice.settings.getGitHub({})).app?.slug === second.slug && forge.revoked.includes(token), () => 'the old app’s connection to end', 45_000);
	expect((await alice.settings.getGitHub({})).connection).toBeUndefined();
	expect(await stored(alice)).toEqual({});
});

/** Settings in light and dark, for docs and review (E2E_SCREENSHOTS=1), as the theme setting picks them. */
async function shoot(page: Page, name: string, at = '#github-app') {
	for (const mode of ['light', 'dark'] as const) {
		await page.evaluate((m) => localStorage.setItem('mode-watcher-mode', m), mode);
		await page.reload();
		await page.getByRole('region', { name: 'GitHub App', exact: true }).getByRole('button').first().waitFor();
		await page.locator(at).evaluate((e) => e.scrollIntoView());
		await page.screenshot({ path: `../docs/screenshots/${name}-${mode}.png` });
	}
}

/** The same without reloading, for a state the page holds (a form filled in): the theme's class switched in place. */
async function shootHere(page: Page, name: string, at = '#github-app') {
	for (const mode of ['light', 'dark'] as const) {
		await page.evaluate((m) => {
			document.documentElement.classList.toggle('dark', m === 'dark');
			document.documentElement.style.colorScheme = m;
		}, mode);
		await page.locator(at).evaluate((e) => e.scrollIntoView());
		await page.waitForTimeout(300); // the colours' transitions
		await page.screenshot({ path: `../docs/screenshots/${name}-${mode}.png` });
	}
}
