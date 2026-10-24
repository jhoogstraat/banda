// The thread page in a browser: the chat, its cards, the right pane and the header's actions, as each user sees them.
import { devices, type Page } from '@playwright/test';
import { AutoPushState, PermissionMode } from '../src/lib/gen/banda/v1/banda_pb.js';
import { expect, repo, Status, test, type Thread, type User } from './fixtures.ts';

// Its placeholder (the accessible name) says what a post does, for the owner or a teammate.
const composer = (page: Page) => page.getByRole('textbox', { name: /^(Ask the agent…|Message the team|Reply to |Only )/ });

/** The thread's page as user, once its header shows the thread. */
async function open(user: User, t: Thread, options?: Parameters<User['page']>[0]) {
	const page = await user.page(options);
	await page.goto(`/t/${t.id}`);
	await expect(page.getByRole('heading', { name: (await t.get()).title })).toBeVisible();
	return page;
}

async function post(page: Page, text: string) {
	await composer(page).fill(text);
	await composer(page).press('Enter');
	await expect(composer(page)).toHaveValue('');
}

test('a turn streams in: the working agent, its running tool call, then the folded tool rows with their output and the turn’s cost', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create({ prompt: 'e2e-hold:stream' });
	const page = await open(alice, t);
	await expect(page.getByText('Holding stream')).toBeVisible({ timeout: 90_000 });
	await expect(page.getByText('Agent is working…')).toBeVisible();
	await expect(page.getByRole('button', { name: 'Stop' })).toBeVisible();
	await expect(page.getByRole('button', { name: /wait for stream/ })).toBeVisible();

	await t.release('stream');
	await expect(page.getByText('Created hello1.txt with claude-opus-5-5 · high')).toBeVisible();
	await expect(page.getByText(/Turn finished in \d+\.\ds · \$0\.0100/)).toBeVisible();
	await expect(page.getByText('Agent is working…')).toBeHidden();
	const tools = page.getByRole('button', { name: /^Used 9 tools/ });
	await tools.click();
	await page.getByRole('button', { name: /cat hello1\.txt/ }).click();
	await expect(page.getByText(/\$ cat hello1\.txt\s+hello 1/)).toBeVisible();
});

test('permission and plan cards: the owner allows a call asked before it streamed (B15) and approves a plan; the teammate waits', { tag: '@key' }, async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await alice.threads.permissionSetMode({ threadId: t.id, mode: PermissionMode.ASK });
	const [page, bobPage] = [await open(alice, t), await open(bob, t)];

	await test.step('a call asked for before it streamed: one card with its command; allowed, the call gets its output', async () => {
		await t.send('e2e-ask-first');
		const card = page.getByRole('group', { name: 'Permission for Bash' });
		await expect(card.getByText('$ echo e2e-permitted >> permitted.txt')).toBeVisible();
		await expect(page.getByText('Agent is waiting for an answer')).toBeVisible();
		await expect(bobPage.getByText(`Waiting for ${alice.id} to answer`)).toBeVisible();
		await expect(bobPage.getByRole('button', { name: 'Allow once' })).toHaveCount(0);
		await card.getByRole('button', { name: 'Allow once' }).click();
		await expect(card.getByText(`Allowed once by ${alice.id}`)).toBeVisible();
		await expect(bobPage.getByText(`Allowed once by ${alice.id}`)).toBeVisible();
		await expect(page.getByText('Ran it')).toBeVisible();
		await page.getByRole('button', { name: /echo e2e-permitted/ }).click();
		await expect(page.getByText(/\$ echo e2e-permitted >> permitted\.txt\s+ran/)).toBeVisible();
	});

	await test.step('a plan: only the owner approves it, choosing how the agent works from then on', async () => {
		await alice.threads.permissionSetMode({ threadId: t.id, mode: PermissionMode.PLAN });
		await t.send('e2e-plan');
		await expect(page.getByText('The agent has a plan and asks to start working')).toBeVisible();
		await expect(page.getByText('Write plan.txt')).toBeVisible();
		await expect(bobPage.getByText(`Waiting for ${alice.id} to answer`)).toBeVisible();
		await page.getByRole('button', { name: 'Approve, accept edits' }).click();
		await expect(page.getByText('Plan approved', { exact: true })).toBeVisible();
		await expect(bobPage.getByText(`Plan approved by ${alice.id}, now: accept edits`)).toBeVisible();
		await expect(page.getByRole('button', { name: 'Permissions: Accept edits' })).toBeVisible();
	});
});

test('question, edit and rule cards: bob votes and alice answers; alice edits a command, always allows a narrower rule and takes it back', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const [page, bobPage] = [await open(alice, t), await open(bob, t)];

	await test.step('a question: bob votes, alice sees the vote, picks and types her own, and the agent gets her answers', async () => {
		await t.send('e2e-question');
		const bobCard = bobPage.getByRole('group', { name: 'Question' });
		await bobCard.getByRole('radio', { name: /Postgres/ }).check();
		await bobCard.getByRole('checkbox', { name: /Cache/ }).check();
		await bobCard.getByRole('button', { name: 'Vote' }).click();
		await expect(bobCard.getByText('Your vote is in.')).toBeVisible();
		await expect(bobCard.getByRole('button', { name: 'Send answers' })).toHaveCount(0);
		const card = page.getByRole('group', { name: 'Question' });
		await expect(card.getByRole('radio', { name: new RegExp(`Postgres.*${bob.id}`) })).toBeVisible();
		await expect(card.getByRole('button', { name: 'Send answers' })).toBeDisabled();
		await card.getByRole('radio', { name: /SQLite/ }).check();
		await card.getByRole('checkbox', { name: /Search/ }).check();
		await card.getByRole('textbox', { name: 'Other answer: Extras' }).fill('Queues');
		await card.getByRole('button', { name: 'Send answers' }).click();
		await expect(page.getByText('Answers: {"Which database?":"SQLite","Which extras?":"Search, Queues"}')).toBeVisible();
		await expect(bobCard.getByText(`Answered by ${alice.id}`)).toBeVisible();
		await expect(bobCard.getByText('Search, Queues')).toBeVisible();
	});

	await alice.threads.permissionSetMode({ threadId: t.id, mode: PermissionMode.ASK });
	await test.step('alice edits a command before allowing it; bob sees what ran', async () => {
		await t.send('e2e-ask edit');
		const card = page.getByRole('group', { name: 'Permission for Bash' });
		await expect(bobPage.getByRole('button', { name: 'Edit' })).toHaveCount(0);
		await card.getByRole('button', { name: 'Edit' }).click();
		await card.getByRole('textbox', { name: 'Command' }).fill('echo e2e-edited >> permitted.txt');
		await card.getByRole('button', { name: 'Allow with changes' }).click();
		await expect(bobPage.getByText(`Allowed once by ${alice.id}, with changes`)).toBeVisible();
		await expect(bobPage.getByText('$ echo e2e-edited >> permitted.txt')).toBeVisible();
	});

	await test.step('alice always allows "touch …", listed under Allowed, and takes it back', async () => {
		await t.send(`e2e-ask e2e-cmd:${Buffer.from('touch a.txt').toString('base64')}`);
		await page.getByRole('group', { name: 'Permission for Bash' }).last().getByRole('button', { name: 'Always allow touch …' }).click();
		await expect(page.getByText(`touch … allowed in this thread by ${alice.id}`)).toBeVisible();
		await bobPage.getByRole('button', { name: 'Allowed: 1' }).click();
		await expect(bobPage.getByRole('menuitem', { name: 'Stop allowing touch …' })).toBeDisabled();
		await bobPage.keyboard.press('Escape');
		await page.getByRole('button', { name: 'Allowed: 1' }).click();
		await page.getByRole('menuitem', { name: 'Stop allowing touch …' }).click();
		await expect(bobPage.getByText(`${alice.id} no longer always allows touch …`)).toBeVisible();
		await expect(page.getByRole('button', { name: 'Allowed: 1' })).toHaveCount(0);
	});
});

test('Claude Code retrying an overloaded API is one calm line above the composer until output resumes, never stored', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const page = await open(alice, t);
	await post(page, 'e2e-retry:overload');
	await expect(page.getByText('Anthropic is overloaded · retrying 3/10')).toBeVisible();
	await t.release('overload');
	await expect(page.getByText('Created hello2.txt')).toBeVisible();
	await expect(page.getByText(/retrying/)).toHaveCount(0);
	expect(await t.of('agentRetrying')).toHaveLength(0);
});

test('compacting the agent’s context (/compact) is one quiet stored line in the chat', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const page = await open(alice, t);
	await post(page, '/compact');
	await expect(page.getByText('Context compacted: the agent now works from a summary of the conversation before this point.')).toBeVisible();
	expect((await t.of('notice')).map((n) => n.text)).toContainEqual(expect.stringMatching(/^Context compacted:/));
});

test('the owner’s composer offers Claude Code’s slash commands after a "/" and sends the choice as text; a teammate’s doesn’t', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const page = await open(alice, t);
	await composer(page).pressSequentially('/com');
	const option = page.getByRole('option', { name: /\/compact/ });
	await expect(option).toContainText('Free up context by summarizing the conversation so far');
	await expect(page.getByRole('option', { name: /\/init/ })).toHaveCount(0);
	await composer(page).press('Enter');
	await expect(composer(page)).toHaveValue('/compact ');
	await composer(page).press('Enter');
	await expect(page.getByText(/^Context compacted:/)).toBeVisible();

	const bobPage = await open(bob, t);
	await composer(bobPage).pressSequentially('/com');
	await expect(bobPage.getByRole('listbox')).toHaveCount(0);
});

test('the agent’s task list is the plan pinned above the composer while a task is open', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const page = await open(alice, t);
	await post(page, 'e2e-tasks');
	await expect(page.getByText('Half done')).toBeVisible();
	// The CLI keeps the first turn's two done tasks in the list.
	await expect(page.getByRole('button', { name: 'Plan 3/4 done' })).toBeVisible();
	await expect(page.getByRole('listitem').filter({ hasText: 'Writing the fix' })).toBeVisible(); // the task in progress
	await expect(page.getByRole('listitem').filter({ hasText: 'Read the code' })).toBeVisible();
});

test('the agent entering plan mode itself shows in the header and the chat', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const page = await open(alice, t);
	await post(page, 'e2e-enter-plan');
	await expect(page.getByText('The agent switched to Plan')).toBeVisible();
	await expect(page.getByRole('button', { name: 'Permissions: Plan' })).toBeVisible();
});

test('turn requests: bob asks the agent and waits; alice approves one, which runs, and declines another with a note', async ({ as }) => {
	const [alice, bob] = [await as('alice').apiKey(), as('bob')];
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const [page, bobPage] = [await open(alice, t), await open(bob, t)];

	await post(bobPage, '@agent e2e: from bob');
	await expect(bobPage.getByText(`Waiting for ${alice.id} to approve`)).toBeVisible();
	await expect(page.getByText('asks the agent')).toBeVisible();
	// The card is compact: the API key note is behind an info button, the decline note behind its own.
	await expect(page.getByRole('button', { name: 'Runs on your API key' })).toBeVisible();
	await expect(page.getByText(/in this thread's environment/)).toBeHidden();
	await expect(page.getByRole('textbox', { name: 'Decline note' })).toBeHidden();
	await expect(page.getByRole('button', { name: /^Approve all/ })).toBeHidden();
	await page.getByRole('button', { name: 'Approve', exact: true }).click();
	await expect(bobPage.getByText(`Approved by ${alice.id}`)).toBeVisible();
	await expect(bobPage.getByText('Created hello2.txt')).toBeVisible({ timeout: 60_000 });

	await post(bobPage, '@agent not this one');
	await page.getByRole('button', { name: 'Add a note to the decline' }).click();
	await page.getByRole('textbox', { name: 'Decline note' }).fill('not now');
	await page.getByRole('button', { name: 'Decline', exact: true }).click();
	await expect(bobPage.getByText(`Declined by ${alice.id}: not now`)).toBeVisible();
	expect(await t.of('turnEnded')).toHaveLength(2);
});

test('turn requests: with several waiting, alice approves them all at once, in the order they were posted', async ({ as }) => {
	const [alice, bob] = [await as('alice').apiKey(), as('bob')];
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const [page, bobPage] = [await open(alice, t), await open(bob, t)];
	for (const n of [1, 2, 3]) await post(bobPage, `@agent e2e: request ${n}`);
	await expect(page.getByRole('button', { name: 'Approve', exact: true })).toHaveCount(3);
	await page.getByRole('button', { name: 'Approve all (3)' }).click();
	await expect(bobPage.getByText(`Approved by ${alice.id}`)).toHaveCount(3);
	await expect(page.getByRole('button', { name: /^Approve/ })).toHaveCount(0);
	const requests = (await t.events()).filter((e) => e.kind.case === 'turnRequest').map((e) => e.seq);
	const resolved = await t.of('turnRequestResolved');
	expect(resolved.map((r) => [r.requestSeq, r.approved])).toEqual(requests.map((s) => [s, true]));
});

test('review: alice comments on a line of the diff and sends the review; the chat shows it and the agent addresses it', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const page = await open(alice, t);
	await page.getByRole('button', { name: 'View diff' }).click(); // the turn that changed it links the diff
	const file = page.locator('[data-file="hello1.txt"]');
	await file.getByRole('button', { name: 'Comment on line 1' }).click();
	await page.getByPlaceholder('Comment on this line').fill('e2e: say hello back');
	await page.getByRole('button', { name: 'Add to review' }).click();
	await expect(page.getByText('1 pending comment')).toBeVisible();
	await page.getByRole('button', { name: 'Send review' }).click();
	await expect(page.getByText('1 pending comment')).toBeHidden();
	await expect(page.getByRole('button', { name: 'hello1.txt:1' })).toBeVisible(); // the review card in the chat
	await expect(file.getByText('e2e: say hello back')).toBeVisible(); // and posted at its line
	await expect(page.getByText('Created hello2.txt')).toBeVisible({ timeout: 60_000 });
});

test('the right pane: the files tab reads the checkout, the owner’s terminal runs commands, the browser tab frames the agent’s dev server', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const t = await alice.create({ prompt: 'start the preview' });
	await t.waitForStatus(Status.AWAITING_INPUT);
	await t.turn(() => t.send('e2e: a file'));
	// Before the page opens: a port that starts listening while it is open opens the pane by itself.
	await t.waitForEvent((e) => e.kind.case === 'portOpened', 'portOpened');
	const page = await open(alice, t);
	await page.getByRole('button', { name: 'Toggle right pane' }).click();

	await test.step('files', async () => {
		await page.getByRole('tab', { name: 'Files' }).click();
		const files = page.getByRole('tabpanel', { name: 'Files' });
		await files.getByRole('button', { name: 'hello1.txt', exact: true }).click();
		await expect(files).toContainText('hello 1');
		await files.getByRole('button', { name: 'Back to the files' }).click();
		await expect(files.getByRole('button', { name: 'README', exact: true })).toBeVisible();
	});
	await test.step('terminal', async () => {
		await page.getByRole('tab', { name: 'Terminal' }).click();
		const term = page.locator('.xterm');
		await expect(term).toContainText('$', { timeout: 30_000 }); // the prompt
		await term.click();
		await page.keyboard.type('echo e2e-$((6*7))\n');
		await expect(term).toContainText('e2e-42');
	});
	await test.step('browser', async () => {
		await page.getByRole('tab', { name: 'Browser' }).click();
		const frame = page.frameLocator('iframe[title="Preview of port 5173"]');
		await expect(frame.getByText('e2e-preview-page')).toBeVisible();
	});
	await test.step('a teammate gets no terminal', async () => {
		const bobPage = await open(bob, t);
		await bobPage.getByRole('button', { name: 'Toggle right pane' }).click();
		await bobPage.getByRole('tab', { name: 'Terminal' }).click();
		await expect(bobPage.getByText('Only the thread owner can open the terminal.')).toBeVisible();
	});
});

test('the model chip switches the model; a switch that fails to save goes back to the saved model (B19)', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const page = await open(alice, t);
	await page.getByRole('button', { name: 'Model: claude-opus-5-5' }).click();
	await page.getByRole('menuitemradio', { name: 'claude-sonnet-5-5' }).click();
	await expect(page.getByText(`${alice.id} switched to claude-sonnet-5-5 · high`)).toBeVisible();
	expect((await t.get()).model).toBe('claude-sonnet-5-5');

	await page.route('**/banda.v1.ThreadService/SetAgentSettings', (route) => route.abort());
	await page.getByRole('button', { name: 'Model: claude-sonnet-5-5' }).click();
	await page.getByRole('menuitemradio', { name: 'claude-haiku-5-5' }).click();
	await expect(page.getByRole('alert')).toBeVisible();
	await expect(page.getByRole('button', { name: 'Model: claude-sonnet-5-5' })).toBeVisible();
	expect((await t.get()).model).toBe('claude-sonnet-5-5');
});

test('settle and snooze from the header’s menu; the sidebar files the thread under Settled, then Snoozed', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const page = await open(alice, t);
	const title = (await t.get()).title;
	const section = (name: string) => page.getByRole('group', { name, exact: true });
	const menu = (item: string | RegExp) => page.getByRole('button', { name: 'More actions' }).click().then(() => page.getByRole('menuitem', { name: item }));

	await (await menu(/^Settle/)).click();
	await section('Settled').locator('summary').click();
	await expect(section('Settled').getByRole('link', { name: title })).toBeVisible();
	await (await menu(/^Unsettle/)).click();
	await expect(section('Settled')).toBeHidden();

	await (await menu('Snooze')).click();
	await page.getByRole('menuitem', { name: '1 hour' }).click();
	await page.getByRole('button', { name: 'More actions' }).click();
	await expect(page.getByRole('menuitem', { name: /^Snoozed until / })).toBeVisible();
	await page.keyboard.press('Escape');
	await section('Snoozed').locator('summary').click();
	await expect(section('Snoozed').getByRole('link', { name: title })).toBeVisible();
});

// With a token the owner gets "Create a pull request" (forge.spec.ts); without one, the link to the forge.
test('the pull request button replaces Push once the agent’s work is pushed, without a reload (B17)', async ({ as }) => {
	const alice = as('alice');
	await alice.settings.setSecret({ name: 'GIT_TOKEN', value: 'e2e' }); // https remotes push only with a token
	const t = await alice.create({ repoUrl: 'https://github.com/e2e/plain.git', prompt: 'e2e: commit' });
	await t.holdPush(); // so the push ends after the page's refreshes for the turn
	const page = await open(alice, t);
	await expect(page.getByText('Created hello1.txt')).toBeVisible({ timeout: 90_000 });
	await t.waitForStatus(Status.AWAITING_INPUT);
	await page.waitForTimeout(1000);
	await expect(page.getByRole('button', { name: 'Push the branch to origin' })).toBeVisible();
	await t.releasePush();
	await expect(page.getByText(/Pushed banda\/\w+ to origin\./)).toBeVisible();
	await expect(page.getByRole('button', { name: 'Create a pull request' })).toBeVisible();
});

test('a thread page whose first loads fail (the thread, who is signed in) shows the thread to its owner once retries succeed (B21)', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const page = await alice.page();
	// Longer than the replay's refresh: only a retry brings the thread. From the first load, however long
	// the page takes to start (over 3 s on Actions).
	let [down, failed] = [0, 0];
	await page.route(/\/banda\.v1\.(ThreadService\/GetThread|UserService\/Me)$/, (route) => {
		down ||= Date.now() + 3000;
		return Date.now() < down ? (failed++, route.fulfill({ status: 503, body: 'down' })) : route.fallback();
	});
	await page.goto(`/t/${t.id}`);
	await expect(page.getByText('Created hello1.txt')).toBeVisible();
	await expect(page.getByRole('heading', { name: (await t.get()).title })).toBeVisible();
	await expect(page.getByRole('button', { name: 'Model: claude-opus-5-5' })).toBeVisible();
	expect(failed).toBeGreaterThan(0);
});

test('a thread that doesn’t exist says so, with no composer to post into (B21), no empty thread or pane, and a way home', async ({ as }) => {
	const page = await as('alice').page();
	await page.goto('/t/nosuchthread');
	await expect(page.getByRole('heading', { name: 'Thread not found' })).toBeVisible();
	await expect(composer(page)).toHaveCount(0);
	await expect(page.getByText('reconnecting…')).toBeHidden();
	await expect(page.getByText('Untitled thread')).toBeHidden();
	await expect(page.getByText('No messages yet')).toBeHidden();
	await expect(page.getByText(/not_found/)).toBeHidden();
	await expect(page.getByRole('tab', { name: 'Diff' })).toHaveCount(0);
	await page.getByRole('link', { name: 'Go to your threads' }).click();
	await expect(page).toHaveURL('/');
});

test('a thread that fails to load says why and loads on Try again', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const page = await alice.page();
	const getThread = /\/banda\.v1\.ThreadService\/GetThread$/;
	await page.route(getThread, (route) => route.fulfill({ status: 503, body: 'down' }));
	await page.goto(`/t/${t.id}`);
	await expect(page.getByRole('heading', { name: "Couldn't load this thread" })).toBeVisible();
	await expect(composer(page)).toHaveCount(0);
	await page.unroute(getThread);
	// The page also tries again by itself every 2 s, and on a slow machine that can come first and take the button away.
	await page.getByRole('button', { name: 'Try again' }).click({ timeout: 2000 }).catch(() => {});
	await expect(page.getByRole('heading', { name: (await t.get()).title })).toBeVisible();
	await expect(composer(page)).toBeVisible();
});

test('an errored thread says which setup step failed with the end of its output; alice retries or opens the terminal, bob is told who can fix it', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const t = await alice.create({ repoUrl: repo('setup-fails'), prompt: 'e2e: never runs' });
	await t.waitForStatus(Status.ERRORED, 120_000);
	const step = (await t.of('setupStep')).find((s) => s.output.includes('setup sees'))!;
	const [page, bobPage] = [await open(alice, t), await open(bob, t)];
	const card = page.getByRole('alert').filter({ hasText: 'Setup failed' });
	await expect(card).toContainText(`Setup failed: ${step.title}`);
	await expect(card.getByLabel(`Output of ${step.title}`)).toContainText(/setup sees[\s\S]*exit status 7/);
	await expect(card.getByRole('button', { name: 'Retry' })).toBeVisible();
	await card.getByRole('button', { name: 'Open terminal' }).click();
	await expect(page.getByRole('tab', { name: 'Terminal' })).toHaveAttribute('aria-selected', 'true');

	const bobCard = bobPage.getByRole('alert').filter({ hasText: 'Setup failed' });
	await expect(bobCard).toContainText(`Only ${alice.id} can retry this thread.`);
	await expect(bobCard.getByRole('button')).toHaveCount(0);
});

test('a thread that can’t push tells only its owner, compactly, with a link to add GIT_TOKEN in Settings', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const t = await alice.create({ repoUrl: 'https://github.com/e2e/plain.git' }); // https pushes only with a token
	await t.waitFor('unpushable', (i) => i.autoPush === AutoPushState.UNAVAILABLE);
	const [page, bobPage] = [await open(alice, t), await open(bob, t)];
	await expect(page.getByText('Created hello1.txt')).toBeVisible();
	await expect(bobPage.getByText('Created hello1.txt')).toBeVisible();
	const add = page.getByRole('link', { name: 'Add GIT_TOKEN in Settings' });
	await expect(add).toHaveCount(1);
	await expect(bobPage.getByText(/GIT_TOKEN/)).toHaveCount(0);
	await add.click();
	await expect(page).toHaveURL(/\/settings\?secret=GIT_TOKEN#secrets$/);
	await expect(page.getByRole('region', { name: 'Secrets & variables' }).getByRole('textbox', { name: 'Name' })).toHaveValue('GIT_TOKEN');
});

test('phone: the sidebar is a drawer that lists the thread and closes once one is opened', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create({ repoUrl: repo(), prompt: 'e2e: on the phone' });
	await t.waitForStatus(Status.AWAITING_INPUT);
	const { defaultBrowserType: _, ...phone } = devices['iPhone 13'];
	const page = await alice.page(phone);
	await page.goto('/');
	await page.getByRole('button', { name: 'Toggle Sidebar' }).click();
	const drawer = page.getByRole('dialog');
	await drawer.getByRole('link', { name: /e2e: on the phone/ }).click();
	await expect(page).toHaveURL(`/t/${t.id}`);
	await expect(drawer).toBeHidden();
	await expect(page.getByText('Created hello1.txt')).toBeVisible();
	await page.getByRole('button', { name: 'Toggle right pane' }).click();
	await expect(page.getByRole('tab', { name: 'Diff' })).toBeVisible();
	await page.getByRole('button', { name: 'Close the pane' }).click();
	await expect(composer(page)).toBeVisible();
});

test('the header keeps the title in view, on a phone too; the rest is in a labelled menu with shortcuts, the trust switch included', async ({ as }) => {
	const alice = await as('alice').apiKey();
	const t = await alice.create({ prompt: 'e2e: a long title that would never fit next to a row of a dozen icon buttons on a phone' });
	await t.waitForStatus(Status.AWAITING_INPUT);
	const { defaultBrowserType: _, ...phone } = devices['iPhone 13'];
	const page = await open(alice, t, phone);
	const width = page.viewportSize()!.width;
	expect((await page.getByRole('heading', { level: 1 }).boundingBox())!.width).toBeGreaterThan(100);
	for (const control of await page.locator('header').getByRole('button').all()) {
		const box = (await control.boundingBox())!;
		expect(box.x + box.width).toBeLessThanOrEqual(width);
	}

	await page.getByRole('button', { name: 'More actions' }).click();
	await expect(page.getByRole('menuitem', { name: /^Fork: try another way/ })).toContainText('F');
	await expect(page.getByRole('menuitem', { name: /^Spawn a sub-thread/ })).toContainText('B');
	await expect(page.getByRole('menuitem', { name: /^Switch to chat mode/ })).toContainText('M');
	const trust = page.getByRole('menuitemcheckbox', { name: /^Teammates can run the agent/ });
	await expect(trust).toHaveAttribute('aria-checked', 'false');
	await trust.click();
	await expect.poll(async () => (await t.get()).trusted).toBe(true);
	await expect(trust).toHaveAttribute('aria-checked', 'true');
});

test('on a subscription the teammate’s composer says only the owner runs the agent and points to Fork; the owner’s menu has no trust switch', async ({ as }) => {
	const [alice, bob] = [as('alice'), as('bob')];
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const [page, bobPage] = [await open(alice, t), await open(bob, t)];
	await expect(bobPage.getByPlaceholder(`Only ${alice.id} runs the agent (their Claude subscription). Fork to run it on yours.`)).toBeVisible();
	await page.getByRole('button', { name: 'More actions' }).click();
	await expect(page.getByRole('menuitem', { name: /^Switch to chat mode/ })).toBeVisible();
	await expect(page.getByRole('menuitemcheckbox', { name: /^Teammates can run the agent/ })).toHaveCount(0);
});

test('a teammate’s post switches to chat mode: the chat says who and why, the owner’s mode chip switches back, placeholders say what a post does', async ({ as }) => {
	const [alice, bob] = [await as('alice').apiKey(), as('bob')];
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const [page, bobPage] = [await open(alice, t), await open(bob, t)];
	await expect(page.getByPlaceholder('Ask the agent…')).toBeVisible();
	await expect(bobPage.getByPlaceholder(`Reply to ${alice.id} — @agent to request a turn`)).toBeVisible();

	await post(bobPage, 'e2e: nice work');
	await expect(page.getByText(`Chat mode: ${bob.id} posted, so the agent answers only @agent`)).toBeVisible();
	await expect(page.getByPlaceholder('Message the team — @agent to ask the agent')).toBeVisible();
	await expect(bobPage.getByRole('button', { name: 'Chat mode', exact: true })).toBeDisabled();

	await page.getByRole('button', { name: 'Chat mode: switch to agent mode' }).click();
	await expect(page.getByText(`${alice.id} switched back to agent mode`)).toBeVisible();
	await expect(page.getByPlaceholder('Ask the agent…')).toBeVisible();
	await expect(bobPage.getByRole('button', { name: 'Agent mode', exact: true })).toBeVisible();
});

test('the right pane stays closed until asked for, resizes by its divider (remembered), and opens on the first dev server; the chat links preview and diff', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT);
	const page = await open(alice, t);
	await expect(page.getByText('Created hello1.txt')).toBeVisible();
	const tab = (name: string) => page.getByRole('tab', { name });
	await expect(tab('Diff')).toBeHidden();

	await test.step('the divider resizes the pane, by keyboard and by dragging; the width survives a reload', async () => {
		await page.keyboard.press('p'); // the pane's shortcut
		const divider = page.getByRole('separator', { name: 'Resize the pane' });
		const width = Number(await divider.getAttribute('aria-valuenow'));
		await divider.focus();
		await page.keyboard.press('ArrowLeft');
		await expect(divider).toHaveAttribute('aria-valuenow', String(width + 24));
		const box = (await divider.boundingBox())!;
		await page.mouse.move(box.x + 2, box.y + 200);
		await page.mouse.down();
		await page.mouse.move(box.x - 98, box.y + 200, { steps: 5 });
		await page.mouse.up();
		await expect.poll(async () => Number(await divider.getAttribute('aria-valuenow'))).toBeGreaterThan(width + 100);
		const dragged = await divider.getAttribute('aria-valuenow');
		await page.reload();
		await expect(tab('Diff')).toBeHidden();
		await page.getByRole('button', { name: 'Toggle right pane' }).click();
		await expect(divider).toHaveAttribute('aria-valuenow', dragged!);
		await page.getByRole('button', { name: 'Toggle right pane' }).click();
		await expect(tab('Diff')).toBeHidden();
	});

	await test.step('a dev server starting opens the Browser tab on it; the chat links it and the diff', async () => {
		await t.send('start the preview');
		await expect(tab('Browser')).toHaveAttribute('aria-selected', 'true', { timeout: 60_000 });
		await expect(page.frameLocator('iframe[title="Preview of port 5173"]').getByText('e2e-preview-page')).toBeVisible();
		await expect(page.getByText('http.server is listening on :5173')).toBeVisible();
		await page.getByRole('button', { name: 'View diff' }).click();
		await expect(tab('Diff')).toHaveAttribute('aria-selected', 'true');
		await page.getByRole('button', { name: 'Open preview' }).click();
		await expect(tab('Browser')).toHaveAttribute('aria-selected', 'true');
	});
});
