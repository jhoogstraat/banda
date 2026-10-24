<script lang="ts" module>
	import { timestampDate, timestampFromDate } from '@bufbuild/protobuf/wkt';
	import { api } from '#lib/api.js';
	import type { Thread } from '#lib/gen/banda/v1/banda_pb.js';

	const at = (days: number, hour = 9) => {
		const d = new Date();
		d.setDate(d.getDate() + days);
		d.setHours(hour, 0, 0, 0);
		return d;
	};
	const later = (h: number) => new Date(Date.now() + h * 3600_000);
	export const presets: [string, () => Date][] = [
		['1 hour', () => later(1)],
		['4 hours', () => later(4)],
		['Tomorrow 09:00', () => at(1)],
		['Next Monday 09:00', () => at((8 - new Date().getDay()) % 7 || 7)]
	];

	// Also used by the command palette. No date unsnoozes.
	export const snooze = (t: Thread, d?: Date) => api.snooze({ threadId: t.id, until: d && timestampFromDate(d) });
	export const settle = (t: Thread) => api.settle({ threadId: t.id, settled: !t.settled });
	export const isSnoozed = (t: Thread) => !!t.snoozedUntil && timestampDate(t.snoozedUntil) > new Date();
</script>

<script lang="ts">
	import BellOffIcon from '@lucide/svelte/icons/bell-off';
	import BotIcon from '@lucide/svelte/icons/bot';
	import CheckIcon from '@lucide/svelte/icons/check';
	import EllipsisIcon from '@lucide/svelte/icons/ellipsis';
	import FolderGit2Icon from '@lucide/svelte/icons/folder-git-2';
	import GitBranchPlusIcon from '@lucide/svelte/icons/git-branch-plus';
	import GitForkIcon from '@lucide/svelte/icons/git-fork';
	import HandshakeIcon from '@lucide/svelte/icons/handshake';
	import MessagesSquareIcon from '@lucide/svelte/icons/messages-square';
	import RotateCcwIcon from '@lucide/svelte/icons/rotate-ccw';
	import { Button } from '#lib/components/ui/button/index.js';
	import IconButton from '#lib/components/icon-button.svelte';
	import * as Menu from '#lib/components/ui/dropdown-menu/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { Mode, Status } from '#lib/gen/banda/v1/banda_pb.js';
	import { threadKeys } from '#lib/shortcuts.svelte.js';

	// The header's "…" menu: everything about the thread that isn't needed at a glance, labelled, with its shortcut.
	// The command palette runs the same actions. `act` runs a call, reports its error and refreshes the page.
	let {
		thread,
		owner,
		act,
		toggleMode,
		retry,
		repoFixable,
		onfork,
		onspawn,
		onhandoff,
		onrepo
	}: {
		thread: Thread;
		owner: boolean;
		act: (call: Promise<unknown>) => Promise<boolean>;
		toggleMode: () => void;
		retry: () => void;
		repoFixable: boolean;
		onfork: () => void;
		onspawn: () => void;
		onhandoff: () => void;
		onrepo: () => void;
	} = $props();

	const when = (d: Date) => d.toLocaleString([], { dateStyle: 'short', timeStyle: 'short' });
	const until = $derived(thread.snoozedUntil && timestampDate(thread.snoozedUntil));
	const snoozed = $derived(isSnoozed(thread));
	let custom = $state('');
	let open = $state(false);

	const pick = (d?: Date) => {
		open = false;
		return act(snooze(thread, d));
	};
</script>

<Menu.Root bind:open>
	<Menu.Trigger>
		{#snippet child({ props })}
			<IconButton {...props} icon={EllipsisIcon} label="More actions" />
		{/snippet}
	</Menu.Trigger>
	<Menu.Content align="end" class="w-64">
		<Menu.Item onclick={onfork}><GitForkIcon /> Fork: try another way<Menu.Shortcut>{threadKeys.fork}</Menu.Shortcut></Menu.Item>
		<Menu.Item onclick={onspawn}><GitBranchPlusIcon /> Spawn a sub-thread<Menu.Shortcut>{threadKeys.spawn}</Menu.Shortcut></Menu.Item>
		{#if owner && !thread.handoffTo}<Menu.Item onclick={onhandoff}><HandshakeIcon /> Hand off to a teammate</Menu.Item>{/if}
		{#if owner && (!thread.repoUrl || repoFixable)}<Menu.Item onclick={onrepo}><FolderGit2Icon /> {thread.repoUrl ? 'Change the repository' : 'Add a repository'}</Menu.Item>{/if}
		<Menu.Separator />
		<Menu.Sub>
			<Menu.SubTrigger><BellOffIcon /> {snoozed ? `Snoozed until ${when(until!)}` : 'Snooze'}</Menu.SubTrigger>
			<Menu.SubContent class="w-56">
				{#each presets as [label, date] (label)}
					<Menu.Item onclick={() => pick(date())}>{label}</Menu.Item>
				{/each}
				<Menu.Separator />
				<div class="flex gap-1 p-1" onkeydown={(e) => e.key !== 'Escape' && e.stopPropagation()} role="presentation">
					<Input type="datetime-local" bind:value={custom} class="h-7 min-w-0 text-xs" aria-label="Snooze until" />
					<Button size="sm" disabled={!custom} onclick={() => pick(new Date(custom))}>Set</Button>
				</div>
				{#if snoozed}
					<Menu.Separator />
					<Menu.Item onclick={() => pick()}>Unsnooze</Menu.Item>
				{/if}
			</Menu.SubContent>
		</Menu.Sub>
		<Menu.Item onclick={() => act(settle(thread))}><CheckIcon /> {thread.settled ? 'Unsettle' : 'Settle'}<Menu.Shortcut>{threadKeys.settle}</Menu.Shortcut></Menu.Item>
		{#if owner}
			<Menu.Separator />
			<Menu.Item onclick={toggleMode}>
				{#if thread.mode === Mode.CHAT}<BotIcon /> Back to agent mode{:else}<MessagesSquareIcon /> Switch to chat mode{/if}
				<Menu.Shortcut>{threadKeys.mode}</Menu.Shortcut>
			</Menu.Item>
			{#if thread.status === Status.ERRORED}
				<Menu.Item onclick={retry}><RotateCcwIcon /> Retry</Menu.Item>
			{/if}
			<!-- A switch, not a check mark: the setting stays, unlike the actions above. Only on an API key: a
				subscription runs only its owner's turns. -->
			{#if thread.teammateTurns}
			<Menu.CheckboxItem
				checked={thread.trusted}
				onCheckedChange={(trusted) => act(api.trust({ threadId: thread.id, trusted }))}
				class="pr-1.5 [&>[data-slot=dropdown-menu-checkbox-item-indicator]]:hidden"
			>
				<span class="flex-1">
					Teammates can run the agent
					<span class="block text-xs text-muted-foreground">{thread.trusted ? 'Their @agent posts run right away' : 'Their @agent posts wait for your approval'}</span>
				</span>
				<span aria-hidden="true" class="flex h-4 w-7 shrink-0 items-center rounded-full p-0.5 transition-colors {thread.trusted ? 'bg-primary' : 'bg-input'}">
					<span class="size-3 rounded-full bg-background shadow transition-transform {thread.trusted ? 'translate-x-3' : ''}"></span>
				</span>
			</Menu.CheckboxItem>
			{/if}
		{/if}
	</Menu.Content>
</Menu.Root>
