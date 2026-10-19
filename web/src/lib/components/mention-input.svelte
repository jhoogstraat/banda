<script lang="ts">
	import { tick } from 'svelte';
	import BotIcon from '@lucide/svelte/icons/bot';
	import * as PromptInput from '#lib/components/ai-elements/prompt-input/index.js';
	import { threads } from '#lib/threads.svelte.js';

	// `picked` collects the people chosen from the popup; the caller keeps those still in the text.
	type Option = { id?: string; name: string };
	let { picked = $bindable([]), placeholder }: { picked?: { id: string; name: string }[]; placeholder?: string } = $props();

	let value = $state('');
	let ta = $state<HTMLTextAreaElement | null>(null);
	let query = $state<string>(); // text after the "@" before the caret; undefined when closed
	let sel = $state(0);
	let pos = $state({ left: 0, bottom: 0 });

	const options = $derived<Option[]>(
		query === undefined
			? []
			: [{ name: 'agent' }, ...threads.users.filter((u) => u.id !== threads.me?.id)].filter((o) =>
					o.name.toLowerCase().startsWith(query!.toLowerCase())
				)
	);

	function sync() {
		if (!ta) return;
		query = /(?:^|\s)@([^\s@]*)$/.exec(ta.value.slice(0, ta.selectionStart))?.[1];
		const r = ta.getBoundingClientRect(); // fixed: the prompt box clips overflow
		pos = { left: r.left, bottom: innerHeight - r.top + 4 };
	}

	async function pick(o: Option) {
		const at = ta!.selectionStart - query!.length - 1;
		const end = at + o.name.length + 2;
		value = `${value.slice(0, at)}@${o.name} ${value.slice(ta!.selectionStart)}`;
		if (o.id && !picked.some((u) => u.id === o.id)) picked.push({ id: o.id, name: o.name });
		query = undefined;
		await tick();
		ta!.setSelectionRange(end, end);
	}

	// Capture phase: runs before the textarea submits on Enter.
	function key(e: KeyboardEvent) {
		if (!options.length || e.isComposing) return;
		if (e.key === 'ArrowDown' || e.key === 'ArrowUp') sel = (sel + (e.key === 'ArrowDown' ? 1 : -1) + options.length) % options.length;
		else if (e.key === 'Enter' || e.key === 'Tab') pick(options[Math.min(sel, options.length - 1)]);
		else if (e.key === 'Escape') query = undefined;
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
	onfocusout={() => (query = undefined)}
>
	<PromptInput.Textarea bind:ref={ta} bind:value {placeholder} />
</div>

{#if options.length}
	<ul
		role="listbox"
		class="fixed z-50 max-h-60 w-56 overflow-y-auto rounded-lg bg-popover p-1 text-sm text-popover-foreground shadow-md ring-1 ring-foreground/10"
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
				{#if !o.id}<BotIcon class="size-4" />{/if}{o.name}
			</li>
		{/each}
	</ul>
{/if}
