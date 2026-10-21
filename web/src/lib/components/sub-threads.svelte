<script lang="ts">
	import GitForkIcon from '@lucide/svelte/icons/git-fork';
	import StatusDot from '#lib/components/status-dot.svelte';
	import { api } from '#lib/api.js';
	import type { Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { threads, unread } from '#lib/threads.svelte.js';

	// `version` changes with the parent's lineage events, which refetch the list.
	let { threadId, version = 0 }: { threadId: string; version?: number } = $props();

	let fetched = $state<Thread[]>([]);
	$effect(() => {
		void version;
		api.lineageChildren({ threadId }).then((r) => (fetched = r.threads), () => {});
	});
	// My own sub-threads stay live through the inbox; teammates' ones refresh with the lineage events.
	const children = $derived.by(() => {
		const live = threads.list.filter((t) => t.parentId === threadId);
		return [...live, ...fetched.filter((f) => !live.some((l) => l.id === f.id))];
	});
</script>

{#if children.length}
	<nav class="flex shrink-0 items-center gap-1.5 overflow-x-auto border-b px-3 py-1.5 text-xs" aria-label="Sub-threads">
		<span class="flex shrink-0 items-center gap-1 text-muted-foreground"><GitForkIcon class="size-3.5" /> {children.length}</span>
		{#each children as c (c.id)}
			<a
				href="/t/{c.id}"
				title={c.title}
				class="flex max-w-52 shrink-0 items-center gap-1.5 rounded-full border px-2 py-0.5 hover:bg-muted {unread(c) ? 'font-semibold' : 'text-muted-foreground'}"
			>
				<StatusDot status={c.status} />
				<span class="truncate">{c.title || 'Untitled thread'}</span>
				{#if unread(c)}<span class="size-1.5 shrink-0 rounded-full bg-primary" aria-label="Unread"></span>{/if}
			</a>
		{/each}
	</nav>
{/if}
