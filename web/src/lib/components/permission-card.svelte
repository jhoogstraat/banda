<script lang="ts">
	import CheckIcon from '@lucide/svelte/icons/check';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import ShieldQuestionIcon from '@lucide/svelte/icons/shield-question';
	import XIcon from '@lucide/svelte/icons/x';
	import { Loader } from '#lib/components/ai-elements/loader/index.js';
	import { Response } from '#lib/components/ai-elements/response/index.js';
	import ToolInput from '#lib/components/ai-elements/tool/tool-input.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { Textarea } from '#lib/components/ui/textarea/index.js';
	import { api } from '#lib/api.js';
	import type { Item } from '#lib/events.js';
	import { PermissionDecision, PermissionMode } from '#lib/gen/banda/v1/banda_pb.js';
	import { narrowRule, permissionLabel, ruleLabel } from '#lib/permissions.js';
	import { userName } from '#lib/threads.svelte.js';
	import { diff, input, rel } from '#lib/tools.js';

	// The agent asking before a tool call, inline in the chat. The owner answers, or anyone once teammates are trusted;
	// a plan (ExitPlanMode) only the owner approves, choosing how the agent works from then on. The owner may change a
	// command or an edit before allowing it, and "always allow" offers a narrower rule than the whole tool where one fits.
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
	// The input it ran with, if the answer changed it.
	const changed = $derived(item.resolved?.updatedInputJson ? JSON.parse(item.resolved.updatedInputJson) : undefined);
	const args = $derived(changed ?? input(tool));
	const edits = $derived(changed ? undefined : diff(tool));
	const can = $derived(owner || (trusted && !plan));
	const rule = $derived(narrowRule(name, args));
	// The field the owner may change before allowing: the command, or the new text of an edit.
	const field = $derived(name === 'Bash' ? 'command' : name === 'Edit' ? 'new_string' : name === 'Write' ? 'content' : '');
	let reason = $state('');
	let busy = $state(false);
	let editing = $state(false);
	let draft = $state('');

	async function answer(decision: PermissionDecision, mode = PermissionMode.UNSPECIFIED, rule = '') {
		busy = true;
		let updatedInputJson = '';
		if (editing && decision !== PermissionDecision.DENY && draft !== args[field])
			updatedInputJson = JSON.stringify({ ...JSON.parse(item.req.inputJson || '{}'), [field]: draft });
		await act(api.permissionAnswer({ threadId, requestId: item.req.id, decision, message: reason.trim(), mode, rule, updatedInputJson }));
		busy = false;
	}

	const outcome = (r: NonNullable<typeof item.resolved>) => {
		const by = userName(r.by);
		switch (r.decision) {
			case PermissionDecision.ALLOW:
				return plan ? `Plan approved by ${by}, now: ${permissionLabel(r.mode).toLowerCase()}` : `Allowed once by ${by}${changed ? ', with changes' : ''}`;
			case PermissionDecision.ALLOW_ALWAYS:
				return `${ruleLabel(r.rule || name)} allowed in this thread by ${by}${changed ? ', with changes' : ''}`;
			case PermissionDecision.DENY:
				return `${plan ? 'Sent back for more planning' : 'Denied'} by ${by}${r.message ? `: ${r.message}` : ''}`;
			case PermissionDecision.CANCELED:
				return 'Withdrawn by the agent';
			default:
				return 'Lost: the environment restarted before an answer. Ask the agent again if it still needs this.';
		}
	};
</script>

<div role="group" aria-label={plan ? 'Plan' : `Permission for ${name}`} class="space-y-2 rounded-lg border px-4 py-3 text-sm {item.resolved ? 'bg-muted/20' : 'border-attention/40 bg-attention/5 highlighter:border-border highlighter:bg-card'}">
	<div class="flex items-center gap-2">
		<ShieldQuestionIcon class="size-4 shrink-0 {item.resolved ? 'text-muted-foreground' : 'text-attention-text'}" />
		<span class="min-w-0 flex-1">
			<span class={item.resolved ? '' : 'marker'}>{#if plan}The agent has a plan and asks to start working{:else}The agent asks to use <span class="font-medium highlighter:font-bold">{name}</span>{/if}</span>
		</span>
	</div>
	{#if item.req.reason && !plan}<p class="text-xs text-muted-foreground">{item.req.reason}</p>{/if}

	{#if plan}
		<div class="max-h-96 overflow-auto rounded-md bg-muted/30 px-3 py-2">
			<Response content={args.plan || 'See the plan above.'} class="h-auto" />
		</div>
	{:else if editing && !item.resolved}
		{#if field !== 'command'}<p class="font-mono text-xs text-muted-foreground">{rel(args.file_path)}</p>{/if}
		<Textarea bind:value={draft} aria-label={field === 'command' ? 'Command' : 'New text'} class="min-h-20 font-mono text-xs" />
	{:else if name === 'Bash'}
		<pre class="overflow-x-auto rounded-md bg-zinc-950 p-3 font-mono text-xs whitespace-pre-wrap text-zinc-200"><span class="text-emerald-400">$</span> {args.command}</pre>
	{:else if edits}
		<p class="font-mono text-xs text-muted-foreground">{rel(args.file_path)}</p>
		<pre class="max-h-72 overflow-auto rounded-md bg-muted/30 py-1 font-mono text-xs leading-5">{#each edits as line, i (i)}<div
					class="px-3 {line[0] === '+' ? 'bg-diff-add/15 text-diff-add-foreground' : line[0] === '-' ? 'bg-diff-del/15 text-diff-del-foreground' : 'text-muted-foreground'}">{line === '@@' ? '⋯' : line || ' '}</div>{/each}</pre>
	{:else}
		<ToolInput input={args} />
	{/if}

	{#if item.resolved}
		<p class="flex items-center gap-1.5 text-xs text-muted-foreground">
			{#if item.resolved.decision === PermissionDecision.ALLOW || item.resolved.decision === PermissionDecision.ALLOW_ALWAYS}<CheckIcon class="size-3.5 text-success-foreground" />{:else}<XIcon class="size-3.5" />{/if}
			{outcome(item.resolved)}
		</p>
	{:else if can}
		<div class="flex flex-wrap items-center gap-2">
			{#if plan}
				<Button size="sm" class="highlighter:bg-attention-surface highlighter:text-attention-foreground highlighter:hover:bg-attention-surface/80" disabled={busy} onclick={() => answer(PermissionDecision.ALLOW, PermissionMode.ACCEPT_EDITS)}><CheckIcon /> Approve, accept edits</Button>
				<Button size="sm" variant="outline" disabled={busy} onclick={() => answer(PermissionDecision.ALLOW, PermissionMode.ASK)}>Approve, ask before each edit</Button>
			{:else}
				<Button size="sm" class="highlighter:bg-attention-surface highlighter:text-attention-foreground highlighter:hover:bg-attention-surface/80" disabled={busy} onclick={() => answer(PermissionDecision.ALLOW)}><CheckIcon /> {editing ? 'Allow with changes' : 'Allow once'}</Button>
				{#if rule}<Button size="sm" variant="outline" disabled={busy} onclick={() => answer(PermissionDecision.ALLOW_ALWAYS, undefined, rule)}>Always allow {ruleLabel(rule)}</Button>{/if}
				<Button size="sm" variant="outline" disabled={busy} onclick={() => answer(PermissionDecision.ALLOW_ALWAYS)}>Always allow {name} in this thread</Button>
				{#if owner && field && !editing}
					<Button size="sm" variant="ghost" disabled={busy} onclick={() => ((draft = args[field] ?? ''), (editing = true))}><PencilIcon /> Edit</Button>
				{/if}
			{/if}
		</div>
		<div class="flex items-center gap-2">
			<Input
				bind:value={reason}
				placeholder={plan ? 'What to change (optional)' : 'Reason (optional)'}
				class="h-8 min-w-0 flex-1 text-xs"
				aria-label="Reason"
				onkeydown={(e) => e.key === 'Enter' && !busy && answer(PermissionDecision.DENY)}
			/>
			<Button size="sm" variant="outline" disabled={busy} onclick={() => answer(PermissionDecision.DENY)}><XIcon /> {plan ? 'Keep planning' : 'Deny'}</Button>
		</div>
	{:else}
		<p class="flex items-center gap-2 text-xs text-muted-foreground"><Loader size={12} /> Waiting for {ownerName} to answer</p>
	{/if}
</div>
