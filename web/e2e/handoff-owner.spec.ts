// Handing a thread to a teammate (HandoffOffer); handoff.spec.ts is the Specification agent's handoff to an Engineer.
// Handing a thread to a teammate: the offer and briefing, accepting (the environment restarts as theirs),
// declining, cancelling, and who may do what.
import { expect, kube, mcpDirective, refused, Status, test, userHash, type User } from './fixtures.ts';

/** A teammate who has signed in once, so others can hand them threads. */
const teammate = async (u: User) => (await u.users.me({}), u);

test('accepting a handoff makes the thread the teammate’s: the next turns run with their Claude volume and secrets, in a session that gets the conversation and the briefing', async ({ as }) => {
	const [alice, bob] = [await teammate(as('alice')), await teammate(as('bob'))];
	const secret = `e2e-bob-${Date.now()}`;
	await bob.settings.setSecret({ name: 'E2E_SECRET', value: secret });
	const t = await alice.create({ prompt: 'e2e: build the thing' });
	await t.waitForStatus(Status.AWAITING_INPUT);

	await test.step('the offer asks the idle agent for a briefing, as alice’s turn; bob sees the thread under Mentioned', async () => {
		// The note reaches the briefing prompt: the fake holds that turn, and later echoes bob’s first prompt.
		await alice.threads.handoffOffer({ threadId: t.id, toUserId: bob.id, note: 'tests are next e2e-hold:brief e2e-echo' });
		expect((await t.get()).handoffTo).toBe(bob.id);
		expect((await bob.threads.listMentioned({})).threads.map((x) => x.id)).toContain(t.id);
		const offered = (await t.of('handoffOffered'))[0];
		expect([offered.toUserId, offered.noBriefing]).toEqual([bob.id, '']);
	});

	await test.step('bob can’t accept while the briefing is being written', async () => {
		await t.waitForStatus(Status.WORKING);
		expect(await refused(bob.threads.handoffAccept({ threadId: t.id }))).toMatch(/working/);
		await t.turn(() => t.release('brief'));
		expect((await t.prompts()).at(-1)).toContain(`is handing this thread over to ${bob.id}`);
	});

	await test.step('accepting restarts the environment as bob’s, with a fresh session that gets the conversation and the briefing', async () => {
		const bt = t.as(bob);
		const info = await bt.turn(() => bob.threads.handoffAccept({ threadId: t.id }));
		expect([info.ownerId, info.handoffTo]).toEqual([bob.id, '']);
		const first = (await bt.prompts()).at(-1)!;
		expect(first).toContain(`${alice.id} handed it over to ${bob.id}`);
		expect(first).toContain(`${alice.id}: e2e: build the thing`);
		expect(first).toMatch(new RegExp(`Your briefing for ${bob.id}, written before the handover:\\n\\nReceived: ${alice.id} is handing`));
		const resolved = await t.events().then((es) => es.find((e) => e.kind.case === 'handoffResolved')!);
		expect([resolved.actor, resolved.kind.value]).toMatchObject([bob.id, { accepted: true }]);
	});

	await test.step('the pod mounts bob’s Claude volume and secrets, not alice’s', async () => {
		const spec = JSON.parse(await kube('get', 'pod', t.pod, '-o', 'json')).spec;
		expect(spec.volumes.find((v: { name: string }) => v.name === 'claude').persistentVolumeClaim.claimName).toBe(`claude-${userHash(bob.id)}`);
		const from = spec.containers[0].envFrom.map((e: { secretRef: { name: string } }) => e.secretRef.name);
		expect(from).toContain(`banda-env-${userHash(bob.id)}`);
		expect(from).not.toContain(`banda-env-${userHash(alice.id)}`);
		const env = spec.containers[0].env as { name: string; value?: string }[];
		expect(env.find((e) => e.name === 'BANDA_SESSION_KEY')?.value).toBe('/handoff-1'); // a new Claude Code session
		expect(env.find((e) => e.name === 'BANDA_GIT_NAME')?.value).toBe(bob.id); // commits are bob's now (dev auth: name = id)
		expect(await t.as(bob).terminal('echo secret-$E2E_SECRET')).toContain(`secret-${secret}`);
		await refused(alice.terminal((signal) => alice.threads.openTerminal({ threadId: t.id, cols: 80, rows: 24, command: 'true' }, { signal })));
	});

	await test.step('the thread moves to bob’s list; his posts start turns, alice’s are chat', async () => {
		expect((await alice.threads.listThreads({})).threads.map((x) => x.id)).not.toContain(t.id);
		expect((await bob.threads.listThreads({})).threads.map((x) => x.id)).toContain(t.id);
		expect((await bob.threads.listMentioned({})).threads.map((x) => x.id)).not.toContain(t.id);
		await t.as(bob).turn(() => t.as(bob).send('keep going'));
		expect(await refused(alice.threads.handoffOffer({ threadId: t.id, toUserId: alice.id }))).toMatch(/only the thread owner/);
	});
});

test('a handoff can be declined by the recipient and cancelled by the owner; nobody else hands off, accepts or withdraws', async ({ as }) => {
	const [alice, bob, carol] = [as('alice'), await teammate(as('bob')), await teammate(as('carol'))];
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await t.snooze();
	await t.waitForStatus(Status.SUSPENDED);

	await refused(bob.threads.handoffOffer({ threadId: t.id, toUserId: bob.id }));
	await refused(alice.threads.handoffOffer({ threadId: t.id, toUserId: 'nobody-e2e' }));
	await alice.threads.handoffOffer({ threadId: t.id, toUserId: bob.id });
	expect((await t.of('handoffOffered')).at(-1)!.noBriefing).toBe('the environment is paused');
	expect((await t.get()).status).toBe(Status.SUSPENDED); // no briefing, and it stays asleep
	await refused(alice.threads.handoffOffer({ threadId: t.id, toUserId: carol.id }));
	await refused(carol.threads.handoffAccept({ threadId: t.id }));
	await refused(carol.threads.handoffDecline({ threadId: t.id }));

	await bob.threads.handoffDecline({ threadId: t.id });
	expect((await t.get()).handoffTo).toBe('');
	await refused(bob.threads.handoffAccept({ threadId: t.id }));
	expect((await bob.threads.listMentioned({})).threads.map((x) => x.id)).not.toContain(t.id);

	await alice.threads.handoffOffer({ threadId: t.id, toUserId: carol.id });
	await alice.threads.handoffDecline({ threadId: t.id });
	const resolved = (await t.events()).filter((e) => e.kind.case === 'handoffResolved');
	expect(resolved.map((e) => [e.actor, e.kind.value])).toMatchObject([
		[bob.id, { accepted: false }],
		[alice.id, { accepted: false }]
	]);
	const info = await t.get();
	expect([info.ownerId, info.handoffTo, info.status]).toEqual([alice.id, '', Status.SUSPENDED]);
});

test('in the browser: alice hands off from the header, bob accepts from the card in the chat', async ({ as }) => {
	const [alice, bob] = [as('alice'), await teammate(as('bob'))];
	const t = await alice.create({ prompt: 'e2e: hand me over' });
	await t.waitForStatus(Status.AWAITING_INPUT);

	const page = await alice.page();
	await page.goto(`/t/${t.id}`);
	const menu = (p: typeof page) => p.getByRole('button', { name: 'More actions' }).click().then(() => p.getByRole('menuitem', { name: 'Hand off to a teammate' }));
	await (await menu(page)).click();
	await page.getByRole('combobox', { name: 'Teammate' }).selectOption(bob.id);
	await page.getByRole('textbox', { name: 'Note' }).fill('over to you');
	await page.getByRole('button', { name: 'Hand off', exact: true }).click();
	const card = page.getByRole('group', { name: `Handoff to ${bob.id}` });
	await expect(card.getByText('over to you')).toBeVisible();
	await expect(card.getByText(`Waiting for ${bob.id} to accept`)).toBeVisible();
	await expect(await menu(page)).toHaveCount(0);
	await page.keyboard.press('Escape');

	const bobs = await bob.page();
	await bobs.goto('/');
	await bobs.getByRole('region', { name: 'Mentioned' }).getByRole('link', { name: /e2e: hand me over/ }).click();
	const accept = bobs.getByRole('group', { name: `Handoff to ${bob.id}` }).getByRole('button', { name: 'Accept' });
	await t.waitForStatus(Status.AWAITING_INPUT); // the briefing is written
	await accept.click();
	await expect(card.getByText(`${bob.id} accepted`)).toBeVisible();
	await t.as(bob).waitFor('to be bob’s and idle', (x) => x.ownerId === bob.id && x.status === Status.AWAITING_INPUT);
	await expect(await menu(bobs)).toBeVisible();
});

test('an agent offers its own thread through MCP (handoff_thread): the same card, by the agent, with its briefing; the teammate still accepts and the new session gets the briefing', async ({ as }) => {
	const [alice, bob] = [await teammate(as('alice')), await teammate(as('bob'))];
	const briefing = 'Done: the file. Next: tests. e2e-echo';
	const t = await alice.create({ prompt: mcpDirective('handoff_thread', { to: bob.id, briefing }) });
	await t.waitForStatus(Status.AWAITING_INPUT); // its turn wrote no other briefing
	expect(await t.reply()).toBe(`MCP handoff_thread: Offered this thread to ${bob.id}; they still have to accept it.`);
	const offers = (await t.events()).filter((e) => e.kind.case === 'handoffOffered');
	expect(offers.map((e) => [e.actor, e.kind.value])).toMatchObject([[`agent:${t.id}`, { toUserId: bob.id, note: briefing, noBriefing: '' }]]);
	expect((await t.get()).ownerId).toBe(alice.id);
	expect((await bob.threads.listMentioned({})).threads.map((x) => x.id)).toContain(t.id);
	expect(await t.mcp('handoff_thread', { to: 'nobody-e2e', briefing })).toContain('no such teammate');

	const bt = t.as(bob);
	await bt.turn(() => bob.threads.handoffAccept({ threadId: t.id }));
	expect((await bt.get()).ownerId).toBe(bob.id);
	expect((await bt.prompts()).at(-1)).toContain(`Your briefing for ${bob.id}, written before the handover:\n\n${briefing}`);
});
