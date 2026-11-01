<script lang="ts">
	import { Status } from '#lib/gen/banda/v1/banda_pb.js';

	// waiting: "Waiting for you · Bash" (see waiting() in threads.svelte.ts) overrides the status: the agent is blocked on a person.
	let { status, label = false, waiting = '' }: { status: Status; label?: boolean; waiting?: string } = $props();

	// [dot style, short label]
	const look = {
		[Status.STARTING]: ['bg-muted-foreground', 'Starting'],
		[Status.WORKING]: ['bg-info animate-pulse highlighter:bg-foreground', 'Working'],
		[Status.AWAITING_INPUT]: ['bg-success highlighter:bg-muted-foreground', 'Ready'],
		[Status.ERRORED]: ['bg-destructive', 'Error'],
		[Status.SUSPENDED]: ['border border-muted-foreground', 'Paused']
	} as Partial<Record<Status, [string, string]>>;
	const [dot, text] = $derived(waiting ? ['bg-attention ring-2 ring-attention/30 highlighter:ring-1 highlighter:ring-foreground highlighter:dark:ring-2 highlighter:dark:ring-attention/30', waiting] : (look[status] ?? ['', '']));
</script>

<span class="inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap">
	<span class="size-2 shrink-0 rounded-full {dot}"></span>
	{#if label}<span class="text-xs {waiting ? 'font-medium text-attention-text marker' : 'text-muted-foreground'}">{text}</span>{/if}
</span>
