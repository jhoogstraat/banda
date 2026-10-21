// Sign-in through the run's own Dex (deploy/dex.yaml's users) against the run's OIDC API.
import type { Browser, Page } from '@playwright/test';
import { expect, OIDC_API, test } from './fixtures.ts';

const me = (page: Page) => page.request.post(`${OIDC_API}/banda.v1.UserService/Me`, { data: {} });

async function signIn(browser: Browser, user: string) {
	const page = await (await browser.newContext({ baseURL: OIDC_API })).newPage();
	await page.goto('/');
	await page.getByLabel(/email/i).fill(`${user}@example.com`);
	await page.getByLabel(/password/i).fill('password');
	await page.getByRole('button', { name: /log ?in/i }).click();
	await expect(page).toHaveURL(`${OIDC_API}/`);
	return page;
}

test('alice and bob sign in through Dex side by side; signing out ends only alice’s session', async ({ browser }) => {
	const [alice, bob] = [await signIn(browser, 'alice'), await signIn(browser, 'bob')];
	expect((await (await me(alice)).json()).user).toMatchObject({ name: 'alice', email: 'alice@example.com' });
	expect((await (await me(bob)).json()).user).toMatchObject({ name: 'bob', email: 'bob@example.com' });
	await alice.getByText('Sign out').click();
	expect((await me(alice)).status()).toBe(401);
	expect((await me(bob)).status()).toBe(200);
});

test('without a session the API refuses requests, whatever headers they carry', async () => {
	const r = await fetch(`${OIDC_API}/banda.v1.UserService/Me`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json', 'X-Banda-User': 'alice' },
		body: '{}'
	});
	expect(r.status).toBe(401);
	expect((await fetch(`${OIDC_API}/files`, { method: 'POST', headers: { 'X-Banda-User': 'alice' }, body: 'x' })).status).toBe(401);
});
