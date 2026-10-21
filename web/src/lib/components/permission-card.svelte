<script lang="ts">
	import CheckIcon from '@lucide/svelte/icons/check';
	import ShieldQuestionIcon from '@lucide/svelte/icons/shield-question';
	import XIcon from '@lucide/svelte/icons/x';
	import { Loader } from '#lib/components/ai-elements/loader/index.js';
	import { Response } from '#lib/components/ai-elements/response/index.js';
	import ToolInput from '#lib/components/ai-elements/tool/tool-input.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { api } from '#lib/api.js';
	import type { Item } from '#lib/events.js';
	import { PermissionDecision, PermissionMode } from '#lib/gen/banda/v1/banda_pb.js';
	import { permissionLabel } from '#lib/permissions.js';
	import { userName } from '#lib/threads.svelte.js';
	import { diff, input, rel } from '#lib/tools.js';

	// The agent asking before a tool call, inline in the chat. The owner answers, or anyone once teammates are trusted;
	// a plan (ExitPlanMode) only the owner approves, choosing how the agent works from then on.
	let {
		item,
		threadId,
		owner,
		trusted,
		ownerName,
		act
	}: {
		item: Extract<Item, { kind: 'permission' }>;
		threadId: string;
		owner: boolean;
		trusted: boolean;
		ownerName: string;
		act: (call: Promise<unknown>) => Promise<boolean>;
	} = $props();

	const tool = $derived(item.tool);
	const name = $derived(item.req.toolName);
	const plan = $derived(name === 'ExitPlanMode');
	const args = $derived(input(tool));
	const edits = $derived(diff(tool));
	const can = $derived(owner || (trusted && !plan));
	let reason = $state('');
	let busy = $state(false);

	async function answer(decision: PermissionDecision, mode = PermissionMode.UNSPECIFIED) {
		busy = true;
		await act(api.permissionAnswer({ threadId, requestId: item.req.id, decision, message: reason.trim(), mode }));
		busy = false;
	}

	const outcome = (r: NonNullable<typeof item.resolved>) => {
		const by = userName(r.by);
		switch (r.decision) {
			case PermissionDecision.ALLOW:
				return plan ? `Plan approved by ${by}, now: ${permissionLabel(r.mode).toLowerCase()}` : `Allowed once by ${by}`;
			case PermissionDecision.ALLOW_ALWAYS:
				return `${name} allowed in this thread by ${by}`;
			case PermissionDecision.DENY:
				return `${plan ? 'Sent back for more planning' : 'Denied'} by ${by}${r.message ? `: ${r.message}` : ''}`;
			case PermissionDecision.CANCELED:
				return 'Withdrawn by the agent';
			default:
				return 'Lost: the environment restarted before an answer. Ask the agent again if it still needs this.';
		}
	};
</script>

<div class="space-y-2 rounded-lg border px-4 py-3 text-sm {item.resolved ? 'bg-muted/20' : 'border-amber-500/40 bg-amber-500/5'}">
	<div class="flex items-center gap-2">
		<ShieldQuestionIcon class="size-4 shrink-0 {item.resolved ? 'text-muted-foreground' : 'text-amber-500'}" />
		<span class="min-w-0 flex-1">
			{#if plan}The agent has a plan and asks to start working{:else}The agent asks to use <span class="font-medium">{name}</span>{/if}
		</span>
	</div>
	{#if item.req.reason && !plan}<p class="text-xs text-muted-foreground">{item.req.reason}</p>{/if}

	{#if plan}
		<div class="max-h-96 overflow-auto rounded-md bg-muted/30 px-3 py-2">
			<Response content={args.plan || 'See the plan above.'} class="h-auto" />
		</div>
	{:else if name === 'Bash'}
		<pre class="overflow-x-auto rounded-md bg-zinc-950 p-3 font-mono text-xs whitespace-pre-wrap text-zinc-200"><span class="text-emerald-400">$</span> {args.command}</pre>
	{:else if edits}
		<p class="font-mono text-xs text-muted-foreground">{rel(args.file_path)}</p>
		<pre class="max-h-72 overflow-auto rounded-md bg-muted/30 py-1 font-mono text-xs leading-5">{#each edits as line, i (i)}<div
					class="px-3 {line[0] === '+' ? 'bg-green-500/15 text-green-700 dark:text-green-300' : line[0] === '-' ? 'bg-red-500/15 text-red-700 dark:text-red-300' : 'text-muted-foreground'}">{line === '@@' ? '⋯' : line || ' '}</div>{/each}</pre>
	{:else}
		<ToolInput input={args} />
	{/if}

	{#if item.resolved}
		<p class="flex items-center gap-1.5 text-xs text-muted-foreground">
			{#if item.resolved.decision === PermissionDecision.ALLOW || item.resolved.decision === PermissionDecision.ALLOW_ALWAYS}<CheckIcon class="size-3.5 text-emerald-500" />{:else}<XIcon class="size-3.5" />{/if}
			{outcome(item.resolved)}
		</p>
	{:else if can}
		<div class="flex flex-wrap items-center gap-2">
			{#if plan}
				<Button size="sm" disabled={busy} onclick={() => answer(PermissionDecision.ALLOW, PermissionMode.ACCEPT_EDITS)}><CheckIcon /> Approve, accept edits</Button>
				<Button size="sm" variant="outline" disabled={busy} onclick={() => answer(PermissionDecision.ALLOW, PermissionMode.ASK)}>Approve, ask before each edit</Button>
			{:else}
				<Button size="sm" disabled={busy} onclick={() => answer(PermissionDecision.ALLOW)}><CheckIcon /> Allow once</Button>
				<Button size="sm" variant="outline" disabled={busy} onclick={() => answer(PermissionDecision.ALLOW_ALWAYS)}>Always allow {name} in this thread</Button>
			{/if}
		</div>
		<div class="flex items-center gap-2">
			<Input bind:value={reason} placeholder={plan ? 'What to change (optional)' : 'Reason (optional)'} class="h-8 min-w-0 flex-1 text-xs" aria-label="Reason" />
			<Button size="sm" variant="outline" disabled={busy} onclick={() => answer(PermissionDecision.DENY)}><XIcon /> {plan ? 'Keep planning' : 'Deny'}</Button>
		</div>
	{:else}
		<p class="flex items-center gap-2 text-xs text-muted-foreground"><Loader size={12} /> Waiting for {ownerName} to answer</p>
	{/if}
</div>
