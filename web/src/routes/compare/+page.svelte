<script lang="ts">
	import { page } from '$app/state';
	import TrophyIcon from '@lucide/svelte/icons/trophy';
	import DiffView from '#lib/components/diff-view.svelte';
	import PageHeader from '#lib/components/page-header.svelte';
	import StatusDot from '#lib/components/status-dot.svelte';
	import { Badge } from '#lib/components/ui/badge/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import { api } from '#lib/api.js';
	import { parseDiff, type Comment } from '#lib/diff.js';
	import { Status, type RaceEntry } from '#lib/gen/banda/v1/banda_pb.js';
	import { loadThreads, threads } from '#lib/threads.svelte.js';

	// Threads side by side: a race (?race=<id>) and/or any threads (?ids=a,b). Each diff can be
	// reviewed like in its thread; the review goes to that thread.
	const raceId = $derived(page.url.searchParams.get('race') ?? '');
	const threadIds = $derived((page.url.searchParams.get('ids') ?? '').split(',').filter(Boolean));

	let entries = $state<RaceEntry[]>([]);
	let loaded = $state(false);
	let error = $state('');
	let reviews = $state<Record<string, { pending: Comment[]; ask: boolean; focus?: Comment }>>({});

	// ponytail: polls every 3 s while a try is running; subscribe to the inbox stream if this gets heavy.
	let timer: ReturnType<typeof setTimeout>;
	async function load() {
		clearTimeout(timer);
		try {
			entries = (await api.raceCompare({ raceId, threadIds })).entries;
			for (const e of entries) reviews[e.thread!.id] ??= { pending: [], ask: true };
			error = '';
			loaded = true;
		} catch (e) {
			error = (e as Error).message;
		}
		if (entries.some((e) => e.thread!.status === Status.STARTING || e.thread!.status === Status.WORKING)) timer = setTimeout(load, 3000);
	}
	$effect(() => {
		[raceId, threadIds];
		load();
		return () => clearTimeout(timer);
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
			load();
			return true;
		} catch (e) {
			error = (e as Error).message;
			return false;
		}
	}

	const stats = (diff: string) => {
		const files = parseDiff(diff);
		return { files: files.length, add: files.reduce((n, f) => n + f.add, 0), del: files.reduce((n, f) => n + f.del, 0) };
	};
	const seconds = (ms: bigint) => {
		const s = Math.round(Number(ms) / 1000);
		return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${s % 60}s`;
	};
</script>

<div class="flex min-w-0 flex-1 flex-col">
	<PageHeader>
		<h1 class="text-sm font-medium">Compare {entries.length} tries</h1>
		{#if error}<span class="truncate text-xs text-destructive">{error}</span>{/if}
	</PageHeader>
	<div class="grid min-h-0 flex-1 auto-cols-[minmax(22rem,1fr)] grid-flow-col overflow-x-auto">
		{#each entries as e (e.thread!.id)}
			{@const t = e.thread!}
			{@const s = stats(t.diff)}
			{@const picked = entries.length > 1 && !t.settled && entries.every((o) => o === e || o.thread!.settled)}
			<section class="flex min-h-0 min-w-0 flex-col border-r last:border-r-0">
				<div class="space-y-1.5 border-b p-3">
					<div class="flex items-center gap-2">
						<StatusDot status={t.status} label />
						<a class="min-w-0 flex-1 truncate text-sm font-medium hover:underline" href="/t/{t.id}">{t.title}</a>
						{#if t.ownerId === threads.me?.id}
							{#if picked}
								<Badge><TrophyIcon /> Picked</Badge>
							{:else if t.settled}
								<Badge variant="outline" class="font-normal text-muted-foreground">Settled</Badge>
							{:else}
								<Button size="xs" variant="outline" onclick={() => pick(t.id)}><TrophyIcon /> Pick this one</Button>
							{/if}
						{/if}
					</div>
					<div class="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
						<span>{t.model} · {t.effort}</span>
						<span>{e.turns} {e.turns === 1 ? 'turn' : 'turns'}</span>
						<span>${e.costUsd.toFixed(2)}</span>
						<span>{seconds(e.durationMs)}</span>
						<span>
							{s.files} {s.files === 1 ? 'file' : 'files'}
							<span class="text-emerald-600 dark:text-emerald-400">+{s.add}</span>
							<span class="text-red-600 dark:text-red-400">−{s.del}</span>
						</span>
					</div>
				</div>
				<div class="min-h-0 flex-1 overflow-auto">
					<DiffView
						diff={t.diff}
						bind:pending={reviews[t.id].pending}
						bind:ask={reviews[t.id].ask}
						bind:focus={reviews[t.id].focus}
						posted={[]}
						onsend={(summary) => review(t.id, summary)}
					/>
				</div>
			</section>
		{:else}
			<p class="p-6 text-sm text-muted-foreground">{error ? '' : loaded ? 'Nothing to compare: no such tries.' : 'Loading…'}</p>
		{/each}
	</div>
</div>
