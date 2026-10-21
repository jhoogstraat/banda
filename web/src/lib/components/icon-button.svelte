<script lang="ts">
	import type { Component } from 'svelte';
	import { mergeProps } from 'svelte-toolbelt';
	import { Button, type ButtonProps } from '#lib/components/ui/button/index.js';
	import { Tooltip, TooltipContent, TooltipTrigger } from '#lib/components/ui/tooltip/index.js';

	// A ghost icon button with a tooltip. `keys` is its shortcut hint. Extra props (also a menu trigger's) go to the button.
	let { icon: Icon, label, keys, ...rest }: ButtonProps & { icon: Component; label: string; keys?: string } = $props();
</script>

<Tooltip>
	<TooltipTrigger>
		{#snippet child({ props })}
			<Button variant="ghost" size="icon-sm" aria-label={label} {...mergeProps(props, rest)}><Icon /></Button>
		{/snippet}
	</TooltipTrigger>
	<TooltipContent>{label}{#if keys}<kbd class="ml-2 font-mono opacity-60">{keys}</kbd>{/if}</TooltipContent>
</Tooltip>
