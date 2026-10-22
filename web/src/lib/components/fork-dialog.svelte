<script lang="ts">
	import { goto } from '$app/navigation';
	import BrainIcon from '@lucide/svelte/icons/brain';
	import CpuIcon from '@lucide/svelte/icons/cpu';
	import SplitIcon from '@lucide/svelte/icons/split';
	import OptionMenu, { efforts, models, raceTries, tryOptions } from '#lib/components/option-menu.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Dialog from '#lib/components/ui/dialog/index.js';
	import { Textarea } from '#lib/components/ui/textarea/index.js';
	import { api } from '#lib/api.js';
	import type { Thread } from '#lib/gen/banda/v1/banda_pb.js';

	// Forks a thread (anyone's) into new threads of the caller: same changes and conversation,
	// optionally another model or effort, a new instruction, several tries.
	let { open = $bindable(false), thread }: { open: boolean; thread: Thread } = $props();

	let instruction = $state('');
	let model = $state('');
	let effort = $state('');
	let tries = $state(tryOptions[0]);
	let busy = $state(false);
	let error = $state('');
	$effect(() => {
		if (!open) return;
		[model, effort, tries, error] = [thread.model, thread.effort, tryOptions[0], ''];
	});

	async function fork(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		try {
			const { threads } = await api.fork({ threadId: thread.id, prompt: instruction, tries: raceTries(tries, model, effort) });
			open = false;
			instruction = '';
			await goto(threads.length > 1 ? `/compare?race=${threads[0].raceId}` : `/t/${threads[0].id}`);
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
			<Dialog.Title>Fork this thread</Dialog.Title>
			<Dialog.Description>
				{thread.repoUrl
					? 'New threads of yours that start from its changes and know its conversation.'
					: 'New threads of yours that know its conversation. Without a repository its files stay here.'}
				This thread stays as it is.
			</Dialog.Description>
		</Dialog.Header>
		<form class="flex flex-col gap-3" onsubmit={fork}>
			<Textarea bind:value={instruction} placeholder="What should the fork do differently? Leave empty to have it sum up where things stand." rows={4} />
			<div class="flex flex-wrap items-center gap-1">
				<OptionMenu label="Model" icon={CpuIcon} bind:value={model} options={models} />
				<OptionMenu label="Effort" icon={BrainIcon} bind:value={effort} options={efforts} />
				<OptionMenu label="Try N ways" icon={SplitIcon} bind:value={tries} options={tryOptions} />
				<Button type="submit" class="ml-auto" disabled={busy}>Fork</Button>
			</div>
			{#if error}<p class="text-sm text-destructive">{error}</p>{/if}
		</form>
	</Dialog.Content>
</Dialog.Root>
