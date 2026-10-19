<script lang="ts">
	import { Button } from '#lib/components/ui/button/index.js';
	import { api } from '#lib/api.js';
	import { Mode, Status, type Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { threads } from '#lib/threads.svelte.js';

	let { thread, act }: { thread: Thread; act: (call: Promise<unknown>) => Promise<boolean> } = $props();
</script>

{#if thread.mode === Mode.CHAT}
	<div class="mb-2 flex items-center gap-2 rounded-md border bg-muted/50 py-1 pr-1 pl-3 text-xs text-muted-foreground">
		<span class="flex-1">Chat mode — the agent only replies when someone mentions @agent.</span>
		{#if threads.me?.id === thread.ownerId}
			<Button variant="ghost" size="xs" onclick={() => act(api.setMode({ threadId: thread.id, mode: Mode.AGENT }))}>
				Back to agent mode
			</Button>
		{/if}
	</div>
{/if}
{#if thread.status === Status.SUSPENDED}
	<p class="mb-2 text-xs text-muted-foreground">The environment is paused; sending a prompt resumes it.</p>
{/if}
