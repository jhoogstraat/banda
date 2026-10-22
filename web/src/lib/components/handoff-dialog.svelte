<script lang="ts">
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Dialog from '#lib/components/ui/dialog/index.js';
	import { NativeSelect, NativeSelectOption } from '#lib/components/ui/native-select/index.js';
	import { Textarea } from '#lib/components/ui/textarea/index.js';
	import { api } from '#lib/api.js';
	import { Status, type Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { threads } from '#lib/threads.svelte.js';

	// Offers the owner's thread to a teammate, who accepts it in the chat.
	let { open = $bindable(false), thread }: { open: boolean; thread: Thread } = $props();

	let to = $state('');
	let note = $state('');
	let error = $state('');
	let busy = $state(false);
	const teammates = $derived(threads.users.filter((u) => u.id !== threads.me?.id));

	async function offer(e: Event) {
		e.preventDefault();
		if (!to || busy) return;
		busy = true;
		try {
			await api.handoffOffer({ threadId: thread.id, toUserId: to, note });
			open = false;
			to = note = error = '';
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
			<Dialog.Title>Hand off this thread</Dialog.Title>
			<Dialog.Description>
				Once they accept, it is theirs: the agent runs on their Claude subscription with their secrets, on the same branch and
				checkout.
				{#if thread.status === Status.AWAITING_INPUT}The agent first writes them a briefing.{:else}The agent isn't idle, so it writes no briefing.{/if}
			</Dialog.Description>
		</Dialog.Header>
		<form class="space-y-3" onsubmit={offer}>
			<NativeSelect bind:value={to} class="w-full" aria-label="Teammate">
				<NativeSelectOption value="" disabled>Choose a teammate</NativeSelectOption>
				{#each teammates as u (u.id)}<NativeSelectOption value={u.id}>{u.name}</NativeSelectOption>{/each}
			</NativeSelect>
			<Textarea bind:value={note} rows={3} placeholder="A note for them (optional)" aria-label="Note" />
			{#if error}<p class="text-sm text-destructive">{error}</p>{/if}
			<Dialog.Footer><Button type="submit" disabled={!to || busy}>Hand off</Button></Dialog.Footer>
		</form>
	</Dialog.Content>
</Dialog.Root>
