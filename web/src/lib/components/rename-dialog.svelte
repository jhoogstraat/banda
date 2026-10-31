<script lang="ts">
	import { untrack } from 'svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Dialog from '#lib/components/ui/dialog/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { api } from '#lib/api.js';
	import type { Thread } from '#lib/gen/banda/v1/banda_pb.js';

	// The owner gives the thread a better title than its first prompt.
	let { open = $bindable(false), thread }: { open: boolean; thread: Thread } = $props();

	let title = $state('');
	let error = $state('');
	let busy = $state(false);
	$effect(() => {
		if (open) untrack(() => ([title, error] = [thread.title, ''])); // not again on a refresh
	});

	async function rename(e: Event) {
		e.preventDefault();
		if (!title.trim() || busy) return;
		busy = true;
		try {
			await api.renameThread({ threadId: thread.id, title });
			open = false;
		} catch (err) {
			error = (err as Error).message;
		} finally {
			busy = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="sm:max-w-md">
		<Dialog.Header>
			<Dialog.Title>Rename this thread</Dialog.Title>
			<Dialog.Description>Everyone sees the new title.</Dialog.Description>
		</Dialog.Header>
		<form class="space-y-3" onsubmit={rename}>
			<Input bind:value={title} maxlength={60} aria-label="Title" />
			{#if error}<p class="text-sm text-destructive">{error}</p>{/if}
			<Dialog.Footer>
				<Button type="submit" disabled={!title.trim() || busy}>Rename</Button>
			</Dialog.Footer>
		</form>
	</Dialog.Content>
</Dialog.Root>
