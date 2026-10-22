// People: a person's page (/u/<id>) with their threads by what matters, the names that link to it, and the
// account menu in the sidebar's footer.
import { expect, Status, test } from './fixtures.ts';

test('a teammate’s page: bob follows alice’s name there, sees her thread work and finish live, her settled one folded, and her here', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	await alice.users.me({}); // known to the team before bob looks
	const settled = await alice.create({ prompt: 'e2e: settled by alice' });
	await settled.waitForStatus(Status.AWAITING_INPUT);
	await alice.threads.settle({ threadId: settled.id, settled: true });
	const t = await alice.create({ prompt: 'e2e-hold:person' });
	const page = await bob.page();

	await test.step('the team board’s column header links to her page', async () => {
		await page.goto('/team');
		await page.getByRole('heading', { level: 2, name: alice.id }).getByRole('link').click();
		await expect(page).toHaveURL(`/u/${alice.id}`);
		await expect(page.getByRole('heading', { level: 1, name: alice.id })).toBeVisible();
	});

	await test.step('her threads by what matters, live', async () => {
		const group = (name: string) => page.getByRole('region', { name });
		await expect(group('Working').getByRole('link', { name: /e2e-hold:person/ })).toBeVisible();
		await t.waitForStatus(Status.WORKING);
		await t.release('person');
		await expect(group('Done recently').getByRole('link', { name: /e2e-hold:person/ })).toContainText(`Ready for ${alice.id}`);
		await expect(page.getByRole('link', { name: /settled by alice/ })).toBeHidden();
		await page.getByRole('button', { name: '1 settled or older' }).click();
		await expect(group('Settled or older').getByRole('link', { name: /settled by alice/ })).toBeVisible();
	});

	await test.step('whether she is here now', async () => {
		await expect(page.getByText('Here now in')).toBeHidden();
		const hers = await alice.page();
		await hers.goto(`/t/${t.id}`);
		await expect(page.getByText('Here now in').getByRole('link')).toHaveAttribute('href', `/t/${t.id}`);
		await hers.close();
	});

	await test.step('the thread header’s owner and her messages link to her page', async () => {
		await page.goto(`/t/${t.id}`);
		const links = page.getByRole('main').getByRole('link', { name: alice.id });
		await expect(links).toHaveCount(2); // the owner in the header, the author of her prompt
		await links.last().click();
		await expect(page).toHaveURL(`/u/${alice.id}`);
	});

	await test.step('the command palette goes to her', async () => {
		await page.keyboard.press('ControlOrMeta+k');
		const palette = page.getByRole('dialog', { name: 'Command palette' });
		await palette.getByPlaceholder('Search threads, teammates and actions').fill(`go to ${alice.id}`);
		await palette.getByRole('option', { name: new RegExp(`Go to ${alice.id}$`) }).click();
		await expect(page).toHaveURL(`/u/${alice.id}`);
	});
});

test('account menu: my avatar opens my profile, Settings (also G S), notifications, shortcuts and sign-out', async ({ as }) => {
	const bob = as('bob');
	const mine = await bob.create({ prompt: 'e2e: bob’s own' });
	await mine.waitForStatus(Status.AWAITING_INPUT);
	const page = await bob.page({ permissions: ['notifications'] }); // answered: no Enable entry
	await page.goto('/');
	const menu = async () => {
		await page.getByRole('button', { name: 'Account' }).click();
		return page.getByRole('menu');
	};

	let m = await menu();
	await expect(m.getByRole('menuitem', { name: 'Notifications' })).toHaveAttribute('href', '/settings#notifications');
	await expect(m.getByRole('menuitem', { name: 'Keyboard shortcuts' })).toHaveAttribute('href', '/settings#shortcuts');
	await expect(m.getByRole('menuitem', { name: 'Sign out' })).toHaveAttribute('href', '/auth/logout');
	await expect(m.getByRole('menuitem', { name: 'Enable notifications' })).toBeHidden();
	await m.getByRole('menuitem', { name: 'Your profile' }).click();
	await expect(page).toHaveURL(`/u/${bob.id}`);
	await expect(page.getByRole('heading', { level: 1, name: 'You' })).toBeVisible();
	await expect(page.getByRole('region', { name: 'Done recently' }).getByRole('link', { name: /bob’s own/ })).toBeVisible();
	await expect(page.getByRole('link', { name: 'Settings' })).toHaveAttribute('href', '/settings');

	m = await menu();
	await m.getByRole('menuitem', { name: /^Settings/ }).click();
	await expect(page).toHaveURL('/settings');
	await page.goto('/team'); // a page without a focused composer
	await expect(page.getByRole('heading', { level: 1, name: 'Team' })).toBeVisible();
	await page.keyboard.press('g');
	await page.keyboard.press('s');
	await expect(page).toHaveURL('/settings');
});

test('a person nobody on the team is says so', async ({ as }) => {
	const page = await as('alice').page();
	await page.goto('/u/nobody-here');
	await expect(page.getByRole('heading', { name: 'Person not found' })).toBeVisible();
});
