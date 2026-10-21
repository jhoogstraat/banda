<script lang="ts" module>
	// The first is the default, as in the API.
	export const models = ['claude-opus-5-5', 'claude-sonnet-5-5', 'claude-haiku-5-5'];
	export const efforts = ['low', 'medium', 'high', 'xhigh', 'max'];
	// How many threads to start with one prompt; "Each model" races the models.
	export const tryOptions = ['1 try', '2 tries', '3 tries', '4 tries', 'Each model'];
	export const raceTries = (choice: string, model: string, effort: string) =>
		choice === 'Each model'
			? models.map((model) => ({ model, effort }))
			: Array.from({ length: parseInt(choice) || 1 }, () => ({ model, effort }));
</script>

<script lang="ts">
	import type { Component } from 'svelte';
	import * as PromptInput from '#lib/components/ai-elements/prompt-input/index.js';
	import * as DropdownMenu from '#lib/components/ui/dropdown-menu/index.js';

	// A composer toolbar chip that picks one of a few values (model, effort).
	let {
		value = $bindable(),
		options,
		icon: Icon,
		label
	}: { value: string; options: string[]; icon: Component; label: string } = $props();
</script>

<DropdownMenu.Root>
	<DropdownMenu.Trigger>
		{#snippet child({ props })}
			<PromptInput.Button {...props} size="sm" aria-label="{label}: {value}"><Icon /> {value}</PromptInput.Button>
		{/snippet}
	</DropdownMenu.Trigger>
	<DropdownMenu.Content align="start" class="w-48">
		<DropdownMenu.Label>{label}</DropdownMenu.Label>
		<DropdownMenu.RadioGroup bind:value>
			{#each options as o (o)}<DropdownMenu.RadioItem value={o} closeOnSelect>{o}</DropdownMenu.RadioItem>{/each}
		</DropdownMenu.RadioGroup>
	</DropdownMenu.Content>
</DropdownMenu.Root>
