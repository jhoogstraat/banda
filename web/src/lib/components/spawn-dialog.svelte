<script lang="ts">
	import { goto } from '$app/navigation';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Dialog from '#lib/components/ui/dialog/index.js';
	import { Textarea } from '#lib/components/ui/textarea/index.js';
	import { api } from '#lib/api.js';
	import type { Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { threads } from '#lib/threads.svelte.js';

	let { open = $bindable(false), thread }: { open: boolean; thread: Thread } = $props();

	let prompt = $state('');
	let error = $state('');
	let busy = $state(false);

	async function spawn(e?: Event) {
		e?.preventDefault();
		if (!prompt.trim() || busy) return;
		busy = true;
		try {
			const { thread: child } = await api.spawnThread({ parentId: thread.id, prompt });
			open = false;
			prompt = error = '';
			goto(`/t/${child!.id}`);
		} catch (err) {
			error = (err as Error).message;
		} finally {
			busy = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="sm:max-w-lg">
		<Dialog.Header>
			<Dialog.Title>Spawn a sub-thread</Dialog.Title>
			<Dialog.Description>
				{#if thread.repoUrl}
					A new agent in its own sandbox. It starts from <code>{thread.branch}</code> if that was pushed, else from
					<code>{thread.baseBranch}</code>, and reports back here when it finishes.
				{:else}
					A new agent in its own sandbox, without a repository like this thread. It reports back here when it finishes.
				{/if}
				{#if threads.me?.id !== thread.ownerId}It is yours and runs on your Claude subscription.{/if}
			</Dialog.Description>
		</Dialog.Header>
		<form class="space-y-3" onsubmit={spawn}>
			<Textarea
				bind:value={prompt}
				rows={5}
				placeholder="What should it work on?"
				aria-label="Prompt"
				onkeydown={(e) => e.key === 'Enter' && (e.metaKey || e.ctrlKey) && spawn(e)}
			/>
			{#if error}<p class="text-sm text-destructive">{error}</p>{/if}
			<Dialog.Footer><Button type="submit" disabled={!prompt.trim() || busy}>Spawn</Button></Dialog.Footer>
		</form>
	</Dialog.Content>
</Dialog.Root>
