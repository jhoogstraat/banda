// Team secrets: environment variables in every teammate's threads, set by anyone in Settings.
import { randomBytes } from 'node:crypto';
import { expect, refused, Status, test } from './fixtures.ts';

// Every test of the run shares the team's secrets, so each names its own.
const unique = (name: string) => `${name}_${randomBytes(3).toString('hex').toUpperCase()}`;
const json = (v: unknown) => JSON.stringify(v, (_, x) => (typeof x === 'bigint' ? `${x}` : x));

test('team secrets: anyone sets, replaces and deletes them; the list says who set them last, never their values', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const name = unique('E2E_TEAM');
	const value = `e2e-team-${Date.now()}`;
	await alice.settings.setTeamSecret({ name, value });
	for (const n of ['ANTHROPIC_API_KEY', 'CLAUDE_X', 'BANDA_THREAD_ID', 'PATH', 'lower', '1X'])
		await refused(alice.settings.setTeamSecret({ name: n, value: 'x' }));
	await refused(alice.settings.setTeamSecret({ name: unique('E2E_OAUTH'), value: 'sk-ant-oat01-e2e' }));
	const listed = async () => (await bob.settings.listTeamSecrets({})).secrets.find((s) => s.name === name);
	expect((await listed())?.updatedBy).toBe(alice.id);
	await bob.settings.setTeamSecret({ name, value: `${value}-bob` });
	expect((await listed())?.updatedBy).toBe(bob.id);
	expect(json(await alice.settings.listTeamSecrets({}))).not.toContain(value);
	expect((await bob.settings.getSettings({})).secrets.map((s) => s.name)).not.toContain(name);
	await bob.settings.deleteTeamSecret({ name });
	expect(await listed()).toBeUndefined();
});

test('a team secret reaches a teammate’s thread, below their own of the same name, and is redacted; a deleted one is gone on the next start', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const [team, both] = [unique('E2E_TEAM'), unique('E2E_BOTH')];
	const value = `e2e-team-${Date.now()}`;
	await alice.settings.setTeamSecret({ name: team, value });
	await alice.settings.setTeamSecret({ name: both, value: 'e2e-team-loses' });
	await bob.settings.setSecret({ name: both, value: 'e2e-bob-wins' });
	const t = await bob.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	expect(await t.terminal(`echo team-$${team} both-$${both}`)).toContain(`team-${value} both-e2e-bob-wins`);

	await test.step('the value is redacted from what the agent reports', async () => {
		await t.turn(() => t.send(`e2e-echo ${value}`));
		expect((await t.prompts()).at(-1)).toContain('e2e-echo [redacted]');
		expect(json(await t.of('agentText'))).not.toContain(value);
	});
	await test.step('a deleted team secret is gone when the environment next starts', async () => {
		await alice.settings.deleteTeamSecret({ name: team });
		await t.snooze();
		await t.podGone();
		await t.turn(() => t.send('wake up'));
		expect(await t.terminal(`echo team-\${${team}:-none} both-$${both}`)).toContain('team-none both-e2e-bob-wins');
	});
});

test('settings: a teammate replaces the team’s secret and shows as the last to set it, deletes it and adds one', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const name = unique('E2E_TEAM');
	await alice.settings.setTeamSecret({ name, value: 'e2e-alice' });
	const page = await bob.page();
	await page.goto('/settings');
	const team = page.getByRole('region', { name: 'Team secrets' });
	await expect(team.getByText(`set by ${alice.id} · `)).toBeVisible();
	await expect(page.getByRole('region', { name: 'Secrets & variables' }).getByText(name)).toBeHidden(); // not one of Bob's own

	await team.getByRole('button', { name: `Replace ${name}` }).click();
	await team.getByLabel(`New value of ${name}`).fill('e2e-bob');
	await team.getByRole('button', { name: 'Save', exact: true }).click();
	await expect(team.getByText(`set by ${bob.id} · `)).toBeVisible();
	expect((await alice.settings.listTeamSecrets({})).secrets.find((s) => s.name === name)?.updatedBy).toBe(bob.id);

	await team.getByRole('button', { name: `Delete ${name}` }).click();
	await page.getByRole('dialog').getByRole('button', { name: 'Delete secret' }).click();
	await expect(team.getByText(name, { exact: true })).toBeHidden();

	const added = unique('E2E_ADDED');
	await team.getByRole('textbox', { name: 'Name' }).fill(added);
	await team.getByLabel('Value', { exact: true }).fill('x');
	await team.getByRole('button', { name: 'Add', exact: true }).click();
	await expect(team.getByText(added, { exact: true })).toBeVisible();
	await expect(team.getByText(`set by ${bob.id} · `)).toBeVisible();
});
