// Workload identity: Banda as an OIDC issuer of short-lived tokens for each running thread's owner, for the
// audiences the team adds in Settings (internal/identity, internal/api/identity.go, cmd/runner/idtokens.go).
import { createClient } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';
import { LoginAction, RunnerService } from '../src/lib/gen/banda/v1/banda_pb.js';
import { recipes } from '../src/lib/identity-recipes.ts';
import { expect, kexec, kube, OIDC_API, sql, Status, test, until, userHash, type Api, type User } from './fixtures.ts';

const dir = '/run/banda/id-tokens';
const issuer = 'https://banda.e2e.example';

type Claims = {
	iss: string;
	aud: string;
	sub: string;
	iat: number;
	nbf: number;
	exp: number;
	jti: string;
	email: string;
	email_verified: boolean;
	thread: string;
	repo: string;
	branch: string;
};

const b64 = (s: string) => Buffer.from(s, 'base64url');

/** The token's claims, after checking its RS256 signature against the API's published keys. */
async function verified(api: Api, token: string): Promise<Claims> {
	const [h, p, sig] = token.split('.');
	const header = JSON.parse(b64(h).toString());
	const { keys } = (await (await fetch(`${api.url}/.well-known/jwks.json`)).json()) as { keys: (JsonWebKey & { kid: string })[] };
	const jwk = keys.find((k) => k.kid === header.kid);
	expect(header.alg, 'alg').toBe('RS256');
	expect(jwk, `kid ${header.kid} in the JWKS`).toBeTruthy();
	const key = await crypto.subtle.importKey('jwk', jwk!, { name: 'RSASSA-PKCS1-v1_5', hash: 'SHA-256' }, false, ['verify']);
	expect(await crypto.subtle.verify('RSASSA-PKCS1-v1_5', key, b64(sig), new TextEncoder().encode(`${h}.${p}`)), 'signature').toBe(true);
	return JSON.parse(b64(p).toString());
}

/** A user who signed in with an email: tokens name it. */
async function signedIn(u: User, api: Api) {
	await u.users.me({});
	await sql(`UPDATE users SET email = '${u.id}@e2e.example' WHERE id = '${u.id}'`, api.db);
	return u;
}

// E2E_SCREENSHOTS=1: the Settings section, dark (the default) and light, in the test's output directory.
// tab: a recipe to show instead of the first; long lines must scroll, not run under the copy button.
async function shots(u: User, name: string, tab?: string) {
	if (!process.env.E2E_SCREENSHOTS) return;
	for (const mode of ['dark', 'light']) {
		const page = await u.page({ viewport: { width: 1280, height: 2400 } });
		await page.addInitScript((m) => localStorage.setItem('mode-watcher-mode', m), mode);
		await page.goto('/settings#workload-identity');
		const section = page.getByRole('region', { name: 'Workload identity' });
		await expect(section.getByText('.well-known/jwks.json')).toBeVisible();
		const recipes = section.getByRole('button', { name: /^Recipes/ }); // folded until asked for
		if (await recipes.count()) await recipes.click();
		if (tab) await section.getByRole('tab', { name: tab }).click();
		await section.screenshot({ path: test.info().outputPath(`${name}-${mode}.png`) });
	}
}

const token = (pod: string, file: string) => kexec(pod, `cat ${dir}/${file} 2>/dev/null || true`).then((s) => s.trim());
/** RunnerService as the pod's runner, with its runner token. */
const asRunner = async (api: Api, pod: string) => {
	const runner = createClient(RunnerService, createConnectTransport({ baseUrl: api.url }));
	return { runner, token: await kexec(pod, 'printf %s "$BANDA_RUNNER_TOKEN"') };
};

test('discovery and the public keys are served at PUBLIC_URL, publicly', async () => {
	const r = await fetch(`${OIDC_API}/.well-known/openid-configuration`);
	expect(r.status).toBe(200);
	expect(r.headers.get('cache-control')).toBe('public, max-age=300');
	const d = await r.json();
	expect(d).toMatchObject({
		issuer: OIDC_API,
		jwks_uri: `${OIDC_API}/.well-known/jwks.json`,
		id_token_signing_alg_values_supported: ['RS256'],
		response_types_supported: ['id_token'],
		subject_types_supported: ['public']
	});
	expect(d.claims_supported).toEqual(expect.arrayContaining(['sub', 'email', 'thread', 'repo', 'branch']));
	const { keys } = await (await fetch(d.jwks_uri)).json();
	expect(keys.length).toBeGreaterThan(0);
	expect(keys[0]).toMatchObject({ kty: 'RSA', alg: 'RS256', use: 'sig', e: 'AQAB' });
	expect(keys[0]).not.toHaveProperty('d'); // public only
});

test('an audience added in Settings puts a signed token in running threads, refreshed, and removing it takes the token away', { tag: '@key' }, async ({ as, startApi }) => {
	const api = await startApi({ PUBLIC_URL: issuer, TEST_ID_TOKEN_REFRESH: '3s' });
	const alice = await signedIn(as('alice', api), api);
	const t = await alice.create();
	const info = await t.waitForStatus(Status.AWAITING_INPUT);
	expect(await kexec(t.pod, `ls -A ${dir}`)).toBe(''); // off until an audience is added
	expect(await kexec(t.pod, `grep " ${dir} " /proc/mounts`)).toMatch(/^\S+ \S+ tmpfs /); // in memory, not on the volume

	const page = await alice.page();
	await test.step('Settings shows the issuer, no audiences, and adds one', async () => {
		await page.goto('/settings#workload-identity');
		const section = page.getByRole('region', { name: 'Workload identity' });
		await expect(section.getByText(issuer, { exact: true })).toBeVisible();
		await expect(section.getByText(`${issuer}/.well-known/jwks.json`)).toBeVisible();
		await expect(section.getByText('cloud providers require https')).toHaveCount(0);
		await expect(section.getByText(/^Off: threads get no tokens/)).toBeVisible();
		await section.getByRole('textbox', { name: 'Audience' }).fill('vault');
		await section.getByRole('button', { name: 'Add audience' }).click();
		await expect(section.getByText(`${dir}/vault`).first()).toBeVisible();
		await expect(section.getByText(`added by ${alice.id}`)).toBeVisible();
		await expect(section.getByRole('tab', { name: 'OpenBao / Vault' })).toBeVisible();
		await expect(section.getByRole('tabpanel', { name: 'OpenBao / Vault' })).toContainText(`jwt=@${dir}/vault`); // the recipes carry the real path
		await shots(alice, 'vault');
		await shots(alice, 'aws', 'AWS');
	});

	let first = '';
	await test.step('the running thread gets a token naming its owner, its thread, repository and branch', async () => {
		first = await until(() => token(t.pod, 'vault').then((s) => s || undefined), () => `no token in ${dir}/vault`, 30_000);
		const c = await verified(api, first);
		expect(c).toMatchObject({
			iss: issuer,
			aud: 'vault',
			sub: `user:${alice.id}@e2e.example`,
			email: `${alice.id}@e2e.example`,
			email_verified: true,
			thread: t.id,
			repo: 'e2e-git/plain',
			branch: info.branch
		});
		expect([c.exp - c.iat, c.nbf]).toEqual([15 * 60, c.iat]);
		expect(await kexec(t.pod, 'printf %s "$BANDA_ID_TOKEN_DIR"')).toBe(dir);
	});

	await test.step('a refresh replaces the file with a new token', async () => {
		const next = await until(() => token(t.pod, 'vault').then((s) => (s && s !== first ? s : undefined)), () => 'the token was not refreshed', 30_000);
		expect((await verified(api, next)).jti).not.toBe((await verified(api, first)).jti);
	});

	await test.step('removing the audience in Settings deletes the file', async () => {
		await page.getByRole('button', { name: 'Remove vault' }).click();
		await page.getByRole('dialog').getByRole('button', { name: 'Remove audience' }).click();
		await expect(page.getByText(/^Off: threads get no tokens/)).toBeVisible();
		await expect.poll(() => kexec(t.pod, `ls -A ${dir}`), { timeout: 30_000 }).toBe('');
	});

	await test.step('a wrong runner token mints nothing', async () => {
		const runner = createClient(RunnerService, createConnectTransport({ baseUrl: api.url }));
		await expect(runner.idTokens({ threadId: t.id, token: 'wrong' })).rejects.toThrow(/bad token/);
	});
});

test('an http issuer is flagged, and without audiences Settings says it is off and offers common ones', async ({ as }) => {
	const page = await as('alice').page();
	await page.goto('/settings#workload-identity');
	const section = page.getByRole('region', { name: 'Workload identity' });
	await expect(section.getByText(/cloud providers require https and a valid certificate/)).toBeVisible();
	await expect(section.getByText(/^Off: threads get no tokens/)).toBeVisible();
	await expect(section.getByRole('button', { name: 'sts.amazonaws.com' })).toBeVisible();
	await expect(section.getByRole('tab')).toHaveCount(0); // no recipes without an audience to fill in
	await shots(as('alice'), 'off');
});

test('the recipes hold on a plain http origin: the vault keys are shown, and Settings takes their team secrets', async ({ as, startApi }) => {
	const api = await startApi({ PUBLIC_URL: 'http://banda.lan:8080' });
	const alice = await signedIn(as('alice', api), api);
	await alice.settings.addIdTokenAudience({ audience: 'vault' });
	const page = await alice.page();
	await page.addInitScript(() => Object.defineProperty(crypto, 'subtle', { value: undefined })); // as on http://<lan address>
	await page.goto('/settings#workload-identity');
	const section = page.getByRole('region', { name: 'Workload identity' });
	await section.getByRole('button', { name: /^Recipes/ }).click();
	await expect(section.getByRole('tabpanel', { name: 'OpenBao / Vault' })).toContainText('jwt_validation_pubkeys=@banda-1.pem');

	const notes = recipes({ issuer: 'https://b.example', audience: 'vault', path: '/p', repo: '', email: 'a@b.c', pems: [] }).flatMap((r) => r.steps.map((s) => s.note ?? ''));
	const names = [...new Set(notes.flatMap((n) => [...n.matchAll(/team secret ([A-Z_]+)/g)].map((m) => m[1])))];
	expect(names).toContain('KUBECONFIG_YAML');
	for (const name of names) {
		await alice.settings.setTeamSecret({ name, value: 'x' });
		await alice.settings.deleteTeamSecret({ name });
	}
});

test('the login environment gets no identity tokens', async ({ as, startApi }) => {
	const api = await startApi({ PUBLIC_URL: issuer });
	const alice = await signedIn(as('alice', api), api);
	await alice.settings.addIdTokenAudience({ audience: 'vault' });
	await alice.terminal((signal) => alice.settings.openLoginTerminal({ harness: 'claude', action: LoginAction.LOGIN, cols: 80, rows: 24 }, { signal }));
	const pod = `login-${userHash(alice.id)}`;
	const spec = JSON.parse(await kube('get', 'pod', pod, '-o', 'json')).spec;
	expect(spec.volumes.map((v: { name: string }) => v.name)).not.toContain('id-tokens');
	expect(spec.containers[0].env.map((e: { name: string }) => e.name)).not.toContain('BANDA_ID_TOKEN_DIR');
	expect(await kexec(pod, `ls ${dir} 2>&1 || true`)).toMatch(/No such file/);
});

test('after a handoff the next token names the new owner, and the old pod mints none naming them', { tag: '@key' }, async ({ as, startApi }) => {
	const api = await startApi({ PUBLIC_URL: issuer });
	const [alice, bob] = [await signedIn(as('alice', api), api), await signedIn(as('bob', api), api)];
	await alice.settings.addIdTokenAudience({ audience: 'sts.amazonaws.com' });
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const before = await until(() => token(t.pod, 'sts.amazonaws.com').then((s) => s || undefined), () => 'no token', 30_000);
	expect((await verified(api, before)).sub).toBe(`user:${alice.id}@e2e.example`);
	const old = await asRunner(api, t.pod);

	await test.step('asking again within the refresh interval gets the same tokens, not new signatures', async () => {
		const [a, b] = [await old.runner.idTokens({ threadId: t.id, token: old.token }), await old.runner.idTokens({ threadId: t.id, token: old.token })];
		expect(b.tokens[0].token).toBe(a.tokens[0].token);
	});

	await alice.threads.handoffOffer({ threadId: t.id, toUserId: bob.id });
	await until(async () => ((await t.of('turnEnded')).length >= 2 ? true : undefined), () => 'no briefing turn', 90_000);

	// The old pod asks for tokens in a loop while bob accepts: none it gets may name bob.
	let accepting = true;
	const subs = new Set<string>();
	const ask = async () => {
		while (accepting) {
			const r = await old.runner.idTokens({ threadId: t.id, token: old.token }).catch(() => undefined);
			for (const tk of r?.tokens ?? []) subs.add((await verified(api, tk.token)).sub);
		}
	};
	const askers = Array.from({ length: 8 }, ask);
	await t.as(bob).turn(() => bob.threads.handoffAccept({ threadId: t.id }));
	accepting = false;
	await Promise.all(askers);
	expect([...subs], 'subjects the old pod got').not.toContain(`user:${bob.id}@e2e.example`);
	await expect(old.runner.idTokens({ threadId: t.id, token: old.token })).rejects.toThrow(/bad token/);

	await expect
		.poll(async () => {
			const s = await token(t.pod, 'sts.amazonaws.com');
			return s && s !== before ? (await verified(api, s)).sub : '';
		}, { timeout: 60_000 })
		.toBe(`user:${bob.id}@e2e.example`);
});

test('an owner whose email the IdP left unverified, or who shares it with another user, gets no tokens and the chat says why', async ({ as, startApi }) => {
	const api = await startApi({ PUBLIC_URL: issuer });
	const [alice, bob] = [await signedIn(as('alice', api), api), await signedIn(as('bob', api), api)];
	await alice.settings.addIdTokenAudience({ audience: 'vault' });
	await sql(`UPDATE users SET email_verified = false WHERE id = '${alice.id}'`, api.db);
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const { runner, token: rt } = await asRunner(api, t.pod);
	await expect(runner.idTokens({ threadId: t.id, token: rt })).rejects.toThrow(/hasn't verified the email/);
	await t.waitForEvent((e) => e.kind.case === 'notice' && e.kind.value.text.includes("hasn't verified the email"), 'the notice', 30_000);
	expect(await kexec(t.pod, `ls -A ${dir}`)).toBe('');

	await sql(`UPDATE users SET email_verified = true WHERE id = '${alice.id}'`, api.db);
	await sql(`UPDATE users SET email = '${alice.id.toUpperCase()}@e2e.example' WHERE id = '${bob.id}'`, api.db); // emails ignore case
	await expect(runner.idTokens({ threadId: t.id, token: rt })).rejects.toThrow(/another Banda user signed in with the email/);

	await sql(`UPDATE users SET email = '${bob.id}@e2e.example' WHERE id = '${bob.id}'`, api.db);
	const res = await runner.idTokens({ threadId: t.id, token: rt });
	expect((await verified(api, res.tokens[0].token)).sub).toBe(`user:${alice.id}@e2e.example`);
});

test('a local repository gives an empty repo claim: file:// names no host', async ({ as, startApi }) => {
	const api = await startApi({ PUBLIC_URL: issuer });
	const alice = await signedIn(as('alice', api), api);
	await alice.settings.addIdTokenAudience({ audience: 'vault' });
	const t = await alice.create({ repoUrl: 'file:///opt/e2e/plain.git' });
	await t.waitForStatus(Status.AWAITING_INPUT);
	const tk = await until(() => token(t.pod, 'vault').then((s) => s || undefined), () => 'no token', 30_000);
	expect((await verified(api, tk)).repo).toBe('');
});
