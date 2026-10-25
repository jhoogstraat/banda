<script lang="ts">
	import { tick } from 'svelte';
	import BotIcon from '@lucide/svelte/icons/bot';
	import * as PromptInput from '#lib/components/ai-elements/prompt-input/index.js';
	import type { SlashCommand } from '#lib/gen/banda/v1/banda_pb.js';
	import { threads } from '#lib/threads.svelte.js';

	// `commands` (the owner's, who alone talks to the agent): a "/" at the start offers them; the choice is sent as text.
	type Option = { id?: string; name: string; slash?: SlashCommand };
	let {
		placeholder,
		commands = [],
		value = $bindable(''),
		ta = $bindable(null)
	}: { placeholder?: string; commands?: SlashCommand[]; value?: string; ta?: HTMLTextAreaElement | null } = $props();
	let query = $state<string>(); // text after the "@" before the caret (names may contain spaces); undefined when closed
	let slash = $state<string>(); // text after a leading "/" before the caret; undefined when closed
	let sel = $state(0);
	let pos = $state({ left: 0, bottom: 0 });

	const options = $derived<Option[]>(
		slash !== undefined
			? commands.some((c) => c.name === slash) // typed in full: Enter sends it
				? []
				: commands.filter((c) => c.name.startsWith(slash!)).map((c) => ({ name: c.name, slash: c }))
			: query === undefined
			? []
			: [{ name: 'agent' }, ...threads.users.filter((u) => u.id !== threads.me?.id)].filter((o) =>
					o.name.toLowerCase().startsWith(query!.toLowerCase())
				)
	);

	function sync() {
		if (!ta) return;
		const before = ta.value.slice(0, ta.selectionStart);
		slash = /^\/(\S*)$/.exec(before)?.[1];
		query = /(?:^|\s)@([^@\n]*)$/.exec(before)?.[1];
		const r = ta.getBoundingClientRect(); // fixed: the prompt box clips overflow
		pos = { left: r.left, bottom: innerHeight - r.top + 4 };
	}

	async function pick(o: Option) {
		if (o.slash) {
			value = `/${o.name} ${value.slice(ta!.selectionStart)}`;
			slash = undefined;
			await tick();
			ta!.setSelectionRange(o.name.length + 2, o.name.length + 2);
			return;
		}
		const at = ta!.selectionStart - query!.length - 1;
		const end = at + o.name.length + 2;
		value = `${value.slice(0, at)}@${o.name} ${value.slice(ta!.selectionStart)}`;
		query = undefined;
		await tick();
		ta!.setSelectionRange(end, end);
	}

	// Capture phase: runs before the textarea submits on Enter.
	function key(e: KeyboardEvent) {
		if (!options.length || e.isComposing) return;
		if (e.key === 'ArrowDown' || e.key === 'ArrowUp') sel = (sel + (e.key === 'ArrowDown' ? 1 : -1) + options.length) % options.length;
		else if (e.key === 'Enter' || e.key === 'Tab') pick(options[Math.min(sel, options.length - 1)]);
		else if (e.key === 'Escape') query = slash = undefined;
		else return;
		e.preventDefault();
		e.stopPropagation();
	}
</script>

<div
	role="presentation"
	oninput={() => {
		sel = 0;
		sync();
	}}
	onkeyup={(e) => /^(Arrow(Left|Right)|Home|End)$/.test(e.key) && sync()}
	onclick={sync}
	onkeydowncapture={key}
	onfocusout={() => (query = slash = undefined)}
>
	<PromptInput.Textarea bind:ref={ta} bind:value {placeholder} />
</div>

{#if options.length}
	<ul
		role="listbox"
		class="fixed z-50 max-h-60 {slash === undefined ? 'w-56' : 'w-96'} overflow-y-auto rounded-lg bg-popover p-1 text-sm text-popover-foreground shadow-md ring-1 ring-foreground/10"
		style="left:{pos.left}px;bottom:{pos.bottom}px"
	>
		{#each options as o, i (o.id ?? o.name)}
			<!-- svelte-ignore a11y_click_events_have_key_events (the textarea handles the keys; mousedown keeps its focus) -->
			<li
				role="option"
				aria-selected={i === sel}
				class="flex cursor-default items-center gap-1.5 rounded-md px-1.5 py-1 {i === sel ? 'bg-accent text-accent-foreground' : ''}"
				onmousedown={(e) => e.preventDefault()}
				onclick={() => pick(o)}
				onmouseenter={() => (sel = i)}
			>
				{#if o.slash}
					<span class="shrink-0 font-mono">/{o.name}</span>
					{#if o.slash.argumentHint}<span class="shrink-0 font-mono text-xs text-muted-foreground">{o.slash.argumentHint}</span>{/if}
					<span class="min-w-0 flex-1 truncate text-xs text-muted-foreground">{o.slash.description}</span>
				{:else}
					{#if !o.id}<BotIcon class="size-4" />{/if}{o.name}
				{/if}
			</li>
		{/each}
	</ul>
{/if}
