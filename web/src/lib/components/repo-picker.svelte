<script lang="ts">
	import FolderIcon from '@lucide/svelte/icons/folder';
	import FolderGit2Icon from '@lucide/svelte/icons/folder-git-2';
	import GitBranchIcon from '@lucide/svelte/icons/git-branch';
	import * as PromptInput from '#lib/components/ai-elements/prompt-input/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as DropdownMenu from '#lib/components/ui/dropdown-menu/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { api } from '#lib/api.js';
	import type { ForgeRepo } from '#lib/gen/banda/v1/banda_pb.js';
	import { noRepo, projectName, projects } from '#lib/threads.svelte.js';

	// The composer chip for the repository (or noRepo) and base branch of a new thread.
	let { repoUrl = $bindable(), baseBranch = $bindable() }: { repoUrl: string; baseBranch: string } = $props();

	let open = $state(false);
	let custom = $state('');
	const known = $derived([...new Set([repoUrl, ...projects()].filter((url) => url && url !== noRepo))]);
	// The caller's repositories on the forges they have a token for, loaded when the menu first opens; the URL field
	// filters them, and what is typed is searched on the forges too (debounced), for those beyond the first page.
	let mine = $state<ForgeRepo[]>([]);
	let found = $state<ForgeRepo[]>([]);
	let asked = false;
	$effect(() => {
		if (!open || asked) return;
		asked = true;
		api.listForgeRepos({}).then((r) => (mine = r.repos)).catch(() => {}); // without them the field still takes a URL
	});
	$effect(() => {
		const query = custom.trim();
		if (!open || !query || /[:/@]/.test(query)) return; // a URL isn't searched
		const timer = setTimeout(() => {
			api.listForgeRepos({ query }).then((r) => custom.trim() === query && (found = r.repos)).catch(() => {});
		}, 300);
		return () => clearTimeout(timer);
	});
	const matches = $derived(
		[...mine, ...found.filter((f) => !mine.some((r) => r.url === f.url))]
			.filter((r) => !known.includes(r.url) && r.name.toLowerCase().includes(custom.trim().toLowerCase()))
			.slice(0, 8)
	);
	const Icon = $derived(repoUrl === noRepo ? FolderIcon : FolderGit2Icon);

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
			<PromptInput.Button {...props} size="sm" class="min-w-0" title={repoUrl === noRepo ? 'The agent works in an empty folder' : repoUrl}>
				<Icon />
				<span class="truncate">{repoUrl ? projectName(repoUrl) : 'Choose repository'}</span>
				{#if repoUrl !== noRepo}
					<GitBranchIcon class="ml-1" />
					{baseBranch}
				{/if}
			</PromptInput.Button>
		{/snippet}
	</DropdownMenu.Trigger>
	<DropdownMenu.Content align="start" class="w-80">
		<DropdownMenu.Label>Repository</DropdownMenu.Label>
		<DropdownMenu.RadioGroup bind:value={repoUrl}>
			{#each known as url (url)}
				<DropdownMenu.RadioItem value={url} title={url}><span class="truncate">{projectName(url)}</span></DropdownMenu.RadioItem>
			{/each}
			{#if matches.length}
				<DropdownMenu.Label class="text-xs font-normal text-muted-foreground">Your repositories</DropdownMenu.Label>
				{#each matches as r (r.url)}
					<DropdownMenu.RadioItem value={r.url} title={r.description || r.url}><span class="truncate">{r.name}</span></DropdownMenu.RadioItem>
				{/each}
			{/if}
			<DropdownMenu.RadioItem value={noRepo} title="For work without code: the agent starts in an empty folder">No repository</DropdownMenu.RadioItem>
		</DropdownMenu.RadioGroup>
		<form class="flex gap-1 p-1" onsubmit={useCustom}>
			<Input bind:value={custom} onkeydown={keepKeys} placeholder="Search yours, or a URL" aria-label="Repository URL" class="h-7 min-w-0 text-xs" />
			<Button type="submit" size="sm" disabled={!custom.trim()}>Use</Button>
		</form>
		{#if repoUrl !== noRepo}
			<DropdownMenu.Separator />
			<DropdownMenu.Label>Base branch</DropdownMenu.Label>
			<div class="p-1"><Input bind:value={baseBranch} onkeydown={keepKeys} aria-label="Base branch" class="h-7 text-xs" /></div>
		{/if}
	</DropdownMenu.Content>
</DropdownMenu.Root>
