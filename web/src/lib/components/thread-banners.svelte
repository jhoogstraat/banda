<script lang="ts">
	import GitForkIcon from '@lucide/svelte/icons/git-fork';
	import MessagesSquareIcon from '@lucide/svelte/icons/messages-square';
	import { Button } from '#lib/components/ui/button/index.js';
	import { api } from '#lib/api.js';
	import { Mode, type Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { threads } from '#lib/threads.svelte.js';

	let { thread, act }: { thread: Thread; act: (call: Promise<unknown>) => Promise<boolean> } = $props();

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

{#if thread.mode === Mode.CHAT}
	<div class="mb-2 flex items-center gap-2 rounded-lg border bg-muted/40 py-1 pr-1 pl-3 text-xs text-muted-foreground">
		<MessagesSquareIcon class="size-3.5 shrink-0" />
		<span class="flex-1">Chat mode: the agent only replies to @agent.</span>
		{#if threads.me?.id === thread.ownerId}
			<Button variant="ghost" size="xs" onclick={() => act(api.setMode({ threadId: thread.id, mode: Mode.AGENT }))}>
				Back to agent mode
			</Button>
		{/if}
	</div>
{/if}
