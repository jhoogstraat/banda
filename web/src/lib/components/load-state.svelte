<script lang="ts">
	import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
	import RotateCcwIcon from '@lucide/svelte/icons/rotate-ccw';
	import SearchXIcon from '@lucide/svelte/icons/search-x';
	import { Button } from '#lib/components/ui/button/index.js';

	// What a page shows instead of the thing it couldn't load: missing (no `retry`), or a failed load to retry.
	let { title, description, retry }: { title: string; description: string; retry?: () => void } = $props();
</script>

<div role={retry ? 'alert' : 'status'} class="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-center">
	{#if retry}<CircleAlertIcon class="size-8 text-destructive" />{:else}<SearchXIcon class="size-8 text-muted-foreground" />{/if}
	<div class="space-y-1">
		<h2 class="text-base font-medium">{title}</h2>
		<p class="max-w-sm text-sm text-muted-foreground">{description}</p>
	</div>
	<div class="flex gap-2">
		{#if retry}<Button size="sm" onclick={retry}><RotateCcwIcon /> Try again</Button>{/if}
		<Button size="sm" variant={retry ? 'outline' : 'default'} href="/">Go to your threads</Button>
	</div>
</div>
