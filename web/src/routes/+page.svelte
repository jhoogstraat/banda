<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import BotIcon from '@lucide/svelte/icons/bot';
	import BrainIcon from '@lucide/svelte/icons/brain';
	import CpuIcon from '@lucide/svelte/icons/cpu';
	import { ConversationEmptyState } from '#lib/components/ai-elements/conversation/index.js';
	import * as PromptInput from '#lib/components/ai-elements/prompt-input/index.js';
	import OptionMenu from '#lib/components/option-menu.svelte';
	import PageHeader from '#lib/components/page-header.svelte';
	import RepoPicker from '#lib/components/repo-picker.svelte';
	import { api } from '#lib/api.js';
	import { loadThreads, projects, threads } from '#lib/threads.svelte.js';

	// A new thread is an empty chat: the first message creates it. The last choices are remembered
	// (the app is a client-only SPA, so localStorage is always there).
	const saved = (key: string, fallback: string) => localStorage.getItem(`banda.${key}`) ?? fallback;
	let repoUrl = $state(threads.project || saved('repoUrl', ''));
	let baseBranch = $state(saved('baseBranch', 'main'));
	let model = $state(saved('model', 'claude-opus-5-5'));
	let effort = $state(saved('effort', 'high'));
	// Without a remembered choice, default to the most recently active repository once the sidebar has loaded.
	$effect(() => {
		if (!repoUrl) repoUrl = projects()[0] ?? '';
	});
	// The new-thread dialog links here with ?repo=<url>.
	$effect(() => {
		const repo = page.url.searchParams.get('repo');
		if (repo) repoUrl = repo;
	});
	let busy = $state(false);
	let error = $state('');

	async function submit({ text }: { text: string }) {
		if (!text.trim()) return;
		if (!repoUrl) {
			error = 'Choose a repository first.';
			throw new Error(error); // keeps the typed prompt in the box
		}
		busy = true;
		error = '';
		for (const [k, v] of Object.entries({ repoUrl, baseBranch, model, effort })) localStorage.setItem(`banda.${k}`, v);
		try {
			const { thread } = await api.createThread({ repoUrl, baseBranch, model, effort, prompt: text });
			loadThreads().catch(() => {});
			await goto(`/t/${thread!.id}`);
		} catch (err) {
			error = (err as Error).message;
			busy = false;
			throw err;
		}
	}
</script>

<div class="flex min-w-0 flex-1 flex-col">
	<PageHeader><h1 class="text-sm font-medium">New thread</h1></PageHeader>

	<div class="flex min-h-0 flex-1 items-center justify-center overflow-y-auto p-6">
		<ConversationEmptyState
			title="What should the agent work on?"
			description="It works in its own sandbox, on its own branch. Teammates can follow along and join in."
		>
			{#snippet icon()}<BotIcon class="size-8" />{/snippet}
		</ConversationEmptyState>
	</div>

	<div class="mx-auto w-full max-w-3xl shrink-0 px-4 pb-4">
		{#if error}<p class="mb-2 text-sm text-destructive">{error}</p>{/if}
		<PromptInput.Root onSubmit={submit}>
			<fieldset disabled={busy} class="contents">
				<PromptInput.Body>
					<PromptInput.Textarea placeholder="Describe the task. Enter sends, Shift+Enter adds a line." />
				</PromptInput.Body>
				<PromptInput.Toolbar>
					<PromptInput.Tools class="min-w-0 flex-wrap">
						<RepoPicker bind:repoUrl bind:baseBranch />
						<OptionMenu
							label="Model"
							icon={CpuIcon}
							bind:value={model}
							options={['claude-opus-5-5', 'claude-sonnet-5-5', 'claude-haiku-5-5']}
						/>
						<OptionMenu label="Effort" icon={BrainIcon} bind:value={effort} options={['low', 'medium', 'high', 'xhigh', 'max']} />
					</PromptInput.Tools>
					<PromptInput.Submit status={busy ? 'submitted' : undefined} />
				</PromptInput.Toolbar>
			</fieldset>
		</PromptInput.Root>
	</div>
</div>
