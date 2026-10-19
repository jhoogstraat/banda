<script lang="ts">
	import { onMount } from 'svelte';
	import { FitAddon } from '@xterm/addon-fit';
	import { Terminal } from '@xterm/xterm';
	import '@xterm/xterm/css/xterm.css';
	import { Button } from '#lib/components/ui/button/index.js';
	import { api } from '#lib/api.js';

	// Opens a shell in the thread's environment, or runs `command` instead.
	let { threadId, command = '' }: { threadId: string; command?: string } = $props();

	let el: HTMLDivElement;
	let term: Terminal;
	let ctrl: AbortController;
	let ended = $state(false);
	let error = $state('');

	async function open(cmd: string) {
		ctrl?.abort();
		const mine = (ctrl = new AbortController());
		ended = false;
		error = '';
		term.reset();
		term.focus();
		try {
			const req = { threadId, cols: term.cols, rows: term.rows, command: cmd };
			for await (const r of api.openTerminal(req, { signal: mine.signal })) {
				term.write(r.data);
				if (r.exited) break;
			}
		} catch (e) {
			if (!mine.signal.aborted) error = (e as Error).message;
		}
		if (!mine.signal.aborted) ended = true;
	}

	onMount(() => {
		term = new Terminal({
			cursorBlink: true,
			fontSize: 13,
			fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
			theme: { background: '#0a0a0a', foreground: '#fafafa', cursor: '#fafafa' }
		});
		const fit = new FitAddon();
		term.loadAddon(fit);
		term.open(el);
		fit.fit();

		// Keystrokes are separate requests; chain them so they reach the shell in order.
		let queue: Promise<unknown> = Promise.resolve();
		const enc = new TextEncoder();
		term.onData((d) => {
			queue = queue.then(() => api.writeTerminal({ threadId, data: enc.encode(d) })).catch(() => {});
		});
		term.onResize(({ cols, rows }) => api.resizeTerminal({ threadId, cols, rows }).catch(() => {}));
		// The pane may be hidden (zero size); fitting then would shrink the shell to nothing.
		const ro = new ResizeObserver(() => el.clientWidth && fit.fit());
		ro.observe(el);

		open(command);
		return () => {
			ro.disconnect();
			ctrl.abort(); // the API closes the terminal when the stream goes away
			term.dispose();
		};
	});
</script>

<div class="relative h-full bg-[#0a0a0a] p-2">
	<div bind:this={el} class="h-full"></div>
	{#if ended}
		<div class="absolute inset-x-0 bottom-0 flex items-center gap-3 border-t bg-background/95 px-3 py-2 text-sm">
			<span class="min-w-0 flex-1 truncate text-muted-foreground">{error || 'Terminal exited.'}</span>
			<Button size="sm" variant="outline" onclick={() => open('')}>Reopen</Button>
		</div>
	{/if}
</div>
