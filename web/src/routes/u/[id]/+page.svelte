<script lang="ts">
	import { Skeleton } from '#lib/components/ui/skeleton/index.js';
	import { page } from '$app/state';
	import SettingsIcon from '@lucide/svelte/icons/settings';
	import LoadState from '#lib/components/load-state.svelte';
	import PageHeader from '#lib/components/page-header.svelte';
	import ThreadGroup from '#lib/components/thread-group.svelte';
	import UserAvatar from '#lib/components/user-avatar.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import { api } from '#lib/api.js';
	import type { Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { boardGroup, threads, userName, watchTeam, type BoardGroups } from '#lib/threads.svelte.js';

	// A person's page: who they are, whether they're here, their threads grouped as on the team board. The recent,
	// unsettled ones come live from TeamWatch (status, presence); the rest (settled, or quiet for a week) from one
	// ListThreads, folded.
	const id = $derived(page.params.id ?? '');
	const me = $derived(threads.me?.id ?? '');
	const user = $derived(id === me ? threads.me : threads.users.find((u) => u.id === id));
	const name = $derived(id === me ? 'You' : userName(id));

	let board = $state<Thread[]>();
	let offline = $state(false);
	$effect(() => {
		const ctrl = new AbortController();
		watchTeam(ctrl.signal, (list) => {
			if (list) board = list;
			offline = !list;
		});
		return () => ctrl.abort();
	});

	let all = $state<Thread[]>([]);
	let olderFailed = $state(false);
	$effect(() => {
		const owner = id;
		all = [];
		api.listThreads({ ownerId: owner })
			.then((r) => owner === id && ([all, olderFailed] = [r.threads, false]))
			.catch(() => owner === id && (olderFailed = true));
	});

	const groups = $derived.by(() => {
		const g: BoardGroups = { needs: [], working: [], done: [], older: [] };
		for (const t of board ?? []) if (t.ownerId === id) g[boardGroup(t)].push(t);
		return g;
	});
	const live = $derived(new Set(board?.map((t) => t.id)));
	const rest = $derived(board ? all.filter((t) => !live.has(t.id)) : []);
	// ponytail: presence is the board's (threads active this week); someone reading an older thread isn't seen.
	const here = $derived(board?.find((t) => t.presentUserIds.includes(id)));
	let older = $state(false);
	let earlier = $state(false);
</script>

<div class="flex min-w-0 flex-1 flex-col">
	<PageHeader>
		<h1 class="text-sm font-medium">{name}</h1>
		{#if offline}<span class="text-xs text-muted-foreground">reconnecting…</span>{/if}
	</PageHeader>
	{#if threads.me && !user}
		<LoadState title="Person not found" description="Nobody on the team has this id, or the link is wrong." />
	{:else}
		<div class="min-h-0 flex-1 overflow-y-auto">
			<div class="mx-auto flex max-w-2xl flex-col gap-4 p-4">
				<div class="flex items-center gap-3">
					<UserAvatar name={userName(id)} class="size-12 text-base" />
					<div class="min-w-0 flex-1 leading-tight">
						<h2 class="truncate text-base font-medium">{userName(id)}</h2>
						<p class="truncate text-sm text-muted-foreground">{user?.email}</p>
						{#if here}
							<p class="truncate text-xs text-muted-foreground">
								<span class="mr-1 inline-block size-1.5 rounded-full bg-success align-middle"></span>Here now in
								<a href="/t/{here.id}" class="text-foreground hover:underline">{here.title || 'Untitled thread'}</a>
							</p>
						{/if}
					</div>
					{#if id === me}<Button variant="outline" size="sm" href="/settings"><SettingsIcon /> Settings</Button>{/if}
				</div>
				{#if board}
					<ThreadGroup name={id === me ? 'Needs you' : 'Needs them'} list={groups.needs} />
					<ThreadGroup name="Working" list={groups.working} />
					<ThreadGroup name="Done recently" list={groups.done} />
					{#if groups.older.length}
						{#if older}
							<ThreadGroup name="Paused earlier" list={groups.older} />
						{:else}
							<button class="px-1 text-left text-xs text-muted-foreground hover:text-foreground" onclick={() => (older = true)}>
								{groups.older.length} paused earlier
							</button>
						{/if}
					{/if}
					{#if rest.length}
						{#if earlier}
							<ThreadGroup name="Settled or older" list={rest} />
						{:else}
							<button class="px-1 text-left text-xs text-muted-foreground hover:text-foreground" onclick={() => (earlier = true)}>
								{rest.length} settled or older
							</button>
						{/if}
					{/if}
					{#if olderFailed}
						<p role="alert" class="px-1 text-sm text-destructive">Couldn't load the older threads.</p>
					{:else if !Object.values(groups).some((l) => l.length) && !rest.length}
						<p class="px-1 text-sm text-muted-foreground">No threads yet.</p>
					{/if}
				{:else}
					<div class="space-y-2" aria-hidden="true" data-testid="board-skeleton">
						{#each [0, 1, 2] as i (i)}<Skeleton class="h-16" />{/each}
					</div>
				{/if}
			</div>
		</div>
	{/if}
</div>
