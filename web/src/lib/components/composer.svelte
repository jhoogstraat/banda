<script lang="ts" module>
	import { onDestroy, tick } from 'svelte';
	import type { PromptInputAttachmentData } from '#lib/components/ai-elements/prompt-input/index.js';
	import type { Attachment } from '#lib/gen/banda/v1/banda_pb.js';

	// The composer's text and files per thread: they survive switching threads (files picked here don't, see onDestroy), and "Use as draft" fills them.
	// The text also survives a reload, in this browser.
	const drafts = $state<Record<string, string>>({});
	const draftKey = (id: string) => `banda-draft-${id}`;
	const draft = (id: string) => drafts[id] ?? localStorage.getItem(draftKey(id)) ?? '';
	function setDraft(id: string, text: string) {
		drafts[id] = text;
		if (text) localStorage.setItem(draftKey(id), text);
		else localStorage.removeItem(draftKey(id));
	}
	const files = $state<Record<string, PromptInputAttachmentData[]>>({});
	let input: HTMLTextAreaElement | null = null;

	/** Puts a teammate's proposal into the owner's composer, after what they typed, and focuses it: they edit it and send it as their own.
	 *  Its files stay the uploads they are (`remoteUrl`, the id its upload's): the API lets the owner post a proposal's files. */
	export async function useAsDraft(threadId: string, text: string, attachments: Attachment[]) {
		const typed = draft(threadId).trimEnd();
		if (text) setDraft(threadId, typed ? `${typed}\n${text}` : text);
		const have = files[threadId] ?? [];
		const added = attachments.filter((a) => !have.some((f) => f.id === a.id));
		files[threadId] = [
			...have,
			...added.map((a) => ({ id: a.id, file: new File([], a.name, { type: a.mediaType }), filename: a.name, mediaType: a.mediaType, remoteUrl: `/files/${a.id}` }))
		];
		await tick();
		input?.focus();
		input?.setSelectionRange(input.value.length, input.value.length);
	}
</script>

<script lang="ts">
	import BotIcon from '@lucide/svelte/icons/bot';
	import BrainIcon from '@lucide/svelte/icons/brain';
	import ContactRoundIcon from '@lucide/svelte/icons/contact-round';
	import CpuIcon from '@lucide/svelte/icons/cpu';
	import MessagesSquareIcon from '@lucide/svelte/icons/messages-square';
	import ShieldCheckIcon from '@lucide/svelte/icons/shield-check';
	import ShieldIcon from '@lucide/svelte/icons/shield';
	import SquareIcon from '@lucide/svelte/icons/square';
	import XIcon from '@lucide/svelte/icons/x';
	import { Loader } from '#lib/components/ai-elements/loader/index.js';
	import * as PromptInput from '#lib/components/ai-elements/prompt-input/index.js';
	import type { PromptInputMessage } from '#lib/components/ai-elements/prompt-input/index.js';
	import AttachButton, { attachLimits } from '#lib/components/attach-button.svelte';
	import IconButton from '#lib/components/icon-button.svelte';
	import MentionInput from '#lib/components/mention-input.svelte';
	import OptionMenu, { efforts, models } from '#lib/components/option-menu.svelte';
	import Plan from '#lib/components/plan.svelte';
	import ThreadBanners from '#lib/components/thread-banners.svelte';
	import { Badge } from '#lib/components/ui/badge/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as DropdownMenu from '#lib/components/ui/dropdown-menu/index.js';
	import { Textarea } from '#lib/components/ui/textarea/index.js';
	import { api } from '#lib/api.js';
	import { where, type Comment } from '#lib/diff.js';
	import type { Chat } from '#lib/events.js';
	import { Mode, Status, type Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { permissionLabel, permissionModes, ruleLabel } from '#lib/permissions.js';
	import { profileLabel, profileLabels, profileName } from '#lib/profiles.js';
	import { userName } from '#lib/threads.svelte.js';

	// The bottom of a thread: the context line (activity, typing, mode), the error, banners, the plan and the prompt box.
	// typing: the names of the others typing. send throws to keep the prompt; typed reports typing.
	// pending and ask are the review being written: send sends it along, so it shows here, each comment editable and removable.
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
		toggleMode,
		interrupt,
		pending = $bindable(),
		ask = $bindable()
	}: {
		thread?: Thread;
		owner: boolean;
		chat: Chat;
		typing: string[];
		error: string;
		act: (call: Promise<unknown>) => Promise<boolean>;
		send: (m: PromptInputMessage) => Promise<void>;
		typed: () => void;
		setAgent: (change: { model?: string; effort?: string; profile?: string }) => void;
		toggleMode: () => void;
		interrupt: () => void;
		pending: Comment[];
		ask: boolean;
	} = $props();

	const typingText = $derived(
		typing.length > 2 ? `${typing.length} people are typing…` : typing.length ? `${typing.join(' and ')} ${typing.length > 1 ? 'are' : 'is'} typing…` : ''
	);
	const chatMode = $derived(thread?.mode === Mode.CHAT);
	// Claude Code retrying a failed request to Anthropic, in the CLI's own categories.
	const retrying = $derived.by(() => {
		const r = chat.retry;
		if (!r) return '';
		const why =
			{ overloaded: 'Anthropic is overloaded', rate_limit: 'Anthropic is rate limiting requests', server_error: `Anthropic had a server error (${r.errorStatus})` }[r.error] ??
			(r.errorStatus ? `Anthropic answered ${r.errorStatus}` : 'Can’t reach Anthropic');
		return `${why} · retrying ${r.attempt}/${r.maxRetries}`;
	});
	// Says what a post does here: the owner's starts a turn in agent mode; in chat mode, and for teammates, only @agent does.
	// On a subscription only the owner runs the agent (Anthropic's terms): teammates propose prompts, which the owner may
	// use as a draft of their own, and fork for their own work.
	// The box clears after a successful send, from the thread it was sent from: by then another thread may be open.
	async function submit(m: PromptInputMessage) {
		const id = thread?.id ?? '';
		await send(m);
		setDraft(id, '');
		files[id] = [];
	}
	// A file picked here has a preview that dies with this page, so it goes too; a proposal's is an upload and stays.
	onDestroy(() => {
		const id = thread?.id ?? '';
		if (files[id]) files[id] = files[id].filter((f) => f.remoteUrl);
	});
	const placeholder = $derived(
		!thread
			? 'Message…'
			: !owner && !thread.teammateTurns
				? `Only ${userName(thread.ownerId)} runs the agent. Suggest a prompt with @agent, or Fork for your own task.`
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
				<ShieldIcon class="size-3 text-warning-foreground" /><span>Agent is waiting for an answer</span>
			{:else if thread?.status === Status.WORKING && retrying}
				<Loader size={12} /><span>{retrying}</span>
			{:else if thread?.status === Status.WORKING}
				<Loader size={12} /><span>Agent is {chat.streaming ? 'writing' : 'working'}…</span>
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
				class="flex shrink-0 items-center gap-1 rounded-full border px-2 py-0.5 pointer-coarse:py-1.5 enabled:hover:bg-muted enabled:hover:text-foreground {chatMode ? 'border-info/40 text-info-foreground' : ''}"
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
	<PromptInput.Root onSubmit={submit} clearOnSubmit={false} bind:attachments={() => files[thread?.id ?? ''], (v) => (files[thread?.id ?? ''] = v ?? [])} {...attachLimits} onError={(e) => (error = e.message)}>
		<fieldset disabled={thread?.status === Status.STARTING} class="contents" oninput={typed}>
			<PromptInput.Attachments class="px-2">
				{#snippet children(a)}<PromptInput.Attachment data={a} />{/snippet}
			</PromptInput.Attachments>
			{#if pending.length}
				<div class="max-h-64 space-y-1.5 overflow-y-auto px-3 pt-2" role="group" aria-label="Pending review comments">
					<div class="flex items-center gap-2 text-xs">
						<span class="font-medium">{pending.length} pending comment{pending.length > 1 ? 's' : ''}</span>
						<label class="ml-auto flex items-center gap-1.5 text-muted-foreground"><input type="checkbox" bind:checked={ask} class="accent-primary" /> Ask @agent</label>
					</div>
					{#each pending as c, i (i)}
						<div class="overflow-hidden rounded-md border text-xs">
							<div class="flex items-center gap-1 bg-muted/40 px-2 py-0.5">
								<span class="min-w-0 flex-1 truncate font-mono">{where(c)}</span>
								<IconButton icon={XIcon} label="Remove the comment on {where(c)}" class="size-5" onclick={() => (pending = pending.filter((_, j) => j !== i))} />
							</div>
							{#if c.code}<pre class="max-h-24 overflow-auto border-t px-2 py-1 font-mono leading-5">{c.code}</pre>{/if}
							<Textarea bind:value={c.body} aria-label="Comment on {where(c)}" class="min-h-0 rounded-none border-0 border-t text-xs shadow-none focus-visible:ring-0" rows={2} />
						</div>
					{/each}
				</div>
			{/if}
			<PromptInput.Body>
				<MentionInput
					{placeholder}
					commands={owner ? chat.commands : []}
					bind:value={() => draft(thread?.id ?? ''), (v) => setDraft(thread?.id ?? '', v)}
					bind:ta={() => input, (r) => (input = r)}
				/>
			</PromptInput.Body>
			<PromptInput.Toolbar class="items-end">
				<!-- The chips scroll sideways on a phone, as on the new thread page, so the box stays small. -->
				<div class="flex min-w-0 items-center gap-1.5 py-0.5 pl-1 max-sm:overflow-x-auto max-sm:[scrollbar-width:none] sm:flex-wrap">
					<AttachButton />
					{#if thread && owner}
						<!-- A running agent switches model, effort and profile once its turns have ended, permissions at once. -->
						<OptionMenu label="Model" icon={CpuIcon} bind:value={() => thread!.model, (model) => setAgent({ model })} options={models} />
						<OptionMenu label="Effort" icon={BrainIcon} bind:value={() => thread!.effort, (effort) => setAgent({ effort })} options={efforts} />
						<OptionMenu
							label="Profile"
							icon={ContactRoundIcon}
							options={profileLabels}
							bind:value={() => profileLabel(thread!.profile), (v) => setAgent({ profile: profileName(v) })}
						/>
						<OptionMenu
							label="Permissions"
							icon={ShieldIcon}
							options={Object.keys(permissionModes)}
							bind:value={() => permissionLabel(thread!.permissionMode), (v) => act(api.permissionSetMode({ threadId: thread!.id, mode: permissionModes[v] }))}
						/>
					{:else if thread}
						<Badge variant="outline" class="font-normal text-muted-foreground"><CpuIcon /> {thread.model}</Badge>
						<Badge variant="outline" class="font-normal text-muted-foreground"><BrainIcon /> {thread.effort}</Badge>
						<Badge variant="outline" class="font-normal text-muted-foreground"><ContactRoundIcon /> {profileLabel(thread.profile)}</Badge>
						<Badge variant="outline" class="font-normal text-muted-foreground"><ShieldIcon /> {permissionLabel(thread.permissionMode)}</Badge>
					{/if}
					{#if thread?.allowedTools.length}
						<!-- What the agent does without asking in this thread; whoever answers its requests can take it back. -->
						<DropdownMenu.Root>
							<DropdownMenu.Trigger>
								{#snippet child({ props })}
									<PromptInput.Button {...props} size="sm" aria-label="Allowed: {thread!.allowedTools.length}"><ShieldCheckIcon /> Allowed · {thread!.allowedTools.length}</PromptInput.Button>
								{/snippet}
							</DropdownMenu.Trigger>
							<DropdownMenu.Content align="start" class="w-64">
								<DropdownMenu.Label>Runs without asking</DropdownMenu.Label>
								{#each thread.allowedTools as rule (rule)}
									<DropdownMenu.Item
										disabled={!owner && !thread.trusted}
										onSelect={() => act(api.permissionRevoke({ threadId: thread!.id, rule }))}
										aria-label="Stop allowing {ruleLabel(rule)}"
										class="font-mono text-xs"
									>
										<span class="min-w-0 flex-1 truncate">{ruleLabel(rule)}</span>
										{#if owner || thread.trusted}<XIcon class="text-muted-foreground" />{/if}
									</DropdownMenu.Item>
								{/each}
							</DropdownMenu.Content>
						</DropdownMenu.Root>
					{/if}
				</div>
				{#if owner && thread?.status === Status.WORKING}
					<Button type="button" variant="destructive" size="sm" class="shrink-0" onclick={interrupt}><SquareIcon class="fill-current" /> Stop</Button>
				{/if}
				<PromptInput.Submit class="shrink-0" />
			</PromptInput.Toolbar>
		</fieldset>
	</PromptInput.Root>
</div>
