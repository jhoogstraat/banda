<script lang="ts">
	import { Input } from '#lib/components/ui/input/index.js';

	// Logins to ask for a review, typed or picked from the repository's (choices): the Pull request tab's and the
	// create dialog's. id names the choices' datalist.
	let { value = $bindable(''), choices, id }: { value: string; choices: string[]; id: string } = $props();
</script>

<Input bind:value list={id} placeholder={choices.length ? 'Pick or type logins' : 'Logins, comma-separated'} aria-label="Reviewers to ask" class="h-7 text-xs" />
<datalist {id}>
	{#each choices as l (l)}<option value={l}></option>{/each}
</datalist>
