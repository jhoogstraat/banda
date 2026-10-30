<script lang="ts">
	// A small button that copies text and says so, for values shown in Settings.
	import CheckIcon from '@lucide/svelte/icons/check';
	import CopyIcon from '@lucide/svelte/icons/copy';
	import { Button } from '#lib/components/ui/button/index.js';
	import { UseClipboard } from '#lib/hooks/use-clipboard.svelte.js';

	let { text, label, class: className = '' }: { text: string; label: string; class?: string } = $props();
	const clipboard = new UseClipboard({ delay: 1500 });
</script>

<Button size="icon-xs" variant="ghost" class={className} aria-label={label} title={label} onclick={() => clipboard.copy(text)}>
	{#if clipboard.status === 'success'}<CheckIcon class="text-emerald-700 dark:text-emerald-400" />{:else}<CopyIcon />{/if}
</Button>
<span class="sr-only" aria-live="polite">{clipboard.status === 'success' ? 'Copied' : clipboard.status === 'failure' ? 'Copying failed' : ''}</span>
