<script lang="ts">
	import { Status } from '#lib/gen/banda/v1/banda_pb.js';

	let { status, label = false }: { status: Status; label?: boolean } = $props();

	// [dot style, short label]
	const look = {
		[Status.STARTING]: ['bg-muted-foreground', 'Starting'],
		[Status.WORKING]: ['bg-sky-400 animate-pulse', 'Working'],
		[Status.AWAITING_INPUT]: ['bg-amber-400', 'Needs input'],
		[Status.ERRORED]: ['bg-red-500', 'Error'],
		[Status.SUSPENDED]: ['border border-muted-foreground', 'Paused']
	} as Partial<Record<Status, [string, string]>>;
</script>

<span class="inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap">
	<span class="size-2 shrink-0 rounded-full {look[status]?.[0]}"></span>
	{#if label}<span class="text-xs text-muted-foreground">{look[status]?.[1]}</span>{/if}
</span>
