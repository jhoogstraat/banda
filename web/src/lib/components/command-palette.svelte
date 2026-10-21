<script lang="ts">
	import { untrack } from 'svelte';
	import { goto } from '$app/navigation';
	import { timestampMs } from '@bufbuild/protobuf/wkt';
	import { computeCommandScore } from 'bits-ui';
	import KeyboardIcon from '@lucide/svelte/icons/keyboard';
	import SettingsIcon from '@lucide/svelte/icons/settings';
	import SquarePenIcon from '@lucide/svelte/icons/square-pen';
	import StatusDot from '#lib/components/status-dot.svelte';
	import UserAvatar from '#lib/components/user-avatar.svelte';
	import * as Command from '#lib/components/ui/command/index.js';
	import * as Dialog from '#lib/components/ui/dialog/index.js';
	import { api } from '#lib/api.js';
	import type { Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { newThread, recent, shortcuts, ui, type Command as Cmd } from '#lib/shortcuts.svelte.js';
	import { projectName, threads, userName } from '#lib/threads.svelte.js';

	let query = $state('');
	// Teammates' threads, loaded each time the palette opens.
	let others = $state<Thread[]>([]);
	const loadOthers = () =>
		Promise.all(threads.users.filter((u) => u.id !== threads.me?.id).map((u) => api.listThreads({ ownerId: u.id })))
			.then((rs) => (others = rs.flatMap((r) => r.threads)))
			.catch(() => {});
	// Only on open/close: the sidebar's polling must not reload or clear the query.
	$effect(() => {
		if (ui.palette) untrack(loadOthers);
		else query = '';
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

<Command.Dialog bind:open={ui.palette} {filter} title="Command palette" description="Search threads and actions">
	<Command.Input placeholder="Search threads, teammates and actions" bind:value={query} />
	<Command.List class="max-h-[60vh]">
		<Command.Empty>Nothing found.</Command.Empty>
		<Command.Group heading={query ? 'Threads' : 'Recent'}>
			{#each query ? all : all.slice(0, 8) as t (t.id)}
				{@const owner = userName(t.ownerId)}
				<Command.Item value="{t.title} {projectName(t.repoUrl)} {owner} {t.id}" onSelect={() => run({ label: t.id, run: () => goto(`/t/${t.id}`) })}>
					<StatusDot status={t.status} />
					<span class="truncate">{t.title || 'Untitled thread'}</span>
					<span class="ml-auto flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
						{projectName(t.repoUrl)}
						{#if t.ownerId !== threads.me?.id}<UserAvatar name={owner} class="size-4" />{/if}
					</span>
				</Command.Item>
			{/each}
		</Command.Group>
		{#if ui.commands.length}
			<Command.Group heading="This thread">
				{#each ui.commands as c (c.label)}{@render item(c)}{/each}
			</Command.Group>
		{/if}
		<Command.Group heading="General">
			{#each global as c (c.label)}{@render item(c)}{/each}
		</Command.Group>
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
