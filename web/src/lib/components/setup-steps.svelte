<script lang="ts">
	// A numbered list of setup steps in Settings: the ones done ticked, the current one open with its body, the
	// ones after it named only, so the whole path is visible from the start.
	import type { Snippet } from 'svelte';
	import CheckIcon from '@lucide/svelte/icons/check';

	let { steps, current, body }: { steps: string[]; /** 1-based. */ current: number; body: Snippet<[number]> } = $props();
</script>

<ol class="divide-y rounded-lg border">
	{#each steps as title, i (title)}
		{@const n = i + 1}
		{@const done = n < current}
		<li class="flex gap-3 p-4" aria-current={n === current ? 'step' : undefined}>
			<span
				class="grid size-6 shrink-0 place-items-center rounded-full text-xs font-medium {done
					? 'bg-success/15 text-success-foreground'
					: n === current
						? 'bg-primary text-primary-foreground'
						: 'border text-muted-foreground'}"
				aria-hidden="true"
			>
				{#if done}<CheckIcon class="size-3.5" />{:else}{n}{/if}
			</span>
			<div class="min-w-0 flex-1 space-y-3">
				<h4 class="pt-0.5 text-sm {n > current ? 'text-muted-foreground' : 'font-medium'}">
					{title}{#if done}<span class="sr-only"> (done)</span>{/if}
				</h4>
				{@render body(n)}
			</div>
		</li>
	{/each}
</ol>
