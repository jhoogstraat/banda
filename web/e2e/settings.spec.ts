// A user's account: the Claude login environment, secrets and the API key.
import { AuthMethod, LoginAction, LoginState, SetupState } from '../src/lib/gen/banda/v1/banda_pb.ts';
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

test('secrets: set, reserved and invalid names and subscription tokens refused, values never returned', async ({ as }) => {
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
