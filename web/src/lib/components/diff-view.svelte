<script lang="ts">
	import { tick } from 'svelte';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import GitMergeConflictIcon from '@lucide/svelte/icons/git-merge-conflict';
	import GitCompareIcon from '@lucide/svelte/icons/git-compare';
	import MessageSquareIcon from '@lucide/svelte/icons/message-square';
	import XIcon from '@lucide/svelte/icons/x';
	import IconButton from '#lib/components/icon-button.svelte';
	import MentionText from '#lib/components/mention-text.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { Textarea } from '#lib/components/ui/textarea/index.js';
	import { endsAt, locate, parseDiff, tone, type Comment } from '#lib/diff.js';
	import type { Overlap } from '#lib/gen/banda/v1/banda_pb.js';
	import { userName } from '#lib/threads.svelte.js';

	// A unified diff to review: click a line number (drag or shift-click for a range) to comment.
	// Comments collect in `pending` until the review is sent; `posted` (from the chat) show where they still apply.
	// Setting `focus` scrolls to a comment's lines. Files that `overlaps` (other threads) change too are marked.
	let {
		diff,
		overlaps = [],
		pending = $bindable(),
		posted,
		focus = $bindable(),
		ask = $bindable(),
		onsend
	}: {
		diff: string;
		overlaps?: Overlap[];
		pending: Comment[];
		posted: (Comment & { by: string })[];
		focus?: Comment;
		ask: boolean;
		onsend: (summary: string) => Promise<boolean>;
	} = $props();

	// Big files start collapsed, so a large diff stays fast.
	const BIG = 400;
	const files = $derived(parseDiff(diff));
	let open = $state<Record<string, boolean>>({});
	const isOpen = (f: { path: string; lines: unknown[] }) => open[f.path] ?? f.lines.length <= BIG;
	let listOpen = $state(false);
	// Per file, what the threads that also change it are called.
	const shared = $derived.by(() => {
		const by = new Map<string, string[]>();
		for (const o of overlaps) for (const p of o.paths) by.set(p, [...(by.get(p) ?? []), o.title || 'Untitled thread']);
		return by;
	});
	let scroller = $state<HTMLElement>();

	// Comments by file and the index of the line they end at.
	const notes = $derived.by(() => {
		const at = new Map<string, Map<number, { c: Comment; by?: string; pending?: number }[]>>();
		const add = (c: Comment, n: { c: Comment; by?: string; pending?: number }) => {
			const f = files.find((f) => f.path === c.path);
			const i = f?.lines.findIndex((l) => endsAt(c, l)) ?? -1;
			if (i < 0) return;
			const m = at.get(c.path) ?? at.set(c.path, new Map()).get(c.path)!;
			m.set(i, [...(m.get(i) ?? []), n]);
		};
		posted.forEach((c) => add(c, { c, by: c.by }));
		pending.forEach((c, i) => add(c, { c, pending: i }));
		return at;
	});

	// The line range being selected (dragging) or commented on.
	let sel = $state<{ path: string; from: number; to: number; anchor: number; drag: boolean }>();
	let draft = $state('');
	let summary = $state('');
	let sending = $state(false);
	let flash = $state<string>();
	const inSel = (path: string, i: number) => sel?.path === path && i >= sel.from && i <= sel.to;

	function down(e: PointerEvent, path: string, i: number) {
		if (e.button) return;
		e.preventDefault();
		const anchor = e.shiftKey && sel?.path === path ? sel.anchor : i;
		sel = { path, anchor, from: Math.min(anchor, i), to: Math.max(anchor, i), drag: true };
	}
	function over(path: string, i: number) {
		if (sel?.drag && sel.path === path) [sel.from, sel.to] = [Math.min(sel.anchor, i), Math.max(sel.anchor, i)];
	}

	function add() {
		const f = files.find((f) => f.path === sel?.path);
		if (!f || !sel || !draft.trim()) return;
		pending = [...pending, { path: f.path, body: draft.trim(), ...locate(f.lines, sel.from, sel.to) }];
		sel = undefined;
		draft = '';
	}

	async function send() {
		sending = true;
		if (await onsend(summary)) summary = '';
		sending = false;
	}

	async function show(path: string, line?: number) {
		open[path] = true;
		await tick();
		const el = scroller?.querySelector(line === undefined ? `[data-file="${CSS.escape(path)}"]` : `[data-line="${CSS.escape(`${path}:${line}`)}"]`);
		el?.scrollIntoView({ block: line === undefined ? 'start' : 'center' });
		if (line !== undefined) {
			flash = `${path}:${line}`;
			setTimeout(() => (flash = undefined), 1500);
		}
	}

	$effect(() => {
		if (!focus || !scroller) return;
		const c = focus;
		focus = undefined;
		const i = files.find((f) => f.path === c.path)?.lines.findIndex((l) => endsAt(c, l)) ?? -1;
		show(c.path, i < 0 ? undefined : i);
	});
</script>

<svelte:window onpointerup={() => sel && (sel.drag = false)} />

{#snippet overlapMark(path: string)}
	{#if shared.has(path)}
		<span role="img" class="shrink-0 text-warning-foreground" aria-label="Also changed in {shared.get(path)!.join(', ')}" title="Also changed in {shared.get(path)!.join(', ')}">
			<GitMergeConflictIcon class="size-3" />
		</span>
	{/if}
{/snippet}

{#snippet note(c: Comment, by: string | undefined, index: number | undefined)}
	<div class="my-1 ml-[4.5rem] mr-2 rounded-md border bg-card px-3 py-2 font-sans text-sm {index === undefined ? '' : 'border-warning/50'}">
		<div class="mb-0.5 flex items-center gap-1.5 text-xs text-muted-foreground">
			<MessageSquareIcon class="size-3" />
			{index === undefined ? userName(by ?? '') : 'Pending'}
			{#if index !== undefined}
				<IconButton icon={XIcon} label="Remove the comment" class="ml-auto size-5" onclick={() => (pending = pending.filter((_, j) => j !== index))} />
			{/if}
		</div>
		<p class="whitespace-pre-wrap"><MentionText text={c.body} /></p>
	</div>
{/snippet}

<div class="flex min-h-0 flex-1 flex-col">
	{#if pending.length}
		<div class="flex shrink-0 flex-wrap items-center gap-2 border-b bg-warning/5 px-3 py-2 text-xs">
			<span class="font-medium">{pending.length} pending comment{pending.length > 1 ? 's' : ''}</span>
			<Input bind:value={summary} placeholder="Summary (optional)" class="h-7 min-w-32 flex-1 text-xs" />
			<label class="flex items-center gap-1.5 text-muted-foreground"><input type="checkbox" bind:checked={ask} class="accent-primary" /> Ask @agent</label>
			<Button size="sm" variant="ghost" onclick={() => (pending = [])}>Discard</Button>
			<Button size="sm" disabled={sending} onclick={send}>Send review</Button>
		</div>
	{/if}

	<div bind:this={scroller} class="min-h-0 flex-1 overflow-y-auto">
		{#if files.length}
			<button class="flex w-full items-center gap-1 px-3 py-2 text-left text-xs text-muted-foreground hover:bg-muted/50" onclick={() => (listOpen = !listOpen)}>
				<ChevronRightIcon class="size-3.5 transition-transform {listOpen ? 'rotate-90' : ''}" />
				{files.length} file{files.length > 1 ? 's' : ''} changed
				<span class="font-mono text-diff-add-foreground">+{files.reduce((n, f) => n + f.add, 0)}</span>
				<span class="font-mono text-diff-del-foreground">−{files.reduce((n, f) => n + f.del, 0)}</span>
			</button>
			{#if listOpen}
				<ul class="pb-2 text-xs">
					{#each files as f (f.path)}
						<li>
							<button class="flex w-full items-center gap-2 px-3 py-0.5 pl-8 text-left hover:bg-muted/50 pointer-coarse:py-2.5" onclick={() => show(f.path)}>
								<span class="min-w-0 flex-1 truncate font-mono" title={f.path}>{f.path}</span>
								{@render overlapMark(f.path)}
								<span class="shrink-0 font-mono text-diff-add-foreground">+{f.add}</span>
								<span class="shrink-0 font-mono text-diff-del-foreground">−{f.del}</span>
							</button>
						</li>
					{/each}
				</ul>
			{/if}
			{#each files as f (f.path)}
				{@const fileNotes = notes.get(f.path)}
				<section data-file={f.path} class="border-t [content-visibility:auto] [contain-intrinsic-size:auto_2.5rem]">
					<button
						class="sticky top-0 z-10 flex w-full items-center gap-2 bg-card px-3 py-1.5 text-left text-xs hover:bg-muted"
						onclick={() => (open[f.path] = !isOpen(f))}
					>
						<ChevronRightIcon class="size-3.5 shrink-0 text-muted-foreground transition-transform {isOpen(f) ? 'rotate-90' : ''}" />
						<span class="min-w-0 flex-1 truncate font-mono" title={f.path}>{f.path}</span>
						{#if fileNotes?.size}<MessageSquareIcon class="size-3 shrink-0 text-muted-foreground" />{/if}
						{@render overlapMark(f.path)}
						<span class="shrink-0 font-mono text-diff-add-foreground">+{f.add}</span>
						<span class="shrink-0 font-mono text-diff-del-foreground">−{f.del}</span>
					</button>
					{#if isOpen(f)}
						<div class="py-1 font-mono text-xs leading-5">
							{#each f.lines as l, i}
								{@const n = l.new ?? l.old}
								<div
									data-line="{f.path}:{i}"
									class="flex {tone(l.text)} {inSel(f.path, i) ? 'bg-primary/20!' : ''} {flash === `${f.path}:${i}` ? 'ring-1 ring-primary ring-inset' : ''}"
								>
									{#if n === undefined}
										<span class="w-[4.5rem] shrink-0"></span>
									{:else}
										<button
											class="group/num flex w-[4.5rem] shrink-0 cursor-pointer select-none text-right text-muted-foreground hover:text-foreground"
											title="Comment (drag or shift-click for a range)"
											aria-label="Comment on line {n}"
											onpointerdown={(e) => down(e, f.path, i)}
											onpointerenter={() => over(f.path, i)}
										>
											<span class="w-8">{l.old ?? ''}</span><span class="w-8">{l.new ?? ''}</span>
											<span class="w-2.5 text-primary opacity-0 group-hover/num:opacity-100">+</span>
										</button>
									{/if}
									<span class="min-w-0 flex-1 pr-3 break-all whitespace-pre-wrap">{l.text || ' '}</span>
								</div>
								{#each fileNotes?.get(i) ?? [] as x}{@render note(x.c, x.by, x.pending)}{/each}
								{#if sel && !sel.drag && sel.path === f.path && sel.to === i}
									<div class="my-1 ml-[4.5rem] mr-2 space-y-2 rounded-md border bg-card p-2 font-sans">
										<!-- svelte-ignore a11y_autofocus -->
										<Textarea
											bind:value={draft}
											autofocus
											placeholder="Comment on {sel.from === sel.to ? 'this line' : `${sel.to - sel.from + 1} lines`}"
											class="min-h-14 text-sm"
											onkeydown={(e) => {
												if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) add();
												else if (e.key === 'Escape') sel = undefined;
											}}
										/>
										<div class="flex justify-end gap-1">
											<Button size="sm" variant="ghost" onclick={() => (sel = undefined)}>Cancel</Button>
											<Button size="sm" disabled={!draft.trim()} onclick={add}>Add to review</Button>
										</div>
									</div>
								{/if}
							{/each}
						</div>
					{/if}
				</section>
			{/each}
		{:else}
			<div class="flex flex-col items-center gap-2 p-10 text-sm text-muted-foreground">
				<GitCompareIcon class="size-6" />
				No changes yet
			</div>
		{/if}
	</div>
</div>
