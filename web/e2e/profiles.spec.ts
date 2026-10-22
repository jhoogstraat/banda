// Agent profiles: the text appended to the agent's system prompt, chosen per thread (the owner's default from
// Settings unless named), switched mid-thread by the owner, replaced by a repo's .banda/profiles/<name>.md.
// The fake agent names the first line of the profile text it was started with in its reply.
import { readFileSync } from 'node:fs';
import type { Page } from '@playwright/test';
import { expect, refused, repo, Status, test, type Thread, type User } from './fixtures.ts';

/** The first line of a built-in profile's text (cmd/runner/profiles). */
const builtin = (name: string) => readFileSync(new URL(`../../cmd/runner/profiles/${name}.md`, import.meta.url), 'utf8').split('\n')[0];

test('a thread runs with the profile it was created with; the engineer profile adds nothing; unknown profiles are refused', async ({ as }) => {
	const alice = as('alice');
	await refused(alice.create({ profile: 'manager' }));
	await refused(alice.threads.race({ repoUrl: repo(), prompt: 'e2e', tries: [{}, {}], profile: 'manager' }));
	const [spec, engineer] = [await alice.create({ profile: 'spec' }), await alice.create()];
	expect((await spec.get()).profile).toBe('spec');
	expect((await engineer.get()).profile).toBe('engineer');
	await spec.waitForStatus(Status.AWAITING_INPUT);
	expect(await spec.reply()).toBe(`Created hello1.txt with claude-opus-5-5 · high · ${builtin('spec')}`);
	await engineer.waitForStatus(Status.AWAITING_INPUT);
	expect(await engineer.reply()).toBe('Created hello1.txt with claude-opus-5-5 · high');
});

test('new threads that name no profile get the owner’s default from Settings', async ({ as }) => {
	const alice = as('alice');
	expect((await alice.settings.getSettings({})).defaultProfile).toBe('engineer');
	await refused(alice.settings.setDefaultProfile({ profile: '' }));
	await alice.settings.setDefaultProfile({ profile: 'devops' });
	expect((await alice.settings.getSettings({})).defaultProfile).toBe('devops');
	expect((await as('bob').settings.getSettings({})).defaultProfile).toBe('engineer');
	const t = await alice.create();
	expect((await t.get()).profile).toBe('devops');
	await t.waitForStatus(Status.AWAITING_INPUT);
	expect(await t.reply()).toContain(`· ${builtin('devops')}`);
});

test('the owner switches the profile mid-turn: the turn finishes as it was, the next one runs with the new text, the chat records it; a teammate can’t switch', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await refused(t.as(bob).user.threads.setAgentSettings({ threadId: t.id, profile: 'business' }));
	await refused(alice.threads.setAgentSettings({ threadId: t.id, profile: 'manager' }));
	await t.send('keep going e2e-hold:profile');
	await t.waitForEvent((e) => e.kind.case === 'agentText' && e.kind.value.text === 'Holding profile', 'the held turn');
	await alice.threads.setAgentSettings({ threadId: t.id, profile: 'business' });
	expect((await t.get()).profile).toBe('business');
	await t.turn(() => t.release('profile'));
	expect(await t.reply()).toBe('Created hello2.txt with claude-opus-5-5 · high');
	await t.turn(() => t.send('and again'));
	expect(await t.reply()).toBe(`Created hello3.txt with claude-opus-5-5 · high · ${builtin('business')}`);
	// The event says only what changed: the profile, not the model and effort.
	const changes = (await t.of('agentSettings')).map((s) => [s.model, s.effort, s.profile]);
	expect(changes).toEqual([['', '', 'business']]);
	expect((await t.events()).find((e) => e.kind.case === 'agentSettings')?.actor).toBe(alice.id);
});

test('a repo’s .banda/profiles/<name>.md replaces that built-in’s text, and only that one', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create({ repoUrl: repo('profiles'), profile: 'spec' });
	await t.waitForStatus(Status.AWAITING_INPUT);
	expect(await t.reply()).toContain('· Spec as the e2e repo wants it');
	await alice.threads.setAgentSettings({ threadId: t.id, profile: 'devops' });
	await t.turn(() => t.send('and again'));
	expect(await t.reply()).toContain(`· ${builtin('devops')}`);
});

test('sub-threads (people’s and agents’), forks and races keep the profile; read_thread shows it', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const parent = await alice.create({ profile: 'spec' });
	await parent.waitForStatus(Status.AWAITING_INPUT);
	const spawned = (await bob.threads.spawnThread({ parentId: parent.id, prompt: 'e2e: bob’s sub-thread' })).thread!;
	expect(spawned.profile).toBe('spec');
	expect((await bob.threads.spawnThread({ parentId: parent.id, prompt: 'e2e: bob’s business', profile: 'business' })).thread!.profile).toBe('business');
	await refused(bob.threads.spawnThread({ parentId: parent.id, prompt: 'e2e', profile: 'nobody' }));
	const out = await parent.mcp('create_thread', { prompt: 'e2e: an agent’s sub-thread' });
	const child = alice.thread(out.match(/thread_id[^a-z0-9]+([a-z0-9]{26})/)![1]);
	expect((await child.get()).profile).toBe('spec');
	const engineer = await parent.mcp('create_thread', { prompt: 'e2e: an engineer', profile: 'engineer' });
	expect((await alice.thread(engineer.match(/thread_id[^a-z0-9]+([a-z0-9]{26})/)![1]).get()).profile).toBe('engineer');
	expect(await parent.mcp('read_thread', { thread_id: child.id })).toContain('profile: spec');

	const [fork] = (await bob.threads.fork({ threadId: parent.id })).threads;
	expect(fork.profile).toBe('spec');
	const race = (await bob.threads.race({ repoUrl: repo(), prompt: 'e2e', tries: [{}, {}], profile: 'business' })).threads;
	expect(race.map((r) => r.profile)).toEqual(['business', 'business']);
});

/** The Profile chip of the page's composer. */
const chip = (page: Page, label: string) => page.getByRole('button', { name: `Profile: ${label}` });

async function open(user: User, t: Thread) {
	const page = await user.page();
	await page.goto(`/t/${t.id}`);
	await expect(page.getByRole('heading', { name: (await t.get()).title })).toBeVisible();
	return page;
}

test('browser: the new-thread composer’s Profile chip starts at the default from Settings; in a thread the owner switches it and teammates see it', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const page = await alice.page();
	await page.goto('/settings');
	await page.getByRole('radio', { name: /^Specification/ }).check();
	await expect.poll(async () => (await alice.settings.getSettings({})).defaultProfile).toBe('spec');

	await page.goto('/');
	await expect(chip(page, 'Specification')).toBeVisible();
	await page.getByRole('button', { name: 'Choose repository' }).click();
	await page.getByLabel('Repository URL').fill(repo());
	await page.getByRole('button', { name: 'Use' }).click();
	await chip(page, 'Specification').click();
	await page.getByRole('menuitemradio', { name: 'Business' }).click();
	await page.getByPlaceholder('Describe the task.').fill('e2e: a business thread');
	await page.keyboard.press('Enter');
	await expect(page).toHaveURL(/\/t\//);
	const t = alice.thread(page.url().split('/t/')[1]);
	expect((await t.get()).profile).toBe('business');
	await t.waitForStatus(Status.AWAITING_INPUT);

	await chip(page, 'Business').click();
	await page.getByRole('menuitemradio', { name: 'DevOps' }).click();
	await expect(page.getByText(`${alice.id} switched to DevOps`)).toBeVisible();
	expect((await t.get()).profile).toBe('devops');

	const bobPage = await open(bob, t);
	await expect(bobPage.getByText(`${alice.id} switched to DevOps`)).toBeVisible();
	await expect(bobPage.getByText('DevOps', { exact: true })).toBeVisible();
	await expect(chip(bobPage, 'DevOps')).toHaveCount(0);
});

test('browser: the spawn dialog’s Profile chip starts at the parent’s and picks the sub-thread’s', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const parent = await alice.create({ profile: 'spec' });
	await parent.waitForStatus(Status.AWAITING_INPUT);
	const page = await open(bob, parent);
	await page.getByRole('button', { name: 'More actions' }).click();
	await page.getByRole('menuitem', { name: /^Spawn a sub-thread/ }).click();
	const dialog = page.getByRole('dialog');
	await dialog.getByRole('button', { name: 'Profile: Specification' }).click();
	await page.getByRole('menuitemradio', { name: 'Business' }).click();
	await dialog.getByPlaceholder('What should it work on?').fill('e2e: a business sub-thread');
	await dialog.getByRole('button', { name: 'Spawn' }).click();
	await expect(page).toHaveURL(/\/t\//);
	await expect(page).not.toHaveURL(`/t/${parent.id}`);
	expect((await bob.thread(page.url().split('/t/')[1]).get()).profile).toBe('business');
});
