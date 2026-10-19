<script lang="ts">
	import FolderGit2Icon from '@lucide/svelte/icons/folder-git-2';
	import GitBranchIcon from '@lucide/svelte/icons/git-branch';
	import * as PromptInput from '#lib/components/ai-elements/prompt-input/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as DropdownMenu from '#lib/components/ui/dropdown-menu/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { projectName, projects } from '#lib/threads.svelte.js';

	// The composer chip for the repository and base branch of a new thread.
	let { repoUrl = $bindable(), baseBranch = $bindable() }: { repoUrl: string; baseBranch: string } = $props();

	let open = $state(false);
	let custom = $state('');
	const known = $derived([...new Set([repoUrl, ...projects()].filter(Boolean))]);

	function useCustom(e: SubmitEvent) {
		e.preventDefault();
		repoUrl = custom.trim();
		custom = '';
		open = false;
	}
	// The menu's typeahead would otherwise swallow keys typed into its inputs.
	const keepKeys = (e: KeyboardEvent) => e.key !== 'Escape' && e.stopPropagation();
</script>

<DropdownMenu.Root bind:open>
	<DropdownMenu.Trigger>
		{#snippet child({ props })}
			<PromptInput.Button {...props} size="sm" class="min-w-0" title={repoUrl}>
				<FolderGit2Icon />
				<span class="truncate">{repoUrl ? projectName(repoUrl) : 'Choose repository'}</span>
				<GitBranchIcon class="ml-1" />
				{baseBranch}
			</PromptInput.Button>
		{/snippet}
	</DropdownMenu.Trigger>
	<DropdownMenu.Content align="start" class="w-80">
		<DropdownMenu.Label>Repository</DropdownMenu.Label>
		{#if known.length}
			<DropdownMenu.RadioGroup bind:value={repoUrl}>
				{#each known as url (url)}
					<DropdownMenu.RadioItem value={url} title={url}><span class="truncate">{projectName(url)}</span></DropdownMenu.RadioItem>
				{/each}
			</DropdownMenu.RadioGroup>
		{/if}
		<form class="flex gap-1 p-1" onsubmit={useCustom}>
			<Input bind:value={custom} onkeydown={keepKeys} placeholder="https://github.com/org/repo.git" aria-label="Repository URL" class="h-7 min-w-0 text-xs" />
			<Button type="submit" size="sm" disabled={!custom.trim()}>Use</Button>
		</form>
		<DropdownMenu.Separator />
		<DropdownMenu.Label>Base branch</DropdownMenu.Label>
		<div class="p-1"><Input bind:value={baseBranch} onkeydown={keepKeys} aria-label="Base branch" class="h-7 text-xs" /></div>
	</DropdownMenu.Content>
</DropdownMenu.Root>
