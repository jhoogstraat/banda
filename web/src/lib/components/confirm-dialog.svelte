<script lang="ts" module>
	type Ask = { title: string; body: string; action: string };
	let asked = $state<Ask & { answer: (yes: boolean) => void }>();

	/** Asks before something that can't be undone; the button names the action. */
	export const confirmAction = (ask: Ask) => new Promise<boolean>((answer) => (asked = { ...ask, answer }));
</script>

<script lang="ts">
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Dialog from '#lib/components/ui/dialog/index.js';

	function answer(yes: boolean) {
		asked?.answer(yes);
		asked = undefined;
	}
</script>

<Dialog.Root open={!!asked} onOpenChange={(open) => !open && answer(false)}>
	<Dialog.Content class="sm:max-w-md">
		<Dialog.Header>
			<Dialog.Title>{asked?.title}</Dialog.Title>
			<Dialog.Description>{asked?.body}</Dialog.Description>
		</Dialog.Header>
		<Dialog.Footer>
			<Button variant="outline" onclick={() => answer(false)}>Cancel</Button>
			<Button variant="destructive" onclick={() => answer(true)}>{asked?.action}</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
