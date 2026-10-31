<script lang="ts">
	import { untrack } from 'svelte';
	import { goto } from '$app/navigation';
	import { timestampMs } from '@bufbuild/protobuf/wkt';
	import { computeCommandScore } from 'bits-ui';
	import InboxIcon from '@lucide/svelte/icons/inbox';
	import KeyboardIcon from '@lucide/svelte/icons/keyboard';
	import SearchIcon from '@lucide/svelte/icons/search';
	import SettingsIcon from '@lucide/svelte/icons/settings';
	import SquarePenIcon from '@lucide/svelte/icons/square-pen';
	import UsersIcon from '@lucide/svelte/icons/users';
	import SearchHit from '#lib/components/search-hit.svelte';
	import StatusDot from '#lib/components/status-dot.svelte';
	import UserAvatar from '#lib/components/user-avatar.svelte';
	import * as Command from '#lib/components/ui/command/index.js';
	import * as Dialog from '#lib/components/ui/dialog/index.js';
	import { api } from '#lib/api.js';
	import type { SearchResult, Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { newThread, recent, shortcuts, ui, type Command as Cmd } from '#lib/shortcuts.svelte.js';
	import { projectName, threads, userName, waiting } from '#lib/threads.svelte.js';

	let query = $state('');
	// Teammates' threads, loaded each time the palette opens.
	let others = $state<Thread[]>([]);
	const loadOthers = () =>
		Promise.all(threads.users.filter((u) => u.id !== threads.me?.id).map((u) => api.listThreads({ ownerId: u.id })))
			.then((rs) => (others = rs.flatMap((r) => r.threads)))
			.catch(() => {});
	// Only on open: the sidebar's polling must not reload or clear the query.
	$effect(() => {
		if (!ui.palette) return;
		query = '';
		untrack(loadOthers);
	});

	// From 3 characters on, the best few messages matching the query follow the threads.
	let hits = $state<SearchResult[]>([]);
	let searchFailed = $state(false);
	let asked = 0;
	$effect(() => {
		const q = query.trim();
		const n = ++asked;
		if (q.length < 3) return void (hits = []);
		const timer = setTimeout(
			() =>
				api
					.search({ query: q, limit: 5 })
					.then((r) => n === asked && ([hits, searchFailed] = [r.results, false]))
					.catch(() => n === asked && (searchFailed = true)),
			150
		);
		return () => clearTimeout(timer);
	});

	// Recently opened first, then recently active.
	const all = $derived.by(() => {
		const byId = new Map([...threads.list, ...threads.mentioned, ...others].map((t) => [t.id, t]));
		const r = recent();
		const rank = (t: Thread) => (r.includes(t.id) ? r.indexOf(t.id) : r.length);
		const age = (t: Thread) => -(t.updatedAt ? timestampMs(t.updatedAt) : 0);
		return [...byId.values()].sort((a, b) => rank(a) - rank(b) || age(a) - age(b));
	});

	const global: Cmd[] = [
		{ label: 'New thread', icon: SquarePenIcon, keys: 'C', run: newThread },
		{ label: 'Go to Decisions', icon: InboxIcon, keys: 'G D', run: () => goto('/decisions') },
		{ label: 'Go to Team', icon: UsersIcon, keys: 'G T', run: () => goto('/team') },
		{ label: 'Search messages', icon: SearchIcon, run: () => goto('/search') },
		{ label: 'Go to Settings', icon: SettingsIcon, keys: 'G S', run: () => goto('/settings') },
		{ label: 'Keyboard shortcuts', icon: KeyboardIcon, keys: '?', run: () => (ui.help = true) }
	];

	// Every word must match, in any order ("bob router" finds bob's router thread).
	const filter = (value: string, search: string) =>
		search
			.split(/\s+/)
			.filter(Boolean)
			.reduce((score, w) => score * computeCommandScore(value, w), 1);

	function run(c: Cmd) {
		ui.palette = false;
		c.run();
	}
</script>

{#snippet item(c: Cmd)}
	<Command.Item value={c.label} onSelect={() => run(c)}>
		{#if c.icon}<c.icon />{/if}
		{c.label}
		{#if c.keys}<Command.Shortcut>{c.keys}</Command.Shortcut>{/if}
	</Command.Item>
{/snippet}

{#snippet actions()}
	{#if ui.commands.length}
		<Command.Group heading="This thread">
			{#each ui.commands as c (c.label)}{@render item(c)}{/each}
		</Command.Group>
	{/if}
	<Command.Group heading="General">
		{#each global as c (c.label)}{@render item(c)}{/each}
	</Command.Group>
	<!-- Teammates' pages only while searching ("go to bob"), so the list stays short. -->
	{#if query}
		<Command.Group heading="People">
			{#each threads.users as u (u.id)}
				<Command.Item value="Go to {u.name} {u.email}{u.id === threads.me?.id ? ' your profile' : ''}" onSelect={() => run({ label: u.id, run: () => goto(`/u/${u.id}`) })}>
					<UserAvatar name={u.name} class="size-4" />Go to {u.id === threads.me?.id ? 'your profile' : u.name}
				</Command.Item>
			{/each}
		</Command.Group>
	{/if}
{/snippet}

<Command.Dialog bind:open={ui.palette} {filter} title="Command palette" description="Search threads and actions">
	<Command.Input placeholder="Search threads, teammates and actions" bind:value={query} />
	<Command.List class="max-h-[60vh]">
		<Command.Empty>Nothing found.</Command.Empty>
		<!-- bits-ui sorts within groups only: while searching, actions come first so they aren't buried under threads. -->
		{#if query}{@render actions()}{/if}
		<Command.Group heading={query ? 'Threads' : 'Recent'}>
			{#each query ? all : all.slice(0, 8) as t (t.id)}
				{@const owner = userName(t.ownerId)}
				<Command.Item value="{t.title} {projectName(t.repoUrl)} {owner} {t.id} {t.parentTitle}" onSelect={() => run({ label: t.id, run: () => goto(`/t/${t.id}`) })}>
					<StatusDot status={t.status} waiting={waiting(t)} />
					<span class="truncate">{t.title || 'Untitled thread'}</span>
					{#if t.parentId}<span class="min-w-0 shrink truncate text-xs text-muted-foreground">↖ {t.parentTitle}</span>{/if}
					<span class="ml-auto flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
						{projectName(t.repoUrl)}
						{#if t.ownerId !== threads.me?.id}<UserAvatar name={owner} class="size-4" />{/if}
					</span>
				</Command.Item>
			{/each}
		</Command.Group>
		{#if query.trim().length >= 3}
			<!-- The query itself is in each value, so the client-side filter keeps the server's results. -->
			<Command.Group heading="Messages">
				{#if searchFailed}<p role="alert" class="px-2 py-1 text-xs text-destructive">Couldn't search messages.</p>{/if}
				{#each hits as hit (`${hit.threadId}#${hit.seq}`)}
					<Command.Item value="{query} {hit.threadId} {hit.seq}" onSelect={() => run({ label: '', run: () => goto(`/t/${hit.threadId}#${hit.seq}`) })}>
						<SearchHit {hit} compact />
					</Command.Item>
				{/each}
				<Command.Item value="{query} search all messages" onSelect={() => run({ label: '', run: () => goto(`/search?q=${encodeURIComponent(query.trim())}`) })}>
					<SearchIcon />
					<span class="truncate">See all results for “{query.trim()}”</span>
				</Command.Item>
			</Command.Group>
		{/if}
		{#if !query}{@render actions()}{/if}
	</Command.List>
</Command.Dialog>

<Dialog.Root bind:open={ui.help}>
	<Dialog.Content class="sm:max-w-sm">
		<Dialog.Header><Dialog.Title>Keyboard shortcuts</Dialog.Title></Dialog.Header>
		<dl class="grid grid-cols-[auto_1fr] items-center gap-x-4 gap-y-2 text-sm">
			{#each shortcuts as [keys, what] (keys)}
				<dt><kbd class="rounded border bg-muted px-1.5 py-0.5 font-mono text-xs">{keys}</kbd></dt>
				<dd class="text-muted-foreground">{what}</dd>
			{/each}
		</dl>
	</Dialog.Content>
</Dialog.Root>
