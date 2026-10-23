// Sign-in through Banda's sign-in page and the run's own Dex (the kind values' users, Banda's theme),
// served by the run's OIDC API at /dex. That API names its provider Dex (OIDC_PROVIDER_NAME).
import type { Browser, Page } from '@playwright/test';
import { expect, OIDC_API, test } from './fixtures.ts';

const me = (page: Page) => page.request.post(`${OIDC_API}/banda.v1.UserService/Me`, { data: {} });
// E2E_SCREENSHOTS=1: pictures of the sign-in pages in the test's output directory.
const shot = (page: Page, name: string) =>
	process.env.E2E_SCREENSHOTS ? page.screenshot({ path: test.info().outputPath(`${name}.png`) }) : undefined;

/** Opens path signed out and signs in from Banda's sign-in page through Dex's form, back on path. */
async function signIn(browser: Browser, user: string, path = '/') {
	const page = await (await browser.newContext({ baseURL: OIDC_API })).newPage();
	await page.goto(path);
	await expect(page).toHaveURL(`${OIDC_API}/signin?next=${encodeURIComponent(path)}`);
	await expect(page.getByRole('heading', { name: 'Banda' })).toBeVisible();
	await shot(page, 'signin');
	await page.getByRole('link', { name: 'Sign in with Dex' }).click();
	// Dex, in Banda's look: the password form is its only step.
	await expect(page).toHaveTitle('Banda');
	await expect(page.getByText('Sign in with your email address and password.')).toBeVisible();
	await expect(page.locator('body')).toHaveCSS('font-family', /Inter Variable/);
	await page.getByLabel(/email/i).fill(`${user}@example.com`);
	await page.getByLabel(/password/i).fill('password');
	await shot(page, 'dex');
	await page.getByRole('button', { name: 'Sign in' }).click();
	await expect(page).toHaveURL(`${OIDC_API}${path}`);
	return page;
}

test('alice and bob sign in through Dex side by side; signing out ends only alice’s session', { tag: '@key' }, async ({ browser }) => {
	const [alice, bob] = [await signIn(browser, 'alice'), await signIn(browser, 'bob', '/search?q=hello')];
	expect((await (await me(alice)).json()).user).toMatchObject({ name: 'alice', email: 'alice@example.com' });
	expect((await (await me(bob)).json()).user).toMatchObject({ name: 'bob', email: 'bob@example.com' });
	await alice.getByRole('button', { name: 'Account' }).click();
	await alice.getByRole('menuitem', { name: 'Sign out' }).click();
	await expect(alice).toHaveURL(`${OIDC_API}/signin?signed-out`);
	await expect(alice.getByRole('status')).toHaveText('You signed out.');
	expect((await me(alice)).status()).toBe(401);
	expect((await me(bob)).status()).toBe(200);
	await alice.getByRole('link', { name: 'Sign in with Dex' }).click();
	await expect(alice.getByLabel(/email/i)).toBeVisible();
});

test('a failed sign-in comes back to the sign-in page and says why', async ({ browser }) => {
	const page = await (await browser.newContext({ baseURL: OIDC_API })).newPage();
	await page.goto('/auth/callback?state=forged&code=x');
	await expect(page).toHaveURL(`${OIDC_API}/signin?error=state`);
	await expect(page.getByRole('alert')).toHaveText(/expired or was started in another tab/);

	// Dex refuses (as when someone cancels): the page says so and still goes back to next.
	await page.goto('/auth/login?next=%2Fsearch');
	await expect(page.getByLabel(/password/i)).toBeVisible();
	const flow = (await page.context().cookies()).find((c) => c.name === 'banda_oidc')!;
	const state = new URLSearchParams(flow.value).get('state');
	await page.goto(`/auth/callback?state=${state}&error=access_denied`);
	await expect(page).toHaveURL(`${OIDC_API}/signin?error=denied&next=%2Fsearch`);
	await expect(page.getByRole('alert')).toHaveText('Dex didn’t sign you in: it was cancelled or refused.');
	await expect(page.getByRole('link', { name: 'Sign in with Dex' })).toHaveAttribute('href', '/auth/login?next=%2Fsearch');
});

test('without a session the API refuses requests, whatever headers they carry', { tag: '@key' }, async () => {
	const r = await fetch(`${OIDC_API}/banda.v1.UserService/Me`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json', 'X-Banda-User': 'alice' },
		body: '{}'
	});
	expect(r.status).toBe(401);
	expect((await fetch(`${OIDC_API}/files`, { method: 'POST', headers: { 'X-Banda-User': 'alice' }, body: 'x' })).status).toBe(401);
});
