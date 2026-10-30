<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { Code, ConnectError } from '@connectrpc/connect';
	import GavelIcon from '@lucide/svelte/icons/gavel';
	import TrophyIcon from '@lucide/svelte/icons/trophy';
	import { Response } from '#lib/components/ai-elements/response/index.js';
	import DiffView from '#lib/components/diff-view.svelte';
	import PageHeader from '#lib/components/page-header.svelte';
	import StatusDot from '#lib/components/status-dot.svelte';
	import { Badge } from '#lib/components/ui/badge/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import { api } from '#lib/api.js';
	import { parseDiff, type Comment } from '#lib/diff.js';
	import { IsMobile } from '#lib/hooks/is-mobile.svelte.js';
	import { Status, type RaceEntry, type Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { loadThreads, threads, userName } from '#lib/threads.svelte.js';
	import { ago } from '#lib/utils.js';

	// Threads side by side: a race (?race=<id>) and/or any threads (?ids=a,b). Each diff can be
	// reviewed like in its thread; the review goes to that thread. Without either: the recent races.
	const raceId = $derived(page.url.searchParams.get('race') ?? '');
	const threadIds = $derived((page.url.searchParams.get('ids') ?? '').split(',').filter(Boolean));

	let entries = $state<RaceEntry[]>([]);
	let races = $state<Thread[][]>();
	let missing = $state(false);
	let error = $state('');
	let reviews = $state<Record<string, { pending: Comment[]; ask: boolean; focus?: Comment }>>({});
	// The agent's reply as it streams, by thread.
	let streaming = $state<Record<string, string>>({});
	let expanded = $state<Record<string, boolean>>({});
	const mobile = new IsMobile();

	async function load() {
		try {
			if (!raceId && !threadIds.length) {
				const by = new Map<string, Thread[]>();
				for (const t of (await api.raceList({})).threads) by.set(t.raceId, [...(by.get(t.raceId) ?? []), t]);
				races = [...by.values()];
				return;
			}
			entries = (await api.raceCompare({ raceId, threadIds })).entries;
			for (const e of entries) reviews[e.thread!.id] ??= { pending: [], ask: true };
			error = '';
		} catch (e) {
			if (ConnectError.from(e).code === Code.NotFound) missing = true;
			else error = (e as Error).message;
		}
	}
	$effect(() => {
		[raceId, threadIds];
		[entries, races, missing] = [[], undefined, false];
		load();
	});

	// Live: one watch per try. What changes a column reloads just that try.
	const timers: Record<string, ReturnType<typeof setTimeout>> = {};
	function refresh(id: string) {
		clearTimeout(timers[id]);
		timers[id] = setTimeout(async () => {
			try {
				const [e] = (await api.raceCompare({ threadIds: [id] })).entries;
				const i = entries.findIndex((x) => x.thread!.id === id);
				if (i >= 0) entries[i] = e;
			} catch {}
		}, 250);
	}
	async function watch(id: string, signal: AbortSignal) {
		let seq = 0n;
		while (!signal.aborted) {
			try {
				for await (const { event } of api.watchThread({ threadId: id, afterSeq: seq, tail: 1 }, { signal })) {
					if (!event) continue;
					if (event.seq) seq = event.seq;
					const k = event.kind;
					if (k.case === 'agentTextDelta') streaming[id] = (streaming[id] ?? '') + k.value.text;
					if (k.case === 'agentText') streaming[id] = '';
					if (['agentText', 'turnEnded', 'diffChanged', 'statusChanged', 'agentSettings', 'forkNote'].includes(k.case!)) refresh(id);
				}
			} catch {
				if (signal.aborted) return;
			}
			await new Promise((r) => setTimeout(r, 1000));
		}
	}
	const watched = $derived(entries.map((e) => e.thread!.id).join(','));
	$effect(() => {
		const ctrl = new AbortController();
		for (const id of watched.split(',').filter(Boolean)) watch(id, ctrl.signal);
		return () => ctrl.abort();
	});

	async function pick(id: string) {
		try {
			await api.racePick({ threadId: id });
			loadThreads().catch(() => {});
			load();
		} catch (e) {
			error = (e as Error).message;
		}
	}

	async function review(id: string, summary: string) {
		const r = reviews[id];
		let text = summary.trim();
		if (r.ask && !/@agent\b/.test(text)) text = `@agent ${text || 'please address these review comments.'}`;
		try {
			await api.sendMessage({ threadId: id, message: { text, review: r.pending, mentionsAgent: /@agent\b/.test(text) } });
			r.pending = [];
			return true;
		} catch (e) {
			error = (e as Error).message;
			return false;
		}
	}

	const running = (t: Thread) => t.status === Status.STARTING || t.status === Status.WORKING;
	const isPicked = (e: RaceEntry) => entries.length > 1 && !e.thread!.settled && entries.every((o) => o === e || o.thread!.settled);
	const task = $derived((entries[0]?.thread?.title ?? '').replace(/ \(\d+\/\d+\)$/, ''));
	// A try is told apart by its model and effort; the same settings get a number.
	const labels = $derived.by(() => {
		const plain = entries.map((e) => `${e.thread!.model.replace(/^claude-/, '')} · ${e.thread!.effort}`);
		return plain.map((l, i) => (plain.filter((o) => o === l).length > 1 ? `${l} #${i + 1}` : l));
	});
	const files = $derived(entries.map((e) => parseDiff(e.thread!.diff)));
	const stats = (i: number) => ({
		files: files[i].length,
		add: files[i].reduce((n, f) => n + f.add, 0),
		del: files[i].reduce((n, f) => n + f.del, 0)
	});

	// What differs: every changed file, which tries changed it and whether their changes are the same.
	const differs = $derived.by(() => {
		const paths = [...new Set(files.flat().map((f) => f.path))];
		return paths.map((path) => {
			const per = files.map((fs) => fs.find((f) => f.path === path));
			const bodies = new Set(per.flatMap((f) => (f ? [f.lines.filter((l) => !l.text.startsWith('@@')).map((l) => l.text).join('\n')] : [])));
			const touched = per.filter(Boolean).length;
			const kind = touched < entries.length ? `Only in ${touched} of ${entries.length}` : bodies.size > 1 ? 'Differs' : 'Same';
			return { path, per, kind };
		});
	});
	// Lines up a file in every column (on a phone, in the try on screen).
	function align(path: string) {
		entries.forEach((e, i) => {
			if (files[i].some((f) => f.path === path) && (!mobile.current || e.thread!.id === current))
				reviews[e.thread!.id].focus = { path, startLine: 0, endLine: 0, old: false, code: '', body: '' };
		});
	}

	// Phone: the tries stack; the switcher jumps between them and shows the one on screen (the last at the bottom).
	let scroller = $state<HTMLElement>();
	let current = $state('');
	const jump = (id: string) => document.getElementById(`try-${id}`)?.scrollIntoView({ block: 'start' });
	function onscroll() {
		if (!mobile.current || !scroller) return;
		const top = scroller.getBoundingClientRect().top + 60;
		const bottom = scroller.scrollTop + scroller.clientHeight >= scroller.scrollHeight - 2;
		current = bottom ? entries.at(-1)!.thread!.id : entries.findLast((e) => (document.getElementById(`try-${e.thread!.id}`)?.getBoundingClientRect().top ?? 0) <= top)?.thread!.id ?? '';
	}

	// Judge: a sub-thread of the viewer's, under the picked try (or the first), that reads every try's work.
	let judging = $state(false);
	const MAX_DIFF = 30_000;
	function judgePrompt() {
		const tries = entries.map((e, i) => {
			const t = e.thread!;
			const diff = t.diff.length > MAX_DIFF ? `${t.diff.slice(0, MAX_DIFF)}\n… (cut)` : t.diff;
			return `## Try ${i + 1}: ${labels[i]} (Banda thread ${t.id})\n\n${e.turns} turns, $${e.costUsd.toFixed(2)}, ${seconds(e.durationMs)}.\n\n### Its last reply\n\n${e.reply || '(none)'}\n\n### Its diff\n\n~~~~~diff\n${diff || '(no changes)'}\n~~~~~`;
		});
		return `Judge the ${entries.length} tries of "${task}"\n\n${entries.length} agents each worked on the same task in a branch of their own. Their last replies and diffs follow. Don't change any files. Rank the tries from best to worst with a short reason each, then recommend one and say what the others did better that is worth carrying over.\n\n${tries.join('\n\n')}`;
	}
	async function judge() {
		judging = true;
		try {
			const base = entries.find(isPicked) ?? entries[0];
			const { thread } = await api.spawnThread({ parentId: base.thread!.id, prompt: judgePrompt() });
			loadThreads().catch(() => {});
			await goto(`/t/${thread!.id}`);
		} catch (e) {
			error = (e as Error).message;
		} finally {
			judging = false;
		}
	}

	const seconds = (ms: bigint) => {
		const s = Math.round(Number(ms) / 1000);
		return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${s % 60}s`;
	};
	const raceTitle = (t: Thread) => t.title.replace(/ \(\d+\/\d+\)$/, '');
	const mine = $derived((races ?? []).filter((r) => r[0].ownerId === threads.me?.id));
	const theirs = $derived((races ?? []).filter((r) => r[0].ownerId !== threads.me?.id));
</script>

{#snippet raceList(title: string, list: Thread[][])}
	<section class="space-y-1">
		<h2 class="px-2 text-xs font-medium text-muted-foreground">{title}</h2>
		{#each list as r (r[0].raceId)}
			<a href="/compare?race={r[0].raceId}" class="flex items-center gap-3 rounded-md px-2 py-2 hover:bg-muted/50">
				<span class="min-w-0 flex-1">
					<span class="block truncate text-sm">{raceTitle(r[0])}</span>
					<span class="block truncate text-xs text-muted-foreground">
						{r.length} tries{r[0].ownerId === threads.me?.id ? '' : ` · ${userName(r[0].ownerId)}`} · {ago(r[0].createdAt, threads.now)}
					</span>
				</span>
				<span class="flex gap-1">{#each r as t (t.id)}<StatusDot status={t.status} />{/each}</span>
			</a>
		{:else}
			<p class="px-2 text-sm text-muted-foreground">None yet.</p>
		{/each}
	</section>
{/snippet}

<div class="flex min-w-0 flex-1 flex-col">
	<PageHeader>
		<h1 class="shrink-0 text-sm font-medium">{entries.length ? `Compare ${entries.length} tries` : 'Races'}</h1>
		<span class="min-w-0 flex-1 truncate text-sm text-muted-foreground">{task}</span>
		{#if error}<span class="truncate text-xs text-destructive">{error}</span>{/if}
		{#if entries.length > 1}
			<Button size="sm" variant="outline" disabled={judging} onclick={judge} aria-label="Ask an agent to judge">
				<GavelIcon /><span class="hidden sm:inline">Ask an agent to judge</span><span class="sm:hidden">Judge</span>
			</Button>
		{/if}
	</PageHeader>

	{#if races}
		<div class="mx-auto w-full max-w-2xl space-y-6 overflow-y-auto p-4">
			{@render raceList('Your races', mine)}
			{@render raceList('Teammates’ races', theirs)}
		</div>
	{:else if missing}
		<div class="flex flex-col items-center gap-2 p-10 text-center text-sm text-muted-foreground">
			<h2 class="text-base font-medium text-foreground">No such race</h2>
			<p>Nothing to compare: no tries match this link.</p>
			<Button size="sm" variant="outline" href="/compare">Recent races</Button>
		</div>
	{:else if !entries.length}
		<p class="p-6 text-sm text-muted-foreground">{error ? '' : 'Loading…'}</p>
	{:else}
		<div bind:this={scroller} {onscroll} class="min-h-0 flex-1 overflow-y-auto md:flex md:flex-col md:overflow-hidden">
			{#if differs.length}
				<details open aria-label="What differs" class="shrink-0 border-b">
					<summary class="cursor-pointer px-3 py-2 text-xs font-medium">
						What differs: {differs.length} {differs.length === 1 ? 'file' : 'files'}
					</summary>
					<div class="grid max-h-48 overflow-y-auto pb-2 text-xs" style="grid-template-columns: minmax(0, 1fr) repeat({entries.length}, auto) auto">
						<div class="col-span-full grid grid-cols-subgrid gap-x-3 px-3 pb-0.5 text-muted-foreground">
							<span></span>{#each labels as l}<span>{l}</span>{/each}<span></span>
						</div>
						{#each differs as d (d.path)}
							<button class="col-span-full grid grid-cols-subgrid items-center gap-x-3 px-3 py-0.5 text-left hover:bg-muted/50" onclick={() => align(d.path)}>
								<span class="truncate font-mono" title={d.path}>{d.path}</span>
								{#each d.per as f, i}
									<span class="font-mono" title={labels[i]}>
										{#if f}<span class="text-green-700 dark:text-green-400">+{f.add}</span> <span class="text-red-600 dark:text-red-400">−{f.del}</span>{:else}<span class="text-muted-foreground">—</span>{/if}
									</span>
								{/each}
								<span class={d.kind === 'Same' ? 'text-muted-foreground' : 'font-medium text-amber-700 dark:text-amber-500'}>{d.kind}</span>
							</button>
						{/each}
					</div>
				</details>
			{/if}
			<nav class="sticky top-0 z-20 flex gap-1 overflow-x-auto border-b bg-background p-1.5 md:hidden" aria-label="Tries">
				{#each entries as e, i (e.thread!.id)}
					<Button
						size="xs"
						variant={(current || entries[0].thread!.id) === e.thread!.id ? 'secondary' : 'ghost'}
						aria-current={(current || entries[0].thread!.id) === e.thread!.id}
						onclick={() => jump(e.thread!.id)}
					>
						<StatusDot status={e.thread!.status} />{labels[i]}
					</Button>
				{/each}
			</nav>
			<div class="md:grid md:min-h-0 md:flex-1 md:auto-cols-[minmax(22rem,1fr)] md:grid-flow-col md:overflow-x-auto">
				{#each entries as e, i (e.thread!.id)}
					{@const t = e.thread!}
					{@const s = stats(i)}
					{@const reply = streaming[t.id] || e.reply}
					<section id="try-{t.id}" aria-label={labels[i]} class="flex scroll-mt-11 flex-col border-b md:min-h-0 md:min-w-0 md:border-r md:border-b-0 md:last:border-r-0">
						<div class="space-y-1.5 border-b p-3">
							<div class="flex items-center gap-2">
								<a class="min-w-0 flex-1 truncate text-sm font-medium hover:underline" href="/t/{t.id}" title={t.title}>{labels[i]}</a>
								<StatusDot status={t.status} label />
								{#if t.ownerId === threads.me?.id}
									{#if isPicked(e)}
										<Badge><TrophyIcon /> Picked</Badge>
									{:else if t.settled}
										<Badge variant="outline" class="font-normal text-muted-foreground">Settled</Badge>
									{:else}
										<Button size="xs" variant="outline" onclick={() => pick(t.id)}><TrophyIcon /> Pick this one</Button>
									{/if}
								{/if}
							</div>
							<div class="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
								<span>{e.turns} {e.turns === 1 ? 'turn' : 'turns'}</span>
								<span>${e.costUsd.toFixed(2)}</span>
								<span>{seconds(e.durationMs)}</span>
								{#if t.repoUrl}
									<span>
										{s.files} {s.files === 1 ? 'file' : 'files'}
										<span class="text-emerald-700 dark:text-emerald-400">+{s.add}</span>
										<span class="text-red-600 dark:text-red-600 dark:text-red-400">−{s.del}</span>
									</span>
								{/if}
							</div>
						</div>
						{#if reply}
							<div class="shrink-0 border-b px-3 py-2 text-sm" aria-label="Last reply of {labels[i]}" role="region">
								<div class={expanded[t.id] ? 'max-h-[50vh] overflow-y-auto' : 'max-h-24 overflow-hidden'}>
									<Response content={reply} class="h-auto" />
								</div>
								{#if reply.length > 280 || reply.split('\n').length > 4}
									<button class="mt-1 text-xs text-muted-foreground hover:text-foreground" onclick={() => (expanded[t.id] = !expanded[t.id])}>
										{expanded[t.id] ? 'Show less' : 'Show all'}
									</button>
								{/if}
							</div>
						{:else if running(t)}
							<p class="shrink-0 border-b px-3 py-2 text-xs text-muted-foreground">Working on it…</p>
						{/if}
						{#if t.repoUrl}
						<div class="flex flex-col md:min-h-0 md:flex-1">
							<DiffView
								diff={t.diff}
								bind:pending={reviews[t.id].pending}
								bind:ask={reviews[t.id].ask}
								bind:focus={reviews[t.id].focus}
								posted={[]}
								onsend={(summary) => review(t.id, summary)}
							/>
						</div>
						{:else}
							<p class="p-4 text-sm text-muted-foreground">No repository, so no diff: open the try to read its work.</p>
						{/if}
					</section>
				{/each}
			</div>
		</div>
	{/if}
</div>
