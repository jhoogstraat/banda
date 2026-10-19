<script lang="ts">
	import { goto } from '$app/navigation';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { Label } from '#lib/components/ui/label/index.js';
	import { NativeSelect, NativeSelectOption } from '#lib/components/ui/native-select/index.js';
	import { Textarea } from '#lib/components/ui/textarea/index.js';
	import { api } from '#lib/api.js';
	import { loadThreads } from '#lib/threads.svelte.js';

	let repoUrl = $state('');
	let baseBranch = $state('main');
	let model = $state('claude-opus-5-5');
	let effort = $state('high');
	let prompt = $state('');
	let busy = $state(false);
	let error = $state('');

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = '';
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

<div class="flex-1 overflow-y-auto p-8">
	<form onsubmit={submit} class="mx-auto flex max-w-xl flex-col gap-4">
		<h1 class="text-xl font-semibold tracking-tight">New thread</h1>
		<div class="flex flex-col gap-1.5">
			<Label for="repo">Repository URL</Label>
			<Input id="repo" bind:value={repoUrl} placeholder="https://github.com/org/repo.git" required />
		</div>
		<div class="flex flex-col gap-1.5">
			<Label for="base">Base branch</Label>
			<Input id="base" bind:value={baseBranch} required />
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
			<Textarea id="prompt" bind:value={prompt} rows={6} placeholder="What should the agent do?" required />
		</div>
		{#if error}<p class="text-sm text-destructive">{error}</p>{/if}
		<Button type="submit" disabled={busy}>{busy ? 'Creating…' : 'Create thread'}</Button>
	</form>
</div>
