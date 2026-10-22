<script lang="ts">
	import GitMergeConflictIcon from '@lucide/svelte/icons/git-merge-conflict';
	import ListFilterIcon from '@lucide/svelte/icons/list-filter';
	import IconButton from '#lib/components/icon-button.svelte';
	import PageHeader from '#lib/components/page-header.svelte';
	import StatusDot from '#lib/components/status-dot.svelte';
	import UserAvatar from '#lib/components/user-avatar.svelte';
	import * as DropdownMenu from '#lib/components/ui/dropdown-menu/index.js';
	import { api } from '#lib/api.js';
	import { Status, type Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { noRepo, projectName, projectOf, threads, unread, userName, waiting } from '#lib/threads.svelte.js';
	import { ago } from '#lib/utils.js';
	import { timestampMs } from '@bufbuild/protobuf/wkt';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import Brief from '#lib/components/brief.svelte';

	// Everyone's recent, unsettled threads, live from TeamWatch.
	let board = $state<Thread[]>([]);
	let offline = $state(false);

	$effect(() => {
		const ctrl = new AbortController();
		(async () => {
			while (!ctrl.signal.aborted) {
				try {
					for await (const res of api.teamWatch({}, { signal: ctrl.signal })) {
						board = res.threads;
						offline = false;
					}
				} catch {
					if (ctrl.signal.aborted) return;
				}
				offline = true;
				await new Promise((r) => setTimeout(r, 1000));
			}
		})();
		return () => ctrl.abort();
	});

	const me = $derived(threads.me?.id ?? '');
	const projects = $derived([...new Set(board.map(projectOf))]);
	const shown = $derived(board.filter((t) => !threads.project || projectOf(t) === threads.project));
	const last = (t: Thread) => {
		const ts = t.activity?.time ?? t.updatedAt;
		return ts ? timestampMs(ts) : 0;
	};

	// Each person's threads by what matters: blocked on them or with fresh results (paused after 30 min idle), the
	// agent working, paused in the last day, and paused before (only counted). The server's order: latest first.
	type Groups = { needs: Thread[]; working: Thread[]; done: Thread[]; older: Thread[] };
	function group(t: Thread): keyof Groups {
		if (t.turnRequests || t.permissionPending) return 'needs';
		if (t.status === Status.STARTING || t.status === Status.WORKING) return 'working';
		if (t.status !== Status.SUSPENDED) return 'needs';
		return threads.now - last(t) < 86_400_000 ? 'done' : 'older';
	}
	// Me first, then whoever was active most recently; people with nothing but older threads are quiet.
	const people = $derived.by(() => {
		const by = new Map<string, Groups>();
		const add = (id: string) => by.get(id) ?? by.set(id, { needs: [], working: [], done: [], older: [] }).get(id)!;
		if (me) add(me);
		for (const t of shown) add(t.ownerId)[group(t)].push(t);
		for (const u of threads.users) add(u.id);
		const recent = (g: Groups) => Math.max(0, ...[...g.needs, ...g.working, ...g.done, ...g.older].map(last));
		return [...by]
			.map(([id, g]) => ({ id, ...g, quiet: id !== me && !g.needs.length && !g.working.length && !g.done.length }))
			.toSorted((a, b) => Number(b.id === me) - Number(a.id === me) || recent(b) - recent(a));
	});
	let everyone = $state(false);
	const quiet = $derived(people.filter((p) => p.quiet).length);

	// Unfolded people (all on wide screens; on a phone only me), unfolded Done and older lists.
	const wide = typeof matchMedia !== 'undefined' && matchMedia('(min-width: 768px)').matches;
	let open = $state<Record<string, boolean>>({});
	let all = $state<Record<string, boolean>>({});

	function doing(t: Thread) {
		if (waiting(t)) return waiting(t);
		if (t.status === Status.AWAITING_INPUT) return `Ready for ${t.ownerId === me ? 'you' : userName(t.ownerId)}`;
		if (t.status === Status.SUSPENDED) return 'Paused';
		const a = t.activity;
		if (!a?.text) return t.status === Status.ERRORED ? 'Errored' : '';
		return a.actor === 'agent' ? `Agent: ${a.text}` : a.actor === 'system' ? a.text : `${userName(a.actor)}: ${a.text}`;
	}
</script>

<div class="flex min-w-0 flex-1 flex-col">
	<PageHeader>
		<h1 class="text-sm font-medium">Team</h1>
		{#if offline}<span class="text-xs text-muted-foreground">reconnecting…</span>{/if}
		<div class="ml-auto flex items-center gap-1">
			{#if quiet}
				<button
					class="rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-accent hover:text-foreground"
					aria-pressed={everyone}
					onclick={() => (everyone = !everyone)}
				>
					{everyone ? 'Hide quiet' : `Show ${quiet} quiet`}
				</button>
			{/if}
			<DropdownMenu.Root>
				<DropdownMenu.Trigger>
					{#snippet child({ props })}
						<IconButton
							{...props}
							icon={ListFilterIcon}
							label={threads.project ? `Project: ${projectName(threads.project)}` : 'Filter by project'}
							variant={threads.project ? 'secondary' : 'ghost'}
						/>
					{/snippet}
				</DropdownMenu.Trigger>
				<DropdownMenu.Content align="end" class="w-60">
					<DropdownMenu.RadioGroup bind:value={threads.project}>
						<DropdownMenu.RadioItem value="">All projects</DropdownMenu.RadioItem>
						{#each projects as p (p)}
							<DropdownMenu.RadioItem value={p} title={p === noRepo ? undefined : p}><span class="truncate">{projectName(p)}</span></DropdownMenu.RadioItem>
						{/each}
					</DropdownMenu.RadioGroup>
				</DropdownMenu.Content>
			</DropdownMenu.Root>
		</div>
	</PageHeader>

	<div class="min-h-0 flex-1 overflow-y-auto">
		<Brief />
		<div class="flex flex-col gap-3 p-4 md:snap-x md:flex-row md:items-start md:gap-4 md:overflow-x-auto">
			{#each people.filter((p) => everyone || !p.quiet) as p (p.id)}
				{@const unfolded = open[p.id] ?? (wide || p.id === me)}
				<section class="flex shrink-0 flex-col gap-2 md:w-72 md:snap-start">
					<h2>
						<button
							class="flex w-full items-center gap-2 rounded-md px-1 py-0.5 text-left text-sm font-medium hover:bg-accent/50"
							aria-expanded={unfolded}
							onclick={() => (open[p.id] = !unfolded)}
						>
							<ChevronRightIcon class="size-3 shrink-0 text-muted-foreground transition-transform {unfolded ? 'rotate-90' : ''}" />
							<UserAvatar name={userName(p.id)} class="size-6" />
							<span class="truncate">{p.id === me ? 'You' : userName(p.id)}</span>
							<span class="ml-auto text-xs font-normal whitespace-nowrap text-muted-foreground tabular-nums">
								{[
									p.needs.length && `${p.needs.length} waiting`,
									p.working.length && `${p.working.length} working`,
									p.done.length && `${p.done.length} done`
								]
									.filter(Boolean)
									.join(' · ')}
							</span>
						</button>
					</h2>
					{#if unfolded}
						{@render list(p.id, p.id === me ? 'Needs you' : 'Needs them', p.needs)}
						{@render list(p.id, 'Working', p.working)}
						{@render list(p.id, 'Done recently', p.done, 3)}
						{#if p.older.length}
							{#if all[`${p.id}older`]}
								{@render list(p.id, 'Paused earlier', p.older)}
							{:else}
								<button
									class="px-1 text-left text-xs text-muted-foreground hover:text-foreground"
									onclick={() => (all[`${p.id}older`] = true)}
								>
									{p.older.length} paused earlier
								</button>
							{/if}
						{/if}
						{#if !p.needs.length && !p.working.length && !p.done.length && !p.older.length}
							<p class="px-1 text-xs text-muted-foreground">Nothing this week.</p>
						{/if}
					{/if}
				</section>
			{/each}
		</div>
	</div>
</div>

<!-- One group of a person's threads, the first `max` shown until unfolded. -->
{#snippet list(owner: string, name: string, list: Thread[], max = Infinity)}
	{#if list.length}
		{@const more = all[owner + name] ? 0 : list.length - max}
		<section aria-label={name} class="flex flex-col gap-2">
			<h3 class="px-1 text-[11px] font-medium tracking-wider text-muted-foreground uppercase">
				{name} <span class="tabular-nums">{list.length}</span>
			</h3>
			{#each more > 0 ? list.slice(0, max) : list as t (t.id)}{@render card(t)}{/each}
			{#if more > 0}
				<button class="px-1 text-left text-xs text-muted-foreground hover:text-foreground" onclick={() => (all[owner + name] = true)}>
					{more} more
				</button>
			{/if}
		</section>
	{/if}
{/snippet}

{#snippet card(t: Thread)}
	{@const working = t.status === Status.WORKING || t.status === Status.STARTING}
	<a
		href="/t/{t.id}"
		class="block rounded-lg border bg-card p-3 text-sm transition-colors hover:border-foreground/25 {working
			? 'border-l-2 border-l-sky-400'
			: ''} {t.status === Status.ERRORED ? 'border-l-2 border-l-red-500' : ''} {t.status === Status.SUSPENDED
			? 'opacity-60'
			: ''}"
	>
		<div class="flex items-center gap-2">
			<StatusDot status={t.status} waiting={waiting(t)} />
			<span class="truncate {unread(t) ? 'font-semibold' : ''}">{t.title || 'Untitled thread'}</span>
			{#if unread(t)}<span class="ml-auto size-1.5 shrink-0 rounded-full bg-primary" aria-label="Unread"></span>{/if}
		</div>
		<p class="mt-1 truncate text-xs {t.status === Status.ERRORED ? 'text-destructive' : 'text-muted-foreground'}" title={doing(t)}>
			{doing(t) || ' '}
		</p>
		<div class="mt-2 flex items-center gap-2 text-[11px] text-muted-foreground">
			<span class="truncate">{projectName(t.repoUrl)}</span>
			<span class="shrink-0 tabular-nums">{ago(t.activity?.time ?? t.updatedAt, threads.now)}</span>
			{#if t.overlaps.length}
				{@const label = `Overlaps with ${t.overlaps.map((o) => o.title || 'Untitled thread').join(', ')}`}
				<span role="img" class="flex shrink-0 items-center gap-0.5 text-amber-600 dark:text-amber-400" aria-label={label} title={label}>
					<GitMergeConflictIcon class="size-3" />{t.overlaps.length}
				</span>
			{/if}
			{#if t.presentUserIds.length}
				<span class="ml-auto flex shrink-0 -space-x-1.5" title="Here now: {t.presentUserIds.map(userName).join(', ')}">
					{#each t.presentUserIds.slice(0, 3) as u (u)}
						<UserAvatar name={userName(u)} class="size-5 ring-2 ring-card" />
					{/each}
				</span>
			{/if}
		</div>
	</a>
{/snippet}
