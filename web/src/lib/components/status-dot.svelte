<script lang="ts">
	import { Status } from '#lib/gen/banda/v1/banda_pb.js';

	// waiting: "Waiting for you · Bash" (see waiting() in threads.svelte.ts) overrides the status: the agent is blocked on a person.
	let { status, label = false, waiting = '' }: { status: Status; label?: boolean; waiting?: string } = $props();

	// [dot style, short label]
	const look = {
		[Status.STARTING]: ['bg-muted-foreground', 'Starting'],
		[Status.WORKING]: ['bg-info animate-pulse', 'Working'],
		[Status.AWAITING_INPUT]: ['bg-success', 'Ready'],
		[Status.ERRORED]: ['bg-destructive', 'Error'],
		[Status.SUSPENDED]: ['border border-muted-foreground', 'Paused']
	} as Partial<Record<Status, [string, string]>>;
	const [dot, text] = $derived(waiting ? ['bg-warning ring-2 ring-warning/30', waiting] : (look[status] ?? ['', '']));
</script>

<span class="inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap">
	<span class="size-2 shrink-0 rounded-full {dot}"></span>
	{#if label}<span class="text-xs {waiting ? 'font-medium text-warning-foreground' : 'text-muted-foreground'}">{text}</span>{/if}
</span>
