// The interface's floor: what holds on every screen (contrast, touch targets, theme, motion) and the layouts that broke.
import { devices, type Page } from '@playwright/test';
import { PermissionMode } from '../src/lib/gen/banda/v1/banda_pb.js';
import { expect, repo, Status, test } from './fixtures.ts';

/** Text under 4.5:1 (3:1 when large) against what is painted behind it, as "ratio size text". */
const lowContrast = (page: Page) =>
	page.evaluate(() => {
		const ctx = document.createElement('canvas').getContext('2d', { willReadFrequently: true })!;
		const rgba = (css: string) => {
			ctx.clearRect(0, 0, 1, 1);
			ctx.fillStyle = css;
			ctx.fillRect(0, 0, 1, 1);
			const d = ctx.getImageData(0, 0, 1, 1).data;
			return [d[0], d[1], d[2], d[3] / 255];
		};
		const over = (top: number[], under: number[]) => [0, 1, 2].map((i) => top[i] * top[3] + under[i] * (1 - top[3])).concat(1);
		const behind = (el: Element) => {
			const layers: number[][] = [];
			for (let e: Element | null = el; e; e = e.parentElement) {
				const c = rgba(getComputedStyle(e).backgroundColor);
				if (c[3]) layers.unshift(c);
				if (c[3] === 1) break;
			}
			return layers.reduce((under, top) => over(top, under), [255, 255, 255, 1]);
		};
		const luminance = (c: number[]) => {
			const [r, g, b] = c.slice(0, 3).map((v) => ((v /= 255) <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
			return 0.2126 * r + 0.7152 * g + 0.0722 * b;
		};
		const low = new Set<string>();
		const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
		for (let n = walker.nextNode(); n; n = walker.nextNode()) {
			const el = n.parentElement!;
			const box = el.getBoundingClientRect();
			const style = getComputedStyle(el);
			if (!n.nodeValue!.trim() || !box.width || box.bottom < 0 || box.top > innerHeight || style.visibility !== 'visible') continue;
			if (el.closest('[disabled], [aria-disabled=true], script, style, .sr-only')) continue;
			let opacity = 1;
			for (let e: Element | null = el; e; e = e.parentElement) opacity *= +getComputedStyle(e).opacity;
			const bg = behind(el);
			const color = rgba(style.color);
			color[3] *= opacity;
			if (!color[3]) continue; // invisible on purpose, until hovered
			const [a, b] = [luminance(over(color, bg)), luminance(bg)];
			const ratio = (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
			if (ratio < (parseFloat(style.fontSize) >= 24 ? 3 : 4.5)) low.add(`${ratio.toFixed(2)} ${style.fontSize} ${n.nodeValue!.trim().slice(0, 40)}`);
		}
		return [...low];
	});

test('all text reads at 4.5:1 in both themes, on the cards, lists and diffs of a busy day', async ({ as }) => {
	test.setTimeout(240_000);
	const alice = as('alice');
	const asking = await alice.create({ prompt: 'Run the check', permissionMode: PermissionMode.ASK });
	await asking.waitForStatus(Status.AWAITING_INPUT);
	asking.send('e2e-ask please').catch(() => {});
	const question = await alice.create({ prompt: 'e2e-question' });
	const { threads: race } = await alice.threads.race({ baseBranch: 'main', repoUrl: 'git://e2e-git/plain.git', prompt: 'Try two ways', tries: [{}, {}] });
	await asking.waitFor('its question', (t) => !!t.permissionPending);
	await question.waitFor('its question', (t) => !!t.permissionPending);
	for (const r of race) await alice.thread(r.id).waitForStatus(Status.AWAITING_INPUT);
	for (const colorScheme of ['dark', 'light'] as const) {
		const page = await alice.page({ colorScheme });
		await page.addInitScript((s) => localStorage.setItem('mode-watcher-mode', s), colorScheme);
		for (const url of [`/t/${asking.id}`, `/t/${question.id}`, '/decisions', `/compare?race=${race[0].raceId}`, '/team', '/settings', '/search']) {
			await page.goto(url);
			await page.waitForTimeout(1500);
			expect(await lowContrast(page), `${colorScheme} ${url}`).toEqual([]);
		}
		await page.close();
	}
});

test('buttons and selects are at least 36px tall on a touch screen', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create({ prompt: 'Run the check', permissionMode: PermissionMode.ASK });
	await t.waitForStatus(Status.AWAITING_INPUT);
	t.send('e2e-ask please').catch(() => {});
	await t.waitFor('its question', (i) => !!i.permissionPending);
	const page = await alice.page(devices['iPhone 13']);
	await page.goto(`/t/${t.id}`);
	await expect(page.getByRole('button', { name: 'Allow once' })).toBeVisible();
	const small = await page.evaluate(() =>
		[...document.querySelectorAll<HTMLElement>('[data-slot=button], [data-slot=native-select]')]
			.map((b) => [b.getBoundingClientRect().height, b.textContent!.trim() || b.getAttribute('aria-label')] as const)
			.filter(([h]) => h && h < 36)
	);
	expect(small).toEqual([]);
	const chips = page.getByRole('button', { name: /^Model:/ }).locator('..');
	expect(await chips.evaluate((el) => el.getBoundingClientRect().height)).toBeLessThan(48); // the composer's chips stay one row

	for (const path of ['/', '/settings', '/team', '/decisions', `/u/${alice.id}`]) {
		await page.goto(path);
		await expect(page.getByRole('button').first()).toBeVisible();
		await page.waitForTimeout(1000); // what each page loads
		const tiny = await page.evaluate(() =>
			[...document.querySelectorAll<HTMLElement>('[data-slot=button], [data-slot=native-select]')]
				.map((b) => [b.getBoundingClientRect().height, b.textContent!.trim() || b.getAttribute('aria-label')] as const)
				.filter(([h]) => h && h < 36)
		);
		expect(tiny, path).toEqual([]);
	}
});

test('a small inline link in the chat is easy to hit on a touch screen', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create({ repoUrl: repo(), prompt: 'e2e: commit' });
	await t.waitForStatus(Status.AWAITING_INPUT);
	const page = await alice.page(devices['iPhone 13']);
	await page.goto(`/t/${t.id}`);
	expect((await page.getByRole('button', { name: 'View diff' }).boundingBox())!.height).toBeGreaterThanOrEqual(36);
});

test('what was typed survives a reload, until it is sent', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create({ prompt: 'Draft here' });
	await t.waitForStatus(Status.AWAITING_INPUT);
	const page = await alice.page();
	await page.goto(`/t/${t.id}`);
	const box = page.getByPlaceholder('Ask the agent…');
	await box.fill('half a thought');
	await page.reload();
	await expect(box).toHaveValue('half a thought');
	await box.press('Enter');
	await expect(page.getByText('half a thought')).toBeVisible();
	await expect(box).toHaveValue('');
	await page.reload();
	await expect(page.getByText('half a thought')).toBeVisible();
	await expect(box).toHaveValue('');

	await page.goto('/');
	const prompt = page.getByPlaceholder('Describe the task.');
	await prompt.fill('a new task');
	await page.reload();
	await expect(prompt).toHaveValue('a new task');
});

test('on a phone Stop sits by the composer', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create({ prompt: 'e2e-hold:stop' });
	await t.waitForStatus(Status.WORKING);
	const page = await alice.page(devices['iPhone 13']);
	await page.goto(`/t/${t.id}`);
	const stop = page.getByRole('button', { name: 'Stop', exact: true });
	const box = (await stop.boundingBox())!;
	expect(box.y).toBeGreaterThan(page.viewportSize()!.height / 2);
	await stop.click();
	await t.waitForStatus(Status.AWAITING_INPUT);
	await expect(stop).toHaveCount(0);
});

test('failures say so instead of looking empty', async ({ as }) => {
	const alice = as('alice');
	const page = await alice.page();
	await page.goto('/no-such-page');
	await expect(page.getByRole('heading', { name: 'Page not found' })).toBeVisible();

	await page.route('**/GetSettings', (route) => route.abort());
	await page.goto('/');
	await expect(page.getByText("Couldn't load your defaults")).toBeVisible();

	await page.route('**/ListThreads', (route) => (route.request().postData()?.includes(alice.id) ? route.abort() : route.continue()));
	await page.goto(`/u/${alice.id}`);
	await expect(page.getByText("Couldn't load the older threads.")).toBeVisible();
	await expect(page.getByText('No threads yet.')).toHaveCount(0);
});

test('coming back to a thread shows what is new since you last looked', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create({ prompt: 'First part' });
	await t.waitForStatus(Status.AWAITING_INPUT);
	const page = await alice.page();
	await page.goto(`/t/${t.id}`);
	const mark = page.getByRole('separator').filter({ hasText: 'New since you last looked' });
	await expect(page.getByRole('heading', { name: 'First part' })).toBeVisible();
	await expect(mark).toHaveCount(0); // the first visit has no last look
	await expect.poll(() => page.evaluate((id) => localStorage.getItem(`banda-seen-${id}`), t.id)).toBeTruthy();
	await page.goto('/');

	await t.send('Second part');
	await t.waitForStatus(Status.AWAITING_INPUT);
	await page.goto(`/t/${t.id}`);
	await expect(mark).toBeInViewport();
	await expect(page.getByText('Second part').first()).toBeVisible();
});

test('a reply copies with one click', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create({ prompt: 'Say something' });
	await t.waitForStatus(Status.AWAITING_INPUT);
	const page = await alice.page({ permissions: ['clipboard-read', 'clipboard-write'] });
	await page.goto(`/t/${t.id}`);
	const copy = page.getByRole('button', { name: 'Copy this reply' }).first();
	await copy.locator('..').hover();
	await copy.click();
	await expect(page.getByText('Copied')).toBeAttached();
	expect(await page.evaluate(() => navigator.clipboard.readText())).not.toBe('');
});

test('the owner renames a thread: everyone sees it, and nobody else can', async ({ as }) => {
	const alice = as('alice');
	const bob = as('bob');
	const t = await alice.create({ prompt: 'A poor title' });
	await t.waitForStatus(Status.AWAITING_INPUT);
	await expect(bob.threads.renameThread({ threadId: t.id, title: 'Taken over' })).rejects.toThrow();
	await expect(alice.threads.renameThread({ threadId: t.id, title: '   ' })).rejects.toThrow();

	const page = await alice.page();
	await page.goto(`/t/${t.id}`);
	const bobPage = await bob.page();
	await bobPage.goto(`/t/${t.id}`);
	await page.getByRole('button', { name: 'More actions' }).click();
	await page.getByRole('menuitem', { name: 'Rename' }).click();
	await page.getByRole('dialog').getByLabel('Title').fill('A better title');
	await page.keyboard.press('Enter');
	await expect(page.getByRole('heading', { name: 'A better title' })).toBeVisible();
	await expect(page.getByText(/renamed this thread to A better title/)).toBeVisible();
	await expect(bobPage.getByRole('heading', { name: 'A better title' })).toBeVisible();
	expect((await t.get()).title).toBe('A better title');
	await bobPage.getByRole('button', { name: 'More actions' }).click();
	await expect(bobPage.getByRole('menuitem', { name: 'Rename' })).toHaveCount(0);
});

test('the theme reaches the browser, and reduced motion stops what repeats but not the spinners', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create({ prompt: 'e2e-hold:motion' });
	await t.waitForStatus(Status.WORKING);
	const page = await alice.page({ colorScheme: 'dark', reducedMotion: 'reduce' });
	await page.goto(`/t/${t.id}`);
	await expect(page.getByText('Agent is working…')).toBeVisible();
	const root = await page.evaluate(() => ({ scheme: getComputedStyle(document.documentElement).colorScheme, bar: document.querySelector('meta[name=theme-color]')?.getAttribute('content') }));
	expect(root).toEqual({ scheme: 'dark', bar: '#0a0a0a' });
	const iterations = (selector: string) => page.locator(selector).first().evaluate((el) => getComputedStyle(el).animationIterationCount);
	expect(await iterations('.animate-spin')).toBe('infinite');
	expect(await iterations('.animate-pulse')).toBe('1');
	await t.release('motion');
});

test('a long thread opens at its end, with no arrow to get there', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create({ prompt: 'A first turn' });
	await t.waitForStatus(Status.AWAITING_INPUT);
	await t.turn(() => t.send(`${'A long message about the work. '.repeat(60)}\n\nThe last line`));
	await t.turn(() => t.send('The very last word'));
	const page = await alice.page(devices['iPhone SE']);
	await page.goto(`/t/${t.id}`);
	await expect(page.getByText('The very last word', { exact: true })).toBeInViewport();
	await page.waitForTimeout(1500);
	await expect(page.getByText('The very last word', { exact: true })).toBeInViewport();
	await expect(page.getByRole('button', { name: 'Scroll to the latest message' })).toHaveCount(0);
});

test('a thread still loading shows no empty thread, and the team no empty person', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create({ prompt: 'Loading slowly' });
	await t.waitForStatus(Status.AWAITING_INPUT);
	const page = await alice.page();
	let release = () => {};
	const held = new Promise<void>((r) => (release = r));
	await page.route('**/GetThread', async (route) => (await held, route.continue()));
	await page.route('**/WatchThread', async (route) => (await held, route.continue()));
	await page.goto(`/t/${t.id}`);
	await expect(page.getByRole('button', { name: 'Toggle right pane' })).toBeVisible();
	await expect(page.getByText('Untitled thread')).toHaveCount(0);
	await expect(page.getByText('No messages yet')).toHaveCount(0);
	await expect(page.getByTestId('chat-skeleton')).toBeVisible();
	release();
	await expect(page.getByRole('heading', { name: 'Loading slowly' })).toBeVisible();

	await page.route('**/TeamWatch', async () => {}); // never answers
	await page.goto('/team');
	await expect(page.getByRole('button', { name: 'You', exact: true })).toBeVisible();
	await expect(page.getByText('Nothing this week.')).toHaveCount(0);
	await expect(page.getByTestId('board-skeleton').first()).toBeVisible();
});

test('on a phone a long divider keeps its rules, the send button sits at the bottom, and a date range stays together', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create();
	await t.waitForStatus(Status.AWAITING_INPUT); // its push line is a long divider: on a phone it wraps
	const page = await alice.page(devices['iPhone 13']);
	await page.goto(`/t/${t.id}`);
	const rule = page.getByText(/^Pushed /).locator('..').locator('> div').first();
	expect((await rule.boundingBox())!.width).toBeGreaterThanOrEqual(16);

	await page.goto('/');
	const send = (await page.getByRole('button', { name: 'Submit' }).boundingBox())!;
	const chip = (await page.getByRole('button', { name: /^Options/ }).boundingBox())!;
	expect(send.y + send.height).toBeGreaterThanOrEqual(chip.y + chip.height);

	const wide = await alice.page({ viewport: { width: 1280, height: 720 } });
	await wide.goto('/search');
	const [from, to] = await Promise.all(['From', 'to'].map((l) => wide.getByText(l, { exact: true }).boundingBox()));
	expect(Math.abs(from!.y - to!.y)).toBeLessThan(4);
});

test('a message from an earlier day says its day', async ({ as }) => {
	const alice = as('alice');
	const t = await alice.create({ prompt: 'An old message' });
	await t.waitForStatus(Status.AWAITING_INPUT);
	const page = await alice.page();
	await page.clock.setFixedTime(new Date(Date.now() + 3 * 86_400_000));
	await page.goto(`/t/${t.id}`);
	const day = new Date().toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
	await expect(page.locator('time').first()).toContainText(day);
});
