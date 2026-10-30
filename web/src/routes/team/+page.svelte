<script lang="ts">
	import ListFilterIcon from '@lucide/svelte/icons/list-filter';
	import IconButton from '#lib/components/icon-button.svelte';
	import PageHeader from '#lib/components/page-header.svelte';
	import ThreadGroup from '#lib/components/thread-group.svelte';
	import UserAvatar from '#lib/components/user-avatar.svelte';
	import * as DropdownMenu from '#lib/components/ui/dropdown-menu/index.js';
	import type { Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { boardGroup, lastActive, noRepo, projectName, projectOf, threads, userName, watchTeam, type BoardGroups } from '#lib/threads.svelte.js';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import Brief from '#lib/components/brief.svelte';

	// Everyone's recent, unsettled threads, live from TeamWatch.
	let board = $state<Thread[]>([]);
	let offline = $state(false);

	$effect(() => {
		const ctrl = new AbortController();
		watchTeam(ctrl.signal, (list) => {
			if (list) board = list;
			offline = !list;
		});
		return () => ctrl.abort();
	});

	const me = $derived(threads.me?.id ?? '');
	const projects = $derived([...new Set(board.map(projectOf))]);
	const shown = $derived(board.filter((t) => !threads.project || projectOf(t) === threads.project));

	// Each person's threads by what matters (boardGroup), in the server's order: latest first.
	// Me first, then whoever was active most recently; people with nothing but older threads are quiet.
	const people = $derived.by(() => {
		const by = new Map<string, BoardGroups>();
		const add = (id: string) => by.get(id) ?? by.set(id, { needs: [], working: [], done: [], older: [] }).get(id)!;
		if (me) add(me);
		for (const t of shown) add(t.ownerId)[boardGroup(t)].push(t);
		for (const u of threads.users) add(u.id);
		const recent = (g: BoardGroups) => Math.max(0, ...[...g.needs, ...g.working, ...g.done, ...g.older].map(lastActive));
		return [...by]
			.map(([id, g]) => ({ id, ...g, quiet: id !== me && !g.needs.length && !g.working.length && !g.done.length }))
			.toSorted((a, b) => Number(b.id === me) - Number(a.id === me) || recent(b) - recent(a));
	});
	let everyone = $state(false);
	const quiet = $derived(people.filter((p) => p.quiet).length);

	// Unfolded people (all on wide screens; on a phone only me), unfolded older lists.
	const wide = typeof matchMedia !== 'undefined' && matchMedia('(min-width: 768px)').matches;
	let open = $state<Record<string, boolean>>({});
	let older = $state<Record<string, boolean>>({});
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
		<div class="flex flex-col gap-3 p-4 md:snap-x md:scroll-pl-4 md:flex-row md:items-start md:gap-4 md:overflow-x-auto">
			{#each people.filter((p) => everyone || !p.quiet) as p (p.id)}
				{@const unfolded = open[p.id] ?? (wide || p.id === me)}
				<section class="flex shrink-0 flex-col gap-2 md:w-72 md:snap-start">
					<!-- The row folds the column (its button covers the row); the name on top opens the person's page. -->
					<h2 class="relative flex items-center gap-2 rounded-md px-1 py-0.5 text-sm font-medium hover:bg-accent/50">
						<ChevronRightIcon class="size-3 shrink-0 text-muted-foreground transition-transform {unfolded ? 'rotate-90' : ''}" />
						<a href="/u/{p.id}" class="relative z-10 flex min-w-0 items-center gap-2 hover:underline">
							<UserAvatar name={userName(p.id)} class="size-6" />
							<span class="truncate">{p.id === me ? 'You' : userName(p.id)}</span>
						</a>
						<span class="ml-auto text-xs font-normal whitespace-nowrap text-muted-foreground tabular-nums">
							{[
								p.needs.length && `${p.needs.length} waiting`,
								p.working.length && `${p.working.length} working`,
								p.done.length && `${p.done.length} done`
							]
								.filter(Boolean)
								.join(' · ')}
						</span>
						<button
							class="absolute inset-0 rounded-md"
							aria-label={p.id === me ? 'You' : userName(p.id)}
							aria-expanded={unfolded}
							onclick={() => (open[p.id] = !unfolded)}
						></button>
					</h2>
					{#if unfolded}
						<ThreadGroup name={p.id === me ? 'Needs you' : 'Needs them'} list={p.needs} />
						<ThreadGroup name="Working" list={p.working} />
						<ThreadGroup name="Done recently" list={p.done} max={3} />
						{#if p.older.length}
							{#if older[p.id]}
								<ThreadGroup name="Paused earlier" list={p.older} />
							{:else}
								<button class="px-1 text-left text-xs text-muted-foreground hover:text-foreground" onclick={() => (older[p.id] = true)}>
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
