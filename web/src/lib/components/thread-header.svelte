<script lang="ts">
	import BotIcon from '@lucide/svelte/icons/bot';
	import CornerLeftUpIcon from '@lucide/svelte/icons/corner-left-up';
	import GitBranchPlusIcon from '@lucide/svelte/icons/git-branch-plus';
	import GitForkIcon from '@lucide/svelte/icons/git-fork';
	import GitPullRequestIcon from '@lucide/svelte/icons/git-pull-request';
	import MessagesSquareIcon from '@lucide/svelte/icons/messages-square';
	import PanelRightIcon from '@lucide/svelte/icons/panel-right';
	import SquareIcon from '@lucide/svelte/icons/square';
	import UploadIcon from '@lucide/svelte/icons/upload';
	import IconButton from '#lib/components/icon-button.svelte';
	import PageHeader from '#lib/components/page-header.svelte';
	import StatusDot from '#lib/components/status-dot.svelte';
	import ThreadActions from '#lib/components/thread-actions.svelte';
	import UserAvatar from '#lib/components/user-avatar.svelte';
	import { Tooltip, TooltipContent, TooltipTrigger } from '#lib/components/ui/tooltip/index.js';
	import { Mode, Status, type Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { userName, waiting } from '#lib/threads.svelte.js';

	// here: the others with the thread open. interrupt, toggleMode and push are the command palette's too.
	let {
		thread,
		owner,
		offline,
		here,
		act,
		interrupt,
		toggleMode,
		push,
		forkOpen = $bindable(),
		spawnOpen = $bindable(),
		paneOpen = $bindable()
	}: {
		thread?: Thread;
		owner: boolean;
		offline: boolean;
		here: string[];
		act: (call: Promise<unknown>) => Promise<boolean>;
		interrupt: () => void;
		toggleMode: () => void;
		push: () => void;
		forkOpen: boolean;
		spawnOpen: boolean;
		paneOpen?: boolean;
	} = $props();

	const pr = $derived(thread?.pullRequestUrl ?? '');
</script>

<PageHeader>
	<h1 class="min-w-0 truncate text-sm font-medium">{thread?.title || 'Untitled thread'}</h1>
	{#if thread}<StatusDot status={thread.status} label waiting={waiting(thread)} />{/if}
	{#if thread?.parentId}
		<a href="/t/{thread.parentId}" class="flex min-w-0 shrink items-center gap-1 text-xs text-muted-foreground hover:text-foreground" title="Spawned from {thread.parentTitle}">
			<CornerLeftUpIcon class="size-3.5 shrink-0" /><span class="truncate">from {thread.parentTitle || 'its parent'}</span>
		</a>
	{/if}
	{#if offline}<span class="shrink-0 text-xs text-muted-foreground">reconnecting…</span>{/if}
	<div class="ml-auto flex shrink-0 items-center gap-0.5">
		{#if here.length}
			<div class="mr-1.5 flex -space-x-1.5" role="group" aria-label="Here now">
				{#each here.slice(0, 4) as u (u)}
					<Tooltip>
						<TooltipTrigger>
							{#snippet child({ props })}
								<span {...props}><UserAvatar name={userName(u)} class="size-6 ring-2 ring-background" /></span>
							{/snippet}
						</TooltipTrigger>
						<TooltipContent>{userName(u)} · here now</TooltipContent>
					</Tooltip>
				{/each}
				{#if here.length > 4}
					<Tooltip>
						<TooltipTrigger>
							{#snippet child({ props })}
								<span {...props} class="flex size-6 items-center justify-center rounded-full bg-muted text-[10px] font-medium ring-2 ring-background">+{here.length - 4}</span>
							{/snippet}
						</TooltipTrigger>
						<TooltipContent>{here.slice(4).map(userName).join(', ')} · here now</TooltipContent>
					</Tooltip>
				{/if}
			</div>
		{/if}
		{#if owner && thread?.status === Status.WORKING}
			<IconButton icon={SquareIcon} label="Interrupt the agent" variant="destructive" onclick={interrupt} />
		{/if}
		{#if thread}
			<IconButton
				icon={thread.mode === Mode.CHAT ? MessagesSquareIcon : BotIcon}
				label={thread.mode === Mode.CHAT ? 'Chat mode: switch to agent mode' : 'Agent mode: switch to chat mode'}
				variant={thread.mode === Mode.CHAT ? 'secondary' : 'ghost'}
				disabled={!owner}
				onclick={toggleMode}
			/>
			<IconButton icon={GitForkIcon} label="Fork: try another way from here" onclick={() => (forkOpen = true)} />
			<IconButton icon={GitBranchPlusIcon} label="Spawn a sub-thread" onclick={() => (spawnOpen = true)} />
			<ThreadActions {thread} {act} />
		{/if}
		{#if pr}
			<IconButton icon={GitPullRequestIcon} label="Open a pull request" href={pr} target="_blank" />
		{:else if owner}
			<IconButton icon={UploadIcon} label="Push the branch to origin" onclick={push} />
		{/if}
		<IconButton icon={PanelRightIcon} label="Toggle right pane" onclick={() => (paneOpen = !paneOpen)} />
	</div>
</PageHeader>
