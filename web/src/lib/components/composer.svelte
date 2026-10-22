<script lang="ts">
	import BotIcon from '@lucide/svelte/icons/bot';
	import BrainIcon from '@lucide/svelte/icons/brain';
	import CpuIcon from '@lucide/svelte/icons/cpu';
	import MessagesSquareIcon from '@lucide/svelte/icons/messages-square';
	import ShieldIcon from '@lucide/svelte/icons/shield';
	import { Loader } from '#lib/components/ai-elements/loader/index.js';
	import * as PromptInput from '#lib/components/ai-elements/prompt-input/index.js';
	import type { PromptInputMessage } from '#lib/components/ai-elements/prompt-input/index.js';
	import AttachButton from '#lib/components/attach-button.svelte';
	import MentionInput from '#lib/components/mention-input.svelte';
	import OptionMenu, { efforts, models } from '#lib/components/option-menu.svelte';
	import Plan from '#lib/components/plan.svelte';
	import ThreadBanners from '#lib/components/thread-banners.svelte';
	import { Badge } from '#lib/components/ui/badge/index.js';
	import { api } from '#lib/api.js';
	import type { Chat } from '#lib/events.js';
	import { Mode, Status, type Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { permissionLabel, permissionModes } from '#lib/permissions.js';
	import { userName } from '#lib/threads.svelte.js';

	// The bottom of a thread: the context line (activity, typing, mode), the error, banners, the plan and the prompt box.
	// typing: the names of the others typing. send throws to keep the prompt; typed reports typing.
	let {
		thread,
		owner,
		chat,
		typing,
		error = $bindable(),
		act,
		send,
		typed,
		setAgent,
		toggleMode
	}: {
		thread?: Thread;
		owner: boolean;
		chat: Chat;
		typing: string[];
		error: string;
		act: (call: Promise<unknown>) => Promise<boolean>;
		send: (m: PromptInputMessage) => Promise<void>;
		typed: () => void;
		setAgent: (change: { model?: string; effort?: string }) => void;
		toggleMode: () => void;
	} = $props();

	const typingText = $derived(
		typing.length > 2 ? `${typing.length} people are typing…` : typing.length ? `${typing.join(' and ')} ${typing.length > 1 ? 'are' : 'is'} typing…` : ''
	);
	const chatMode = $derived(thread?.mode === Mode.CHAT);
	// Says what a post does here: the owner's starts a turn in agent mode; in chat mode, and for teammates, only @agent does.
	const placeholder = $derived(
		!thread
			? 'Message…'
			: !owner
				? `Reply to ${userName(thread.ownerId)} — @agent to ${thread.trusted ? 'ask the agent' : 'request a turn'}`
				: chatMode
					? 'Message the team — @agent to ask the agent'
					: 'Ask the agent…'
	);
</script>

<div class="mx-auto w-full max-w-3xl shrink-0 px-4 pb-4">
	<!-- One line of context: who is producing something right now (the agent while it works, people typing) and the mode.
	     Always takes its line, so the composer doesn't jump. -->
	<div class="flex h-7 items-center gap-2 text-xs text-muted-foreground">
		<div class="flex min-w-0 flex-1 items-center gap-2" aria-live="polite">
			{#if thread?.permissionPending}
				<ShieldIcon class="size-3 text-amber-500" /><span>Agent is waiting for permission</span>
			{:else if thread?.status === Status.WORKING}
				<Loader size={12} /><span class="animate-pulse">Agent is {chat.streaming ? 'writing' : 'working'}…</span>
			{/if}
			{#if typingText}
				{#if thread?.status === Status.WORKING}<span>·</span>{/if}
				<span class="flex gap-0.5"><span class="size-1 animate-bounce rounded-full bg-current"></span><span class="size-1 animate-bounce rounded-full bg-current [animation-delay:150ms]"></span><span class="size-1 animate-bounce rounded-full bg-current [animation-delay:300ms]"></span></span>
				<span class="truncate">{typingText}</span>
			{/if}
		</div>
		{#if thread}
			{@const label = chatMode ? 'Chat · @agent to ask' : 'Agent'}
			<!-- The owner switches the mode here; for everyone else it only says what a post does. -->
			<button
				class="flex shrink-0 items-center gap-1 rounded-full border px-2 py-0.5 enabled:hover:bg-muted enabled:hover:text-foreground {chatMode ? 'border-sky-500/40 text-sky-600 dark:text-sky-400' : ''}"
				disabled={!owner}
				aria-label={owner ? `${chatMode ? 'Chat mode' : 'Agent mode'}: switch to ${chatMode ? 'agent' : 'chat'} mode` : `${chatMode ? 'Chat mode' : 'Agent mode'}`}
				title={owner ? `Switch to ${chatMode ? 'agent mode: your posts go to the agent' : 'chat mode: only @agent starts a turn'}` : undefined}
				onclick={toggleMode}
			>
				{#if chatMode}<MessagesSquareIcon class="size-3" />{:else}<BotIcon class="size-3" />{/if}
				{label}
			</button>
		{/if}
	</div>
	{#if error}<p role="alert" class="mb-2 text-sm text-destructive">{error}</p>{/if}
	{#if thread}<ThreadBanners {thread} />{/if}
	<Plan plan={chat.plan} />
	<PromptInput.Root onSubmit={send} multiple maxFiles={5} maxFileSize={5 << 20} onError={(e) => (error = e.message)}>
		<fieldset disabled={thread?.status === Status.STARTING} class="contents" oninput={typed}>
			<PromptInput.Attachments class="px-2">
				{#snippet children(a)}<PromptInput.Attachment data={a} />{/snippet}
			</PromptInput.Attachments>
			<PromptInput.Body>
				<MentionInput {placeholder} />
			</PromptInput.Body>
			<PromptInput.Toolbar>
				<div class="flex items-center gap-1.5 pl-1">
					<AttachButton />
					{#if thread && owner}
						<!-- A running agent switches model and effort once its turns have ended, permissions at once. -->
						<OptionMenu label="Model" icon={CpuIcon} bind:value={() => thread!.model, (model) => setAgent({ model })} options={models} />
						<OptionMenu label="Effort" icon={BrainIcon} bind:value={() => thread!.effort, (effort) => setAgent({ effort })} options={efforts} />
						<OptionMenu
							label="Permissions"
							icon={ShieldIcon}
							options={Object.keys(permissionModes)}
							bind:value={() => permissionLabel(thread!.permissionMode), (v) => act(api.permissionSetMode({ threadId: thread!.id, mode: permissionModes[v] }))}
						/>
					{:else if thread}
						<Badge variant="outline" class="font-normal text-muted-foreground"><CpuIcon /> {thread.model}</Badge>
						<Badge variant="outline" class="font-normal text-muted-foreground"><BrainIcon /> {thread.effort}</Badge>
						<Badge variant="outline" class="font-normal text-muted-foreground"><ShieldIcon /> {permissionLabel(thread.permissionMode)}</Badge>
					{/if}
				</div>
				<PromptInput.Submit />
			</PromptInput.Toolbar>
		</fieldset>
	</PromptInput.Root>
</div>
