<script lang="ts" module>
	// A prompt input's attachment limits (the API checks them too): spread onto PromptInput.Root.
	export const attachLimits = { multiple: true, maxFiles: 5, maxFileSize: 5 << 20 };
</script>

<script lang="ts">
	import PaperclipIcon from '@lucide/svelte/icons/paperclip';
	import { getAttachmentsContext } from '#lib/components/ai-elements/prompt-input/index.js';
	import IconButton from '#lib/components/icon-button.svelte';

	// Opens the prompt input's file picker. Pasting and dropping work too.
	const attachments = getAttachmentsContext();
</script>

<IconButton icon={PaperclipIcon} label="Attach images or text files" onclick={() => attachments.openFileDialog()} />
