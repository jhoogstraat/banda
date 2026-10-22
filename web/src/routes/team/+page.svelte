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
	// Paused threads sink; otherwise most recently active first (the server's order).
	const shown = $derived(
		board
			.filter((t) => !threads.project || projectOf(t) === threads.project)
			.toSorted((a, b) => Number(a.status === Status.SUSPENDED) - Number(b.status === Status.SUSPENDED))
	);
	// One column per person: me first, then whoever was active most recently. Quiet teammates are listed at the end.
	const columns = $derived.by(() => {
		const by = new Map<string, Thread[]>(me ? [[me, []]] : []);
		for (const t of shown) by.set(t.ownerId, [...(by.get(t.ownerId) ?? []), t]);
		return [...by];
	});
	const quiet = $derived(threads.users.filter((u) => !columns.some(([id]) => id === u.id)));
	const busy = (list: Thread[]) => list.filter((t) => t.status === Status.WORKING || t.status === Status.STARTING).length;

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
		<div class="ml-auto">
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

	<div class="flex min-h-0 flex-1 snap-x gap-4 overflow-x-auto p-4">
		{#each columns as [owner, list] (owner)}
			<section class="flex w-72 shrink-0 snap-start flex-col gap-2">
				<h2 class="flex items-center gap-2 px-1 text-sm font-medium">
					<UserAvatar name={userName(owner)} class="size-6" />
					<span class="truncate">{owner === me ? 'You' : userName(owner)}</span>
					<span class="ml-auto text-xs font-normal text-muted-foreground tabular-nums">
						{busy(list) ? `${busy(list)} working · ` : ''}{list.length}
					</span>
				</h2>
				<div class="flex min-h-0 flex-col gap-2 overflow-y-auto pb-2">
					{#each list as t (t.id)}
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
					{:else}
						<p class="px-1 text-xs text-muted-foreground">Nothing running.</p>
					{/each}
				</div>
			</section>
		{/each}
		{#if quiet.length}
			<section class="w-56 shrink-0 snap-start space-y-2 px-1">
				<h2 class="text-sm font-medium text-muted-foreground">Quiet this week</h2>
				{#each quiet as u (u.id)}
					<div class="flex items-center gap-2 text-sm text-muted-foreground"><UserAvatar name={u.name} class="size-5" /> {u.name}</div>
				{/each}
			</section>
		{/if}
	</div>
</div>
