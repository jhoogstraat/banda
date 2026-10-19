<script lang="ts">
	import PageHeader from '#lib/components/page-header.svelte';
	import { goto } from '$app/navigation';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { Label } from '#lib/components/ui/label/index.js';
	import { NativeSelect, NativeSelectOption } from '#lib/components/ui/native-select/index.js';
	import { Textarea } from '#lib/components/ui/textarea/index.js';
	import { api } from '#lib/api.js';
	import { loadThreads, threads } from '#lib/threads.svelte.js';

	// The last repository is remembered (the app is a client-only SPA, so localStorage is always there).
	let repoUrl = $state(localStorage.getItem('banda.repoUrl') ?? '');
	let baseBranch = $state(localStorage.getItem('banda.baseBranch') ?? 'main');
	let model = $state('claude-opus-5-5');
	let effort = $state('high');
	let prompt = $state('');
	let busy = $state(false);
	let error = $state('');

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = '';
		localStorage.setItem('banda.repoUrl', repoUrl);
		localStorage.setItem('banda.baseBranch', baseBranch);
		try {
			const { thread } = await api.createThread({ repoUrl, baseBranch, model, effort, prompt });
			loadThreads().catch(() => {});
			await goto(`/t/${thread!.id}`);
		} catch (err) {
			error = (err as Error).message;
			busy = false;
		}
	}
</script>

<div class="flex min-w-0 flex-1 flex-col">
	<PageHeader />
	<!-- Container queries: the form lays itself out by its own width, not the screen's. -->
	<div class="@container flex flex-1 items-start justify-center overflow-y-auto p-3 @lg:items-center @lg:p-8">
		<form onsubmit={submit} class="flex w-full max-w-xl flex-col gap-4 rounded-xl border bg-card p-4 shadow-sm @lg:p-6">
			<div>
				<h1 class="text-xl font-semibold tracking-tight">New thread</h1>
				<p class="text-sm text-muted-foreground">
					{#if threads.me && !threads.list.length && !threads.viewing}
						You have no threads yet. Point the agent at a repository and describe the task to start your first one.
					{:else}
						A coding agent works on your task in its own sandbox, on its own branch.
					{/if}
				</p>
			</div>
			<div class="flex flex-col gap-1.5">
				<Label for="harness">Harness</Label>
				<NativeSelect id="harness" value="claude-code" class="w-full">
					<NativeSelectOption value="claude-code">Claude Code</NativeSelectOption>
					<NativeSelectOption value="codex" disabled>Codex — soon</NativeSelectOption>
				</NativeSelect>
			</div>
			<div class="grid gap-4 @md:grid-cols-[2fr_1fr]">
				<div class="flex flex-col gap-1.5">
					<Label for="repo">Repository URL</Label>
					<Input id="repo" bind:value={repoUrl} placeholder="https://github.com/org/repo.git" required />
				</div>
				<div class="flex flex-col gap-1.5">
					<Label for="base">Base branch</Label>
					<Input id="base" bind:value={baseBranch} required />
				</div>
			</div>
			<div class="grid grid-cols-2 gap-4">
				<div class="flex flex-col gap-1.5">
					<Label for="model">Model</Label>
					<NativeSelect id="model" bind:value={model} class="w-full">
						<NativeSelectOption value="claude-opus-5-5">claude-opus-5-5</NativeSelectOption>
						<NativeSelectOption value="claude-sonnet-5-5">claude-sonnet-5-5</NativeSelectOption>
						<NativeSelectOption value="claude-haiku-5-5">claude-haiku-5-5</NativeSelectOption>
					</NativeSelect>
				</div>
				<div class="flex flex-col gap-1.5">
					<Label for="effort">Effort</Label>
					<NativeSelect id="effort" bind:value={effort} class="w-full">
						{#each ['low', 'medium', 'high', 'xhigh', 'max'] as e (e)}
							<NativeSelectOption value={e}>{e}</NativeSelectOption>
						{/each}
					</NativeSelect>
				</div>
			</div>
			<div class="flex flex-col gap-1.5">
				<Label for="prompt">Prompt</Label>
				<Textarea
					id="prompt"
					bind:value={prompt}
					rows={6}
					placeholder="What should the agent do?"
					required
					onkeydown={(e) => (e.metaKey || e.ctrlKey) && e.key === 'Enter' && e.currentTarget.form?.requestSubmit()}
				/>
			</div>
			{#if error}<p class="text-sm text-destructive">{error}</p>{/if}
			<Button type="submit" disabled={busy}>
				{busy ? 'Creating…' : 'Create thread'}
				<span class="text-xs opacity-60">⌘/Ctrl + Enter</span>
			</Button>
		</form>
	</div>
</div>
