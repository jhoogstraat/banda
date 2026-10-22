<script lang="ts">
	import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
	import FolderGit2Icon from '@lucide/svelte/icons/folder-git-2';
	import RotateCcwIcon from '@lucide/svelte/icons/rotate-ccw';
	import SquareTerminalIcon from '@lucide/svelte/icons/square-terminal';
	import { Button } from '#lib/components/ui/button/index.js';
	import type { Chat } from '#lib/events.js';
	import { SetupState, type Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { userName } from '#lib/threads.svelte.js';

	// Why an errored thread stopped, from its latest status change (`reason`) and, for a failed setup step,
	// that step's output. The owner retries or debugs in the terminal (the runner stays up after a failed step),
	// or, with `onrepo` (the repository was never checked out), changes or removes the repository.
	let {
		thread,
		owner,
		reason,
		chat,
		retry,
		onterminal,
		onrepo
	}: { thread: Thread; owner: boolean; reason: string; chat: Chat; retry: () => void; onterminal: () => void; onrepo?: () => void } = $props();

	const step = $derived.by(() => {
		if (!reason.startsWith('Setup failed')) return;
		const setup = chat.items.findLast((i) => i.kind === 'setup');
		return setup?.kind === 'setup' ? setup.steps.find((s) => s.state === SetupState.FAILED) : undefined;
	});
	const tail = $derived(step?.output.trimEnd().split('\n').slice(-12).join('\n') ?? '');
</script>

<div role="alert" class="space-y-2 rounded-lg border border-destructive/40 bg-destructive/10 px-4 py-3 text-sm">
	<div class="flex items-start gap-3">
		<CircleAlertIcon class="mt-0.5 size-4 shrink-0 text-destructive" />
		<div class="min-w-0 flex-1">
			<p class="font-medium">{reason || 'The environment stopped.'}</p>
			<p class="text-xs text-muted-foreground">
				{#if owner}
					{#if onrepo}
						Correct the repository's URL or base branch, or remove it. The folder's files are untouched.
					{:else}
						{step ? 'Fix it in the terminal or the repository, then retry.' : 'Retry starts the environment again; the checkout and the chat are kept.'}
					{/if}
				{:else}
					Only {userName(thread.ownerId)} can retry this thread.
				{/if}
			</p>
		</div>
		{#if owner}
			<div class="flex shrink-0 gap-1.5">
				{#if onrepo}<Button size="sm" variant="outline" onclick={onrepo}><FolderGit2Icon /> Change the repository</Button>{/if}
				{#if step}<Button size="sm" variant="outline" onclick={onterminal}><SquareTerminalIcon /> Open terminal</Button>{/if}
				<Button size="sm" onclick={retry}><RotateCcwIcon /> Retry</Button>
			</div>
		{/if}
	</div>
	{#if tail}
		<pre aria-label="Output of {step?.title}" class="max-h-48 overflow-auto rounded-md bg-background/60 p-2 font-mono text-xs whitespace-pre-wrap">{tail}</pre>
	{/if}
</div>
