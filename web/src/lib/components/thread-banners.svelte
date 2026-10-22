<script lang="ts">
	import GitForkIcon from '@lucide/svelte/icons/git-fork';
	import { Button } from '#lib/components/ui/button/index.js';
	import { api } from '#lib/api.js';
	import type { Thread } from '#lib/gen/banda/v1/banda_pb.js';

	let { thread }: { thread: Thread } = $props();

	let source = $state('');
	$effect(() => {
		const from = thread.forkedFrom?.threadId;
		source = '';
		if (from) api.getThread({ threadId: from }).then((r) => (source = r.thread?.title ?? ''), () => {});
	});
</script>

{#if thread.forkedFrom || thread.raceId}
	<div class="mb-2 flex items-center gap-2 rounded-lg border bg-muted/40 py-1 pr-1 pl-3 text-xs text-muted-foreground">
		<GitForkIcon class="size-3.5 shrink-0" />
		<span class="min-w-0 flex-1 truncate">
			{#if thread.forkedFrom}
				Forked from <a class="underline" href="/t/{thread.forkedFrom.threadId}">{source || 'another thread'}</a> after turn {thread.forkedFrom.turn}.
			{:else}
				One of several tries of the same prompt.
			{/if}
		</span>
		{#if thread.raceId}
			<Button variant="ghost" size="xs" href="/compare?race={thread.raceId}">Compare tries</Button>
		{/if}
	</div>
{/if}

