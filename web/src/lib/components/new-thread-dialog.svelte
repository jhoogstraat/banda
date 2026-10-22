<script lang="ts">
	import { goto } from '$app/navigation';
	import DownloadIcon from '@lucide/svelte/icons/download';
	import FolderGit2Icon from '@lucide/svelte/icons/folder-git-2';
	import * as Command from '#lib/components/ui/command/index.js';
	import { noRepo, projectName, projects } from '#lib/threads.svelte.js';

	let { open = $bindable(false) }: { open: boolean } = $props();

	let query = $state('');
	const url = $derived(query.trim());
	const isUrl = $derived(/^(https?:\/\/|git@|ssh:\/\/)/.test(url));

	function pick(repo: string) {
		open = false;
		query = '';
		goto(`/?repo=${encodeURIComponent(repo)}`);
	}
</script>

<Command.Dialog bind:open title="New thread" description="Choose a project">
	<Command.Input placeholder="Search projects or paste a repository URL" bind:value={query} />
	<Command.List>
		<Command.Empty>No projects yet. Paste a repository URL to clone it.</Command.Empty>
		<Command.Group heading="Projects">
			{#each projects() as repo (repo)}
				<Command.Item value={`${projectName(repo)} ${repo}`} onSelect={() => pick(repo)}>
					<FolderGit2Icon />
					<span class="truncate">{projectName(repo)}</span>
					{#if repo !== noRepo}<span class="ml-auto truncate text-xs text-muted-foreground">{repo}</span>{/if}
				</Command.Item>
			{/each}
		</Command.Group>
		{#if isUrl}
			<Command.Group heading="Clone">
				<Command.Item value={query} onSelect={() => pick(url)}>
					<DownloadIcon />
					Clone {url}
				</Command.Item>
			</Command.Group>
		{/if}
	</Command.List>
</Command.Dialog>
