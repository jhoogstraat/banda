<script lang="ts">
	// A Settings section's title, what it is for in a few words, and the section's error if any.
	let { title, note = '', error = '' }: { title: string; note?: string; error?: string } = $props();
</script>

<h3 class="mb-3 flex flex-wrap items-baseline gap-x-2 text-sm font-medium">
	{title}
	{#if note}<span class="text-xs font-normal text-muted-foreground">{note}</span>{/if}
</h3>
{#if error}<p class="mb-3 text-sm text-destructive" role="alert">{error}</p>{/if}
