<script lang="ts" module>
	import { Status } from '../gen/banda/v1/banda_pb';

	export const statusLabel = (s: Status) => Status[s].toLowerCase().replace('_', ' ');
</script>

<script lang="ts">
	let { status, label = false }: { status: Status; label?: boolean } = $props();

	const color = {
		[Status.STARTING]: 'bg-muted-foreground',
		[Status.WORKING]: 'bg-sky-400 animate-pulse',
		[Status.AWAITING_INPUT]: 'bg-amber-400',
		[Status.ERRORED]: 'bg-red-500'
	} as Partial<Record<Status, string>>;
</script>

<span class="inline-flex items-center gap-1.5">
	<span class="size-2 shrink-0 rounded-full {color[status]}"></span>
	{#if label}<span class="text-xs">{statusLabel(status)}</span>{/if}
</span>
