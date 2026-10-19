<script lang="ts">
	import * as Dialog from '#lib/components/ui/dialog/index.js';
	import Terminal from '#lib/components/terminal.svelte';
	import { settingsApi } from '#lib/api.js';
	import { LoginAction } from '#lib/gen/banda/v1/banda_pb.js';

	// The harness's login (or logout) command in the caller's login environment. `onclose` runs when the dialog closes.
	let {
		open = $bindable(false),
		action = LoginAction.LOGIN,
		harness = 'claude',
		name = 'Claude Code',
		onclose
	}: { open: boolean; action?: LoginAction; harness?: string; name?: string; onclose?: () => void } = $props();
</script>

<Dialog.Root bind:open onOpenChange={(o) => !o && onclose?.()}>
	<Dialog.Content class="flex h-[min(36rem,85dvh)] flex-col sm:max-w-3xl">
		<Dialog.Header>
			<Dialog.Title>{action === LoginAction.LOGOUT ? `Log out of ${name}` : `Log in to ${name}`}</Dialog.Title>
			<Dialog.Description>
				{action === LoginAction.LOGOUT
					? 'Signs your account out of all your environments.'
					: 'Open the link shown below, sign in, and paste the code back here. Your login is shared by all your threads.'}
				The environment may take a few seconds to start.
			</Dialog.Description>
		</Dialog.Header>
		<div class="min-h-0 flex-1 overflow-hidden rounded-lg">
			<Terminal
				open={(cols, rows, signal) => settingsApi.openLoginTerminal({ harness, action, cols, rows }, { signal })}
				write={(data) => settingsApi.writeLoginTerminal({ harness, data })}
				resize={(cols, rows) => settingsApi.resizeLoginTerminal({ harness, cols, rows })}
			/>
		</div>
	</Dialog.Content>
</Dialog.Root>
