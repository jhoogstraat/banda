// A user's settings: the Claude login environment, secrets, the API key, the git identity, new threads' defaults,
// notifications, the theme and the section nav.
import { AuthMethod, AutoPushState, LoginAction, LoginState, PermissionMode, SetupState } from '../src/lib/gen/banda/v1/banda_pb.js';
import type { Page } from '@playwright/test';
import { themes } from '../src/lib/themes.ts';
import { expect, kube, refused, Status, test, userHash, type User } from './fixtures.ts';

const loginState = async (u: User) => (await u.settings.getSettings({})).accounts[0]?.loginState;
const loginTerminal = (u: User, action: LoginAction) =>
	u.terminal((signal) => u.settings.openLoginTerminal({ harness: 'claude', action, cols: 80, rows: 24 }, { signal }));

test('login environment: logout and login in its terminal update the login state; it mounts only the Claude volume', async ({ as }) => {
	const alice = as('alice');
	expect(await loginTerminal(alice, LoginAction.LOGOUT)).toContain('logged out');
	await expect.poll(() => loginState(alice)).toBe(LoginState.LOGGED_OUT);
	expect(await loginTerminal(alice, LoginAction.LOGIN)).toContain('Login successful');
	await expect.poll(() => loginState(alice)).toBe(LoginState.LOGGED_IN);
	const pod = JSON.parse(await kube('get', 'pod', `login-${userHash(alice.id)}`, '-o', 'json'));
	expect(pod.spec.containers[0].envFrom).toBeUndefined();
	expect(pod.spec.volumes.filter((v: object) => 'secret' in v || 'persistentVolumeClaim' in v)).toHaveLength(1);
});

test('secrets: set, reserved and invalid names and subscription tokens refused, values never returned', { tag: '@key' }, async ({ as }) => {
	const alice = as('alice');
	const secret = `e2e-secret-${Date.now()}`;
	await alice.settings.setSecret({ name: 'E2E_SECRET', value: secret });
	for (const name of ['ANTHROPIC_API_KEY', 'ANTHROPIC_MODEL', 'CLAUDE_CODE_OAUTH_TOKEN', 'CLAUDE_X', 'BANDA_THREAD_ID', 'PATH', 'lower', '1X'])
		await refused(alice.settings.setSecret({ name, value: 'x' }));
	await refused(alice.settings.setSecret({ name: 'E2E_OAUTH', value: 'sk-ant-oat01-e2e' }));
	await refused(alice.settings.setAuthMethod({ harness: 'claude', authMethod: AuthMethod.API_KEY, apiKey: 'sk-ant-ort01-e2e' }));
	const s = await alice.settings.getSettings({});
	expect(s.secrets.map((x) => x.name)).toContain('E2E_SECRET');
	expect(s.accounts[0].harness).toBe('claude');
	expect(JSON.stringify(s, (_, v) => (typeof v === 'bigint' ? `${v}` : v))).not.toContain(secret);
});

test('API key mode: the key reaches the environment and the login wait is skipped; switching back to the subscription applies when it resumes', async ({ as }) => {
	const u = as('keyed');
	const key = `key-e2e-secret-${Date.now()}`;
	await refused(u.settings.setAuthMethod({ harness: 'claude', authMethod: AuthMethod.API_KEY }));
	await u.settings.setAuthMethod({ harness: 'claude', authMethod: AuthMethod.API_KEY, apiKey: key });
	const s = await u.settings.getSettings({});
	expect(s.accounts[0].authMethod).toBe(AuthMethod.API_KEY);
	expect(s.accounts[0].apiKeySet).toBe(true);
	expect(JSON.stringify(s, (_, v) => (typeof v === 'bigint' ? `${v}` : v))).not.toContain(key);
	const t = await u.create({ prompt: 'e2e: api key' });
	await t.waitForStatus(Status.AWAITING_INPUT);
	expect((await t.of('setupStep')).filter((x) => x.id === 'login' && x.state === SetupState.SKIPPED)).toHaveLength(1);
	expect(await t.terminal('echo key-${#ANTHROPIC_API_KEY}')).toContain(`key-${key.length}`);

	await test.step('switching back to the subscription applies when the environment resumes', async () => {
		await u.settings.setAuthMethod({ harness: 'claude', authMethod: AuthMethod.SUBSCRIPTION });
		await t.snooze();
		await t.podGone();
		await t.turn(() => t.send('wake up'));
		expect(await t.terminal('echo key-${ANTHROPIC_API_KEY:-none}')).toContain('key-none');
	});
});

test('git identity: set in Settings, it is who commits in the sandbox are by; empty uses the sign-in’s; bad ones refused', async ({ as }) => {
	const alice = as('alice');
	expect((await alice.settings.getSettings({})).gitIdentity).toMatchObject({ name: '', email: '', signInName: alice.id });
	await refused(alice.settings.setGitIdentity({ name: 'Alice <x>', email: '' }));
	await refused(alice.settings.setGitIdentity({ name: '', email: 'no-at-sign' }));

	const page = await alice.page();
	await page.goto('/settings#git');
	const git = page.getByRole('region', { name: 'Git identity' });
	await expect(git.getByText(`${alice.id} <banda@localhost>`)).toBeVisible(); // dev auth: no email
	await git.getByLabel('Name').fill('Alice Example');
	await git.getByLabel('Email').fill('alice@example.com');
	await git.getByRole('button', { name: 'Save' }).click();
	await expect(git.getByText('Alice Example <alice@example.com>')).toBeVisible();

	const t = await alice.create({ prompt: 'e2e: commit a file' });
	await t.waitFor('pushed', (i) => i.autoPush === AutoPushState.PUSHED, 150_000);
	expect(await t.origin(`log -1 --format=%an/%ae/%cn/%ce ${(await t.get()).branch}`)).toContain(
		'Alice Example/alice@example.com/Alice Example/alice@example.com'
	);
});

test('defaults for new threads: set in Settings, the composer starts with them and threads that don’t say get them', async ({ as }) => {
	const alice = as('alice');
	const page = await alice.page();
	await page.goto('/settings#defaults');
	const defaults = page.getByRole('region', { name: 'New threads' });
	await defaults.getByLabel('Model').selectOption('claude-sonnet-5-5');
	await expect.poll(async () => (await alice.settings.getSettings({})).threadDefaults?.model).toBe('claude-sonnet-5-5');
	await defaults.getByLabel('Effort').selectOption('low');
	await expect.poll(async () => (await alice.settings.getSettings({})).threadDefaults?.effort).toBe('low');
	await defaults.getByLabel('Permissions').selectOption('Plan');
	await expect
		.poll(async () => (await alice.settings.getSettings({})).threadDefaults)
		.toMatchObject({ model: 'claude-sonnet-5-5', effort: 'low', permissionMode: PermissionMode.PLAN });

	await page.goto('/');
	await page.getByRole('button', { name: 'Options' }).click();
	for (const name of ['Model: claude-sonnet-5-5', 'Effort: low', 'Permissions: Plan']) await expect(page.getByRole('button', { name })).toBeVisible();

	const t = await alice.create({ prompt: 'e2e' });
	expect(await t.get()).toMatchObject({ model: 'claude-sonnet-5-5', effort: 'low', permissionMode: PermissionMode.PLAN });
});

test('defaults: quick changes build on each other while Settings reloads slowly', async ({ as }) => {
	const alice = as('alice');
	const page = await alice.page();
	await page.goto('/settings#defaults');
	const defaults = page.getByRole('region', { name: 'New threads' });
	await expect(defaults.getByLabel('Model')).toHaveValue('claude-opus-5-5');
	// Each save reloads Settings; a slow reload used to leave the next change building on the old values.
	await page.route('**/banda.v1.SettingsService/GetSettings', async (r) => {
		await new Promise((done) => setTimeout(done, 1500));
		await r.continue().catch(() => {});
	});
	const saved = () => page.waitForResponse('**/banda.v1.SettingsService/SetThreadDefaults');
	await Promise.all([saved(), defaults.getByLabel('Model').selectOption('claude-sonnet-5-5')]);
	await Promise.all([saved(), defaults.getByLabel('Effort').selectOption('low')]);
	await Promise.all([saved(), defaults.getByLabel('Permissions').selectOption('Plan')]);
	expect((await alice.settings.getSettings({})).threadDefaults).toMatchObject({ model: 'claude-sonnet-5-5', effort: 'low', permissionMode: PermissionMode.PLAN });
	await expect(defaults.getByLabel('Model')).toHaveValue('claude-sonnet-5-5');
	await expect(defaults.getByLabel('Effort')).toHaveValue('low');
	// Once saved, the page shows the server's values again: a change made elsewhere shows at the next reload.
	await page.unroute('**/banda.v1.SettingsService/GetSettings');
	await alice.settings.setThreadDefaults({ defaults: { model: 'claude-opus-5-5', effort: 'low', permissionMode: PermissionMode.PLAN } });
	await defaults.locator('input[type=radio]:not(:checked)').first().check(); // saving the profile reloads
	await expect(defaults.getByLabel('Model')).toHaveValue('claude-opus-5-5');
});

/** The browser's Notification, recorded: asks once, then shows. */
const stubNotification = (page: Page) =>
	page.addInitScript(() => {
		const w = window as unknown as { shown: string[]; Notification: unknown };
		w.shown = [];
		w.Notification = class {
			static permission = 'default';
			static async requestPermission() {
				return (this.permission = 'granted');
			}
			constructor(title: string) {
				w.shown.push(title);
			}
			close() {}
		};
	});

test('notifications: the section enables them and turns a kind off in this browser', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const page = await alice.page();
	await stubNotification(page);
	const shown = () => page.evaluate(() => (window as unknown as { shown: string[] }).shown);
	await page.goto('/settings#notifications');
	const section = page.getByRole('region', { name: 'Notifications' });
	await expect(section.getByText('Off: the browser hasn’t been asked yet.')).toBeVisible();
	await section.getByRole('button', { name: 'Enable' }).click();
	await expect(section.getByText('On in this browser.')).toBeVisible();

	const t = await alice.create({ prompt: 'e2e' });
	await expect.poll(shown, { timeout: 90_000 }).toContain('Your agent finished');
	await section.getByRole('checkbox', { name: /My agent finishes or fails/ }).uncheck();
	await t.turn(() => t.send('again'));
	// A mention, which still notifies, shows the finished turn has reached the page too.
	const b = await bob.create({ prompt: 'e2e' });
	await b.send(`@${alice.id} look`, { mentionedUserIds: [alice.id] });
	await expect.poll(shown).toContain(`You were mentioned in ${bob.id}'s thread`);
	expect((await shown()).filter((s) => s === 'Your agent finished')).toHaveLength(1);
});

test('appearance: the theme persists in this browser, is on <html> before the app loads, and fetches only its own fonts', async ({ as }) => {
	const page = await as('alice').page();
	await page.goto('/settings#appearance');
	const html = page.locator('html');
	const section = page.getByRole('region', { name: 'Appearance' });
	const radio = (name: string) => section.getByRole('radio', { name, exact: true });
	const font = () => page.evaluate(() => getComputedStyle(document.body).fontFamily);
	await expect(radio('Highlighter')).toBeChecked();
	await expect(html).toHaveAttribute('data-theme', '');
	expect(await font()).toContain('Schibsted');

	await radio('Logbook').check();
	await expect(html).toHaveAttribute('data-theme', 'logbook');
	expect(await font()).toContain('Instrument Sans');
	await expect(radio('Dark')).toBeChecked(); // the mode is its own choice
	await expect(html).toHaveClass(/dark/);
	await page.reload();
	await expect(radio('Logbook')).toBeChecked();
	await expect(html).toHaveAttribute('data-theme', 'logbook');

	// Without the app's code only app.html can have set it.
	await page.route('**/_app/**', (route) => route.abort());
	await page.reload();
	await expect(html).toHaveAttribute('data-theme', 'logbook');
	await page.unroute('**/_app/**');

	await page.reload();
	const loaded = () => page.evaluate(() => [...document.fonts].filter((f) => f.status === 'loaded').map((f) => f.family).join());
	await expect.poll(loaded).toContain('Instrument Sans');
	expect(await loaded()).not.toMatch(/Schibsted|Figtree/);

	await radio('Common room').check();
	await expect(html).toHaveAttribute('data-theme', 'common-room');
	expect(await font()).toContain('Figtree');
	await radio('Highlighter').check();
	await expect(html).toHaveAttribute('data-theme', '');
	expect(await font()).toContain('Schibsted');

	// A name a later version dropped looks like the default.
	await page.evaluate(() => localStorage.setItem('mode-watcher-theme', 'an-old-theme'));
	await page.reload();
	await expect(radio('Highlighter')).toBeChecked();
	expect(await font()).toContain('Schibsted');
});

test('appearance: the browser bar follows the theme and the mode, with no reload', async ({ as }) => {
	const page = await as('alice').page();
	await page.goto('/settings#appearance');
	const section = page.getByRole('region', { name: 'Appearance' });
	const bar = page.locator('meta[name=theme-color]');
	for (const [name, mode] of [['Dark', 'dark'], ['Light', 'light']] as const) {
		await section.getByRole('radio', { name, exact: true }).check();
		for (const t of [...themes, ...themes]) {
			await section.getByRole('radio', { name: t.label, exact: true }).check();
			await expect(bar).toHaveAttribute('content', t.colors[mode]);
		}
	}
	await page.reload();
	await expect(bar).toHaveAttribute('content', themes.at(-1)!.colors.light);
});

test('appearance: dark by default; Light and System persist in this browser', async ({ as }) => {
	const page = await as('alice').page();
	await page.goto('/settings#appearance');
	const html = page.locator('html');
	const section = page.getByRole('region', { name: 'Appearance' });
	await expect(section.getByRole('radio', { name: 'Dark' })).toBeChecked();
	await expect(html).toHaveClass(/dark/);
	await section.getByRole('radio', { name: 'Light', exact: true }).check();
	await expect(html).not.toHaveClass(/dark/);
	await page.reload();
	await expect(section.getByRole('radio', { name: 'Light', exact: true })).toBeChecked();
	await expect(html).not.toHaveClass(/dark/);
	await page.emulateMedia({ colorScheme: 'dark' });
	await section.getByRole('radio', { name: 'System' }).check();
	await expect(html).toHaveClass(/dark/);
});

test('the section nav links to every section; the shortcuts are listed', async ({ as }) => {
	const page = await as('alice').page();
	await page.goto('/settings');
	const nav = page.getByRole('navigation', { name: 'Settings sections' });
	for (const id of ['claude', 'accounts', 'git', 'defaults', 'notifications', 'appearance', 'secrets', 'shortcuts', 'github-app', 'gitlab-app', 'team-secrets', 'workload-identity']) {
		await expect(nav.locator(`a[href="#${id}"]`)).toHaveCount(1);
		await expect(page.locator(`section#${id}`)).toHaveCount(1);
	}
	// Grouped: what is mine, and what the team shares, in the nav and on the page.
	await expect(nav.getByRole('list', { name: 'You' }).getByRole('link')).toHaveText(['Claude Code', 'Accounts', 'Git identity', 'New threads', 'Notifications', 'Appearance', 'Secrets', 'Keyboard shortcuts']);
	await expect(nav.getByRole('list', { name: 'Team' }).getByRole('link')).toHaveText(['GitHub App', 'GitLab', 'Team secrets', 'Workload identity']);
	await expect(page.getByRole('heading', { level: 2, name: 'Team' })).toBeVisible();
	await expect(page.getByText('Shared by everyone: a change here applies to every teammate.')).toBeVisible();
	// It marks the section in view, following clicks and scrolling.
	const current = nav.locator('a[aria-current="location"]');
	await expect(current).toHaveText('Claude Code');
	await nav.getByRole('link', { name: 'Notifications' }).click();
	await expect(current).toHaveText('Notifications');
	await nav.getByRole('link', { name: 'Keyboard shortcuts' }).click();
	const shortcuts = page.getByRole('region', { name: 'Keyboard shortcuts' });
	await expect(shortcuts).toBeInViewport();
	await expect(shortcuts.getByText('Command palette')).toBeVisible();
	await expect(current).toHaveText('Keyboard shortcuts');
	await page.locator('section#git').evaluate((s) => s.scrollIntoView());
	await expect(current).toHaveText('Git identity');
});

test('the account menu’s "Enable notifications" asks the browser once, then links to the settings', async ({ as }) => {
	const page = await as('alice').page();
	await stubNotification(page);
	await page.goto('/');
	await page.getByRole('button', { name: 'Account' }).click();
	await page.getByRole('menuitem', { name: 'Enable notifications' }).click();
	await expect.poll(() => page.evaluate(() => Notification.permission)).toBe('granted');
	await page.getByRole('button', { name: 'Account' }).click();
	await expect(page.getByRole('menuitem', { name: 'Enable notifications' })).toHaveCount(0);
	await page.getByRole('menuitem', { name: 'Notifications' }).click();
	await expect(page).toHaveURL(/\/settings#notifications$/);
	await expect(page.getByRole('region', { name: 'Notifications' }).getByText('On in this browser.')).toBeVisible();
});
