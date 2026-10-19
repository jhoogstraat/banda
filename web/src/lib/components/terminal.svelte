<script lang="ts">
	import { onMount } from 'svelte';
	import { ConnectError } from '@connectrpc/connect';
	import { FitAddon } from '@xterm/addon-fit';
	import { Terminal } from '@xterm/xterm';
	import '@xterm/xterm/css/xterm.css';
	import { Button } from '#lib/components/ui/button/index.js';
	import type { OpenTerminalResponse } from '#lib/gen/banda/v1/banda_pb.js';

	// Driven by a terminal RPC trio: a thread's (a shell) or the login environment's (login or logout).
	// Reopen calls `open` again.
	let {
		open: start,
		write,
		resize
	}: {
		open: (cols: number, rows: number, signal: AbortSignal) => AsyncIterable<OpenTerminalResponse>;
		write: (data: Uint8Array) => Promise<unknown>;
		resize: (cols: number, rows: number) => Promise<unknown>;
	} = $props();

	let el: HTMLDivElement;
	let term: Terminal;
	let ctrl: AbortController;
	let ended = $state(false);
	let error = $state('');

	async function open() {
		ctrl?.abort();
		const mine = (ctrl = new AbortController());
		ended = false;
		error = '';
		term.reset();
		term.focus();
		try {
			for await (const r of start(term.cols, term.rows, mine.signal)) {
				term.write(r.data);
				if (r.exited) break;
			}
		} catch (e) {
			if (!mine.signal.aborted) error = ConnectError.from(e).rawMessage;
		}
		if (!mine.signal.aborted) ended = true;
	}

	onMount(() => {
		term = new Terminal({
			cursorBlink: true,
			fontSize: 13,
			fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
			theme: { background: '#0a0a0a', foreground: '#fafafa', cursor: '#fafafa' },
			// `claude auth login` prints its sign-in URL as an OSC 8 link.
			linkHandler: { activate: (_e, uri) => window.open(uri, '_blank', 'noopener') }
		});
		const fit = new FitAddon();
		term.loadAddon(fit);
		term.open(el);
		fit.fit();

		// Keystrokes are separate requests; chain them so they reach the shell in order.
		let queue: Promise<unknown> = Promise.resolve();
		const enc = new TextEncoder();
		term.onData((d) => {
			queue = queue.then(() => write(enc.encode(d))).catch(() => {});
		});
		term.onResize(({ cols, rows }) => resize(cols, rows).catch(() => {}));
		// The pane may be hidden (zero size); fitting then would shrink the shell to nothing.
		const ro = new ResizeObserver(() => el.clientWidth && fit.fit());
		ro.observe(el);

		open();
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
			<Button size="sm" variant="outline" onclick={open}>Reopen</Button>
		</div>
	{/if}
</div>
