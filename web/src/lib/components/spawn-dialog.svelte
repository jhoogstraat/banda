<script lang="ts">
	import { goto } from '$app/navigation';
	import ContactRoundIcon from '@lucide/svelte/icons/contact-round';
	import * as PromptInput from '#lib/components/ai-elements/prompt-input/index.js';
	import type { PromptInputMessage } from '#lib/components/ai-elements/prompt-input/index.js';
	import AttachButton, { attachLimits } from '#lib/components/attach-button.svelte';
	import OptionMenu from '#lib/components/option-menu.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Dialog from '#lib/components/ui/dialog/index.js';
	import { api, upload } from '#lib/api.js';
	import type { Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { profileLabel, profileLabels, profileName } from '#lib/profiles.js';
	import { threads } from '#lib/threads.svelte.js';

	let { open = $bindable(false), thread }: { open: boolean; thread: Thread } = $props();

	let prompt = $state('');
	// Starts at the parent's, as in the API.
	let profile = $derived(thread.profile);
	let error = $state('');
	let busy = $state(false);

	// Attachments go with the first message and prompt, as on the new-thread page.
	async function spawn({ text, attachments: files }: PromptInputMessage) {
		if (!text.trim() || busy) return;
		busy = true;
		try {
			const attachments = await Promise.all(files.map((f) => upload(f.file)));
			const { thread: child } = await api.spawnThread({ parentId: thread.id, prompt: text, profile, attachments });
			open = false;
			error = '';
			goto(`/t/${child!.id}`);
		} catch (err) {
			error = (err as Error).message;
			throw err; // keeps the typed prompt and the files
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
		{#if error}<p class="text-sm text-destructive">{error}</p>{/if}
		<PromptInput.Root onSubmit={spawn} {...attachLimits} onError={(e) => (error = e.message)}>
			<fieldset disabled={busy} class="contents">
				<PromptInput.Attachments class="px-2">
					{#snippet children(a)}<PromptInput.Attachment data={a} />{/snippet}
				</PromptInput.Attachments>
				<PromptInput.Body>
					<PromptInput.Textarea bind:value={prompt} placeholder="What should it work on?" class="min-h-28" />
				</PromptInput.Body>
				<PromptInput.Toolbar>
					<PromptInput.Tools>
						<AttachButton />
						<OptionMenu label="Profile" icon={ContactRoundIcon} bind:value={() => profileLabel(profile), (v) => (profile = profileName(v))} options={profileLabels} />
					</PromptInput.Tools>
					<Button type="submit" size="sm" disabled={!prompt.trim() || busy}>Spawn</Button>
				</PromptInput.Toolbar>
			</fieldset>
		</PromptInput.Root>
	</Dialog.Content>
</Dialog.Root>
