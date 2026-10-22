<script lang="ts">
	import XIcon from '@lucide/svelte/icons/x';
	import { NativeSelect, NativeSelectOption } from '#lib/components/ui/native-select/index.js';

	// A pull request's labels as chips, removable and added from the repository's (choices) when editable:
	// the Pull request tab's and the create dialog's.
	let {
		labels,
		choices,
		editable,
		disabled = false,
		onadd,
		onremove
	}: { labels: string[]; choices: string[]; editable: boolean; disabled?: boolean; onadd: (l: string) => void; onremove: (l: string) => void } = $props();

	const left = $derived(choices.filter((l) => !labels.includes(l)));
	const add = (e: Event & { currentTarget: HTMLSelectElement }) => {
		const label = e.currentTarget.value;
		e.currentTarget.value = '';
		if (label) onadd(label);
	};
</script>

{#if labels.length || (editable && left.length)}
	<section class="flex flex-wrap items-center gap-1" aria-label="Labels">
		{#each labels as l (l)}
			<span class="flex items-center gap-0.5 rounded-full border px-2 py-0.5 text-xs">
				{l}
				{#if editable}
					<button type="button" class="text-muted-foreground hover:text-foreground" aria-label="Remove label {l}" {disabled} onclick={() => onremove(l)}>
						<XIcon class="size-3" />
					</button>
				{/if}
			</span>
		{/each}
		{#if editable && left.length}
			<NativeSelect size="sm" aria-label="Add label" {disabled} onchange={add}>
				<NativeSelectOption value="">Add label…</NativeSelectOption>
				{#each left as l (l)}<NativeSelectOption value={l}>{l}</NativeSelectOption>{/each}
			</NativeSelect>
		{/if}
	</section>
{/if}
