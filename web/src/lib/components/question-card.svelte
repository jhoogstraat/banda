<script lang="ts">
	import CheckIcon from '@lucide/svelte/icons/check';
	import MessageCircleQuestionIcon from '@lucide/svelte/icons/message-circle-question';
	import XIcon from '@lucide/svelte/icons/x';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { api } from '#lib/api.js';
	import type { Item } from '#lib/events.js';
	import { PermissionDecision } from '#lib/gen/banda/v1/banda_pb.js';
	import { threads, userName } from '#lib/threads.svelte.js';

	// The agent's clarifying questions (AskUserQuestion), inline in the chat. Everyone may vote; the owner's answer,
	// or a trusted teammate's, is what the agent gets. Answers are the chosen labels, several joined by ", ", or the
	// "Other" text (see docs/research/claude-headless.md §4).
	let {
		item,
		threadId,
		can,
		ownerName,
		act
	}: {
		item: Extract<Item, { kind: 'permission' }>;
		threadId: string;
		can: boolean;
		ownerName: string;
		act: (call: Promise<unknown>) => Promise<boolean>;
	} = $props();

	type Question = { question: string; header?: string; multiSelect?: boolean; options: { label: string; description?: string }[] };
	const questions = $derived.by((): Question[] => {
		try {
			return JSON.parse(item.req.inputJson).questions ?? [];
		} catch {
			return [];
		}
	});

	let picks = $state<Record<string, string[]>>({});
	let other = $state<Record<string, string>>({});
	let reason = $state('');
	let busy = $state(false);

	const answerOf = (q: Question) =>
		q.multiSelect ? [...(picks[q.question] ?? []), other[q.question]?.trim()].filter(Boolean).join(', ') : other[q.question]?.trim() || picks[q.question]?.[0] || '';
	const answers = $derived(Object.fromEntries(questions.map((q) => [q.question, answerOf(q)])));
	const complete = $derived(questions.length > 0 && questions.every((q) => answers[q.question]));
	const mine = $derived(item.votes[threads.me?.id ?? '']);

	function pick(q: Question, label: string, on: boolean) {
		const cur = picks[q.question] ?? [];
		picks[q.question] = q.multiSelect ? (on ? [...cur, label] : cur.filter((l) => l !== label)) : [label];
		if (!q.multiSelect) other[q.question] = '';
	}

	// Who voted for an option; votes that match no option are free text.
	const voters = (q: Question, label: string) =>
		Object.entries(item.votes)
			.filter(([, a]) => a[q.question]?.split(', ').includes(label))
			.map(([u]) => userName(u));
	const freeVotes = (q: Question) =>
		Object.entries(item.votes).filter(([, a]) => a[q.question] && !q.options.some((o) => a[q.question].split(', ').includes(o.label)));

	async function send(call: Promise<unknown>) {
		busy = true;
		await act(call);
		busy = false;
	}
	const answer = (decision: PermissionDecision) =>
		send(api.permissionAnswer({ threadId, requestId: item.req.id, decision, message: reason.trim(), answers: decision === PermissionDecision.ALLOW ? answers : {} }));
	const vote = () => send(api.permissionVote({ threadId, requestId: item.req.id, answers }));

	const outcome = (r: NonNullable<typeof item.resolved>) => {
		const by = userName(r.by);
		switch (r.decision) {
			case PermissionDecision.ALLOW:
				return `Answered by ${by}`;
			case PermissionDecision.DENY:
				return `Declined by ${by}${r.message ? `: ${r.message}` : ''}`;
			case PermissionDecision.CANCELED:
				return 'Withdrawn by the agent';
			default:
				return 'Lost: the environment restarted before an answer. Ask the agent again if it still needs this.';
		}
	};
</script>

<div role="group" aria-label="Question" class="space-y-3 rounded-lg border px-4 py-3 text-sm {item.resolved ? 'bg-muted/20' : 'border-attention/40 bg-attention/5 highlighter:border-border highlighter:bg-card'}">
	<div class="flex items-center gap-2">
		<MessageCircleQuestionIcon class="size-4 shrink-0 {item.resolved ? 'text-muted-foreground' : 'text-attention-text'}" />
		<span class="min-w-0 flex-1">The agent asks {questions.length > 1 ? `${questions.length} questions` : 'a question'}</span>
	</div>

	{#each questions as q (q.question)}
		<fieldset class="space-y-1.5" disabled={!!item.resolved || busy}>
			<legend class="mb-1.5 flex items-baseline gap-2">
				{#if q.header}<span class="rounded bg-muted px-1.5 py-0.5 text-xs font-medium text-muted-foreground">{q.header}</span>{/if}
				<span class="font-medium {item.resolved ? '' : 'marker'}">{q.question}</span>
			</legend>
			{#if item.resolved}
				{#if item.resolved.answers[q.question]}<p class="flex items-center gap-1.5 pl-1"><CheckIcon class="size-3.5 text-success-foreground" /> {item.resolved.answers[q.question]}</p>{/if}
			{:else}
				{#each q.options as o (o.label)}
					{@const who = voters(q, o.label)}
					<label class="flex cursor-pointer items-start gap-2.5 rounded-md border px-3 py-2 transition-colors hover:bg-muted/40 has-[:checked]:border-info/60 has-[:checked]:bg-info/10">
						<input
							type={q.multiSelect ? 'checkbox' : 'radio'}
							name="{item.req.id}:{q.question}"
							class="mt-0.5 accent-info"
							checked={picks[q.question]?.includes(o.label) ?? false}
							onchange={(e) => pick(q, o.label, e.currentTarget.checked)}
						/>
						<span class="min-w-0 flex-1">
							<span class="block">{o.label}</span>
							{#if o.description}<span class="block text-xs text-muted-foreground">{o.description}</span>{/if}
						</span>
						{#if who.length}<span class="shrink-0 text-xs text-muted-foreground" title="Votes">{who.join(', ')}</span>{/if}
					</label>
				{/each}
				<Input
					value={other[q.question] ?? ''}
					oninput={(e) => {
						other[q.question] = e.currentTarget.value;
						if (!q.multiSelect && e.currentTarget.value) picks[q.question] = [];
					}}
					placeholder="Other…"
					aria-label="Other answer: {q.header || q.question}"
					class="h-8 text-xs"
				/>
				{#each freeVotes(q) as [u, a] (u)}<p class="pl-1 text-xs text-muted-foreground">{userName(u)}: {a[q.question]}</p>{/each}
			{/if}
		</fieldset>
	{/each}

	{#if item.resolved}
		<p class="flex items-center gap-1.5 text-xs text-muted-foreground">
			{#if item.resolved.decision === PermissionDecision.ALLOW}<CheckIcon class="size-3.5 text-success-foreground" />{:else}<XIcon class="size-3.5" />{/if}
			{outcome(item.resolved)}
		</p>
	{:else if can}
		<div class="flex flex-wrap items-center gap-2">
			<Button size="sm" class="highlighter:bg-attention-surface highlighter:text-attention-foreground highlighter:hover:bg-attention-surface/80" disabled={busy || !complete} onclick={() => answer(PermissionDecision.ALLOW)}><CheckIcon /> Send answers</Button>
			<Input bind:value={reason} placeholder="Why not (optional)" class="h-8 min-w-0 flex-1 text-xs" aria-label="Reason" />
			<Button size="sm" variant="outline" disabled={busy} onclick={() => answer(PermissionDecision.DENY)}><XIcon /> Decline</Button>
		</div>
	{:else}
		<div class="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
			<Button size="sm" variant="outline" disabled={busy || !complete} onclick={vote}>{mine ? 'Change vote' : 'Vote'}</Button>
			<span>{mine ? 'Your vote is in.' : 'Your vote shows here;'} {ownerName} sends the answer.</span>
		</div>
	{/if}
</div>
