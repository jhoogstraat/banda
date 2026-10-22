<script lang="ts">
	import { untrack } from 'svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Dialog from '#lib/components/ui/dialog/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { api } from '#lib/api.js';
	import { Status, type Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { noRepo, projects } from '#lib/threads.svelte.js';

	// Gives the owner's thread without a repository one, or changes or removes one whose checkout failed (prefilled).
	// The environment restarts on it.
	let { open = $bindable(false), thread }: { open: boolean; thread: Thread } = $props();

	let repoUrl = $state('');
	let baseBranch = $state('main');
	let error = $state('');
	let busy = $state(false);
	const known = $derived(projects().filter((url) => url !== noRepo));
	$effect(() => {
		if (open) untrack(() => ([repoUrl, baseBranch, error] = [thread.repoUrl, thread.baseBranch || 'main', ''])); // not again on a refresh
	});

	async function add(e: Event, url = repoUrl.trim()) {
		e.preventDefault();
		if ((!url && !thread.repoUrl) || busy) return;
		busy = true;
		try {
			await api.addRepository({ threadId: thread.id, repoUrl: url, baseBranch: baseBranch.trim() });
			open = false;
			repoUrl = error = '';
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
			<Dialog.Title>{thread.repoUrl ? 'Change the repository' : 'Add a repository'}</Dialog.Title>
			<Dialog.Description>
				{#if thread.repoUrl}
					Its checkout failed and left the agent's folder untouched. Correct the URL or base branch to try again, or remove
					the repository to go on without one.
				{:else}
					The environment restarts with the agent's folder as a checkout of this thread's own branch. The files already in it
					are kept as uncommitted changes, and from then on its work is pushed like any thread's.
				{/if}
			</Dialog.Description>
		</Dialog.Header>
		<form class="space-y-3" onsubmit={add}>
			<Input bind:value={repoUrl} list="repo-dialog-known" placeholder="https://github.com/org/repo.git" aria-label="Repository URL" />
			<datalist id="repo-dialog-known">
				{#each known as url (url)}<option value={url}></option>{/each}
			</datalist>
			<Input bind:value={baseBranch} placeholder="main" aria-label="Base branch" />
			{#if thread.status === Status.WORKING}<p class="text-sm text-muted-foreground">The agent is working: add it once its turn has ended.</p>{/if}
			{#if error}<p class="text-sm text-destructive">{error}</p>{/if}
			<Dialog.Footer>
				{#if thread.repoUrl}<Button variant="outline" disabled={busy} onclick={(e) => add(e, '')}>Remove the repository</Button>{/if}
				<Button type="submit" disabled={!repoUrl.trim() || busy || thread.status === Status.WORKING}>{thread.repoUrl ? 'Change repository' : 'Add repository'}</Button>
			</Dialog.Footer>
		</form>
	</Dialog.Content>
</Dialog.Root>
