<script lang="ts">
	import ForgeAction from '#lib/components/forge-action.svelte';
	import { untrack } from 'svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Dialog from '#lib/components/ui/dialog/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { Label } from '#lib/components/ui/label/index.js';
	import { Textarea } from '#lib/components/ui/textarea/index.js';
	import PullRequestLabels from '#lib/components/pull-request-labels.svelte';
	import ReviewerInput from '#lib/components/reviewer-input.svelte';
	import { api } from '#lib/api.js';
	import type { PullRequestChoicesResponse, Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { logins, noun } from '#lib/pullrequests.js';

	// The owner opens a pull request for the thread's pushed branch, prefilled from the thread's title and the
	// agent's last reply (summary), with reviewers and labels from the repository's lists, or links the one
	// already open for the branch.
	let { open = $bindable(false), thread, summary, ondone }: { open: boolean; thread: Thread; summary: string; ondone: () => void } = $props();

	let title = $state('');
	let body = $state('');
	let draft = $state(false);
	let reviewers = $state('');
	let labels = $state<string[]>([]);
	let choices = $state<PullRequestChoicesResponse>();
	let busy = $state(false);
	let error = $state('');
	const what = $derived(noun(thread.forge));
	// Prefilled when it opens; a thread refresh while it is open keeps what was typed. The lists are asked once;
	// without them reviewers are typed and labels can't be added.
	$effect(() => {
		if (!open) return;
		untrack(() => {
			[title, body, draft, reviewers, labels, error] = [thread.title, summary, false, '', [], ''];
			if (!choices) api.pullRequestChoices({ threadId: thread.id }).then((c) => (choices = c)).catch(() => {});
		});
	});

	async function run(call: () => Promise<unknown>) {
		busy = true;
		try {
			await call();
			open = false;
			ondone();
		} catch (err) {
			error = (err as Error).message;
		} finally {
			busy = false;
		}
	}
	const create = (e: SubmitEvent) => {
		e.preventDefault();
		run(() => api.pullRequestCreate({ threadId: thread.id, title, body, draft, reviewers: logins(reviewers), labels }));
	};
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="sm:max-w-xl">
		<Dialog.Header>
			<Dialog.Title>Open a {what}</Dialog.Title>
			<Dialog.Description>
				<code class="text-xs">{thread.branch}</code> into <code class="text-xs">{thread.baseBranch}</code>, opened with your token.
			</Dialog.Description>
		</Dialog.Header>
		<form class="flex flex-col gap-3" onsubmit={create}>
			<div class="grid gap-1.5">
				<Label for="pr-title">Title</Label>
				<Input id="pr-title" bind:value={title} required />
			</div>
			<div class="grid gap-1.5">
				<Label for="pr-body">Description</Label>
				<Textarea id="pr-body" bind:value={body} rows={10} class="max-h-[50vh] font-mono text-xs" placeholder="Markdown" />
			</div>
			<label class="flex items-center gap-2 text-sm">
				<input type="checkbox" bind:checked={draft} class="size-4 accent-primary" />
				Open as a draft
			</label>
			<div class="grid gap-1.5">
				<Label>Reviewers</Label>
				<ReviewerInput bind:value={reviewers} choices={choices?.reviewers ?? []} id="pr-create-reviewer-choices" />
			</div>
			<PullRequestLabels
				{labels}
				choices={choices?.labels ?? []}
				editable
				disabled={busy}
				onadd={(l) => (labels = [...labels, l])}
				onremove={(l) => (labels = labels.filter((x) => x !== l))}
			/>
			<div class="flex flex-wrap items-center gap-2">
				<Button type="button" variant="ghost" size="sm" disabled={busy} onclick={() => run(() => api.pullRequestLink({ threadId: thread.id }))}>
					Link the open one for this branch
				</Button>
				<Button type="submit" class="ml-auto" disabled={busy || !title.trim()}>Create {what}</Button>
			</div>
			{#if error}
				<div class="space-y-2">
					<p class="text-sm text-destructive" role="alert">{error}</p>
					<ForgeAction message={error} {thread} size="sm" />
				</div>
			{/if}
		</form>
	</Dialog.Content>
</Dialog.Root>
