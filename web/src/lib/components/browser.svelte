<script lang="ts">
	import { ConnectError } from '@connectrpc/connect';
	import AppWindowIcon from '@lucide/svelte/icons/app-window';
	import ExternalLinkIcon from '@lucide/svelte/icons/external-link';
	import PauseIcon from '@lucide/svelte/icons/pause';
	import RotateCwIcon from '@lucide/svelte/icons/rotate-cw';
	import { Loader } from '#lib/components/ai-elements/loader/index.js';
	import IconButton from '#lib/components/icon-button.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { NativeSelect, NativeSelectOption } from '#lib/components/ui/native-select/index.js';
	import { api } from '#lib/api.js';
	import { Status, type PreviewPort } from '#lib/gen/banda/v1/banda_pb.js';

	// The app running in the thread's environment, framed from the preview origin (see internal/api/preview.go).
	// Like Files it never wakes a paused environment; the owner can.
	let { threadId, status, owner }: { threadId: string; status?: Status; owner: boolean } = $props();

	let running = $state<boolean>();
	let ports = $state<PreviewPort[]>([]);
	let port = $state<number>();
	let path = $state('/');
	let src = $state('');
	let error = $state('');
	let wake = $state(false);

	// Dev servers' usual ports, most likely first; otherwise the lowest port.
	const devPorts = [5173, 3000, 4321, 8080, 8000, 4200, 5000];
	const pick = (ps: PreviewPort[]) =>
		ps.map((p) => p.port).sort((a, b) => (devPorts.indexOf(a) + 1 || 99) - (devPorts.indexOf(b) + 1 || 99))[0];

	$effect(() => {
		const ctrl = new AbortController();
		const signal = ctrl.signal;
		(async () => {
			while (!signal.aborted) {
				try {
					for await (const res of api.previewWatch({ threadId, wake }, { signal })) {
						[running, ports, error] = [res.running, res.ports, ''];
						if (!ports.some((p) => p.port === port)) port = pick(ports);
						if (running) wake = false; // woken: don't wake it again on a reconnect
					}
				} catch (e) {
					if (signal.aborted) return;
					error = ConnectError.from(e).rawMessage;
				}
				await new Promise((r) => setTimeout(r, 2000));
			}
		})();
		return () => ctrl.abort();
	});

	// A fresh link every time: it also points this browser's preview cookie back at this thread and port.
	async function go(to = path) {
		if (!port) return;
		path = to.startsWith('/') ? to : '/' + to;
		try {
			src = (await api.previewOpen({ threadId, port, path })).url;
		} catch (e) {
			error = ConnectError.from(e).rawMessage;
		}
	}

	$effect(() => {
		if (port && running) go('/');
	});

	async function newTab() {
		const w = window.open('', '_blank'); // before the await, or popup blockers step in
		if (!w || !port) return;
		w.opener = null;
		try {
			w.location.href = (await api.previewOpen({ threadId, port, path })).url;
		} catch (e) {
			w.close();
			error = ConnectError.from(e).rawMessage;
		}
	}
</script>

{#snippet note(text: string)}
	<p class="text-center text-sm text-muted-foreground">{text}</p>
{/snippet}

{#if running && port}
	<div class="flex h-full flex-col">
		<form
			class="flex shrink-0 items-center gap-1 border-b bg-card px-2 py-1.5"
			onsubmit={(e) => {
				e.preventDefault();
				go();
			}}
		>
			<NativeSelect size="sm" bind:value={port} aria-label="Port" class="shrink-0">
				{#each ports as p (p.port)}
					<NativeSelectOption value={p.port}>Port {p.port}{p.process ? ` · ${p.process}` : ''}</NativeSelectOption>
				{/each}
			</NativeSelect>
			<Input bind:value={path} aria-label="Path" spellcheck="false" class="h-7 min-w-0 flex-1 font-mono text-xs" />
			<IconButton icon={RotateCwIcon} label="Reload" type="button" onclick={() => go()} />
			<IconButton icon={ExternalLinkIcon} label="Open in a new tab" type="button" onclick={newTab} />
		</form>
		{#if error}<p class="px-3 py-1 text-xs text-destructive">{error}</p>{/if}
		{#if src}
			<!-- Its own origin, so the framed app can't reach Banda; the sandbox keeps it from navigating this page. -->
			<iframe
				title="Preview of port {port}"
				{src}
				class="min-h-0 w-full flex-1 bg-white"
				sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-modals allow-downloads"
			></iframe>
		{/if}
	</div>
{:else}
	<div class="flex flex-col items-center gap-3 p-6">
		{#if running === undefined && !error}
			<Loader class="text-muted-foreground" />
		{:else if running}
			<AppWindowIcon class="size-6 text-muted-foreground" />
			{@render note('Nothing is listening yet. When the agent starts a dev server, it shows up here.')}
		{:else if status === Status.SUSPENDED}
			<PauseIcon class="size-6 text-muted-foreground" />
			{@render note('Environment paused.')}
			{#if owner}
				<Button size="sm" variant="outline" disabled={wake} onclick={() => (wake = true)}>Wake it</Button>
			{:else}
				{@render note('It resumes when the owner opens it or the agent gets a prompt.')}
			{/if}
		{:else if status === Status.STARTING || wake}
			<Loader class="text-muted-foreground" />
			{@render note('Starting environment…')}
		{:else}
			<AppWindowIcon class="size-6 text-muted-foreground" />
			{@render note(error || 'The environment is not running.')}
		{/if}
	</div>
{/if}
