<script lang="ts">
	import type { Snippet } from 'svelte';
	import { IsMobile } from '#lib/hooks/is-mobile.svelte.js';

	// The right pane: a column next to the page on desktop, a full-screen layer on phones. Closed until asked for.
	// It is hidden rather than removed, so closing it keeps its contents (the terminal) running.
	// On desktop a divider resizes it (drag, or arrow keys when focused); this browser remembers the width.
	let { open = $bindable(false), children }: { open?: boolean; children: Snippet } = $props();

	const mobile = new IsMobile();
	const key = 'banda.pane-width';
	const min = 320;
	const max = () => Math.max(min, Math.round(window.innerWidth * 0.6));
	const clamp = (w: number) => Math.min(max(), Math.max(min, Math.round(w)));
	let width = $state(clamp(Number(localStorage.getItem(key)) || 512));
	const resize = (w: number) => localStorage.setItem(key, String((width = clamp(w))));

	let aside = $state<HTMLElement>();
	function drag(e: PointerEvent) {
		const el = e.currentTarget as HTMLElement;
		el.setPointerCapture(e.pointerId); // keeps the pointer over the framed preview
		const right = aside!.getBoundingClientRect().right;
		el.onpointermove = (m) => resize(right - m.clientX);
		el.onpointerup = () => (el.onpointermove = el.onpointerup = null);
	}
	function onkeydown(e: KeyboardEvent) {
		const step = e.shiftKey ? 96 : 24;
		const to = { ArrowLeft: width + step, ArrowRight: width - step, Home: max(), End: min }[e.key];
		if (to === undefined) return;
		e.preventDefault();
		resize(to);
	}
</script>

{#if open && !mobile.current}
	<!-- svelte-ignore a11y_no_noninteractive_element_interactions, a11y_no_noninteractive_tabindex: a focusable separator is the ARIA window splitter -->
	<div
		role="separator"
		aria-orientation="vertical"
		aria-label="Resize the pane"
		aria-valuenow={width}
		aria-valuemin={min}
		aria-valuemax={max()}
		tabindex="0"
		class="relative z-10 -mr-1 w-1 shrink-0 cursor-col-resize touch-none outline-none after:absolute after:inset-y-0 after:left-1/2 after:w-px after:-translate-x-1/2 after:bg-border after:transition-[width,background-color] hover:after:w-0.5 hover:after:bg-primary/60 focus-visible:after:w-0.5 focus-visible:after:bg-primary"
		onpointerdown={drag}
		{onkeydown}
	></div>
{/if}
<aside
	bind:this={aside}
	class="{open ? 'flex' : 'hidden'} flex-col bg-background {mobile.current ? 'fixed inset-0 z-40' : 'shrink-0'}"
	style:width={mobile.current ? undefined : `${width}px`}
>
	{@render children()}
</aside>
