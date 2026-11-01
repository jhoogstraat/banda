<script lang="ts">
	import { threads } from '#lib/threads.svelte.js';

	let { text }: { text: string } = $props();

	const escape = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
	// Longest names first, so "@bobby" is not cut at "@bob". The capture group makes odd parts the mentions.
	const parts = $derived(
		text.split(
			new RegExp(
				`(@(?:${[
					'agent\\b',
					...threads.users
						.map((u) => u.name)
						.filter(Boolean)
						.sort((a, b) => b.length - a.length)
						.map(escape)
				].join('|')}))`,
				'g'
			)
		)
	);
</script>

{#each parts as p, i}{#if i % 2}<span class="rounded bg-primary/15 px-0.5 font-medium text-primary highlighter:bg-foreground/10 highlighter:font-semibold highlighter:text-foreground">{p}</span>{:else}{p}{/if}{/each}
