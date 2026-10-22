<script lang="ts" module>
	import { Message, MessageContent, MessageResponse } from '#lib/components/ai-elements/message/index.js';

	export { agentText };
</script>

<script lang="ts">
	// One item of a thread's chat (see events.ts).
	import { timestampDate, type Timestamp } from '@bufbuild/protobuf/wkt';
	import CheckIcon from '@lucide/svelte/icons/check';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import CircleMinusIcon from '@lucide/svelte/icons/circle-minus';
	import CloudOffIcon from '@lucide/svelte/icons/cloud-off';
	import GitBranchPlusIcon from '@lucide/svelte/icons/git-branch-plus';
	import GitForkIcon from '@lucide/svelte/icons/git-fork';
	import GitMergeConflictIcon from '@lucide/svelte/icons/git-merge-conflict';
	import InfoIcon from '@lucide/svelte/icons/info';
	import MessageSquareTextIcon from '@lucide/svelte/icons/message-square-text';
	import XCircleIcon from '@lucide/svelte/icons/x-circle';
	import XIcon from '@lucide/svelte/icons/x';
	import { Loader } from '#lib/components/ai-elements/loader/index.js';
	import { Response } from '#lib/components/ai-elements/response/index.js';
	import Tool from '#lib/components/ai-elements/tool/tool.svelte';
	import ToolContent from '#lib/components/ai-elements/tool/tool-content.svelte';
	import IconButton from '#lib/components/icon-button.svelte';
	import MentionText from '#lib/components/mention-text.svelte';
	import MessageAttachments from '#lib/components/message-attachments.svelte';
	import PermissionCard from '#lib/components/permission-card.svelte';
	import StatusDot from '#lib/components/status-dot.svelte';
	import ToolSteps from '#lib/components/tool-steps.svelte';
	import UserAvatar from '#lib/components/user-avatar.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '#lib/components/ui/collapsible/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { Tooltip, TooltipContent, TooltipTrigger } from '#lib/components/ui/tooltip/index.js';
	import { api } from '#lib/api.js';
	import { permissionLabel } from '#lib/permissions.js';
	import { tone, where, type Comment } from '#lib/diff.js';
	import type { Item } from '#lib/events.js';
	import { Mode, SetupState, Status, type Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { userName } from '#lib/threads.svelte.js';

	let {
		item,
		threadId,
		thread,
		owner,
		act,
		waiting = [],
		onreveal,
		onlogin
	}: {
		item: Item;
		threadId: string;
		thread?: Thread;
		owner: boolean;
		act: (call: Promise<unknown>) => Promise<boolean>;
		// The turn requests still waiting for the owner, oldest first.
		waiting?: Item[];
		onreveal: (c: Comment) => void;
		onlogin: () => void;
	} = $props();

	// The owner's optional note when declining a turn request, behind its note button.
	let note = $state('');
	let noting = $state(false);
	// Approves the waiting requests in the order they were posted; stops at the first that fails.
	async function approveAll() {
		for (const r of waiting) if (r.kind !== 'request' || !(await act(api.approveTurn({ threadId, requestSeq: r.seq })))) return;
	}
	const loginNeeded = (text: string) => /not logged in|rejected the login/.test(text);
	const lineageVerb = { [Status.STARTING]: 'was spawned', [Status.AWAITING_INPUT]: 'finished', [Status.ERRORED]: 'failed' } as Partial<Record<Status, string>>;
	const clock = (t?: Timestamp) =>
		t ? timestampDate(t).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' }) : '';
</script>

{#snippet reviewCards(review: Comment[])}
	{#each review as c, j (j)}
		<Collapsible open class="group/rc overflow-hidden rounded-lg border text-sm">
			<div class="flex items-center gap-1 bg-muted/40 px-2 py-1 text-xs">
				<CollapsibleTrigger class="rounded p-0.5 hover:bg-muted" aria-label="Toggle the code">
					<ChevronRightIcon class="size-3.5 text-muted-foreground transition-transform group-data-[state=open]/rc:rotate-90" />
				</CollapsibleTrigger>
				<button class="min-w-0 truncate font-mono hover:underline" title="Show in the diff" onclick={() => onreveal(c)}>{where(c)}</button>
			</div>
			<CollapsibleContent>
				<pre class="max-h-48 overflow-auto border-t py-1 font-mono text-xs leading-5">{#each c.code.split('\n') as line}<div class="px-3 {tone(line)}">{line || ' '}</div>{/each}</pre>
			</CollapsibleContent>
			<p class="border-t px-3 py-2 whitespace-pre-wrap"><MentionText text={c.body} /></p>
		</Collapsible>
	{/each}
{/snippet}

{#snippet divider(text: string, tone = 'text-muted-foreground')}
	<div class="flex items-center gap-3 text-xs {tone}">
		<div class="h-px flex-1 bg-border"></div>
		{text}
		<div class="h-px flex-1 bg-border"></div>
	</div>
{/snippet}

{#snippet agentText(text: string)}
	<Message from="assistant" class="max-w-full">
		<MessageContent class="w-full"><MessageResponse content={text} /></MessageContent>
	</Message>
{/snippet}

{#if item.kind === 'message'}
	{@const name = userName(item.actor)}
	<div class="flex gap-3">
		<UserAvatar {name} class="mt-0.5 size-7" />
		<div class="min-w-0 flex-1 space-y-1">
			<div class="flex items-baseline gap-2">
				<span class="text-sm font-medium">{name}</span>
				<time class="text-xs text-muted-foreground" title={item.time && timestampDate(item.time).toLocaleString()}>{clock(item.time)}</time>
			</div>
			{#if item.text}
				<Message from="user" class="ml-0 max-w-full">
					<MessageContent class="group-[.is-user]:ml-0"><p class="whitespace-pre-wrap"><MentionText text={item.text} /></p></MessageContent>
				</Message>
			{/if}
			{#if item.attachments.length}<MessageAttachments attachments={item.attachments} />{/if}
			{@render reviewCards(item.review)}
		</div>
	</div>
{:else if item.kind === 'request'}
	{@const name = userName(item.actor)}
	{@const ownerName = userName(thread?.ownerId ?? '')}
	<div class="space-y-1.5 rounded-lg border bg-muted/30 px-3 py-2 text-sm">
		<div class="flex flex-wrap items-center gap-x-2 gap-y-1">
			<UserAvatar {name} class="size-5" />
			<span class="font-medium">{name}</span>
			<span class="text-muted-foreground">asks the agent</span>
			<time class="text-xs text-muted-foreground" title={item.time && timestampDate(item.time).toLocaleString()}>{clock(item.time)}</time>
			{#if item.resolved}
				<span class="ml-auto text-xs text-muted-foreground">
					{item.resolved.approved ? 'Approved' : 'Declined'} by {userName(item.resolved.by)}{item.resolved.note ? `: ${item.resolved.note}` : ''}
				</span>
			{:else if owner}
				<div class="ml-auto flex items-center gap-1">
					<Tooltip>
						<TooltipTrigger>
							{#snippet child({ props })}
								<button {...props} type="button" aria-label="Runs on your Claude subscription" class="rounded p-1 text-muted-foreground hover:text-foreground"><InfoIcon class="size-3.5" /></button>
							{/snippet}
						</TooltipTrigger>
						<TooltipContent>Runs on your Claude subscription, in this thread's environment.</TooltipContent>
					</Tooltip>
					{#if waiting.length > 1 && item === waiting.at(-1)}
						<Button size="xs" variant="outline" onclick={approveAll}>Approve all ({waiting.length})</Button>
					{/if}
					<Button size="xs" onclick={() => act(api.approveTurn({ threadId, requestSeq: item.seq }))}><CheckIcon /> Approve</Button>
					<Button size="xs" variant="ghost" onclick={() => act(api.declineTurn({ threadId, requestSeq: item.seq, note }))}><XIcon /> Decline</Button>
					<IconButton icon={MessageSquareTextIcon} label="Add a note to the decline" size="icon-xs" aria-pressed={noting} variant={noting ? 'secondary' : 'ghost'} onclick={() => (noting = !noting)} />
				</div>
			{:else}
				<span class="ml-auto flex items-center gap-1.5 text-xs text-muted-foreground"><Loader size={12} /> Waiting for {ownerName} to approve</span>
			{/if}
		</div>
		{#if item.text}<p class="whitespace-pre-wrap"><MentionText text={item.text} /></p>{/if}
		{#if item.attachments.length}<MessageAttachments attachments={item.attachments} />{/if}
		{@render reviewCards(item.review)}
		{#if noting && owner && !item.resolved}
			<!-- svelte-ignore a11y_autofocus -->
			<Input bind:value={note} autofocus placeholder="Why not? Sent with the decline." class="h-8 text-xs" aria-label="Decline note" />
		{/if}
	</div>
{:else if item.kind === 'permission'}
	<PermissionCard {item} {threadId} {owner} trusted={!!thread?.trusted} ownerName={userName(thread?.ownerId ?? '')} {act} />
{:else if item.kind === 'text'}
	{@render agentText(item.text)}
{:else if item.kind === 'steps'}
	<ToolSteps steps={item.steps} />
{:else if item.kind === 'turn'}
	{#if item.turn.isError}
		{@render divider(`Turn failed: ${item.turn.error}`, 'text-destructive')}
	{:else}
		{@render divider(`Turn finished in ${(Number(item.turn.durationMs) / 1000).toFixed(1)}s · $${item.turn.costUsd.toFixed(4)}`)}
	{/if}
{:else if item.kind === 'lineage'}
	{@const u = item.update}
	<a href="/t/{u.threadId}" class="flex min-w-0 items-center gap-2 text-xs text-muted-foreground hover:text-foreground">
		<GitBranchPlusIcon class="size-3.5 shrink-0" />
		<StatusDot status={u.status} />
		<span class="truncate"><span class="font-medium text-foreground">{u.title}</span> {lineageVerb[u.status]}{u.status === Status.STARTING && item.actor !== 'agent' ? ` by ${userName(item.actor)}` : ''}{u.text ? `: ${u.text}` : ''}</span>
	</a>
{:else if item.kind === 'overlap'}
	{@const o = item.overlap}
	<a href="/t/{o.threadId}" class="flex min-w-0 items-center gap-2 text-xs text-muted-foreground hover:text-foreground">
		<GitMergeConflictIcon class="size-3.5 shrink-0 text-amber-600 dark:text-amber-400" />
		<span class="truncate">Now overlaps with <span class="font-medium text-foreground">{o.title || 'Untitled thread'}</span> by {userName(o.ownerId)}: <span class="font-mono">{o.paths.join(', ')}</span></span>
	</a>
{:else if item.kind === 'notice'}
	<div class="rounded-lg border border-amber-500/40 bg-amber-500/10 px-4 py-3 text-sm">
		<Response content={item.text} class="h-auto" />
		{#if owner && loginNeeded(item.text)}
			<Button size="sm" class="mt-2" onclick={onlogin}>Log in to Claude Code</Button>
		{/if}
	</div>
{:else if item.kind === 'setup'}
	<div>
		{#each item.steps as step (step.id)}
			{@const failed = step.state === SetupState.FAILED}
			<Tool class="group/tool mb-0 border-0" open={failed}>
				<CollapsibleTrigger
					disabled={!step.output}
					class="flex w-full items-center gap-2 rounded-md px-2 py-1 text-left text-xs enabled:hover:bg-muted/50 {step.state === SetupState.SKIPPED
						? 'text-muted-foreground'
						: ''}"
				>
					{#if step.state === SetupState.DONE}
						<CheckIcon class="size-3.5 shrink-0 text-emerald-500" />
					{:else if failed}
						<XCircleIcon class="size-3.5 shrink-0 text-destructive" />
					{:else if step.state === SetupState.SKIPPED}
						<CircleMinusIcon class="size-3.5 shrink-0" />
					{:else}
						<Loader size={14} class="text-muted-foreground" />
					{/if}
					<span class="min-w-0 flex-1 truncate">{step.title}{step.state === SetupState.SKIPPED ? ' (skipped)' : ''}</span>
					{#if step.output}
						<ChevronRightIcon class="size-3.5 shrink-0 text-muted-foreground transition-transform group-data-[state=open]/tool:rotate-90" />
					{/if}
				</CollapsibleTrigger>
				<ToolContent class="ml-4 border-l pl-2">
					<pre
						class="my-1 max-h-64 overflow-auto rounded-md p-3 text-xs whitespace-pre-wrap {failed
							? 'bg-destructive/10 text-destructive'
							: 'bg-muted/50'}">{step.output}</pre>
				</ToolContent>
			</Tool>
		{/each}
	</div>
{:else if item.kind === 'push' && item.unavailable}
	<!-- Only the owner can fix it, so only the owner is told, once (see events.ts). -->
	{#if owner}
		<div class="flex items-center gap-2 text-xs text-amber-700 dark:text-amber-400">
			<CloudOffIcon class="size-3.5 shrink-0" />
			{#if item.text.includes('GIT_TOKEN')}
				<span class="min-w-0 flex-1">This thread's work only lives in its environment: pushing to origin needs a GIT_TOKEN.</span>
				<Button size="xs" variant="outline" href="/settings?secret=GIT_TOKEN#secrets">Add GIT_TOKEN in Settings</Button>
			{:else}
				<span class="min-w-0 flex-1">{item.text.replaceAll('`', '')}</span>
			{/if}
		</div>
	{/if}
{:else if item.kind === 'push'}
	{@render divider(item.text.replaceAll('`', ''), item.pushed ? 'text-muted-foreground' : 'text-amber-600 dark:text-amber-400')}
{:else if item.kind === 'mode'}
	{@render divider(item.mode === Mode.CHAT ? 'Chat mode on' : 'Agent mode')}
{:else if item.kind === 'settings'}
	{@render divider(`${userName(item.actor)} switched to ${item.settings.model} · ${item.settings.effort}`)}
{:else if item.kind === 'permissionMode'}
	{@render divider(`${userName(item.actor)} switched to ${permissionLabel(item.mode)}`)}
{:else if item.kind === 'fork'}
	{@const n = item.note}
	<div class="flex items-center gap-3 text-xs text-muted-foreground">
		<div class="h-px flex-1 bg-border"></div>
		<GitForkIcon class="size-3.5" />
		<span>
			{userName(item.actor)}
			{#if !n.picked}forked this after turn {n.turn}:
			{:else if n.threadId === threadId}picked this try.
			{:else}picked another try:{/if}
			{#if n.threadId !== threadId}<a class="underline" href="/t/{n.threadId}">{n.title}</a>{/if}
		</span>
		<div class="h-px flex-1 bg-border"></div>
	</div>
{/if}
