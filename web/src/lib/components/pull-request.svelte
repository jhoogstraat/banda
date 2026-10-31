<script lang="ts">
	import { untrack } from 'svelte';
	import { timestampDate, type Timestamp } from '@bufbuild/protobuf/wkt';
	import { ConnectError } from '@connectrpc/connect';
	import ExternalLinkIcon from '@lucide/svelte/icons/external-link';
	import FileDiffIcon from '@lucide/svelte/icons/file-diff';
	import FileCodeIcon from '@lucide/svelte/icons/file-code';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';
	import { Response } from '#lib/components/ai-elements/response/index.js';
	import ForgeAction from '#lib/components/forge-action.svelte';
	import IconButton from '#lib/components/icon-button.svelte';
	import LoadState from '#lib/components/load-state.svelte';
	import PullRequestLabels from '#lib/components/pull-request-labels.svelte';
	import ReviewerInput from '#lib/components/reviewer-input.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { NativeSelect, NativeSelectOption } from '#lib/components/ui/native-select/index.js';
	import { Textarea } from '#lib/components/ui/textarea/index.js';
	import { api } from '#lib/api.js';
	import {
		Mergeable,
		PullState,
		ReviewState,
		type PullRequest,
		type PullRequestChoicesResponse,
		type PullRequestComment,
		type Thread
	} from '#lib/gen/banda/v1/banda_pb.js';
	import { checkStates, checksSummary, duration, live, logins, noun, pullStates, ref, reviewLabels } from '#lib/pullrequests.js';
	import { threads, userName } from '#lib/threads.svelte.js';
	import { ago } from '#lib/utils.js';

	// The Pull request tab: the summary (state, branches, description, labels, reviewers, checks, mergeability, the
	// owner's merge button) and the conversation (comments, reviews, review comments threaded by file and line),
	// where anyone with a token of their own comments and replies. Files changed are the Diff tab's.
	// version changes when the thread hears the pull request changed, which reloads it.
	let { thread, owner, version, ondiff }: { thread: Thread; owner: boolean; version: number; ondiff: () => void } = $props();

	let pr = $state<PullRequest>();
	let canComment = $state(false);
	let loaded = $state(false);
	let loadError = $state('');
	let error = $state('');
	let busy = $state(false);
	let method = $state('');
	let comment = $state('');
	let replying = $state('');
	let reply = $state('');
	let editing = $state(false);
	let title = $state('');
	let body = $state('');
	let reviewers = $state('');
	// Who may review and the repository's labels, asked once when the owner first sees a live one; without them
	// reviewers are typed and labels can't be added.
	let choices = $state<PullRequestChoicesResponse>();
	let askedChoices = false;
	$effect(() => {
		if (!owner || !pr?.summary || !live(pr.summary) || askedChoices) return;
		askedChoices = true;
		api.pullRequestChoices({ threadId: thread.id }).then((c) => (choices = c)).catch(() => {});
	});
	const reviewerChoices = $derived((choices?.reviewers ?? []).filter((l) => l !== pr?.author && !pr?.reviewers.some((r) => r.login === l)));

	async function load(refresh = false) {
		try {
			const res = await api.pullRequestGet({ threadId: thread.id, refresh });
			[pr, canComment, loadError] = [res.pullRequest, res.canComment, ''];
			loaded = true;
			if (pr && !pr.mergeMethods.includes(method)) method = pr.mergeMethods[0] ?? '';
		} catch (e) {
			loadError = ConnectError.from(e).rawMessage;
		}
	}
	$effect(() => {
		void version;
		untrack(() => load());
	});

	// Runs a call that answers the fresh pull request; errors stay on screen until the next call.
	async function act(call: Promise<{ pullRequest?: PullRequest }>) {
		busy = true;
		try {
			const res = await call;
			if (res.pullRequest) pr = res.pullRequest;
			error = '';
			return true;
		} catch (e) {
			error = ConnectError.from(e).rawMessage;
			return false;
		} finally {
			busy = false;
		}
	}
	const threadId = $derived(thread.id);
	const s = $derived(pr?.summary);
	const look = $derived(s ? pullStates[s.state] : undefined);
	const what = $derived(noun(thread.forge));
	const merge = () => act(api.pullRequestMerge({ threadId, method }));
	const setDraft = (draft: boolean) => act(api.pullRequestUpdate({ threadId, draft }));
	async function save() {
		if (await act(api.pullRequestUpdate({ threadId, title, body }))) editing = false;
	}
	async function requestReview() {
		const asked = logins(reviewers);
		if (asked.length && (await act(api.pullRequestUpdate({ threadId, addReviewers: asked })))) reviewers = '';
	}
	const resolve = (c: PullRequestComment) => act(api.pullRequestResolve({ threadId, reviewThread: c.threadId, resolved: !c.resolved }));
	async function send(e: SubmitEvent) {
		e.preventDefault();
		if (await act(api.pullRequestComment({ threadId, body: comment }))) comment = '';
	}
	async function sendReply(to: string) {
		if (await act(api.pullRequestComment({ threadId, body: reply, replyTo: to }))) [reply, replying] = ['', ''];
	}

	// Review comments thread under their first comment; everything else is in time order.
	const conversation = $derived.by(() => {
		const replies = new Map<string, PullRequestComment[]>();
		for (const c of pr?.comments ?? []) if (c.replyTo) replies.set(c.replyTo, [...(replies.get(c.replyTo) ?? []), c]);
		return (pr?.comments ?? []).filter((c) => !c.replyTo).map((c) => ({ c, replies: replies.get(c.id) ?? [] }));
	});
	const mergeText = $derived.by(() => {
		if (!s || !pr) return '';
		if (s.mergeable === Mergeable.CONFLICTS) return `Conflicts with ${pr.baseBranch}: the agent can resolve them`;
		if (s.mergeable === Mergeable.BLOCKED) return pr.mergeDetail || 'Blocked by the repository’s rules';
		if (s.mergeable === Mergeable.CLEAN) return 'No conflicts with ' + pr.baseBranch;
		return 'The forge is still checking for conflicts';
	});
	const methodLabels: Record<string, string> = { merge: 'Merge commit', squash: 'Squash and merge', rebase: 'Rebase and merge' };
	const reviewTone: Record<number, string> = {
		[ReviewState.APPROVED]: 'text-success-foreground',
		[ReviewState.CHANGES_REQUESTED]: 'text-destructive',
		[ReviewState.REQUESTED]: 'text-warning-foreground'
	};
	// "just now", "5m ago".
	const since = (ts?: Timestamp) => (ago(ts, threads.now) === 'now' ? 'just now' : `${ago(ts, threads.now)} ago`);
	const lastLines =(hunk: string) => hunk.split('\n').slice(-4).join('\n');
</script>

{#snippet when(c: PullRequestComment)}
	<span class="text-muted-foreground" title={c.createdAt ? timestampDate(c.createdAt).toLocaleString() : ''}>{ago(c.createdAt, threads.now)}</span>
{/snippet}

{#snippet commentCard(c: PullRequestComment, nested = false)}
	<article class="space-y-1 {nested ? 'border-l-2 pl-3' : ''}" aria-label="Comment by {c.author}">
		<header class="flex items-center gap-1.5 text-xs">
			<span class="font-medium">{c.author}</span>
			{#if c.review}
				<span class={reviewTone[c.review] ?? 'text-muted-foreground'}>{reviewLabels[c.review].toLowerCase()}</span>
			{:else if c.path && !nested}
				<span class="text-muted-foreground">commented on</span>
			{/if}
			{#if c.resolved}<span class="rounded-full border px-1.5 text-[10px] text-muted-foreground">resolved</span>{/if}
			{@render when(c)}
			{#if c.url}<a href={c.url} target="_blank" rel="noreferrer" class="ml-auto text-muted-foreground hover:text-foreground" aria-label="Open on the forge"><ExternalLinkIcon class="size-3" /></a>{/if}
		</header>
		{#if c.path && !nested}
			<div class="overflow-hidden rounded-md border text-[11px]">
				<div class="flex items-center gap-1 border-b bg-muted/40 px-2 py-1 font-mono"><FileCodeIcon class="size-3" />{c.path}{c.line ? `:${c.line}` : ''}</div>
				{#if c.diffHunk}<pre class="overflow-x-auto px-2 py-1 font-mono text-muted-foreground">{lastLines(c.diffHunk)}</pre>{/if}
			</div>
		{/if}
		{#if c.body}<Response content={c.body} class="h-auto text-sm" />{/if}
	</article>
{/snippet}

{#if loadError && !pr}
	<LoadState title="Couldn't load the {what}" description={loadError} retry={() => load()} />
{:else if pr && s && look}
	<div class="space-y-5 p-4 text-sm">
		<section class="space-y-2" aria-label="Summary">
			<div class="flex items-start gap-2">
				<span class="mt-0.5 flex shrink-0 items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-medium {look.class}">
					<look.icon class="size-3.5" />{look.label}
				</span>
				{#if editing}
					<Input bind:value={title} aria-label="Title" class="h-7" />
				{:else}
					<h2 class="min-w-0 flex-1 text-base leading-snug font-semibold">
						{s.title} <a href={s.url} target="_blank" rel="noreferrer" class="font-normal text-muted-foreground hover:underline">{ref(s)}</a>
					</h2>
				{/if}
				{#if owner && !editing && live(s)}
					<IconButton icon={PencilIcon} label="Edit title and description" onclick={() => ([title, body, editing] = [s.title, pr!.body, true])} />
				{/if}
			</div>
			<p class="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
				<span>{pr.author}</span>
				<span>·</span>
				<code>{pr.headBranch}</code> → <code>{pr.baseBranch}</code>
				{#if pr.additions || pr.deletions}
					<span>·</span><span class="text-success-foreground">+{pr.additions}</span><span class="text-destructive">−{pr.deletions}</span>
				{/if}
				{#if pr.changedFiles}<span>· {pr.changedFiles} file{pr.changedFiles === 1 ? '' : 's'}</span>{/if}
				<button class="flex items-center gap-1 hover:text-foreground hover:underline" onclick={ondiff}><FileDiffIcon class="size-3" />View diff</button>
			</p>
			<p class="flex items-center gap-1 text-xs text-muted-foreground">
				{#if pr.error}
					<TriangleAlertIcon class="size-3.5 shrink-0 text-warning-foreground" />
					<span class="text-warning-foreground">As of {since(pr.fetchedAt)}: {pr.error}</span>
					{#if owner}<ForgeAction message={pr.error} {thread} />{/if}
				{:else}
					Updated {since(pr.fetchedAt)}
				{/if}
				<IconButton icon={RefreshCwIcon} label="Refresh from the forge" class="size-6" disabled={busy} onclick={() => load(true)} />
			</p>
		</section>

		{#if live(s)}
			<section class="space-y-2 rounded-lg border p-3" aria-label="Merge">
				<p class="flex items-center gap-1.5 text-xs {s.mergeable === Mergeable.CONFLICTS ? 'text-destructive' : 'text-muted-foreground'}">{mergeText}</p>
				{#if owner}
					<div class="flex flex-wrap items-center gap-2">
						{#if s.state === PullState.DRAFT}
							<Button size="sm" disabled={busy} onclick={() => setDraft(false)}>Ready for review</Button>
						{:else}
							<NativeSelect size="sm" bind:value={method} aria-label="Merge method">
								{#each pr.mergeMethods as m (m)}<NativeSelectOption value={m}>{methodLabels[m] ?? m}</NativeSelectOption>{/each}
							</NativeSelect>
							<Button size="sm" disabled={busy || s.mergeable === Mergeable.CONFLICTS || !method} onclick={merge}>Merge</Button>
							<Button size="sm" variant="ghost" disabled={busy} onclick={() => setDraft(true)}>Convert to draft</Button>
						{/if}
					</div>
				{:else}
					<p class="text-xs text-muted-foreground">{userName(thread.ownerId)} merges it: it goes out with their token.</p>
				{/if}
			</section>
		{:else if s.state === PullState.MERGED}
			<p class="text-xs text-muted-foreground">Merged{pr.mergedBy ? ` by ${pr.mergedBy}` : ''}{pr.mergedAt ? ` ${since(pr.mergedAt)}` : ''}.</p>
		{/if}
		{#if error}
			<div class="flex flex-wrap items-center gap-2">
				<p class="text-xs text-destructive" role="alert">{error}</p>
				<ForgeAction message={error} {thread} />
			</div>
		{/if}

		<section class="space-y-2" aria-label="Description">
			{#if editing}
				<Textarea bind:value={body} rows={10} class="font-mono text-xs" aria-label="Description" />
				<div class="flex gap-2">
					<Button size="sm" disabled={busy || !title.trim()} onclick={save}>Save</Button>
					<Button size="sm" variant="ghost" onclick={() => (editing = false)}>Cancel</Button>
				</div>
			{:else if pr.body}
				<Response content={pr.body} class="h-auto" />
			{:else}
				<p class="text-xs text-muted-foreground italic">No description.</p>
			{/if}
		</section>

		<PullRequestLabels
			labels={pr.labels}
			choices={choices?.labels ?? []}
			editable={owner && live(s)}
			disabled={busy}
			onadd={(l) => act(api.pullRequestUpdate({ threadId, addLabels: [l] }))}
			onremove={(l) => act(api.pullRequestUpdate({ threadId, removeLabels: [l] }))}
		/>

		<section class="space-y-1.5" aria-label="Reviewers">
			<h3 class="text-xs font-medium text-muted-foreground">Reviewers</h3>
			{#each pr.reviewers as r (r.login)}
				<p class="flex items-center gap-2 text-xs"><span class="font-medium">{r.login}</span><span class={reviewTone[r.state] ?? 'text-muted-foreground'}>{reviewLabels[r.state]}</span></p>
			{:else}
				<p class="text-xs text-muted-foreground">None yet.</p>
			{/each}
			{#if owner && live(s)}
				<form class="flex gap-2" onsubmit={(e) => (e.preventDefault(), requestReview())}>
					<ReviewerInput bind:value={reviewers} choices={reviewerChoices} id="pr-reviewer-choices" />
					<Button type="submit" size="sm" variant="outline" disabled={busy || !reviewers.trim()}>Ask for review</Button>
				</form>
			{/if}
		</section>

		<section class="space-y-1.5" aria-label="Checks">
			<h3 class="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
				{#if s.checks}{@const c = checkStates[s.checks]}<c.icon class="size-3.5 {c.class}" />{/if}
				Checks · {checksSummary(pr.checks)}
			</h3>
			<ul class="divide-y rounded-md border">
				{#each pr.checks as c, i (i)}
					{@const st = checkStates[c.state]}
					<li class="flex items-center gap-2 px-2 py-1.5 text-xs">
						<st.icon class="size-3.5 shrink-0 {st.class}" aria-label={st.label} />
						<span class="min-w-0 flex-1 truncate">{c.name}</span>
						<span class="shrink-0 text-muted-foreground">{c.status.replaceAll('_', ' ')}</span>
						{#if c.startedAt}
							<span class="shrink-0 text-muted-foreground tabular-nums">{duration(timestampDate(c.startedAt), c.completedAt ? timestampDate(c.completedAt) : undefined)}</span>
						{/if}
						{#if c.url}<a href={c.url} target="_blank" rel="noreferrer" class="shrink-0 text-muted-foreground hover:text-foreground" aria-label="Details of {c.name}"><ExternalLinkIcon class="size-3" /></a>{/if}
					</li>
				{/each}
			</ul>
		</section>

		<section class="space-y-4" aria-label="Conversation">
			<h3 class="text-xs font-medium text-muted-foreground">Conversation</h3>
			{#each conversation as { c, replies } (c.id)}
				<div class="space-y-2">
					{@render commentCard(c)}
					{#each replies as r (r.id)}{@render commentCard(r, true)}{/each}
					{#if (c.path || c.threadId) && canComment}
						{#if replying === c.id}
							<div class="space-y-1.5 border-l-2 pl-3">
								<Textarea bind:value={reply} rows={2} placeholder="Reply" aria-label="Reply to {c.author}" />
								<div class="flex gap-2">
									<Button size="sm" disabled={busy || !reply.trim()} onclick={() => sendReply(c.id)}>Reply</Button>
									<Button size="sm" variant="ghost" onclick={() => (replying = '')}>Cancel</Button>
								</div>
							</div>
						{:else}
							<div class="flex gap-3 pl-3 text-xs text-muted-foreground">
								<button class="hover:text-foreground hover:underline" onclick={() => ([replying, reply] = [c.id, ''])}>Reply</button>
								{#if c.threadId}
									<button class="hover:text-foreground hover:underline" disabled={busy} onclick={() => resolve(c)}>{c.resolved ? 'Unresolve' : 'Resolve'}</button>
								{/if}
							</div>
						{/if}
					{/if}
				</div>
			{:else}
				<p class="text-xs text-muted-foreground">No comments yet.</p>
			{/each}
			{#if canComment}
				<form class="space-y-1.5" onsubmit={send}>
					<Textarea bind:value={comment} rows={3} placeholder="Leave a comment" aria-label="Comment" />
					<Button type="submit" size="sm" disabled={busy || !comment.trim()}>Comment</Button>
				</form>
			{:else}
				{@const forge = thread.forge === 'gitlab' ? 'GitLab' : 'GitHub'}
				<p class="text-xs text-muted-foreground">
					Comments go out as you: <a href="/settings#{forge.toLowerCase()}" class="underline underline-offset-2">connect {forge}</a>, or add a
					<a href="/settings?secret=GIT_TOKEN#secrets" class="underline underline-offset-2">GIT_TOKEN</a> secret, in Settings to comment here.
				</p>
			{/if}
		</section>
	</div>
{:else if loaded}
	<p class="p-4 text-sm text-muted-foreground">This thread has no {what}.</p>
{:else if !loadError}
	<p class="p-4 text-sm text-muted-foreground">Loading…</p>
{/if}
